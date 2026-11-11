package auriga.ops;

import auriga.common.context.TenantContext;
import auriga.common.persistence.TenantTransactionManager;
import auriga.common.persistence.UuidV7;
import auriga.ops.api.OpsSignals;
import java.util.*;
import java.util.function.Supplier;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.transaction.support.TransactionTemplate;
import tools.jackson.databind.json.JsonMapper;
import tools.jackson.databind.node.ObjectNode;

/** Tenant-owned durable state. No reads of another service's schema. */
public class OpsStore implements OpsSignals {
  private static final JsonMapper JSON = JsonMapper.builder().build();
  private final JdbcTemplate jdbc;
  private final TransactionTemplate tx;
  private final OpsSecrets secrets;
  private final OpsWebhook webhook;

  OpsStore(
      JdbcTemplate jdbc, TenantTransactionManager manager, OpsSecrets secrets, OpsWebhook webhook) {
    this.jdbc = jdbc;
    tx = new TransactionTemplate(manager);
    this.secrets = secrets;
    this.webhook = webhook;
  }

  <T> T transaction(Supplier<T> work) {
    TenantContext.require();
    TenantTransactionManager.verifyTransactionTenant();
    return tx.execute(s -> work.get());
  }

  private UUID tenant() {
    return TenantContext.require();
  }

  @Override
  public void observe(UUID tenant, String source, String subject, String severity, boolean active) {
    OpsError.require(
        Set.of(
                    "node_offline",
                    "instance_failed",
                    "upstream_failed",
                    "credential_expiring",
                    "disk_low")
                .contains(source)
            && subject != null
            && !subject.isBlank()
            && subject.length() <= 160
            && Set.of("warning", "critical").contains(severity));
    TenantContext.run(
        tenant,
        () ->
            transaction(
                () -> {
                  if (active)
                    jdbc.update(
                        "INSERT INTO ops.alert(id,tenant_id,source,subject,severity,first_seen,last_seen) VALUES (?,?,?,?,?,now(),now()) ON CONFLICT (tenant_id,source,subject) WHERE resolved_at IS NULL DO UPDATE SET last_seen=now(),severity=EXCLUDED.severity",
                        UuidV7.next(),
                        tenant,
                        source,
                        subject,
                        severity);
                  else
                    jdbc.update(
                        "UPDATE ops.alert SET resolved_at=now(),last_seen=now() WHERE tenant_id=? AND source=? AND subject=? AND resolved_at IS NULL",
                        tenant,
                        source,
                        subject);
                  return null;
                }));
  }

  ObjectNode alerts() {
    return transaction(
        () -> {
          var out = JSON.createObjectNode();
          var rows = out.putArray("items");
          jdbc.query(
              "SELECT * FROM ops.alert WHERE tenant_id=? ORDER BY last_seen DESC,id DESC LIMIT 500",
              r -> {
                var n = JSON.createObjectNode();
                n.put("id", r.getString("id"));
                n.put("source", r.getString("source"));
                n.put("subject", r.getString("subject"));
                n.put("severity", r.getString("severity"));
                for (String col :
                    List.of(
                        "first_seen",
                        "last_seen",
                        "acknowledged_at",
                        "silenced_until",
                        "resolved_at")) {
                  var t = r.getTimestamp(col);
                  n.put(col, t == null ? null : t.toInstant().toString());
                }
                rows.add(n);
              },
              tenant());
          out.put("limit", 500);
          return out;
        });
  }

  void action(UUID id, String action, int minutes) {
    OpsError.require(Set.of("acknowledge", "silence", "unsilence").contains(action));
    OpsError.require(minutes >= 1 && minutes <= 10080);
    transaction(
        () -> {
          String assignment =
              switch (action) {
                case "acknowledge" -> "acknowledged_at=now()";
                case "unsilence" -> "silenced_until=NULL";
                default -> "silenced_until=now()+interval '1 minute'*" + minutes;
              };
          if (jdbc.update(
                  "UPDATE ops.alert SET " + assignment + " WHERE tenant_id=? AND id=?",
                  tenant(),
                  id)
              != 1) throw OpsError.error("not_found", 404);
          return null;
        });
  }

  ObjectNode channels() {
    return transaction(
        () -> {
          var out = JSON.createObjectNode();
          var rows = out.putArray("items");
          jdbc.query(
              "SELECT id,name,enabled,min_severity FROM ops.notification_channel WHERE tenant_id=? ORDER BY name,id",
              r -> {
                var n = JSON.createObjectNode();
                n.put("id", r.getString("id"));
                n.put("name", r.getString("name"));
                n.put("enabled", r.getBoolean("enabled"));
                n.put("minSeverity", r.getString("min_severity"));
                n.put("configured", true);
                rows.add(n);
              },
              tenant());
          return out;
        });
  }

  void save(UUID id, String name, String url, String bearer, boolean enabled, String minSeverity) {
    OpsError.require(id.version() == 7);
    OpsError.require(
        name != null
            && !name.isBlank()
            && name.length() <= 80
            && Set.of("warning", "critical").contains(minSeverity));
    // URL, query credentials and bearer token never appear in a read response.
    OpsWebhook.target(url);
    OpsError.require(bearer.length() <= 4096 && bearer.chars().allMatch(c -> c >= 33 && c <= 126));
    String encrypted =
        secrets.seal(
            tenant() + ":" + id,
            JSON.createObjectNode().put("url", url).put("bearer", bearer).toString());
    transaction(
        () -> {
          jdbc.update(
              "INSERT INTO ops.notification_channel(id,tenant_id,name,encrypted_target,enabled,min_severity) VALUES (?,?,?,?,?,?) ON CONFLICT (tenant_id,id) DO UPDATE SET name=EXCLUDED.name,encrypted_target=EXCLUDED.encrypted_target,enabled=EXCLUDED.enabled,min_severity=EXCLUDED.min_severity",
              id,
              tenant(),
              name,
              encrypted,
              enabled,
              minSeverity);
          return null;
        });
  }

  void delete(UUID id) {
    transaction(
        () -> {
          if (jdbc.update(
                  "DELETE FROM ops.notification_channel WHERE tenant_id=? AND id=?", tenant(), id)
              != 1) throw OpsError.error("not_found", 404);
          return null;
        });
  }

  void test(UUID id) {
    String encrypted =
        transaction(
            () -> {
              var rows =
                  jdbc.queryForList(
                      "UPDATE ops.notification_channel SET last_test_at=now() WHERE tenant_id=? AND id=? AND (last_test_at IS NULL OR last_test_at < now()-interval '30 seconds') RETURNING encrypted_target",
                      String.class,
                      tenant(),
                      id);
              if (rows.isEmpty()) throw OpsError.error("test_limited", 429);
              return rows.getFirst();
            });
    var target = JSON.readTree(secrets.open(tenant() + ":" + id, encrypted));
    webhook.send(
        target.path("url").asText(),
        target.path("bearer").asText(),
        "{\"type\":\"auriga.ops.test\"}");
  }
}
