package auriga.collab.network.keys;

import static auriga.collab.network.keys.MsgnetV2Verifier.*;

import auriga.collab.app.NetworkJson;
import java.security.SecureRandom;
import java.util.Base64;
import java.util.Map;
import java.util.UUID;
import org.springframework.jdbc.core.JdbcTemplate;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** Frozen installation pins are supplied by the installation/authentication adapter. */
public final class MsgnetControlLedger {
  public record Identity(
      String targetId,
      String identityId,
      String signingSpki,
      String component,
      String gatewayId,
      String sealingSpki,
      String subject) {}

  private final MsgnetRenewal renewal;
  private final JdbcTemplate jdbc;
  private final UUID tenant;
  private final String realm;
  private final Map<String, Identity> identities;

  public MsgnetControlLedger(
      MsgnetRenewal renewal,
      JdbcTemplate jdbc,
      UUID tenant,
      String realm,
      Map<String, Identity> identities) {
    this.renewal = renewal;
    this.jdbc = jdbc;
    this.tenant = tenant;
    this.realm = realm;
    this.identities = Map.copyOf(identities);
  }

  Identity identity(String identityId) {
    return identities.values().stream()
        .filter(i -> i.identityId().equals(identityId))
        .findFirst()
        .orElseThrow(() -> failure("scope_denied"));
  }

  public ObjectNode create(JsonNode request) {
    schema("ControlPackage", request, "invalid_chain");
    renewal.installControl(request.path("control"));
    return renewal.run(
        () -> {
          lock();
          require(
              tenant.toString().equals(text(request, "tenant_id"))
                  && realm.equals(text(request, "realm")),
              "scope_denied");
          var identity = identities.get(text(request, "target_id"));
          require(identity != null, "scope_denied");
          var control = renewal.requiredState().path("control");
          require(
              control.equals(request.path("control"))
                  && MsgnetV2Verifier.digest(control).equals(text(request, "control_digest")),
              "control_rollback");
          var previous = row(identity.targetId());
          if (previous != null && previous.path("package").path("control").equals(control))
            return (ObjectNode) previous.path("package");
          byte[] random = new byte[32];
          new SecureRandom().nextBytes(random);
          var pack =
              MsgnetRenewal.object()
                  .put("package_id", UUID.randomUUID().toString())
                  .put("tenant_id", tenant.toString())
                  .put("realm", realm)
                  .put("target_id", identity.targetId())
                  .put("control_digest", MsgnetV2Verifier.digest(control))
                  .put("challenge", Base64.getUrlEncoder().withoutPadding().encodeToString(random));
          pack.set("control", control);
          var isolation =
              MsgnetRenewal.object()
                  .put("tenant_id", tenant.toString())
                  .put("realm", realm)
                  .put("target_id", identity.targetId())
                  .put(
                      "minimum_control_revision", text(control.path("payload"), "control_revision"))
                  .put("control_digest", MsgnetV2Verifier.digest(control))
                  .put("state", "quarantined")
                  .put(
                      "identity_compromised",
                      previous != null
                          && previous.path("isolation").path("identity_compromised").asBoolean())
                  .put("challenge", text(pack, "challenge"))
                  .put("new_chain_ready", false);
          jdbc.update(
              """
          INSERT INTO collab.msgnet_control_target (id, tenant_id, realm, target_id, identity_id, identity_spki, control_package, isolation_record)
          VALUES (?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (tenant_id, realm, target_id) DO UPDATE SET
            control_package = EXCLUDED.control_package, isolation_record = EXCLUDED.isolation_record, receipt = NULL
          """,
              auriga.common.persistence.UuidV7.next(java.time.Clock.systemUTC()),
              tenant,
              realm,
              identity.targetId(),
              identity.identityId(),
              identity.signingSpki(),
              MsgnetRenewal.canonical(pack),
              MsgnetRenewal.canonical(isolation));
          return pack;
        });
  }

