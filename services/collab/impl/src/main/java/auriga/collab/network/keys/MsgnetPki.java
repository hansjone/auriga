package auriga.collab.network.keys;

import static auriga.collab.network.keys.MsgnetV2Verifier.*;

import auriga.collab.app.NetworkJson;
import java.security.KeyFactory;
import java.security.PrivateKey;
import java.security.cert.CertificateFactory;
import java.security.cert.X509Certificate;
import java.security.spec.PKCS8EncodedKeySpec;
import java.time.Instant;
import java.time.ZoneOffset;
import java.util.Arrays;
import java.util.Base64;
import java.util.List;
import java.util.UUID;
import org.springframework.jdbc.core.JdbcTemplate;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** Durable dual-CA rotation. Every phase waits for the exact signed reader receipts it needs. */
public final class MsgnetPki implements MsgnetMaterials.Transport {
  private final MsgnetRenewal renewal;
  private final MsgnetPrivateKeyStore secrets;
  private final JdbcTemplate jdbc;
  private final UUID tenant;
  private final String realm;
  private final JsonNode config;

  public MsgnetPki(
      MsgnetRenewal renewal,
      MsgnetPrivateKeyStore secrets,
      JdbcTemplate jdbc,
      UUID tenant,
      String realm,
      JsonNode config) {
    this.renewal = renewal;
    this.secrets = secrets;
    this.jdbc = jdbc;
    this.tenant = tenant;
    this.realm = realm;
    this.config = config.deepCopy();
  }

  public void reconcile() {
    renewal.run(
        () -> {
          renewal.lock();
          Instant now = renewal.checkedTime();
          var state = renewal.requiredState();
          var authority = state.path("authority");
          capability(authority.path("payload"), "network_transport");
          var previous = stored();
          ObjectNode payload;
          boolean issueLeaves = false;
          if (previous == null) {
            payload = MsgnetRenewal.object();
            var cas = payload.putArray("cas");
            cas.add(ca("server", now));
            cas.add(ca("client", now));
            payload.put("phase", "dual_trust");
          } else if (!text(previous.path("payload"), "authority_digest")
              .equals(MsgnetV2Verifier.digest(authority))) {
            payload = (ObjectNode) previous.path("payload").deepCopy();
            var oldCas = list(payload.path("cas"));
            var cas = payload.putArray("cas");
            for (var ca : oldCas.subList(Math.max(0, oldCas.size() - 2), oldCas.size()))
              cas.add(ca);
            cas.add(ca("server", now));
            cas.add(ca("client", now));
            payload.put("phase", "dual_trust");
          } else {
            payload = (ObjectNode) previous.path("payload").deepCopy();
            String phase = text(payload, "phase");
            if ("dual_trust".equals(phase)) {
              if (!acknowledged(payload, false)) return null;
              payload.put("phase", "new_leaves");
              issueLeaves = true;
            } else if ("new_leaves".equals(phase)) {
              if (!acknowledged(payload, true)) return null;
              var cas = list(payload.path("cas"));
              var retained = payload.putArray("cas");
              for (var ca : cas.subList(Math.max(0, cas.size() - 2), cas.size())) retained.add(ca);
              payload.put("phase", "retire_old");
            } else {
              boolean renewCa =
                  list(payload.path("cas")).stream()
                      .anyMatch(
                          c ->
                              !now.isBefore(
                                  time(c, "not_after")
                                      .atZone(ZoneOffset.UTC)
                                      .minusYears(config.path("ca_renew_before_years").asLong())
                                      .toInstant()));
              if (renewCa) {
                var cas = (tools.jackson.databind.node.ArrayNode) payload.path("cas");
                cas.add(ca("server", now));
                cas.add(ca("client", now));
                payload.put("phase", "dual_trust");
              } else {
                issueLeaves(payload, authority.path("payload").path("transport_scope"), now, false);
                return null;
              }
            }
          }
          var keyset = state.path("publication").path("keyset").path("payload");
          payload
              .put("schema_version", 2)
              .put("tenant_id", tenant.toString())
              .put("realm", realm)
              .put("authority_id", text(authority.path("payload"), "authority_id"))
              .put("authority_digest", MsgnetV2Verifier.digest(authority))
              .put(
                  "control_revision",
                  text(state.path("control").path("payload"), "control_revision"))
              .put("generation", text(keyset, "generation"))
              .put(
                  "bundle_version",
                  previous == null
                      ? "1"
                      : number(previous.path("payload"), "bundle_version")
                          .add(java.math.BigInteger.ONE)
                          .toString())
              .put("issued_at", now.toString())
              .put(
                  "previous_bundle_digest",
                  previous == null
                      ? "sha256:" + "0".repeat(64)
                      : MsgnetV2Verifier.digest(previous));
          payload.set("scope", authority.path("payload").path("transport_scope").deepCopy());
          var readers = payload.putArray("required_reader_ids");
          var seen = new java.util.HashSet<String>();
          for (var scope : payload.path("scope"))
            if (seen.add(text(scope, "identity_id"))) readers.add(text(scope, "identity_id"));
          if (issueLeaves) issueLeaves(payload, payload.path("scope"), now, true);
          var signed = renewal.signTransport(payload);
          schema("SignedTransportTrustBundle", signed, "invalid_chain");
          renewal.checkedTime();
          jdbc.update(
              "INSERT INTO collab.msgnet_transport (id, tenant_id, realm, bundle) VALUES (?, ?, ?, ?) ON CONFLICT (tenant_id, realm) DO UPDATE SET bundle = EXCLUDED.bundle",
              auriga.common.persistence.UuidV7.next(java.time.Clock.systemUTC()),
              tenant,
              realm,
              MsgnetRenewal.canonical(signed));
          return null;
        });
  }

