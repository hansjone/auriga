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
          if (previous != null) remember(previous);
          var currentAuthority = authority;
          boolean transition =
              previous != null
                  && !text(previous.path("payload"), "authority_digest").equals(digest(authority));
          boolean emergency =
              transition
                  && "emergency_recovery"
                      .equals(
                          text(
                              state.path("control").path("payload").path("transport_transition"),
                              "mode"));
          if (transition
              && !emergency
              && !"retire_old".equals(text(previous.path("payload"), "phase"))) {
            authority = history(digest(previous)).path("authority");
            renewal.verifyTransportAuthority(authority);
            transition = false;
          }
          ObjectNode payload;
          boolean issueLeaves = false;
          if (previous == null) {
            payload = MsgnetRenewal.object();
            var cas = payload.putArray("cas");
            cas.add(ca("server", now));
            cas.add(ca("client", now));
            payload.put("phase", "dual_trust");
          } else if (transition) {
            payload = (ObjectNode) previous.path("payload").deepCopy();
            if (emergency) {
              require(allIsolated(previous.path("payload")), "identity_quarantined");
              payload.putArray("cas");
              jdbc.update(
                  "DELETE FROM collab.msgnet_transport_leaf WHERE tenant_id = ? AND realm = ?",
                  tenant,
                  realm);
              jdbc.update(
                  "UPDATE collab.msgnet_projection_nonce SET superseded = true WHERE tenant_id = ? AND realm = ?",
                  tenant,
                  realm);
            }
            var cas = (tools.jackson.databind.node.ArrayNode) payload.path("cas");
            cas.add(ca("server", now));
            cas.add(ca("client", now));
            payload.put("phase", "dual_trust");
          } else {
            payload = (ObjectNode) previous.path("payload").deepCopy();
            String phase = text(payload, "phase");
            if ("dual_trust".equals(phase)) {
              if (!acknowledged(previous, false)) return null;
              payload.put("phase", "new_leaves");
              issueLeaves = true;
            } else if ("new_leaves".equals(phase)) {
              if (!acknowledged(previous, true)) {
                issueLeaves(payload, authority.path("payload").path("transport_scope"), now, false);
                return null;
              }
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
              .put(
                  "generation",
                  transition
                      ? text(
                          state.path("control").path("payload").path("transport_transition"),
                          "to_generation")
                      : previous == null
                          ? text(keyset, "generation")
                          : text(previous.path("payload"), "generation"))
              .put(
                  "bundle_version",
                  transition
                      ? text(
                          state.path("control").path("payload").path("transport_transition"),
                          "first_bundle_version")
                      : previous == null
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
          if (previous == null || emergency) {
            var readers = payload.putArray("required_reader_ids");
            var seen = new java.util.HashSet<String>();
            for (var scope : payload.path("scope"))
              if (seen.add(text(scope, "identity_id"))) readers.add(text(scope, "identity_id"));
          }
          if (issueLeaves) issueLeaves(payload, payload.path("scope"), now, true);
          number(payload, "bundle_version");
          var signed = renewal.signTransport(payload, authority);
          if (transition) {
            var predecessor = history(digest(previous));
            renewal
                .transportVerifier()
                .transition(
                    signed,
                    currentAuthority,
                    state.path("control"),
                    predecessor,
                    new MsgnetTransportVerifier.SeenTransport(
                        text(previous.path("payload"), "generation"),
                        text(previous.path("payload"), "bundle_version"),
                        digest(previous)),
                    emergency && allIsolated(previous.path("payload")));
            var proof = MsgnetRenewal.object();
            proof.set("control", state.path("control"));
            proof.set("predecessor", predecessor);
            proof.set("first_bundle", signed);
            proof.set("authority", currentAuthority);
            schema("TransportTransitionProof", proof, "invalid_chain");
            jdbc.update(
                "INSERT INTO collab.msgnet_transport_transition (id, tenant_id, realm, authority_digest, generation, proof) VALUES (?, ?, ?, ?, ?, ?)",
                auriga.common.persistence.UuidV7.next(java.time.Clock.systemUTC()),
                tenant,
                realm,
                digest(currentAuthority),
                new java.math.BigDecimal(text(payload, "generation")),
                MsgnetRenewal.canonical(proof));
          }
          remember(signed, authority, state.path("control"));
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

  private boolean acknowledged(JsonNode bundle, boolean handshake) {
    try {
      var readers = readers(bundle, handshake ? "handshake_verified" : "loaded");
      jdbc.update(
          "INSERT INTO collab.msgnet_transport_stage (id, tenant_id, realm, bundle_digest, readers) VALUES (?, ?, ?, ?, ?) ON CONFLICT (tenant_id, realm, bundle_digest) DO NOTHING",
          auriga.common.persistence.UuidV7.next(java.time.Clock.systemUTC()),
          tenant,
          realm,
          digest(bundle),
          MsgnetRenewal.canonical(readers));
      return true;
    } catch (auriga.common.error.AurigaException failure) {
      if (!"collab.msgnet_projection_pending".equals(failure.errorCode().code())) throw failure;
      return false;
    }
  }

  private boolean allIsolated(JsonNode payload) {
    for (var id : payload.path("required_reader_ids")) if (!isolated(id.asString())) return false;
    return true;
  }

  private boolean isolated(String identity) {
    var rows =
        jdbc.queryForList(
            "SELECT isolation_record FROM collab.msgnet_isolation_enforcement WHERE tenant_id = ? AND realm = ? AND identity_id = ? AND control_digest = ?",
            String.class,
            tenant,
            realm,
            identity,
            digest(renewal.requiredState().path("control")));
    return !rows.isEmpty()
        && "quarantined".equals(text(NetworkJson.parse(rows.getFirst(), 1048576), "state"));
  }

  private JsonNode readers(JsonNode bundle, String requiredPhase) {
    var frozen =
        jdbc.queryForList(
            "SELECT readers FROM collab.msgnet_transport_stage WHERE tenant_id = ? AND realm = ? AND bundle_digest = ?",
            String.class,
            tenant,
            realm,
            digest(bundle));
    if (!frozen.isEmpty()) {
      var readers = NetworkJson.parse(frozen.getFirst(), 1048576);
      for (var row : readers) {
        String id = text(row, "identity_id");
        if (isolated(id)) {
          ((ObjectNode) row).put("phase", "isolated").put("isolation_enforced", true);
        } else
          require(
              !contains(
                      renewal
                          .requiredState()
                          .path("control")
                          .path("payload")
                          .path("revoked_installation_ids"),
                      id)
                  && !"isolated".equals(text(row, "phase")),
              "projection_pending");
      }
      return readers;
    }
    var result = MsgnetRenewal.object().putArray("readers");
    for (var id : bundle.path("payload").path("required_reader_ids")) {
      var rows =
          jdbc.queryForList(
              "SELECT authorized_snapshot, local_selector, receipts::text AS receipts, superseded FROM collab.msgnet_projection_nonce WHERE tenant_id = ? AND realm = ? AND installation_id = ? AND local_selector IS NOT NULL ORDER BY id DESC",
              tenant,
              realm,
              id.asString());
      JsonNode selected = null;
      for (var row : rows) {
        var selector = NetworkJson.parse(row.get("local_selector").toString(), 1048576);
        if (!digest(bundle).equals(text(selector, "bundle_digest"))) continue;
        var receipts = NetworkJson.parse(row.get("receipts").toString(), 1048576);
        String phase = receipts.has("handshake_verified") ? "handshake_verified" : "loaded";
        if (!isolated(id.asString())
            && (Boolean.TRUE.equals(row.get("superseded"))
                || !receipts.has(phase)
                || ("handshake_verified".equals(requiredPhase) && !receipts.has(requiredPhase))))
          continue;
        var snapshot = NetworkJson.parse(row.get("authorized_snapshot").toString(), 1048576);
        if (!isolated(id.asString()))
          MsgnetProjectionBinding.verify(snapshot, selector, receipts.path(phase), null);
        var evidence =
            MsgnetRenewal.object()
                .put("reader_id", id.asString())
                .put("identity_id", id.asString())
                .put("phase", isolated(id.asString()) ? "isolated" : phase)
                .put("isolation_enforced", isolated(id.asString()));
        evidence.set("selector", selector);
        selected = evidence;
        break;
      }
      require(selected != null, "projection_pending");
      result.add(selected);
    }
    return result;
  }

  private void remember(JsonNode bundle) {
    var rows =
        jdbc.queryForList(
            "SELECT predecessor FROM collab.msgnet_transport_history WHERE tenant_id = ? AND realm = ? AND bundle_digest = ?",
            String.class,
            tenant,
            realm,
            digest(bundle));
    if (!rows.isEmpty()) return;
    // Upgrade an existing K2 public package using its original signed publication history.
    var publications =
        jdbc.queryForList(
            "SELECT publication FROM collab.msgnet_publication_history WHERE tenant_id = ? AND realm = ? ORDER BY committed_at DESC",
            String.class,
            tenant,
            realm);
    for (var encoded : publications) {
      var p = NetworkJson.parse(encoded, 1048576);
      if (digest(p.path("authority")).equals(text(bundle.path("payload"), "authority_digest"))
          && p.path("control")
              .path("payload")
              .path("control_revision")
              .equals(bundle.path("payload").path("control_revision"))) {
        remember(bundle, p.path("authority"), p.path("control"));
        return;
      }
    }
    throw failure("predecessor_required");
  }

  private void remember(JsonNode bundle, JsonNode authority, JsonNode control) {
    var predecessor = MsgnetRenewal.object();
    predecessor.set("bundle", bundle);
    predecessor.set("authority", authority);
    predecessor.set("control", control);
    schema("TransportPredecessor", predecessor, "invalid_chain");
    jdbc.update(
        "INSERT INTO collab.msgnet_transport_history (id, tenant_id, realm, bundle_digest, generation, bundle_version, predecessor) VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT (tenant_id, realm, bundle_digest) DO NOTHING",
        auriga.common.persistence.UuidV7.next(java.time.Clock.systemUTC()),
        tenant,
        realm,
        digest(bundle),
        new java.math.BigDecimal(text(bundle.path("payload"), "generation")),
        new java.math.BigDecimal(text(bundle.path("payload"), "bundle_version")),
        MsgnetRenewal.canonical(predecessor));
  }

  public JsonNode history(String digest) {
    require(digest.matches("sha256:[0-9a-f]{64}"), "invalid_chain");
    return renewal.run(
        () -> {
          var rows =
              jdbc.queryForList(
                  "SELECT predecessor FROM collab.msgnet_transport_history WHERE tenant_id = ? AND realm = ? AND bundle_digest = ?",
                  String.class,
                  tenant,
                  realm,
                  digest);
          if (rows.isEmpty())
            throw new auriga.common.error.AurigaException(
                auriga.common.error.CommonErrors.NOT_FOUND);
          return NetworkJson.parse(rows.getFirst(), 1048576);
        });
  }

  public JsonNode transitionProof(String digest) {
    require(digest.matches("sha256:[0-9a-f]{64}"), "invalid_chain");
    return renewal.run(
        () -> {
          var rows =
              jdbc.queryForList(
                  "SELECT proof FROM collab.msgnet_transport_transition WHERE tenant_id = ? AND realm = ? AND authority_digest = ? ORDER BY generation DESC LIMIT 1",
                  String.class,
                  tenant,
                  realm,
                  digest);
          if (rows.isEmpty())
            throw new auriga.common.error.AurigaException(
                auriga.common.error.CommonErrors.NOT_FOUND);
          return NetworkJson.parse(rows.getFirst(), 1048576);
        });
  }

  public JsonNode confirmation(JsonNode query) {
    schema("TransportConfirmationQuery", query, "invalid_chain");
    require(
        tenant.toString().equals(text(query, "tenant_id")) && realm.equals(text(query, "realm")),
        "scope_denied");
    try {
      return renewal.run(
          () -> {
            renewal.lock();
            var rows =
                jdbc.queryForList(
                    "SELECT predecessor FROM collab.msgnet_transport_history WHERE tenant_id = ? AND realm = ? AND bundle_digest = ?",
                    String.class,
                    tenant,
                    realm,
                    text(query, "bundle_digest"));
            if (rows.isEmpty()) {
              var versions =
                  jdbc.queryForList(
                      "SELECT bundle_digest FROM collab.msgnet_transport_history WHERE tenant_id = ? AND realm = ? AND bundle_version = ?",
                      String.class,
                      tenant,
                      realm,
                      new java.math.BigDecimal(text(query, "bundle_version")));
              require(versions.isEmpty(), "same_version_conflict");
              throw new auriga.common.error.AurigaException(
                  auriga.common.error.CommonErrors.NOT_FOUND);
            }
            var predecessor = NetworkJson.parse(rows.getFirst(), 1048576);
            var bundle = predecessor.path("bundle");
            require(
                bundle.path("payload").path("bundle_version").equals(query.path("bundle_version")),
                "same_version_conflict");
            var state = renewal.requiredState();
            require(
                number(bundle.path("payload"), "generation")
                        .compareTo(
                            number(state.path("control").path("payload"), "minimum_generation"))
                    >= 0,
                "version_rollback");
            var authority = predecessor.path("authority");
            renewal.verifyTransportAuthority(authority);
            var payload = MsgnetRenewal.object();
            for (String field :
                List.of(
                    "tenant_id",
                    "realm",
                    "authority_id",
                    "authority_digest",
                    "bundle_version",
                    "phase",
                    "required_reader_ids")) payload.set(field, bundle.path("payload").path(field));
            payload.set(
                "control_revision", state.path("control").path("payload").path("control_revision"));
            payload
                .put("bundle_digest", digest(bundle))
                .put("challenge", text(query, "challenge"))
                .put("issued_at", renewal.checkedTime().toString());
            payload.set(
                "readers",
                readers(
                    bundle,
                    "new_leaves".equals(text(bundle.path("payload"), "phase"))
                        ? "handshake_verified"
                        : "loaded"));
            var signed = renewal.signConfirmation(payload, authority);
            schema("SignedTransportConfirmation", signed, "invalid_chain");
            return signed;
          });
    } catch (org.springframework.dao.DataAccessException unavailable) {
      throw failure("projection_pending");
    }
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
                time(NetworkJson.parse(old.getFirst(), 1048576), "not_after")
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
            "UPDATE collab.msgnet_projection_nonce SET superseded = true WHERE tenant_id = ? AND realm = ? AND installation_id = ?",
            tenant,
            realm,
            text(scope, "identity_id"));
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
        renewal.audit(
            "collab.msgnet.certificate_issued",
            "none",
            java.util.Map.of(
                "target_id",
                text(scope, "identity_id"),
                "component",
                component,
                "role",
                role,
                "certificate_not_after",
                text(leaf, "not_after")));
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
    return rows.isEmpty() ? null : NetworkJson.parse(rows.getFirst(), 1048576);
  }

  @Override
  public JsonNode bundle() {
    return renewal.run(
        () -> {
          var bundle = stored();
          require(bundle != null, "projection_pending");
          remember(bundle);
          renewal.verifyTransportAuthority(history(digest(bundle)).path("authority"));
          require(
              number(bundle.path("payload"), "generation")
                      .compareTo(
                          number(
                              renewal.requiredState().path("control").path("payload"),
                              "minimum_generation"))
                  >= 0,
              "version_rollback");
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
                    var leaf = (ObjectNode) NetworkJson.parse(rs.getString(1), 1048576);
                    if (!time(leaf, "not_after").isAfter(renewal.checkedTime())) {
                      require(
                          "dual_trust".equals(text(bundle().path("payload"), "phase")),
                          "projection_pending");
                      return;
                    }
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
