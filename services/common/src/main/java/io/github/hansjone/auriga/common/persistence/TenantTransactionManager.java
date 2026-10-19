package io.github.hansjone.auriga.common.persistence;

import io.github.hansjone.auriga.common.context.TenantContext;
import java.sql.Connection;
import java.sql.PreparedStatement;
import java.sql.SQLException;
import java.util.ArrayDeque;
import java.util.Deque;
import java.util.Optional;
import java.util.UUID;
import javax.sql.DataSource;
import org.springframework.jdbc.support.JdbcTransactionManager;
import org.springframework.transaction.TransactionDefinition;
import org.springframework.transaction.support.TransactionSynchronizationManager;

/**
 * Transaction manager that stamps every transaction with the current tenant for PostgreSQL row
 * level security: {@code set_config('auriga.tenant_id', <tenant>, true)}, i.e. {@code SET LOCAL},
 * which PostgreSQL discards at commit/rollback, so a pooled connection never carries a tenant into
 * the next transaction. Without a tenant (system scope or no binding) the value is set to the empty
 * string, which the RLS policy treats as "no rows".
 *
 * <p>The tenant a transaction started with is also registered ("transaction tenant"). Code that
 * re-binds a different tenant inside that transaction and then touches tenant data (MyBatis-Plus
 * tenant line, outbox publish) fails with {@link TenantMismatchException}: the database would still
 * enforce the old tenant. Switching tenants needs a new transaction ({@code REQUIRES_NEW}).
 *
 * <p>Tenant data must be accessed inside transactions; auto-commit statements see no tenant rows at
 * all (fail closed).
 */
public class TenantTransactionManager extends JdbcTransactionManager {
  public static final String TENANT_SETTING = "auriga.tenant_id";

  /** Thread-bound stack of transaction tenants (nested REQUIRES_NEW pushes a new entry). */
  private static final Object TRANSACTION_TENANTS = new Object();

  public TenantTransactionManager(DataSource dataSource) {
    super(dataSource);
  }

  @Override
  protected void prepareTransactionalConnection(Connection con, TransactionDefinition definition)
      throws SQLException {
    super.prepareTransactionalConnection(con, definition);
    Optional<UUID> tenant = TenantContext.current();
    applyTenant(con, tenant.map(Object::toString).orElse(""));
    stack(true).push(tenant);
  }

  @Override
  protected void doCleanupAfterCompletion(Object transaction) {
    try {
      Deque<Optional<UUID>> stack = stack(false);
      if (stack != null) {
        stack.poll();
        if (stack.isEmpty()) TransactionSynchronizationManager.unbindResource(TRANSACTION_TENANTS);
      }
    } finally {
      super.doCleanupAfterCompletion(transaction);
    }
  }

  @SuppressWarnings("unchecked")
  private static Deque<Optional<UUID>> stack(boolean create) {
    Object existing = TransactionSynchronizationManager.getResource(TRANSACTION_TENANTS);
    if (existing != null || !create) return (Deque<Optional<UUID>>) existing;
    Deque<Optional<UUID>> stack = new ArrayDeque<>();
    TransactionSynchronizationManager.bindResource(TRANSACTION_TENANTS, stack);
    return stack;
  }

  /**
   * Fails when the tenant bound now differs from the tenant the current transaction started with.
   * No-op outside transactions managed by this class.
   */
  public static void verifyTransactionTenant() {
    if (!TransactionSynchronizationManager.isActualTransactionActive()) return;
    Deque<Optional<UUID>> stack = stack(false);
    if (stack == null || stack.isEmpty()) return;
    Optional<UUID> started = stack.peek();
    Optional<UUID> now = TenantContext.current();
    if (!started.equals(now)) {
      throw new TenantMismatchException(
          "Transaction started for tenant "
              + started.map(Object::toString).orElse("<none>")
              + " but tenant "
              + now.map(Object::toString).orElse("<none>")
              + " is bound now; start a new transaction (REQUIRES_NEW) to switch tenants");
    }
  }

  /** Sets the transaction-local tenant on a connection inside an open transaction. */
  public static void applyTenant(Connection con, String tenantId) throws SQLException {
    try (PreparedStatement ps = con.prepareStatement("SELECT set_config(?, ?, true)")) {
      ps.setString(1, TENANT_SETTING);
      ps.setString(2, tenantId);
      ps.execute();
    }
  }

  /** The bound tenant changed inside a transaction. */
  public static final class TenantMismatchException extends IllegalStateException {
    public TenantMismatchException(String message) {
      super(message);
    }
  }
}
