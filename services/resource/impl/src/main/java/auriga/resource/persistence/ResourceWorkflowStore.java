package auriga.resource.persistence;

import auriga.common.context.TenantContext;
import auriga.common.error.*;
import auriga.common.persistence.UuidV7;
import java.time.*;
import java.util.*;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.jdbc.core.namedparam.NamedParameterJdbcTemplate;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.json.JsonMapper;
import tools.jackson.databind.node.ObjectNode;

/** Resource schema only. The application opens the tenant-stamped transaction. */
public final class ResourceWorkflowStore {
  public static final JsonMapper JSON = JsonMapper.builder().build();
  private final NamedParameterJdbcTemplate jdbc;

  public ResourceWorkflowStore(JdbcTemplate jdbc) {
    this.jdbc = new NamedParameterJdbcTemplate(jdbc);
  }

  public Map<String, Object> args(Object... pairs) {
    Map<String, Object> values = new HashMap<>();
    values.put("tenant", TenantContext.require());
    for (int i = 0; i < pairs.length; i += 2) values.put((String) pairs[i], pairs[i + 1]);
    return values;
  }

  public int update(String sql, Map<String, Object> args) {
    return jdbc.update(sql, args);
  }

  public void lock(String scope) {
    jdbc.query(
        "SELECT pg_advisory_xact_lock(hashtextextended(:scope,0))",
        args("scope", TenantContext.require() + ":" + scope),
        (rs, n) -> 0);
  }

  public Map<String, Object> row(String sql, Map<String, Object> args) {
    var rows = jdbc.queryForList(sql, args);
    if (rows.isEmpty()) throw new AurigaException(CommonErrors.NOT_FOUND);
    return rows.getFirst();
  }

  public List<Map<String, Object>> rows(String sql, Map<String, Object> args) {
    return jdbc.queryForList(sql, args);
  }

  public Map<String, Object> version(UUID pkg, UUID version) {
    return row(
        "SELECT * FROM resource.resource_version WHERE tenant_id=:tenant AND package_id=:pkg AND id=:id FOR UPDATE",
        args("pkg", pkg, "id", version));
  }

  public ObjectNode versionJson(Map<String, Object> v) {
    var out = JSON.createObjectNode();
    for (String key : List.of("id", "version", "digest", "state"))
      out.put(key, v.get(key).toString());
    out.put("packageId", v.get("package_id").toString());
    out.put("sizeBytes", ((Number) v.get("size_bytes")).longValue());
    out.put("mediaType", "application/gzip");
    out.put("revision", number(v, "revision"));
    Object created = v.get("created_at");
    out.put(
        "createdAt",
        (created instanceof java.sql.Timestamp t
                ? t.toInstant()
                : ((OffsetDateTime) created).toInstant())
            .toString());
    if (v.get("release_sequence") != null)
      out.put("releaseSequence", number(v, "release_sequence"));
    if (v.get("envelope") != null) out.set("resourceEnvelope", json(v, "envelope"));
    return out;
  }

  public static long number(Map<String, Object> row, String name) {
    return ((Number) row.get(name)).longValue();
  }

  public static JsonNode json(Map<String, Object> row, String name) {
    return row.get(name) == null ? JSON.nullNode() : JSON.readTree(row.get(name).toString());
  }

  public Map<String, Object> review(UUID id) {
    return row(
        "SELECT * FROM resource.resource_review WHERE tenant_id=:tenant AND id=:id FOR UPDATE",
        args("id", id));
  }

  public ObjectNode reviewJson(Map<String, Object> r) {
    return JSON.createObjectNode()
        .put("id", r.get("id").toString())
        .put("packageId", r.get("package_id").toString())
        .put("versionId", r.get("version_id").toString())
        .put("digest", r.get("digest").toString())
        .put("versionRevision", number(r, "version_revision"))
        .put("state", r.get("state").toString())
        .put("revision", number(r, "revision"));
  }

  public List<Map<String, Object>> reviews(UUID version) {
    return jdbc.queryForList(
        "SELECT * FROM resource.resource_review WHERE tenant_id=:tenant AND version_id=:id ORDER BY id",
        args("id", version));
  }

