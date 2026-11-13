package auriga.collab.network.keys;

import static auriga.collab.network.keys.MsgnetV2Verifier.*;

import auriga.collab.app.NetworkJson;
import java.security.KeyFactory;
import java.security.spec.X509EncodedKeySpec;
import java.util.Arrays;
import java.util.Base64;
import java.util.UUID;
import org.springframework.jdbc.core.JdbcTemplate;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** Minimal projection reader; authority, CA and KEK secrets never enter returned material. */
public final class MsgnetMaterials {
  public interface Transport {
    JsonNode bundle();

    JsonNode leaves(MsgnetControlLedger.Identity identity);

    JsonNode confirm(JsonNode receipt, MsgnetControlLedger.Identity identity);
  }

  private final MsgnetRenewal renewal;
  private final MsgnetControlLedger identities;
  private final MsgnetPrivateKeyStore keys;
  private final JdbcTemplate jdbc;
  private final UUID tenant;
  private final String realm;
  private final Transport transport;

  public MsgnetMaterials(
      MsgnetRenewal renewal,
      MsgnetControlLedger identities,
      MsgnetPrivateKeyStore keys,
      JdbcTemplate jdbc,
      UUID tenant,
      String realm,
      Transport transport) {
    this.renewal = renewal;
    this.identities = identities;
    this.keys = keys;
    this.jdbc = jdbc;
    this.tenant = tenant;
    this.realm = realm;
    this.transport = transport;
  }

  private MsgnetControlLedger.Identity authorize(JsonNode request, boolean plaintext) {
    schema("ProjectionRequest", request, "invalid_chain");
    require(
        tenant.toString().equals(text(request, "tenant_id"))
            && realm.equals(text(request, "realm")),
        "scope_denied");
    var identity = identities.identity(text(request, "installation_id"));
    require(
        identity.component().equals(text(request, "component"))
            && identity.gatewayId().equals(text(request, "gateway_id")),
        "scope_denied");
    if (plaintext) {
      var principal =
          auriga.common.security.AuthorizationRules.currentPrincipal()
              .orElseThrow(() -> failure("scope_denied"));
      require(
          tenant.equals(principal.tenantId()) && identity.subject().equals(principal.subject()),
          "scope_denied");
    }
    MsgnetPolicy.identitySignature(
        request,
        "Auriga.NetworkProjectionRequest.v2",
        RootKeyPublication.publicKey(identity.signingSpki()));
    var control = renewal.requiredState().path("control").path("payload");
    require(
        !contains(control.path("revoked_installation_ids"), identity.identityId()),
        "identity_quarantined");
    return identity;
  }

  private JsonNode consume(JsonNode request) {
    var p = NetworkJson.parse(renewal.publication(), 1048576);
    var k = p.path("keyset").path("payload");
    require(
        request.path("expected_generation").equals(k.path("generation"))
            && request.path("expected_keyset_version").equals(k.path("keyset_version")),
        "version_rollback");
    int inserted =
        jdbc.update(
            """
        INSERT INTO collab.msgnet_projection_nonce (id, tenant_id, realm, installation_id, nonce) VALUES (?, ?, ?, ?, ?)
        ON CONFLICT (tenant_id, realm, installation_id, nonce) DO NOTHING
        """,
            auriga.common.persistence.UuidV7.next(java.time.Clock.systemUTC()),
            tenant,
            realm,
            text(request, "installation_id"),
            text(request, "nonce"));
    require(inserted == 1, "invalid_transition");
    return p;
  }

  public ObjectNode protocol(JsonNode request) {
    return renewal.run(
        () -> {
          authorize(request, true);
          require("network".equals(text(request, "component")), "scope_denied");
          return protocolMaterial(consume(request));
        });
  }

