package auriga.collab.app;

import auriga.collab.api.*;
import auriga.collab.grpc.*;
import auriga.common.error.*;
import auriga.common.persistence.TenantTransactionManager;
import auriga.contracts.iam.v1.*;
import java.time.*;
import java.util.*;
import java.util.function.Supplier;

/** Shared native evidence stays lexically scoped; ordinary owner routing is unchanged. */
public final class SharedMessageSource extends NetworkSourceGrpcAuthority {
  record Native(SharedMessageContext context, Instant until) {}

  private final ScopedValue<Native> nativeContext = ScopedValue.newInstance();
  private final SharedMessageIamClient iam;
  private final NetworkLocalTargets locals;
  private final org.springframework.jdbc.core.JdbcTemplate jdbc;
  private final org.springframework.transaction.support.TransactionTemplate tx;

  public SharedMessageSource(
      SharedMessageIamClient iam, TenantTransactionManager manager, NetworkLocalTargets locals) {
    super(iam, manager, locals);
    this.iam = iam;
    this.locals = locals;
    jdbc = new org.springframework.jdbc.core.JdbcTemplate(manager.getDataSource());
    tx = new org.springframework.transaction.support.TransactionTemplate(manager);
  }

  <T> T withNative(SharedMessageContext c, Instant until, Supplier<T> action) {
    return ScopedValue.where(nativeContext, new Native(c, until)).call(action::get);
  }

  @Override
  public VerifiedConnection verifiedConnection(NetworkAuthorityConnection c) {
    NetworkAuthorityClient.binding(c);
    if (c.getSharedContextId().isEmpty()) throw new AurigaException(NetworkErrors.UNAUTHORIZED);
    var live = iam.read(c.getSharedContextId());
    if (!live.getRealm().equals(c.getRealm()) || !live.getNode().equals(c.getNode()))
      throw new AurigaException(NetworkErrors.UNAUTHORIZED);
    Instant until = Instant.parse(live.getExpiresAt());
    if (!nativeContext.isBound()
        || !nativeContext.get().context().getContextId().equals(c.getSharedContextId()))
      throw new AurigaException(NetworkErrors.UNAUTHORIZED);
    return new VerifiedConnection(
        Collections.min(List.of(until, nativeContext.get().until())), null);
  }

  @Override
  public Source currentSource(UUID tenant) {
    if (!nativeContext.isBound()) throw new AurigaException(CommonErrors.UNAVAILABLE);
    var n = nativeContext.get();
    var c = n.context();
    if (!tenant.toString().equals(c.getBinding().getTenantId())
        || !n.until().isAfter(Instant.now())) throw new AurigaException(NetworkErrors.UNAUTHORIZED);
    return new Source(
        c.getRealm(),
        c.getNode(),
        "1",
        "session",
        "agent",
        c.getBinding().getMemberId(),
        "协作来源",
        null,
        Instant.now(),
        c.getSourceBinding(),
        Long.toUnsignedString(c.getSourceRevision()),
        Long.toUnsignedString(c.getPolicyRevision()),
        n.until());
  }

  @Override
  public String resolveTargetType(UUID tenant, String realm, String node, String local) {
    iam.read(realm, node); // shared-only first batch; unknown/ordinary identities fail closed.
    var found = locals.lookup(tenant, realm, node, local);
    if (found == null || !"session".equals(found.type()) || !"1".equals(local))
      throw new AurigaException(NetworkErrors.UNAUTHORIZED);
    return found.type();
  }

  @Override
  public Instant authorizeDelivery(
      UUID tenant, String messageJson, String sourceJson, String node) {
    var message = NetworkJson.parse(messageJson);
    var source = NetworkJson.parse(sourceJson);
    NetworkSchemas.validate("Message", message);
    NetworkSchemas.validate("SourceContext", source);
    var from = message.path("from");
    var to = message.path("to");
    var sender = iam.read(from.path("realm").asString(), from.path("node").asString());
    var receiver = iam.read(to.path("realm").asString(), node);
    var reference = message.path("authorization_ref");
    if (!tenant.toString().equals(sender.getBinding().getTenantId())
        || !sender.getBinding().getMemberId().equals(source.path("user").asString())
        || !sender.getRealm().equals(source.path("realm").asString())
        || !sender.getNode().equals(source.path("node").asString())
        || !"agent".equals(source.path("sender_kind").asString())
        || !NetworkJson.digest(message).equals(source.path("message_digest").asString())
        || !"1".equals(from.path("local").asString())
        || !"session".equals(from.path("type").asString())
        || !"1".equals(to.path("local").asString())
        || !"session".equals(to.path("type").asString())
        || !node.equals(to.path("node").asString())
        || !sender.getRealm().equals(receiver.getRealm())
        || !sender.getSourceBinding().equals(reference.path("source_binding").asString())
        || !Long.toUnsignedString(sender.getSourceRevision())
            .equals(reference.path("source_revision").asString())
        || !"unicast".equals(message.path("message_class").asString()))
      throw new AurigaException(NetworkErrors.UNAUTHORIZED);
    var decision =
        iam.check(
            new auriga.iam.api.NetworkAuthority.Check(
                sender.getRealm(),
                sender.getNode(),
                node,
                "1",
                auriga.iam.api.NetworkAuthority.Sender.AGENT,
                auriga.iam.api.NetworkAuthority.Operation.SEND,
                NetworkJson.canonical(message.path("body"))
                    .getBytes(java.nio.charset.StandardCharsets.UTF_8)
                    .length,
                1,
                1));
    if (!decision.allowed()) throw new AurigaException(NetworkErrors.UNAUTHORIZED);
    tx.executeWithoutResult(
        s -> {
          var registered =
              jdbc.queryForList(
                  """
          SELECT l.id FROM collab.network_local_registration l
          JOIN collab.network_node n ON n.tenant_id=l.tenant_id AND n.realm=l.realm AND n.node_id=l.node_id
          WHERE l.tenant_id=? AND l.realm=? AND l.node_id=? AND l.local_id=1
            AND l.status='registered' AND l.address_type='session' AND l.receive_enabled
            AND n.local_identity_status='healthy' AND n.wire_status='ready'
          FOR UPDATE OF l,n
          """,
                  tenant,
                  receiver.getRealm(),
                  receiver.getNode());
          if (registered.size() != 1) throw new AurigaException(NetworkErrors.UNAUTHORIZED);
        });
    return Collections.min(
        List.of(
            Instant.parse(sender.getExpiresAt()),
            Instant.parse(receiver.getExpiresAt()),
            Instant.parse(message.path("expires_at").asString()),
            Instant.now().plusSeconds(30)));
  }
}