  private boolean acknowledged(JsonNode payload, boolean handshake) {
    for (var id : payload.path("required_reader_ids")) {
      var isolated =
          jdbc.queryForList(
              "SELECT isolation_record FROM collab.msgnet_control_target WHERE tenant_id = ? AND realm = ? AND identity_id = ? AND enforcement_confirmed = true",
              String.class,
              tenant,
              realm,
              id.asString());
      if (!isolated.isEmpty()
          && "quarantined".equals(text(NetworkJson.parse(isolated.getFirst()), "state"))) continue;
      var receipts =
          jdbc.queryForList(
              "SELECT receipt FROM collab.msgnet_projection_receipt WHERE tenant_id = ? AND realm = ? AND installation_id = ? AND bundle_version = ?",
              String.class,
              tenant,
              realm,
              id.asString(),
              new java.math.BigDecimal(text(payload, "bundle_version")));
      if (receipts.isEmpty()) return false;
      String phase = text(NetworkJson.parse(receipts.getFirst()), "phase");
      if (handshake
          ? !"handshake_verified".equals(phase)
          : !List.of("persisted", "loaded", "handshake_verified").contains(phase)) return false;
    }
    return true;
  }

  private ObjectNode ca(String role, Instant now) {
    var issued =
        MsgnetCertificates.issue(
            "auriga-msgnet-" + role,
            now.minusSeconds(300),
            now.atZone(ZoneOffset.UTC).plusYears(config.path("ca_years").asLong()).toInstant(),
            List.of(),
            List.of(),
            null,
            null);
    String id = UUID.randomUUID().toString();
    save(id, "network_transport_ca", "collab", issued);
    return MsgnetRenewal.object()
        .put("ca_id", id)
        .put("role", role)
        .put("certificate_der", certificate(issued.certificate()))
        .put("not_before", issued.certificate().getNotBefore().toInstant().toString())
        .put("not_after", issued.certificate().getNotAfter().toInstant().toString());
  }

