package auriga.iam;

import auriga.common.context.TenantContext;
import auriga.common.persistence.TenantTransactionManager;
import auriga.common.persistence.UuidV7;
import java.time.*;
import java.util.*;
import java.util.function.Supplier;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.transaction.support.TransactionTemplate;
import tools.jackson.databind.node.ObjectNode;

/** Fixed table allowlist, parameterized SQL, explicit tenant predicate plus FORCE RLS. */
public final class IamStore {
  private static final Set<String> TABLES =
      Set.of(
          "account",
          "idp_provider",
          "identity",
          "login_tx",
          "session",
          "session_kv",
          "device",
          "managed_device",
          "signing_key",
          "emergency_account",
          "oauth_grant",
          "account_link_log",
          "rate_limit",
          "team_account",
          "team_totp",
          "team_passkey",
          "team_ceremony",
          "qr_challenge",
          "network_ticket",
          "network_ingress",
          "network_policy",
          "network_grant",
          "network_config");
  private final JdbcTemplate jdbc;
  private final TransactionTemplate transactions;
  private final ScopedValue<Set<String>> responseProjections = ScopedValue.newInstance();

  public IamStore(JdbcTemplate jdbc, TenantTransactionManager manager) {
    this.jdbc = jdbc;
    transactions = new TransactionTemplate(manager);
  }

  JdbcTemplate networkJdbc() {
    return jdbc;
  }

  public <T> T tx(Supplier<T> work) {
    TenantContext.require();
    TenantTransactionManager.verifyTransactionTenant();
    return transactions.execute(s -> work.get());
  }

  /** An explicit response owner, never a transaction synchronization callback. */
  <T> T responseTx(SessionKv projection, Supplier<T> work) {
    if (responseProjections.isBound()) return tx(work);
    requireTransactionReleased();
    var hashes = new LinkedHashSet<String>();
    // Unbind before Spring invokes commit/completion callbacks: those still hold resources,
    // but must never impersonate nested business work inside an owned response.
    T result = tx(() -> ScopedValue.where(responseProjections, hashes).call(work::get));
    requireTransactionReleased();
    projection.awaitProjected(hashes);
    return result;
  }

  // A response that waits for another transaction must own its transaction boundary. Even an
  // afterCompletion callback can still own the connection until Spring finishes cleanup.
  void requireTransactionReleased() {
    var dataSource = jdbc.getDataSource();
    if (org.springframework.transaction.support.TransactionSynchronizationManager
            .isActualTransactionActive()
        || org.springframework.transaction.support.TransactionSynchronizationManager
            .isSynchronizationActive()
        || (dataSource != null
            && org.springframework.transaction.support.TransactionSynchronizationManager
                .hasResource(dataSource)))
      throw new auriga.common.error.AurigaException(auriga.common.error.CommonErrors.UNAVAILABLE);
  }

  public <T> T tenant(UUID tenant, Supplier<T> work) {
    return TenantContext.call(tenant, () -> tx(work));
  }

  public <T> T isolated(UUID tenant, Supplier<T> work) {
    TransactionTemplate isolated = new TransactionTemplate(transactions.getTransactionManager());
    isolated.setPropagationBehavior(
        org.springframework.transaction.TransactionDefinition.PROPAGATION_REQUIRES_NEW);
    return TenantContext.call(tenant, () -> isolated.execute(s -> work.get()));
  }

  void enqueueSessionProjection(String hash, Instant now) {
    TenantTransactionManager.verifyTransactionTenant();
    if (!org.springframework.transaction.support.TransactionSynchronizationManager
        .isActualTransactionActive())
      throw new IllegalStateException("Session projection requires a transaction");
    UUID tenant = TenantContext.require();
    if (responseProjections.isBound()) responseProjections.get().add(hash);
    jdbc.update(
        "INSERT INTO iam.session_projection_tenant(tenant_id) VALUES (?) ON CONFLICT DO NOTHING",
        tenant);
    jdbc.update(
        "INSERT INTO iam.session_projection(id,tenant_id,cookie_hash,next_attempt_at) VALUES (?,?,?,?)"
            + " ON CONFLICT (tenant_id,cookie_hash) DO UPDATE SET attempts=0,next_attempt_at=excluded.next_attempt_at",
        UuidV7.next(),
        tenant,
        hash,
        java.sql.Timestamp.from(now));
  }

  // This directory contains infrastructure tenant IDs only; never read session data outside RLS.
  List<UUID> sessionProjectionTenants(UUID after, int limit) {
    return jdbc.query(
        "SELECT tenant_id FROM iam.session_projection_tenant WHERE (?::uuid IS NULL OR tenant_id>?) ORDER BY tenant_id LIMIT ?",
        (rs, i) -> rs.getObject(1, UUID.class),
        after,
        after,
        limit);
  }

  record SessionProjection(UUID id, String hash, int attempts) {}

  SessionProjection claimSessionProjection(Instant now) {
    var rows =
        jdbc.query(
            "SELECT id,cookie_hash,attempts FROM iam.session_projection WHERE tenant_id=? AND next_attempt_at<=?"
                + " ORDER BY next_attempt_at,id LIMIT 1 FOR UPDATE SKIP LOCKED",
            (rs, i) ->
                new SessionProjection(rs.getObject(1, UUID.class), rs.getString(2), rs.getInt(3)),
            TenantContext.require(),
            java.sql.Timestamp.from(now));
    return rows.isEmpty() ? null : rows.getFirst();
  }

