package auriga.audit;

import auriga.common.context.RequestContext;
import auriga.common.context.TraceContext;
import auriga.common.events.ReceivedEvent;
import auriga.common.observability.SensitiveDataMasker;
import auriga.common.persistence.UuidV7;
import java.nio.charset.StandardCharsets;
import java.sql.Timestamp;
import java.time.Instant;
import java.util.*;
import javax.sql.DataSource;
import org.flywaydb.core.Flyway;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.jdbc.support.JdbcTransactionManager;
import org.springframework.transaction.support.TransactionTemplate;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.json.JsonMapper;

/** Dedicated audit schema. Cross-tenant reads are exposed only through the admin controller. */
public class AuditStore implements auriga.audit.api.UacAuditEvents {
  private final JdbcTemplate jdbc;
  private final TransactionTemplate tx;
  private final JsonMapper json = JsonMapper.builder().build();

  public AuditStore(DataSource ds) {
    this(ds, true);
  }

  public AuditStore(DataSource ds, boolean migrate) {
    if (migrate) {
      auriga.common.persistence.CommonMigrations.migrate(ds, "audit");
      auriga.common.persistence.CommonMigrations.serviceFlyway(
              Flyway.configure()
                  .dataSource(ds)
                  .schemas("audit")
                  .defaultSchema("audit")
                  .locations("classpath:db/audit")
                  .load())
          .migrate();
    }
    jdbc = new JdbcTemplate(ds);
    tx = new TransactionTemplate(new JdbcTransactionManager(ds));
  }

  public record Page(List<JsonNode> items, String nextCursor) {}

  public void accept(ReceivedEvent e) {
    if (!"audit.recorded".equals(e.type()) || e.time() == null)
      throw new IllegalArgumentException("Invalid audit event");
    var data = e.data();
    if (!data.path("action").isString()
        || !Set.of("success", "failure", "denied").contains(data.path("result").asText()))
      throw new IllegalArgumentException("Invalid audit data");
    var safe = json.createObjectNode();
    // Only standardized metadata is retained. Free-form changes/details may contain secrets.
    for (String field : List.of("actor", "target")) {
      var part = json.createObjectNode();
      for (String key :
          field.equals("actor") ? List.of("type", "id", "ip") : List.of("type", "id", "name")) {
        var v = data.path(field).path(key);
        if (v.isString()) part.put(key, SensitiveDataMasker.mask(v.asText()));
      }
      safe.set(field, part);
    }
    safe.put("action", SensitiveDataMasker.mask(data.path("action").asText()));
    safe.put("result", data.path("result").asText());
    safe.put("detailsRedacted", true);
    var uac = UacAuditProjection.project(data);
    if (uac != null) safe.set("details", uac);
    safe.put("id", e.id().toString());
    safe.put("tenantId", e.tenantId().toString());
    var time = e.time().truncatedTo(java.time.temporal.ChronoUnit.MICROS);
    safe.put("time", time.toString());
    safe.put("source", e.source());
    safe.put("traceId", TraceContext.parse(e.traceparent()).map(TraceContext::traceId).orElse(""));
    tx.executeWithoutResult(s -> insert(e.id(), e.tenantId(), time, safe));
  }

  private void insert(UUID id, UUID tenant, Instant time, JsonNode node) {
    jdbc.queryForObject(
        "SELECT set_config('auriga.tenant_id', ?, true)", String.class, tenant.toString());
    jdbc.update(
        "INSERT INTO audit.event(id,tenant_id,occurred_at,actor,action,result,payload) VALUES (?,?,?,?,?,?,?::jsonb) ON CONFLICT(id) DO NOTHING",
        id,
        tenant,
        Timestamp.from(time),
        node.path("actor").path("id").asText(""),
        node.path("action").asText(),
        node.path("result").asText(),
        node.toString());
  }

  @Override
  public auriga.audit.api.UacAuditEvents.Page readUac(
      Instant from, Instant to, UUID provider, UUID correlation, int limit) {
    var tenant = auriga.common.context.TenantContext.require();
    var principal =
        auriga.common.security.AuthorizationRules.currentPrincipal()
            .orElseThrow(
                () ->
                    new auriga.common.error.AurigaException(
                        auriga.common.error.CommonErrors.UNAUTHENTICATED));
    if (!principal.tenantId().equals(tenant)
        || !principal.isAdminContext()
        || !principal.hasPermission("audit.events.read"))
      throw new auriga.common.error.AurigaException(auriga.common.error.CommonErrors.FORBIDDEN);
    if (from == null
        || to == null
        || !from.isBefore(to)
        || java.time.Duration.between(from, to).compareTo(java.time.Duration.ofDays(1)) > 0
        || limit < 1
        || limit > 10000) throw new IllegalArgumentException("Invalid UAC audit range");
    return tx.execute(
        s -> {
          jdbc.queryForObject(
              "SELECT set_config('auriga.tenant_id',?,true)", String.class, tenant.toString());
          var values =
              new ArrayList<Object>(
                  List.of(tenant, Timestamp.from(from), Timestamp.from(to), provider.toString()));
          String sql =
              "SELECT payload::text FROM audit.event WHERE tenant_id=? AND occurred_at>=? AND occurred_at<? AND action LIKE 'iam.uac.%' AND (payload->'details'->>'providerId'=? OR payload->'target'->>'id'=?)";
          values.add(provider.toString());
          if (correlation != null) {
            sql += " AND payload->'details'->>'correlationId'=?";
            values.add(correlation.toString());
          }
          sql += " ORDER BY occurred_at,id LIMIT ?";
          values.add(limit + 1);
          var rows = jdbc.query(sql, (rs, i) -> rs.getString(1), values.toArray());
          recordAccess("audit.events.export");
          return new auriga.audit.api.UacAuditEvents.Page(
              List.copyOf(rows.subList(0, Math.min(rows.size(), limit))), rows.size() > limit);
        });
  }

