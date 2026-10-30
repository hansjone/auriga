package auriga.collab.grpc;

import auriga.collab.api.NetworkLocalTargets;
import auriga.collab.api.NetworkSourceAuthority;
import auriga.collab.app.NetworkErrors;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.persistence.TenantTransactionManager;
import auriga.contracts.iam.v1.NetworkAuthorityConnection;
import auriga.contracts.iam.v1.NetworkConnectionAnchor;
import auriga.contracts.iam.v1.ReadNetworkConnectionAnchorResponse;
import java.time.Instant;
import java.util.UUID;
import java.util.function.Supplier;

/** Lexically scoped verified gateway target; no global/thread-local allow cache. */
public class NetworkSourceGrpcAuthority implements NetworkSourceAuthority {
  private record Association(
      NetworkAuthorityConnection connection, Target target, boolean broadcast) {}

  private static final ScopedValue<Association> CURRENT = ScopedValue.newInstance();
  private final NetworkAuthorityClient client;
  private final javax.sql.DataSource dataSource;
  private final NetworkLocalTargets locals;

  public NetworkSourceGrpcAuthority(
      NetworkAuthorityClient client, TenantTransactionManager manager) {
    this(client, manager, null);
  }

  public NetworkSourceGrpcAuthority(
      NetworkAuthorityClient client, TenantTransactionManager manager, NetworkLocalTargets locals) {
    this.client = client;
    this.locals = locals;
    dataSource = manager.getDataSource();
  }

  public Instant verify(NetworkAuthorityConnection connection) {
    return verifiedConnection(connection).deadline();
  }

  public record VerifiedConnection(Instant deadline, NetworkConnectionAnchor anchor) {}

  public VerifiedConnection verifiedConnection(NetworkAuthorityConnection connection) {
    outsideDatabase();
    var response = client.resolve(connection, "0");
    return new VerifiedConnection(
        NetworkAuthorityClient.deadline(response.getValidUntil()),
        response.hasConnectionAnchor() ? response.getConnectionAnchor() : null);
  }

  public ReadNetworkConnectionAnchorResponse readConnectionAnchor(
      NetworkConnectionAnchor original, NetworkConnectionAnchor current) {
    outsideDatabase();
    return client.readConnectionAnchor(original, current);
  }

  public auriga.contracts.iam.v1.RegisterAgentSourceResponse registerSource(
      auriga.contracts.iam.v1.RegisterAgentSourceRequest request) {
    outsideDatabase();
    return client.registerSource(request);
  }

  public auriga.contracts.iam.v1.RegisterAgentSourceResponse readSourceReceipt(
      auriga.contracts.iam.v1.RegisterAgentSourceRequest original) {
    outsideDatabase();
    return client.readSourceReceipt(original);
  }

  public auriga.contracts.iam.v1.GetAgentSourceResponse getSource(
      auriga.contracts.iam.v1.GetAgentSourceRequest request) {
    outsideDatabase();
    return client.getSource(request);
  }

  public auriga.contracts.iam.v1.RegisterAgentSourceResponse fenceSourceReceipt(
      auriga.contracts.iam.v1.RegisterAgentSourceRequest original) {
    outsideDatabase();
    return client.fenceSourceReceipt(original);
  }

  public auriga.contracts.iam.v1.RevokeAgentSourceResponse revokeSource(
      auriga.contracts.iam.v1.RevokeAgentSourceRequest request) {
    outsideDatabase();
    return client.revokeSource(request);
  }

  public <T> T sourceRequest(Supplier<T> work) {
    return client.sourceRequest(work);
  }

  public <T> T withTarget(NetworkAuthorityConnection connection, Target target, Supplier<T> work) {
    return withTarget(connection, target, false, work);
  }

  public <T> T withTarget(
      NetworkAuthorityConnection connection, Target target, boolean broadcast, Supplier<T> work) {
    return ScopedValue.where(CURRENT, new Association(connection, target, broadcast))
        .call(work::get);
  }

  private void outsideDatabase() {
    if (org.springframework.transaction.support.TransactionSynchronizationManager
            .isActualTransactionActive()
        || org.springframework.transaction.support.TransactionSynchronizationManager
            .isSynchronizationActive()
        || org.springframework.transaction.support.TransactionSynchronizationManager.hasResource(
            dataSource)) throw new AurigaException(CommonErrors.UNAVAILABLE);
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
    outsideDatabase();
    var association = current(tenant);
    if (!node.equals(association.target().node()))
      throw new AurigaException(NetworkErrors.UNAUTHORIZED);
    if (!association.broadcast()
        && "broadcast"
            .equals(auriga.collab.app.NetworkJson.parse(message).path("message_class").asString()))
      throw new AurigaException(NetworkErrors.UNAUTHORIZED);
    return client.authorize(association.connection(), message, source);
  }

  @Override
  public String resolveTargetType(UUID tenant, String realm, String node, String local) {
    return localSnapshot(tenant, realm, node, local).type();
  }

  public NetworkLocalTargets.Registration localSnapshot(
      UUID tenant, String realm, String node, String local) {
    var association = current(tenant);
    if (!realm.equals(association.target().realm()) || !node.equals(association.target().node()))
      throw new AurigaException(NetworkErrors.UNAUTHORIZED);
    if ("0".equals(local))
      return new NetworkLocalTargets.Registration(realm, node, local, "system", "sys");
    if (locals == null) throw new AurigaException(CommonErrors.UNAVAILABLE);
    var registration = locals.lookup(tenant, realm, node, local);
    if (registration == null
        || !realm.equals(registration.realm())
        || !node.equals(registration.node())
        || !local.equals(registration.local())
        || !java.util.Set.of("session", "subagent").contains(registration.type())
        || registration.revision() == null
        || registration.revision().isBlank()) throw new AurigaException(NetworkErrors.UNAUTHORIZED);
    return registration;
  }

  public void revalidateLocal(UUID tenant, NetworkLocalTargets.Registration expected) {
    var association = current(tenant);
    if (!expected.realm().equals(association.target().realm())
        || !expected.node().equals(association.target().node()))
      throw new AurigaException(NetworkErrors.UNAUTHORIZED);
    if ("0".equals(expected.local())) return;
    if (!org.springframework.transaction.support.TransactionSynchronizationManager
            .isActualTransactionActive()
        || !org.springframework.transaction.support.TransactionSynchronizationManager.hasResource(
            dataSource)) throw new AurigaException(CommonErrors.UNAVAILABLE);
    if (locals == null) throw new AurigaException(CommonErrors.UNAVAILABLE);
    var locked = locals.lock(tenant, expected);
    if (locked == null) throw new AurigaException(CommonErrors.UNAVAILABLE);
    if (!expected.equals(locked)) throw new AurigaException(NetworkErrors.CONFLICT);
  }
}
