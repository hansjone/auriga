package io.github.hansjone.auriga.iam;

import io.github.hansjone.auriga.common.context.TenantContext;
import io.github.hansjone.auriga.common.persistence.TenantTransactionManager;
import io.github.hansjone.auriga.common.persistence.UuidV7;
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
          "signing_key",
          "emergency_account",
          "oauth_grant",
          "account_link_log",
          "rate_limit");
  private final JdbcTemplate jdbc;
  private final TransactionTemplate transactions;

  public IamStore(JdbcTemplate jdbc, TenantTransactionManager manager) {
    this.jdbc = jdbc;
    transactions = new TransactionTemplate(manager);
  }

  public <T> T tx(Supplier<T> work) {
    TenantContext.require();
    TenantTransactionManager.verifyTransactionTenant();
    return transactions.execute(s -> work.get());
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
