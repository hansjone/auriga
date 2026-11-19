package auriga.collab.grpc;

import auriga.common.context.TenantContext;
import auriga.common.error.*;
import auriga.common.persistence.TenantTransactionManager;
import auriga.contracts.iam.v1.*;
import io.grpc.Channel;
import java.time.*;
import java.util.*;
import java.util.concurrent.TimeUnit;
import java.util.function.Supplier;
import org.springframework.transaction.TransactionDefinition;
import org.springframework.transaction.support.*;

/** Online shared-only authority. No device-owner fallback and no cached allow result. */
public class SharedMessageIamClient extends NetworkAuthorityClient {
  private final SharedMessageAuthorityServiceGrpc.SharedMessageAuthorityServiceBlockingStub shared;
  private final TransactionTemplate outside;

  public SharedMessageIamClient(Channel channel, TenantTransactionManager manager) {
    super(channel, Duration.ofSeconds(5));
    shared = SharedMessageAuthorityServiceGrpc.newBlockingStub(channel);
    var suspension = new TenantTransactionManager(manager.getDataSource());
    suspension.setTransactionSynchronization(
        AbstractPlatformTransactionManager.SYNCHRONIZATION_NEVER);
    outside = new TransactionTemplate(suspension);
    outside.setPropagationBehavior(TransactionDefinition.PROPAGATION_NOT_SUPPORTED);
  }

  private <T> T call(Supplier<T> action) {
    return outside.execute(s -> action.get());
  }

  public SharedMessageContext open(OpenSharedMessageContextRequest r) {
    return call(
        () ->
            shared.withDeadlineAfter(5, TimeUnit.SECONDS).openSharedMessageContext(r).getContext());
  }

  public SharedMessageContext read(String id) {
    return read(ReadSharedMessageContextRequest.newBuilder().setContextId(id).build());
  }

  public SharedMessageContext read(String realm, String node) {
    return read(ReadSharedMessageContextRequest.newBuilder().setRealm(realm).setNode(node).build());
  }

  private SharedMessageContext read(ReadSharedMessageContextRequest r) {
    var c =
        call(
            () ->
                shared
                    .withDeadlineAfter(5, TimeUnit.SECONDS)
                    .readSharedMessageContext(r)
                    .getContext());
    if (!c.hasBinding()
        || !TenantContext.require().toString().equals(c.getBinding().getTenantId())
        || !Instant.parse(c.getExpiresAt()).isAfter(Instant.now()))
      throw new AurigaException(CommonErrors.FORBIDDEN);
    return c;
  }

  @Override
  public Decision check(Check r) {
    var op =
        switch (r.operation()) {
          case CONNECT -> NetworkAuthorityOperation.NETWORK_AUTHORITY_OPERATION_CONNECT;
          case SEND -> NetworkAuthorityOperation.NETWORK_AUTHORITY_OPERATION_SEND;
          case RECEIVE -> NetworkAuthorityOperation.NETWORK_AUTHORITY_OPERATION_RECEIVE;
          default -> throw new AurigaException(CommonErrors.FORBIDDEN);
        };
    var request =
        CheckNetworkRequest.newBuilder()
            .setRealm(r.realm())
            .setSourceNode(r.sourceNode())
            .setTargetNode(r.targetNode())
            .setLocal(r.local())
            .setSender(NetworkAuthoritySender.NETWORK_AUTHORITY_SENDER_AGENT)
            .setOperation(op)
            .setBodyBytes(r.bodyBytes())
            .setTargetNodes(r.targetNodes())
            .setTargetLocals(r.targetLocals())
            .build();
    var result =
        call(
            () ->
                shared
                    .withDeadlineAfter(5, TimeUnit.SECONDS)
                    .checkSharedMessageNodes(
                        CheckSharedMessageNodesRequest.newBuilder().setCheck(request).build()));
    var limits = new EnumMap<Limit, Long>(Limit.class);
    for (var key : Limit.values()) {
      if (!result.containsLimits(key.name())
          || result.getLimitsOrThrow(key.name()) < 0
          || result.getLimitsOrThrow(key.name()) > key.defaultValue())
        throw new AurigaException(CommonErrors.UNAVAILABLE);
      limits.put(key, result.getLimitsOrThrow(key.name()));
    }
    return new Decision(
        result.getAllowed(), result.getAllowed() ? "allowed" : "network_denied", limits);
  }

  @Override
  public boolean active(String realm, String node) {
    try {
      read(realm, node);
      return true;
    } catch (io.grpc.StatusRuntimeException | AurigaException failure) {
      return false;
    }
  }
}