  private void issueLeaves(JsonNode bundle, JsonNode scopes, Instant now, boolean force) {
    var cas = list(bundle.path("cas"));
    var current = cas.subList(Math.max(0, cas.size() - 2), cas.size());
    for (var scope : scopes) {
      String component = text(scope, "component");
      List<String> roles =
          switch (component) {
            case "network" -> List.of("submit", "broker-client");
            case "edge" -> List.of("edge", "edge-presence");
            case "broker" -> List.of("broker");
            default -> throw failure("scope_denied");
          };
      for (String role : roles) {
        boolean client = List.of("broker-client", "edge-presence").contains(role);
        String eku = client ? "clientAuth" : "serverAuth";
        if (!contains(scope.path("ekus"), eku)) continue;
        var old =
            jdbc.queryForList(
                "SELECT leaf FROM collab.msgnet_transport_leaf WHERE tenant_id = ? AND realm = ? AND identity_id = ? AND role = ?",
                String.class,
                tenant,
                realm,
                text(scope, "identity_id"),
                role);
        if (!force
            && !old.isEmpty()
            && now.isBefore(
                time(NetworkJson.parse(old.getFirst()), "not_after")
                    .minusSeconds(config.path("leaf_renew_before_days").asLong() * 86400)))
          continue;
        var ca =
            current.stream()
                .filter(c -> (client ? "client" : "server").equals(text(c, "role")))
                .findFirst()
                .orElseThrow();
        var certificate = parseCertificate(text(ca, "certificate_der"));
        Instant after = now.plusSeconds(config.path("leaf_days").asLong() * 86400);
        if (after.isAfter(certificate.getNotAfter().toInstant()))
          after = certificate.getNotAfter().toInstant();
        require(after.isAfter(now), "projection_pending");
        var issued =
            MsgnetCertificates.issue(
                "auriga-msgnet-" + role,
                now.minusSeconds(300),
                after,
                list(scope.path("sans")).stream().map(JsonNode::asString).toList(),
                List.of(eku),
                certificate,
                secret(text(ca, "ca_id"), "network_transport_ca", "collab"));
        jdbc.update(
            "DELETE FROM collab.msgnet_projection_receipt WHERE tenant_id = ? AND realm = ? AND installation_id = ?",
            tenant,
            realm,
            text(scope, "identity_id"));
        String kid = UUID.randomUUID().toString();
        save(kid, "network_transport_leaf", component, issued);
        var leaf =
            MsgnetRenewal.object()
                .put("component", component)
                .put("role", role)
                .put("certificate_der", certificate(issued.certificate()))
                .put("ca_id", text(ca, "ca_id"))
                .put("not_before", issued.certificate().getNotBefore().toInstant().toString())
                .put("not_after", issued.certificate().getNotAfter().toInstant().toString());
        jdbc.update(
            """
            INSERT INTO collab.msgnet_transport_leaf (id, tenant_id, realm, identity_id, role, ca_id, kid, leaf)
            VALUES (?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (tenant_id, realm, identity_id, role) DO UPDATE SET ca_id = EXCLUDED.ca_id, kid = EXCLUDED.kid, leaf = EXCLUDED.leaf
            """,
            auriga.common.persistence.UuidV7.next(java.time.Clock.systemUTC()),
            tenant,
            realm,
            text(scope, "identity_id"),
            role,
            text(ca, "ca_id"),
            kid,
            MsgnetRenewal.canonical(leaf));
      }
    }
  }

  private ObjectNode aad(String id, String purpose, String component) {
    return renewal.aad(id, purpose).put("component", component);
  }

  private void save(String id, String purpose, String component, MsgnetCertificates.Issued issued) {
    byte[] bytes = issued.pair().getPrivate().getEncoded();
    try {
      secrets.install(
          aad(id, purpose, component),
          Base64.getUrlEncoder()
              .withoutPadding()
              .encodeToString(issued.pair().getPublic().getEncoded()),
          bytes);
    } finally {
      Arrays.fill(bytes, (byte) 0);
    }
  }

  private PrivateKey secret(String id, String purpose, String component) {
    byte[] bytes = secrets.read(aad(id, purpose, component));
    try {
      return KeyFactory.getInstance("Ed25519").generatePrivate(new PKCS8EncodedKeySpec(bytes));
    } catch (java.security.GeneralSecurityException e) {
      throw failure("invalid_chain");
    } finally {
      Arrays.fill(bytes, (byte) 0);
    }
  }

