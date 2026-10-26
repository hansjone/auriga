package auriga.collab.network.local;

import auriga.collab.api.NetworkLocalTargets;
import auriga.collab.app.NetworkErrors;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.persistence.TenantTransactionManager;
import java.math.BigDecimal;
import java.math.BigInteger;
import java.util.Objects;
import java.util.UUID;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.transaction.support.TransactionSynchronizationManager;
import org.springframework.transaction.support.TransactionTemplate;

/** Collab's durable opaque identity evidence; contains no native host identifiers. */
public final class LocalTargets implements NetworkLocalTargets {
  private static final BigInteger MAX = new BigInteger("18446744073709551615");
  private final JdbcTemplate jdbc;
  private final TransactionTemplate tx;

  public LocalTargets(JdbcTemplate jdbc, TenantTransactionManager manager) {
    this.jdbc = Objects.requireNonNull(jdbc);
    if (jdbc.getDataSource() == null || jdbc.getDataSource() != manager.getDataSource())
      throw new IllegalArgumentException("Local targets require the collab transaction datasource");
    tx = new TransactionTemplate(manager);
  }

  @Override
  public Registration lookup(UUID tenant, String realm, String node, String local) {
    tenant(tenant);
    if (!positive(realm) || !positive(node) || !positive(local)) return null;
    if (TransactionSynchronizationManager.isActualTransactionActive()
        || TransactionSynchronizationManager.isSynchronizationActive()
        || TransactionSynchronizationManager.hasResource(jdbc.getDataSource())) {
      transaction();
      return project(witness(tenant, realm, node, local, false));
    }
    return tx.execute(s -> project(witness(tenant, realm, node, local, false)));
  }

  @Override
  public Registration lock(UUID tenant, Registration expected) {
    tenant(tenant);
    transaction();
    if (expected == null
        || !positive(expected.realm())
        || !positive(expected.node())
        || !positive(expected.local())) return null;
    var actual =
        project(witness(tenant, expected.realm(), expected.node(), expected.local(), true));
    return expected.equals(actual) ? actual : null;
  }

  /**
   * One read retains all original evidence for trusted same-impl consumers, in this transaction.
   */
  Witness witness(UUID tenant, String realm, String node, String local, boolean lock) {
    tenant(tenant);
    transaction();
    // Registration/READY take node first. Keep that order and hold health through caller commit.
    if (lock) {
      var nodes =
          jdbc.queryForList(
              "SELECT local_identity_status FROM collab.network_node WHERE tenant_id=? AND realm=? AND node_id=? FOR UPDATE",
              tenant,
              realm,
              node);
      if (nodes.isEmpty() || !"healthy".equals(nodes.getFirst().get("local_identity_status")))
        return null;
    }
    var rows =
        jdbc.queryForList(
            """
        SELECT r.id,r.allocation_op_id,r.allocation_fingerprint,r.registration_revision,
               r.realm,r.node_id,r.local_id,r.address_type
          FROM collab.network_local_registration r
          JOIN collab.network_node n USING (tenant_id,realm,node_id)
         WHERE r.tenant_id=? AND r.realm=? AND r.node_id=? AND r.local_id=?
           AND r.status='registered' AND r.address_type IN ('session','subagent')
           AND n.local_identity_status='healthy'
        """
                + (lock ? " FOR UPDATE OF r" : ""),
            tenant,
            realm,
            node,
            new BigDecimal(local));
    if (rows.isEmpty()) return null;
    var row = rows.getFirst();
    return new Witness(
        (UUID) row.get("id"),
        row.get("allocation_op_id").toString(),
        row.get("allocation_fingerprint").toString(),
        ((BigDecimal) row.get("registration_revision")).toBigIntegerExact().toString(),
        row.get("realm").toString(),
        row.get("node_id").toString(),
        ((BigDecimal) row.get("local_id")).toBigIntegerExact().toString(),
        row.get("address_type").toString());
  }

  record Witness(
      UUID registrationId,
      String allocationOpId,
      String fingerprint,
      String revision,
      String realm,
      String node,
      String local,
      String addressType) {}

  private static Registration project(Witness witness) {
    return witness == null
        ? null
        : new Registration(
            witness.realm(),
            witness.node(),
            witness.local(),
            witness.addressType(),
            witness.revision());
  }

  private void transaction() {
    if (!TransactionSynchronizationManager.isActualTransactionActive()
        || !TransactionSynchronizationManager.hasResource(jdbc.getDataSource()))
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    TenantTransactionManager.verifyTransactionTenant();
  }

  private static void tenant(UUID tenant) {
    if (tenant == null
        || TenantContext.PLATFORM_TENANT.equals(tenant)
        || TenantContext.current().filter(tenant::equals).isEmpty())
      throw new AurigaException(NetworkErrors.UNAUTHORIZED);
  }

  private static boolean positive(String value) {
    return value != null
        && value.matches("[1-9][0-9]{0,19}")
        && new BigInteger(value).compareTo(MAX) <= 0;
  }
}
