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
      require(tenant.equals(principal.tenantId()), "scope_denied");
    }
    MsgnetPolicy.identitySignature(
        request,
        "Auriga.NetworkProjectionRequest.v2",
        RootKeyPublication.publicKey(identity.signingSpki()));
    require(
        identity.identityId().equals(text(request.path("identity_auth"), "kid")), "scope_denied");
    identities.requireUsable(identity);
    return identity;
  }

  private JsonNode consume(JsonNode request) {
    renewal.lock();
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
    result.put("publication_digest", MsgnetV2Verifier.digest(publication.path("keyset")));
    result.set("bundle", transport.bundle());
    result.set("leaves", transport.leaves(identity));
    if ("network".equals(identity.component()))
      result.set("protocol", protocolMaterial(publication));
    schema("ProjectionMaterial", result, "invalid_chain");
    var snapshot = MsgnetProjectionBinding.snapshot(result, publication);
    jdbc.update(
        "UPDATE collab.msgnet_projection_nonce SET authorized_snapshot = ? WHERE tenant_id = ? AND realm = ? AND installation_id = ? AND nonce = ?",
        MsgnetRenewal.canonical(snapshot),
        tenant,
        realm,
        identity.identityId(),
        text(request, "nonce"));
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
          schema("ProjectionReceipt", receipt, "invalid_chain");
          renewal.lock();
          require(transport != null, "projection_pending");
          var identity = identities.identity(text(receipt, "installation_id"));
          require(
              identity.identityId().equals(text(receipt.path("identity_auth"), "kid")),
              "scope_denied");
          identities.requireUsable(identity);
          MsgnetPolicy.identitySignature(
              receipt,
              "Auriga.NetworkProjectionReceipt.v2",
              RootKeyPublication.publicKey(identity.signingSpki()));
          require(
              identity.component().equals(text(receipt, "component"))
                  && tenant.toString().equals(text(receipt, "tenant_id"))
                  && realm.equals(text(receipt, "realm")),
              "scope_denied");
          var rows =
              jdbc.queryForList(
                  "SELECT authorized_snapshot, local_selector, receipts::text FROM collab.msgnet_projection_nonce WHERE tenant_id = ? AND realm = ? AND installation_id = ? AND nonce = ? FOR UPDATE",
                  tenant,
                  realm,
                  identity.identityId(),
                  text(receipt, "nonce"));
          require(
              rows.size() == 1 && rows.getFirst().get("authorized_snapshot") != null,
              "invalid_transition");
          var row = rows.getFirst();
          var snapshot = NetworkJson.parse(row.get("authorized_snapshot").toString(), 1048576);
          var selector = receipt.path("local_selector");
          var prior =
              jdbc.queryForList(
                  "SELECT selector FROM collab.msgnet_projection_head WHERE tenant_id = ? AND realm = ? AND installation_id = ? AND component = ?",
                  String.class,
                  tenant,
                  realm,
                  identity.identityId(),
                  identity.component());
          MsgnetProjectionBinding.verify(
              snapshot,
              selector,
              receipt,
              prior.isEmpty() ? null : NetworkJson.parse(prior.getFirst()));
          if (row.get("local_selector") != null)
            require(
                NetworkJson.parse(row.get("local_selector").toString()).equals(selector),
                "same_version_conflict");
          var receipts = (ObjectNode) NetworkJson.parse(row.get("receipts").toString(), 1048576);
          String phase = text(receipt, "phase");
          if (receipts.has(phase)) {
            require(receipts.path(phase).equals(receipt), "same_version_conflict");
            return receipts.path(phase);
          }
          if ("handshake_verified".equals(phase))
            require(receipts.has("loaded"), "projection_pending");
          if ("persisted".equals(phase))
            require(
                !receipts.has("loaded") && !receipts.has("handshake_verified"),
                "invalid_transition");
          if ("loaded".equals(phase))
            require(!receipts.has("handshake_verified"), "invalid_transition");
          receipts.set(phase, receipt);
          jdbc.update(
              "UPDATE collab.msgnet_projection_nonce SET local_selector = ?, receipts = ?::jsonb WHERE tenant_id = ? AND realm = ? AND installation_id = ? AND nonce = ?",
              MsgnetRenewal.canonical(selector),
              MsgnetRenewal.canonical(receipts),
              tenant,
              realm,
              identity.identityId(),
              text(receipt, "nonce"));
          jdbc.update(
              "INSERT INTO collab.msgnet_projection_head (id, tenant_id, realm, installation_id, component, selector) VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT (tenant_id, realm, installation_id, component) DO UPDATE SET selector = EXCLUDED.selector",
              auriga.common.persistence.UuidV7.next(java.time.Clock.systemUTC()),
              tenant,
              realm,
              identity.identityId(),
              identity.component(),
              MsgnetRenewal.canonical(selector));
          // Failure is durable in the nonce ledger and cannot overwrite a successful aggregate.
          if ("failed".equals(phase)) return receipt;
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