  private static String certificate(X509Certificate certificate) {
    try {
      return Base64.getUrlEncoder().withoutPadding().encodeToString(certificate.getEncoded());
    } catch (java.security.cert.CertificateEncodingException e) {
      throw failure("invalid_chain");
    }
  }

  private static X509Certificate parseCertificate(String value) {
    try {
      return (X509Certificate)
          CertificateFactory.getInstance("X.509")
              .generateCertificate(
                  new java.io.ByteArrayInputStream(Base64.getUrlDecoder().decode(value)));
    } catch (java.security.cert.CertificateException e) {
      throw failure("invalid_chain");
    }
  }

  private JsonNode stored() {
    var rows =
        jdbc.queryForList(
            "SELECT bundle FROM collab.msgnet_transport WHERE tenant_id = ? AND realm = ?",
            String.class,
            tenant,
            realm);
    return rows.isEmpty() ? null : NetworkJson.parse(rows.getFirst());
  }

  @Override
  public JsonNode bundle() {
    return renewal.run(
        () -> {
          var bundle = stored();
          require(bundle != null, "projection_pending");
          require(
              text(bundle.path("payload"), "authority_digest")
                  .equals(MsgnetV2Verifier.digest(renewal.requiredState().path("authority"))),
              "authority_revoked");
          return bundle;
        });
  }

  @Override
  public JsonNode leaves(MsgnetControlLedger.Identity identity) {
    return renewal.run(
        () -> {
          var array = MsgnetRenewal.object().putArray("leaves");
          jdbc.query(
              "SELECT leaf, kid FROM collab.msgnet_transport_leaf WHERE tenant_id = ? AND realm = ? AND identity_id = ? ORDER BY role",
              (org.springframework.jdbc.core.RowCallbackHandler)
                  rs -> {
                    var leaf = (ObjectNode) NetworkJson.parse(rs.getString(1));
                    require(identity.component().equals(text(leaf, "component")), "scope_denied");
                    byte[] bytes =
                        secrets.read(
                            aad(rs.getString(2), "network_transport_leaf", identity.component()));
                    try {
                      leaf.put(
                          "private_pkcs8",
                          Base64.getUrlEncoder().withoutPadding().encodeToString(bytes));
                      array.add(leaf);
                    } finally {
                      Arrays.fill(bytes, (byte) 0);
                    }
                  },
              tenant,
              realm,
              identity.identityId());
          return array;
        });
  }

  @Override
  public JsonNode confirm(JsonNode receipt, MsgnetControlLedger.Identity identity) {
    return renewal.run(
        () -> {
          renewal.lock();
          var current = bundle();
          var p = NetworkJson.parse(renewal.publication(), 1048576);
          require(
              receipt.path("bundle_version").equals(current.path("payload").path("bundle_version"))
                  && text(receipt, "bundle_digest").equals(MsgnetV2Verifier.digest(current))
                  && receipt
                      .path("generation")
                      .equals(p.path("keyset").path("payload").path("generation"))
                  && receipt
                      .path("keyset_version")
                      .equals(p.path("keyset").path("payload").path("keyset_version"))
                  && text(receipt, "publication_digest")
                      .equals(MsgnetV2Verifier.digest(p.path("keyset"))),
              "invalid_transition");
          jdbc.update(
              """
          INSERT INTO collab.msgnet_projection_receipt (id, tenant_id, realm, installation_id, bundle_version, receipt)
          VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT (tenant_id, realm, installation_id, bundle_version) DO UPDATE SET receipt = EXCLUDED.receipt
          """,
              auriga.common.persistence.UuidV7.next(java.time.Clock.systemUTC()),
              tenant,
              realm,
              identity.identityId(),
              new java.math.BigDecimal(text(receipt, "bundle_version")),
              MsgnetRenewal.canonical(receipt));
          return receipt;
        });
  }
}
