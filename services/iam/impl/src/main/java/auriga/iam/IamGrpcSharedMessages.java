package auriga.iam;

import auriga.common.context.TenantContext;
import auriga.common.error.*;
import auriga.common.security.*;
import auriga.contracts.iam.v1.*;
import auriga.iam.api.CredentialInstallationScope;
import io.grpc.stub.StreamObserver;

/** Exact collab-only internal authority, separate from the ordinary device owner path. */
public final class IamGrpcSharedMessages
    extends SharedMessageAuthorityServiceGrpc.SharedMessageAuthorityServiceImplBase {
  private final SharedMessages messages;
  private final CredentialInstallationScope installation;

  IamGrpcSharedMessages(SharedMessages messages, CredentialInstallationScope installation) {
    this.messages = messages;
    this.installation = installation;
  }

  private void caller() {
    var p =
        AuthorizationRules.currentPrincipal()
            .orElseThrow(() -> new AurigaException(CommonErrors.UNAUTHENTICATED));
    if (!p.isInternalService()
        || !"svc:collab".equals(p.subject())
        || !TenantContext.PLATFORM_TENANT.equals(p.tenantId())
        || TenantContext.current().filter(installation::permits).isEmpty()
        || TenantContext.PLATFORM_TENANT.equals(TenantContext.require()))
      throw new AurigaException(CommonErrors.FORBIDDEN);
  }

  @Override
  @AuthenticatedOnly
  public void openSharedMessageContext(
      OpenSharedMessageContextRequest r, StreamObserver<OpenSharedMessageContextResponse> o) {
    caller();
    o.onNext(OpenSharedMessageContextResponse.newBuilder().setContext(messages.open(r)).build());
    o.onCompleted();
  }

  @Override
  @AuthenticatedOnly
  public void readSharedMessageContext(
      ReadSharedMessageContextRequest r, StreamObserver<ReadSharedMessageContextResponse> o) {
    caller();
    o.onNext(ReadSharedMessageContextResponse.newBuilder().setContext(messages.read(r)).build());
    o.onCompleted();
  }

  @Override
  @AuthenticatedOnly
  public void checkSharedMessageNodes(
      CheckSharedMessageNodesRequest r, StreamObserver<CheckSharedMessageNodesResponse> o) {
    caller();
    o.onNext(messages.check(r));
    o.onCompleted();
  }
}
