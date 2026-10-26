package auriga.iam;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.persistence.TenantTransactionManager;
import java.math.BigInteger;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.transaction.support.TransactionSynchronizationManager;

/**
 * Tenant-local policy barrier. Caller owns the existing tenant transaction and must acquire
 * network-authority as its FIRST business lock, before identity or policy rows. Never starts a
 * transaction; the policy row lock survives until the caller commits or rolls back.
 */
final class IamNetworkPolicyRevision {
  private static final BigInteger MAX = new BigInteger("18446744073709551615");
  private final JdbcTemplate jdbc;

  IamNetworkPolicyRevision(IamStore store) {
    jdbc = store.networkJdbc();
  }

  BigInteger current() {
    TenantTransactionManager.verifyTransactionTenant();
    if (!TransactionSynchronizationManager.isActualTransactionActive()
        || !TransactionSynchronizationManager.hasResource(jdbc.getDataSource()))
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    var tenant = TenantContext.require();
    jdbc.update(
        "INSERT INTO iam.network_policy_revision(tenant_id,revision) VALUES (?,1)"
            + " ON CONFLICT (tenant_id) DO NOTHING",
        tenant);
    return jdbc.queryForObject(
        "SELECT revision::text FROM iam.network_policy_revision WHERE tenant_id=? FOR UPDATE",
        (rs, i) -> new BigInteger(rs.getString(1)),
        tenant);
  }

  BigInteger bump() {
    BigInteger previous = current();
    if (previous == null || previous.signum() <= 0 || previous.compareTo(MAX) >= 0)
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    int changed =
        jdbc.update(
            "UPDATE iam.network_policy_revision SET revision=revision+1 WHERE tenant_id=?",
            TenantContext.require());
    if (changed != 1) throw new AurigaException(CommonErrors.UNAVAILABLE);
    return previous.add(BigInteger.ONE);
  }
}