  private ObjectNode protocolMaterial(JsonNode publication) {
    var material = MsgnetRenewal.object();
    material.set("publication", publication);
    for (var key : publication.path("keyset").path("payload").path("keys")) {
      String state = text(key, "state");
      if (!"protocol_response".equals(text(key, "purpose"))
          || !java.util.List.of("active", "next").contains(state)) continue;
      byte[] bytes = keys.read(renewal.aad(text(key, "kid"), "protocol_response"));
      try {
        material
            .put(state + "_kid", text(key, "kid"))
            .put(state + "_pkcs8", Base64.getUrlEncoder().withoutPadding().encodeToString(bytes));
      } finally {
        Arrays.fill(bytes, (byte) 0);
      }
    }
    schema("ProtocolMaterial", material, "invalid_chain");
    return material;
  }

  public ObjectNode material(JsonNode request) {
    return renewal.run(
        () -> {
          var identity = authorize(request, true);
          return material(request, identity, consume(request));
        });
  }

  private ObjectNode material(
      JsonNode request, MsgnetControlLedger.Identity identity, JsonNode publication) {
    require(transport != null, "projection_pending");
    var result = MsgnetRenewal.object();
    for (String field :
        java.util.List.of("tenant_id", "realm", "installation_id", "component", "nonce"))
      result.set(field, request.path(field));
    result.set("generation", publication.path("keyset").path("payload").path("generation"));
    result.set("keyset_version", publication.path("keyset").path("payload").path("keyset_version"));
    result.put("publication_digest", digest(publication));
    result.set("bundle", transport.bundle());
    result.set("leaves", transport.leaves(identity));
    if ("network".equals(identity.component()))
      result.set("protocol", protocolMaterial(publication));
    schema("ProjectionMaterial", result, "invalid_chain");
    return result;
  }

  public ObjectNode sealed(JsonNode request) {
    return renewal.run(
        () -> {
          var identity = authorize(request, false);
          var material = material(request, identity, consume(request));
          var metadata = MsgnetRenewal.object();
          for (String field :
              java.util.List.of(
                  "tenant_id",
                  "realm",
                  "installation_id",
                  "component",
                  "nonce",
                  "generation",
                  "keyset_version")) metadata.set(field, material.path(field));
          metadata
              .put("bundle_digest", MsgnetV2Verifier.digest(material.path("bundle")))
              .put("kem", "DHKEM(X25519,HKDF-SHA256)")
              .put("kdf", "HKDF-SHA256")
              .put("aead", "AES-256-GCM");
          try {
            var key =
                KeyFactory.getInstance("X25519")
                    .generatePublic(
                        new X509EncodedKeySpec(
                            Base64.getUrlDecoder().decode(identity.sealingSpki())));
            return MsgnetHpke.seal(metadata, material, key);
          } catch (java.security.GeneralSecurityException e) {
            throw failure("scope_denied");
          }
        });
  }

  public JsonNode confirm(JsonNode receipt) {
    return renewal.run(
        () -> {
          require(transport != null, "projection_pending");
          var identity = identities.identity(text(receipt, "installation_id"));
          MsgnetPolicy.identitySignature(
              receipt,
              "Auriga.NetworkProjectionReceipt.v2",
              RootKeyPublication.publicKey(identity.signingSpki()));
          require(
              identity.component().equals(text(receipt, "component"))
                  && tenant.toString().equals(text(receipt, "tenant_id"))
                  && realm.equals(text(receipt, "realm")),
              "scope_denied");
          require(
              jdbc.queryForObject(
                      "SELECT count(*) FROM collab.msgnet_projection_nonce WHERE tenant_id = ? AND realm = ? AND installation_id = ? AND nonce = ?",
                      Integer.class,
                      tenant,
                      realm,
                      identity.identityId(),
                      text(receipt, "nonce"))
                  == 1,
              "invalid_transition");
          schema("ProjectionReceipt", receipt, "invalid_chain");
          return transport.confirm(receipt, identity);
        });
  }

  static String digest(JsonNode node) {
    try {
      return "sha256:"
          + java.util.HexFormat.of()
              .formatHex(
                  java.security.MessageDigest.getInstance("SHA-256")
                      .digest(
                          MsgnetRenewal.canonical(node)
                              .getBytes(java.nio.charset.StandardCharsets.UTF_8)));
    } catch (java.security.GeneralSecurityException e) {
      throw new IllegalStateException(e);
    }
  }
}