  public Page query(
      Instant from,
      Instant to,
      String actor,
      String action,
      String result,
      String q,
      String cursor,
      int limit,
      boolean export) {
    if (limit < 1
        || limit > (export ? 1000 : 100)
        || (from != null && to != null && !from.isBefore(to)))
      throw new IllegalArgumentException("Invalid range or limit");
    for (String s : List.of(actor, action, result, q))
      if (s.length() > 200) throw new IllegalArgumentException("Filter too long");
    if (!result.isEmpty() && !Set.of("success", "failure", "denied").contains(result))
      throw new IllegalArgumentException("Invalid result");
    return tx.execute(
        status -> {
          jdbc.queryForObject(
              "SELECT set_config('auriga.audit_admin_read', 'true', true)", String.class);
          var args = new ArrayList<Object>();
          var sql = new StringBuilder("SELECT payload::text FROM audit.event WHERE true");
          if (from != null) {
            sql.append(" AND occurred_at>=?");
            args.add(Timestamp.from(from));
          }
          if (to != null) {
            sql.append(" AND occurred_at<?");
            args.add(Timestamp.from(to));
          }
          for (var pair :
              List.of(
                  new String[] {"actor", actor},
                  new String[] {"action", action},
                  new String[] {"result", result}))
            if (!pair[1].isEmpty()) {
              sql.append(" AND ").append(pair[0]).append("=?");
              args.add(pair[1]);
            }
          if (!q.isEmpty()) {
            sql.append(" AND strpos(lower(payload::text),lower(?))>0");
            args.add(q);
          }
          if (cursor != null && !cursor.isEmpty()) {
            try {
              var parts =
                  new String(Base64.getUrlDecoder().decode(cursor), StandardCharsets.UTF_8)
                      .split("\\|", -1);
              if (parts.length != 2) throw new IllegalArgumentException();
              sql.append(" AND (occurred_at,id)<(?,?)");
              args.add(Timestamp.from(Instant.parse(parts[0])));
              args.add(UUID.fromString(parts[1]));
            } catch (RuntimeException ex) {
              throw new IllegalArgumentException("Invalid cursor", ex);
            }
          }
          sql.append(" ORDER BY occurred_at DESC,id DESC LIMIT ?");
          args.add(limit + 1);
          var rows =
              jdbc.query(sql.toString(), (rs, n) -> json.readTree(rs.getString(1)), args.toArray());
          String next = null;
          if (rows.size() > limit) {
            if (export)
              throw new IllegalArgumentException("Export exceeds 1000 rows; narrow filters");
            rows = new ArrayList<>(rows.subList(0, limit));
            var last = rows.getLast();
            next =
                Base64.getUrlEncoder()
                    .withoutPadding()
                    .encodeToString(
                        (last.path("time").asText() + "|" + last.path("id").asText())
                            .getBytes(StandardCharsets.UTF_8));
          }
          recordAccess(export ? "audit.events.export" : "audit.events.query");
          return new Page(List.copyOf(rows), next);
        });
  }

  public JsonNode detail(UUID id) {
    return tx.execute(
        s -> {
          jdbc.queryForObject(
              "SELECT set_config('auriga.audit_admin_read', 'true', true)", String.class);
          var rows =
              jdbc.query(
                  "SELECT payload::text FROM audit.event WHERE id=?",
                  (rs, n) -> json.readTree(rs.getString(1)),
                  id);
          recordAccess("audit.events.detail");
          return rows.isEmpty() ? null : rows.getFirst();
        });
  }

  public void recordDenied() {
    tx.executeWithoutResult(status -> recordAccess("audit.events.denied", "denied"));
  }

  private void recordAccess(String action) {
    recordAccess(action, "success");
  }

  private void recordAccess(String action, String result) {
    var ctx = RequestContext.current().orElseThrow();
    var p = ctx.principal();
    var tenant = p == null ? auriga.common.context.TenantContext.PLATFORM_TENANT : p.tenantId();
    var node = json.createObjectNode();
    var id = UuidV7.next();
    var now = Instant.now().truncatedTo(java.time.temporal.ChronoUnit.MICROS);
    node.put("id", id.toString());
    node.put("tenantId", tenant.toString());
    node.put("time", now.toString());
    node.put("action", action);
    node.put("result", result);
    node.put("source", "auriga/audit");
    node.put("traceId", ctx.trace().traceId());
    var actor =
        json.createObjectNode()
            .put("type", p == null ? "system" : p.type().claimValue())
            .put("id", p == null ? "anonymous" : p.subject());
    if (ctx.clientIp() != null) actor.put("ip", ctx.clientIp());
    node.set("actor", actor);
    insert(id, tenant, now, node);
  }
}