  public Optional<JsonNode> replay(
      UUID actor, String op, String key, String fingerprint, Instant now) {
    var rows =
        jdbc.queryForList(
            "SELECT fingerprint,response::text AS body FROM resource.resource_operation WHERE tenant_id=:tenant AND actor_id=:actor AND operation=:op AND request_key=:key AND expires_at>:now",
            args(
                "actor",
                actor,
                "op",
                op,
                "key",
                key,
                "now",
                OffsetDateTime.ofInstant(now, ZoneOffset.UTC)));
    if (rows.isEmpty()) return Optional.empty();
    if (!fingerprint.equals(rows.getFirst().get("fingerprint")))
      throw new AurigaException(CommonErrors.CONFLICT);
    return Optional.of(JSON.readTree(rows.getFirst().get("body").toString()));
  }

  public void remember(
      UUID actor, String op, String key, String fingerprint, JsonNode result, Instant now) {
    jdbc.update(
        "INSERT INTO resource.resource_operation(id,tenant_id,actor_id,operation,request_key,fingerprint,response,expires_at) VALUES(:id,:tenant,:actor,:op,:key,:fingerprint,CAST(:body AS jsonb),:expiry) ON CONFLICT(tenant_id,actor_id,operation,request_key) DO UPDATE SET fingerprint=excluded.fingerprint,response=excluded.response,expires_at=excluded.expires_at",
        args(
            "id",
            UuidV7.next(),
            "actor",
            actor,
            "op",
            op,
            "key",
            key,
            "fingerprint",
            fingerprint,
            "body",
            result.toString(),
            "expiry",
            OffsetDateTime.ofInstant(now.plusSeconds(86400), ZoneOffset.UTC)));
  }

  public ObjectNode revocations() {
    var rows =
        jdbc.queryForList(
            "SELECT document::text AS body FROM resource.resource_revocations WHERE tenant_id=:tenant",
            args());
    return rows.isEmpty()
        ? null
        : (ObjectNode) JSON.readTree(rows.getFirst().get("body").toString());
  }

  public void saveRevocations(ObjectNode body) {
    jdbc.update(
        "INSERT INTO resource.resource_revocations(id,tenant_id,revision,document) VALUES(:id,:tenant,:rev,CAST(:body AS jsonb)) ON CONFLICT(tenant_id) DO UPDATE SET revision=excluded.revision,document=excluded.document,needs_resign=false",
        args("id", UuidV7.next(), "rev", body.path("revision").asLong(), "body", body.toString()));
  }

  public void enqueueRevocations(long revision) {
    update(
        "INSERT INTO resource.resource_revocation_delivery(tenant_id,instance_id,revision) SELECT DISTINCT tenant_id,instance_id,:rev FROM resource.resource_distribution WHERE tenant_id=:tenant ON CONFLICT(tenant_id,instance_id) DO UPDATE SET revision=greatest(resource.resource_revocation_delivery.revision,excluded.revision)",
        args("rev", revision));
  }

  public boolean needsRevocationSignature() {
    return !rows(
            "SELECT id FROM resource.resource_revocations WHERE tenant_id=:tenant AND needs_resign",
            args())
        .isEmpty();
  }

  public List<Map<String, Object>> distributions(UUID after, int limit) {
    return jdbc.queryForList(
        "SELECT * FROM resource.resource_distribution WHERE tenant_id=:tenant AND id>:after ORDER BY id LIMIT :limit",
        args("after", after, "limit", limit));
  }

  public Map<String, Object> distribution(UUID id) {
    return row(
        "SELECT * FROM resource.resource_distribution WHERE tenant_id=:tenant AND id=:id FOR UPDATE",
        args("id", id));
  }

  public ObjectNode distributionJson(Map<String, Object> row) {
    var out =
        JSON.createObjectNode()
            .put("id", row.get("id").toString())
            .put("instanceId", row.get("instance_id").toString())
            .put("ownerId", row.get("owner_id").toString())
            .put("selectionRevision", number(row, "selection_revision"))
            .put("state", row.get("state").toString())
            .put("revision", number(row, "revision"));
    out.set("selections", json(row, "selections"));
    if (row.get("applied_generation") != null)
      out.put("appliedGeneration", number(row, "applied_generation"));
    if (row.get("generation") != null) out.put("generation", number(row, "generation"));
    if (row.get("observed_generation") != null)
      out.put("observedGeneration", number(row, "observed_generation"));
    if (row.get("ready") != null) out.put("ready", (Boolean) row.get("ready"));
    if (row.get("error_code") != null) out.put("errorCode", row.get("error_code").toString());
    return out;
  }
}
