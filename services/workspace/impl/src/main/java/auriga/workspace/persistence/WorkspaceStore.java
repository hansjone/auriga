package auriga.workspace.persistence;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.persistence.TenantTransactionManager;
import auriga.common.persistence.UuidV7;
import java.sql.Timestamp;
import java.time.Instant;
import java.util.List;
import java.util.Map;
import java.util.UUID;
import java.util.function.Supplier;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.transaction.TransactionDefinition;
import org.springframework.transaction.support.TransactionTemplate;

/** Only parameterized SQL; every unit of work binds RLS with the common transaction manager. */
public final class WorkspaceStore {
  public final JdbcTemplate jdbc;
  private final TransactionTemplate tx;
  private final TransactionTemplate fresh;

  public WorkspaceStore(JdbcTemplate jdbc, TenantTransactionManager manager) {
    this.jdbc = jdbc;
    tx = new TransactionTemplate(manager);
    fresh = new TransactionTemplate(manager);
    fresh.setPropagationBehavior(TransactionDefinition.PROPAGATION_REQUIRES_NEW);
  }

  public UUID tenant() {
    return TenantContext.require();
  }

  public <T> T tx(Supplier<T> op) {
    TenantContext.require();
    TenantTransactionManager.verifyTransactionTenant();
    return tx.execute(s -> op.get());
  }

  public <T> T in(UUID tenant, Supplier<T> op) {
    return TenantContext.call(tenant, () -> fresh.execute(s -> op.get()));
  }

  public void register(UUID id, String kind, UUID tenant) {
    if (org.springframework.transaction.support.TransactionSynchronizationManager
            .isActualTransactionActive()
        && tenant.equals(TenantContext.require())) {
      TenantTransactionManager.verifyTransactionTenant();
      jdbc.update(
          "INSERT INTO workspace_registry(id,tenant_id,resource_tenant,kind) VALUES(?,?,?,?) ON CONFLICT(id) DO NOTHING",
          id,
          TenantContext.PLATFORM_TENANT,
          tenant,
          kind);
      return;
    }
    in(
        TenantContext.PLATFORM_TENANT,
        () ->
            jdbc.update(
                "INSERT INTO workspace_registry(id,tenant_id,resource_tenant,kind) VALUES(?,?,?,?) ON CONFLICT(id) DO NOTHING",
                id,
                tenant(),
                tenant,
                kind));
  }

  public UUID route(UUID id, String kind) {
    return in(
        TenantContext.PLATFORM_TENANT,
        () -> {
          var rows =
              jdbc.queryForList(
                  "SELECT resource_tenant FROM workspace_registry WHERE tenant_id=? AND id=? AND kind=?",
                  tenant(),
                  id,
                  kind);
          if (rows.isEmpty()) throw new AurigaException(CommonErrors.UNAUTHENTICATED);
          return (UUID) rows.getFirst().get("resource_tenant");
        });
  }

  public List<UUID> tenants() {
    return in(
        TenantContext.PLATFORM_TENANT,
        () ->
            jdbc.queryForList(
                "SELECT resource_tenant FROM workspace_registry WHERE tenant_id=? AND kind='tenant'",
                UUID.class,
                tenant()));
  }

  public void lock() {
    UUID tenant = tenant();
    jdbc.update(
        "INSERT INTO workspace_lock(id,tenant_id) VALUES(?,?) ON CONFLICT(tenant_id) DO NOTHING",
        UuidV7.next(),
        tenant);
    jdbc.queryForObject(
        "SELECT id FROM workspace_lock WHERE tenant_id=? FOR UPDATE", UUID.class, tenant);
  }

  public Map<String, Object> instance(UUID id) {
    return one(
        "SELECT * FROM instance WHERE tenant_id=? AND id=? AND NOT deleted FOR UPDATE",
        tenant(),
        id);
  }

  public Map<String, Object> node(UUID id) {
    return one("SELECT * FROM node WHERE tenant_id=? AND id=? FOR UPDATE", tenant(), id);
  }

  public Map<String, Object> one(String sql, Object... args) {
    var rows = jdbc.queryForList(sql, args);
    if (rows.isEmpty()) throw new AurigaException(CommonErrors.NOT_FOUND);
    return rows.getFirst();
  }

  public static Timestamp time(Instant instant) {
    return instant == null ? null : Timestamp.from(instant);
  }

  public static Instant instant(Map<String, Object> row, String name) {
    var value = row.get(name);
    return value == null ? null : ((Timestamp) value).toInstant();
  }
}
