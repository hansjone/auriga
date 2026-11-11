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
  static final JsonMapper JSON = JsonMapper.builder().build();
  final JdbcTemplate jdbc;
  private final TransactionTemplate tx;
  private final TransactionTemplate fresh;
  final OpsSecrets secrets;
  final OpsWebhook webhook;

  OpsStore(
      JdbcTemplate jdbc, TenantTransactionManager manager, OpsSecrets secrets, OpsWebhook webhook) {
    this.jdbc = jdbc;
    tx = new TransactionTemplate(manager);
    fresh = new TransactionTemplate(manager);
    fresh.setPropagationBehavior(
        org.springframework.transaction.TransactionDefinition.PROPAGATION_REQUIRES_NEW);
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
    registerTenant(tenant);
    TenantContext.run(
        tenant,
        () ->
            transaction(
                () -> {
                  if (active) {
                    var row =
                        jdbc.queryForMap(
                            "INSERT INTO ops.alert(id,tenant_id,source,subject,severity,first_seen,last_seen) VALUES (?,?,?,?,?,now(),now()) ON CONFLICT (tenant_id,source,subject) WHERE resolved_at IS NULL DO UPDATE SET last_seen=now(),severity=EXCLUDED.severity,revision=ops.alert.revision+CASE WHEN ops.alert.severity='warning' AND EXCLUDED.severity='critical' THEN 1 ELSE 0 END RETURNING *",
                            UuidV7.next(),
                            tenant,
                            source,
                            subject,
                            severity);
                    if (((Number) row.get("revision")).intValue()
                        > ((Number) row.get("queued_revision")).intValue()) {
                      if (row.get("acknowledged_at") == null
                          && (row.get("silenced_until") == null
                              || ((java.sql.Timestamp) row.get("silenced_until"))
                                  .toInstant()
                                  .isBefore(java.time.Instant.now()))) {
                        var channels =
                            jdbc.queryForList(
                                "SELECT id FROM ops.notification_channel WHERE tenant_id=? AND enabled AND (min_severity='warning' OR ?='critical')",
                                UUID.class,
                                tenant,
                                severity);
                        for (UUID channel : channels) {
                          UUID delivery = UuidV7.next();
                          String payload =
                              JSON.createObjectNode()
                                  .put("type", "auriga.ops.alert")
                                  .put("deliveryId", delivery.toString())
                                  .put("alertId", row.get("id").toString())
                                  .put("source", source)
                                  .put("subject", subject)
                                  .put("severity", severity)
                                  .toString();
                          jdbc.update(
                              "INSERT INTO ops.notification_delivery(id,tenant_id,alert_id,channel_id,revision,payload) VALUES (?,?,?,?,?,?) ON CONFLICT DO NOTHING",
                              delivery,
                              tenant,
                              row.get("id"),
                              channel,
                              row.get("revision"),
                              payload);
                        }
                      }
                      jdbc.update(
                          "UPDATE ops.alert SET queued_revision=revision WHERE tenant_id=? AND id=?",
                          tenant,
                          row.get("id"));
                    }
                  } else
                    jdbc.update(
                        "UPDATE ops.alert SET resolved_at=now(),last_seen=now() WHERE tenant_id=? AND source=? AND subject=? AND resolved_at IS NULL",
                        tenant,
                        source,
                        subject);
                  return null;
                }));
  }

  private void registerTenant(UUID tenant) {
    TenantContext.call(
        TenantContext.PLATFORM_TENANT,
        () ->
            fresh.execute(
                status ->
                    jdbc.update(
                        "INSERT INTO ops.tenant_registry(id,tenant_id,resource_tenant) VALUES (?,?,?) ON CONFLICT(resource_tenant) DO NOTHING",
                        UuidV7.next(),
                        TenantContext.PLATFORM_TENANT,
                        tenant)));
  }

  public java.util.List<UUID> tenants() {
    return TenantContext.call(
        TenantContext.PLATFORM_TENANT,
        () ->
            fresh.execute(
                status ->
                    jdbc.queryForList(
                        "SELECT resource_tenant FROM ops.tenant_registry WHERE tenant_id=? ORDER BY resource_tenant",
                        UUID.class,
                        TenantContext.PLATFORM_TENANT)));
  }

  ObjectNode deliveries(UUID id) {
    return transaction(
        () -> {
          if (jdbc.queryForObject(
                  "SELECT count(*) FROM ops.alert WHERE tenant_id=? AND id=?",
                  Integer.class,
                  tenant(),
                  id)
              == 0) throw OpsError.error("not_found", 404);
          var out = JSON.createObjectNode();
          var items = out.putArray("items");
          jdbc.query(
              "SELECT d.id,d.channel_id,d.status,d.attempts,d.last_error,d.delivered_at,d.next_attempt_at,c.name FROM ops.notification_delivery d LEFT JOIN ops.notification_channel c ON c.id=d.channel_id AND c.tenant_id=d.tenant_id WHERE d.tenant_id=? AND d.alert_id=? ORDER BY d.created_at DESC LIMIT 100",
              r -> {
                var n =
                    JSON.createObjectNode()
                        .put("id", r.getString("id"))
                        .put("channelId", r.getString("channel_id"))
                        .put("channelName", r.getString("name"))
                        .put("status", r.getString("status"))
                        .put("attempts", r.getInt("attempts"))
                        .put("error", r.getString("last_error"));
                for (String col : List.of("delivered_at", "next_attempt_at")) {
                  var time = r.getTimestamp(col);
                  n.put(col, time == null ? null : time.toInstant().toString());
                }
                items.add(n);
              },
              tenant(),
              id);
          return out;
        });
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
          var out = JSON.createObjectNode().put("available", secrets.available());
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
                      "UPDATE ops.notification_channel SET last_test_at=now() WHERE tenant_id=? AND id=? AND (last_test_at IS NULL OR last_test_at < now()-interval '30 seconds') AND (last_delivery_at IS NULL OR last_delivery_at < now()-interval '30 seconds') RETURNING encrypted_target",
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
