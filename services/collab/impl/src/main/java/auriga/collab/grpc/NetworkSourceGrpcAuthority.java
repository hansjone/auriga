package auriga.collab.grpc;

import auriga.collab.api.NetworkSourceAuthority;
import auriga.collab.app.NetworkErrors;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.persistence.TenantTransactionManager;
import auriga.contracts.iam.v1.NetworkAuthorityConnection;
import java.time.Instant;
import java.util.UUID;
import java.util.function.Supplier;
import org.springframework.transaction.TransactionDefinition;
import org.springframework.transaction.support.AbstractPlatformTransactionManager;
import org.springframework.transaction.support.TransactionTemplate;

/** Lexically scoped verified gateway target; no global/thread-local allow cache. */
public class NetworkSourceGrpcAuthority implements NetworkSourceAuthority {
  private record Association(NetworkAuthorityConnection connection, Target target) {}

  private static final ScopedValue<Association> CURRENT = ScopedValue.newInstance();
  private final NetworkAuthorityClient client;
  private final TransactionTemplate outside;

  public NetworkSourceGrpcAuthority(
      NetworkAuthorityClient client, TenantTransactionManager manager) {
    this.client = client;
    var suspension = new TenantTransactionManager(manager.getDataSource());
    suspension.setTransactionSynchronization(
        AbstractPlatformTransactionManager.SYNCHRONIZATION_NEVER);
    outside = new TransactionTemplate(suspension);
    outside.setPropagationBehavior(TransactionDefinition.PROPAGATION_NOT_SUPPORTED);
  }

  public Instant verify(NetworkAuthorityConnection connection) {
    return outside.execute(
        status -> NetworkAuthorityClient.deadline(client.resolve(connection, "0").getValidUntil()));
  }

  public <T> T withTarget(NetworkAuthorityConnection connection, Target target, Supplier<T> work) {
    return ScopedValue.where(CURRENT, new Association(connection, target)).call(work::get);
  }

  private Association current(UUID tenant) {
    if (!CURRENT.isBound()) throw new AurigaException(CommonErrors.UNAVAILABLE);
    var association = CURRENT.get();
    if (!association.connection().getTenantId().equals(tenant.toString()))
      throw new AurigaException(NetworkErrors.UNAUTHORIZED);
    return association;
  }

  @Override
  public Target currentTarget(UUID tenant) {
    return current(tenant).target();
  }

  @Override
  public Source currentSource(UUID tenant) {
    throw new AurigaException(CommonErrors.UNAVAILABLE);
  }

  @Override
  public Instant authorizeDelivery(UUID tenant, String message, String source, String node) {
    var association = current(tenant);
    if (!node.equals(association.target().node()))
      throw new AurigaException(NetworkErrors.UNAUTHORIZED);
    return outside.execute(status -> client.authorize(association.connection(), message, source));
  }

  @Override
  public String resolveTargetType(UUID tenant, String realm, String node, String local) {
    var association = current(tenant);
    if (!realm.equals(association.target().realm()) || !node.equals(association.target().node()))
      throw new AurigaException(NetworkErrors.UNAUTHORIZED);
    return outside.execute(
        status -> client.resolve(association.connection(), local).getTargetType());
  }
}