  SessionProjection claimSessionProjection(String hash, Instant now) {
    var rows =
        jdbc.query(
            "SELECT id,cookie_hash,attempts FROM iam.session_projection WHERE tenant_id=? AND cookie_hash=? AND next_attempt_at<=? FOR UPDATE SKIP LOCKED",
            (rs, i) ->
                new SessionProjection(rs.getObject(1, UUID.class), rs.getString(2), rs.getInt(3)),
            TenantContext.require(),
            hash,
            java.sql.Timestamp.from(now));
    return rows.isEmpty() ? null : rows.getFirst();
  }

  boolean sessionProjectionPending(String hash) {
    return Boolean.TRUE.equals(
        jdbc.queryForObject(
            "SELECT EXISTS(SELECT 1 FROM iam.session_projection WHERE tenant_id=? AND cookie_hash=?)",
            Boolean.class,
            TenantContext.require(),
            hash));
  }

  void completeSessionProjection(UUID id) {
    jdbc.update(
        "DELETE FROM iam.session_projection WHERE tenant_id=? AND id=?",
        TenantContext.require(),
        id);
  }

  void retrySessionProjection(SessionProjection row, Instant next) {
    jdbc.update(
        "UPDATE iam.session_projection SET attempts=LEAST(attempts+1,30),next_attempt_at=? WHERE tenant_id=? AND id=?",
        java.sql.Timestamp.from(next),
        TenantContext.require(),
        row.id());
  }

  private String table(String table) {
    if (!TABLES.contains(table)) throw new IllegalArgumentException("Unknown IAM table");
    return "iam.\"" + table + "\"";
  }

  public ObjectNode insert(String table, String lookup, ObjectNode data, Instant expires) {
    UUID id = lookup.matches("[0-9a-fA-F-]{36}") ? UUID.fromString(lookup) : UuidV7.next();
    data.put("id", id.toString());
    jdbc.update(
        "INSERT INTO "
            + table(table)
            + " (id,tenant_id,lookup_key,data,expires_at) VALUES (?,?,?,?::jsonb,?)",
        id,
        TenantContext.require(),
        lookup,
        data.toString(),
        expires == null ? null : java.sql.Timestamp.from(expires));
    return data;
  }

  public ObjectNode get(String table, String lookup, boolean lock) {
    var rows =
        jdbc.query(
            "SELECT data::text FROM "
                + table(table)
                + " WHERE tenant_id=? AND lookup_key=?"
                + (lock ? " FOR UPDATE" : ""),
            (rs, i) -> Json.read(rs.getString(1)),
            TenantContext.require(),
            lookup);
    return rows.isEmpty() ? null : rows.getFirst();
  }

  public List<ObjectNode> all(String table) {
    return jdbc.query(
        "SELECT data::text FROM " + table(table) + " WHERE tenant_id=?",
        (rs, i) -> Json.read(rs.getString(1)),
        TenantContext.require());
  }

  public ObjectNode byId(String table, String id, boolean lock) {
    if (id == null || !id.matches("[0-9a-fA-F-]{36}")) return null;
    var rows =
        jdbc.query(
            "SELECT data::text FROM "
                + table(table)
                + " WHERE tenant_id=? AND id=?"
                + (lock ? " FOR UPDATE" : ""),
            (rs, i) -> Json.read(rs.getString(1)),
            TenantContext.require(),
            UUID.fromString(id));
    return rows.isEmpty() ? null : rows.getFirst();
  }

  public void save(String table, ObjectNode data) {
    jdbc.update(
        "UPDATE " + table(table) + " SET data=?::jsonb WHERE tenant_id=? AND id=?",
        data.toString(),
        TenantContext.require(),
        UUID.fromString(data.path("id").asString()));
  }

  public <T> T atomic(Supplier<T> work) {
    TransactionTemplate nested = new TransactionTemplate(transactions.getTransactionManager());
    nested.setPropagationBehavior(
        org.springframework.transaction.TransactionDefinition.PROPAGATION_NESTED);
    return nested.execute(s -> work.get());
  }

  public void delete(String table, String lookup) {
    jdbc.update(
        "DELETE FROM " + table(table) + " WHERE tenant_id=? AND lookup_key=?",
        TenantContext.require(),
        lookup);
  }

  public void lock(String namespace) {
    jdbc.queryForObject(
        "SELECT pg_advisory_xact_lock(hashtextextended(?,0)) IS NULL",
        Boolean.class,
        TenantContext.require() + ":" + namespace);
  }

  public void limit(String key, Instant now, int maximum) {
    // Authentication failures must not roll back their own attempt counter.
    isolated(
        TenantContext.require(),
        () -> {
          String bucket = Secrets.hash(key) + ":" + now.getEpochSecond() / 300;
          lock(bucket);
          ObjectNode counter = get("rate_limit", bucket, true);
          if (counter == null)
            insert("rate_limit", bucket, Json.object("count", 1), now.plusSeconds(600));
          else {
            IamError.require(counter.path("count").asInt() < maximum, "rate_limited", 429);
            counter.put("count", counter.path("count").asInt() + 1);
            save("rate_limit", counter);
          }
          return null;
        });
  }
}