  public ObjectNode confirm(JsonNode receipt) {
    return renewal.run(
        () -> {
          lock();
          var row = row(text(receipt, "target_id"));
          require(row != null, "identity_quarantined");
          var identity = identities.get(text(receipt, "target_id"));
          require(identity != null, "scope_denied");
          boolean used = row.has("receipt");
          boolean replay = used && row.path("receipt").equals(receipt);
          var isolation = (ObjectNode) row.path("isolation").deepCopy();
          try {
            MsgnetPolicy.receipt(
                row.path("package"),
                receipt,
                RootKeyPublication.publicKey(identity.signingSpki()),
                identity.identityId(),
                true,
                used,
                replay,
                isolation.path("identity_compromised").asBoolean(),
                isolation.path("new_chain_ready").asBoolean());
          } catch (auriga.common.error.AurigaException e) {
            if (!"collab.msgnet_identity_quarantined".equals(e.errorCode().code())) throw e;
          }
          if (!isolation.path("identity_compromised").asBoolean())
            isolation.put(
                "state", isolation.path("new_chain_ready").asBoolean() ? "ready" : "confirmed");
          jdbc.update(
              "UPDATE collab.msgnet_control_target SET receipt = ?, isolation_record = ? WHERE tenant_id = ? AND realm = ? AND target_id = ?",
              MsgnetRenewal.canonical(receipt),
              MsgnetRenewal.canonical(isolation),
              tenant,
              realm,
              identity.targetId());
          return isolation;
        });
  }

  public ObjectNode enforce(JsonNode request) {
    schema("IsolationRecord", request, "invalid_chain");
    return renewal.run(
        () -> {
          lock();
          var row = row(text(request, "target_id"));
          require(row != null, "identity_quarantined");
          var stored = row.path("isolation");
          require(
              stored
                  .path("minimum_control_revision")
                  .equals(
                      renewal
                          .requiredState()
                          .path("control")
                          .path("payload")
                          .path("control_revision")),
              "control_required");
          for (String field :
              java.util.List.of(
                  "tenant_id",
                  "realm",
                  "target_id",
                  "minimum_control_revision",
                  "control_digest",
                  "challenge"))
            require(stored.path(field).equals(request.path(field)), "invalid_transition");
          boolean compromised =
              stored.path("identity_compromised").asBoolean()
                  || request.path("identity_compromised").asBoolean();
          if (!"quarantined".equals(text(request, "state")))
            require(row.has("receipt") && !compromised, "identity_quarantined");
          if ("ready".equals(text(request, "state")))
            require(request.path("new_chain_ready").asBoolean(), "identity_quarantined");
          var next = (ObjectNode) request.deepCopy();
          next.put("identity_compromised", compromised);
          if (compromised) next.put("state", "quarantined").put("new_chain_ready", false);
          jdbc.update(
              "UPDATE collab.msgnet_control_target SET isolation_record = ?, enforcement_confirmed = ? WHERE tenant_id = ? AND realm = ? AND target_id = ?",
              MsgnetRenewal.canonical(next),
              "quarantined".equals(text(next, "state")),
              tenant,
              realm,
              text(request, "target_id"));
          return next;
        });
  }

  private ObjectNode row(String target) {
    var rows =
        jdbc.query(
            "SELECT control_package, receipt, isolation_record FROM collab.msgnet_control_target WHERE tenant_id = ? AND realm = ? AND target_id = ?",
            (rs, n) -> {
              var value = MsgnetRenewal.object();
              value.set("package", NetworkJson.parse(rs.getString(1)));
              if (rs.getString(2) != null) value.set("receipt", NetworkJson.parse(rs.getString(2)));
              value.set("isolation", NetworkJson.parse(rs.getString(3)));
              return value;
            },
            tenant,
            realm,
            target);
    return rows.isEmpty() ? null : rows.getFirst();
  }

  private void lock() {
    jdbc.queryForObject(
        "SELECT pg_advisory_xact_lock(hashtextextended(?, 0))",
        Object.class,
        tenant + ":network-keys:" + realm);
  }
}
