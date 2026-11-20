package auriga.iam;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.security.*;
import auriga.contracts.iam.v1.*;
import auriga.iam.api.CredentialInstallationScope;
import io.grpc.stub.StreamObserver;
import java.util.Set;

/** Runtime relays carry internal service JWTs over the existing authenticated gRPC server. */
public class IamGrpcSharedTasks extends SharedTaskServiceGrpc.SharedTaskServiceImplBase {
  private static final Set<String> HOST_RELAYS = Set.of("svc:agent");
  private static final Set<String> READERS = Set.of("svc:agent", "svc:ai-gateway", "svc:collab");
  private final SharedTasks tasks;
  private final CredentialInstallationScope installation;
  private final SharedOutbound outbound;

  IamGrpcSharedTasks(
      SharedTasks tasks, CredentialInstallationScope installation, SharedOutbound outbound) {
    this.tasks = tasks;
    this.installation = installation;
    this.outbound = outbound;
  }

  private void caller(Set<String> allowed) {
    var principal =
        AuthorizationRules.currentPrincipal()
            .orElseThrow(() -> new AurigaException(CommonErrors.UNAUTHENTICATED));
    // InternalTokenVerifier checks the auriga.internal audience; it is not a permission claim.
    if (!principal.isInternalService()
        || !TenantContext.PLATFORM_TENANT.equals(principal.tenantId())
        || !allowed.contains(principal.subject())
        || TenantContext.current().filter(installation::permits).isEmpty()
        || TenantContext.PLATFORM_TENANT.equals(TenantContext.require()))
      throw new AurigaException(CommonErrors.FORBIDDEN);
  }

  @Override
  @AuthenticatedOnly
  public void beginSharedTask(
      BeginSharedTaskRequest request, StreamObserver<BeginSharedTaskResponse> response) {
    caller(HOST_RELAYS);
    response.onNext(tasks.begin(request));
    response.onCompleted();
  }

  @Override
  @AuthenticatedOnly
  public void issueSharedCapability(
      IssueSharedCapabilityRequest request,
      StreamObserver<IssueSharedCapabilityResponse> response) {
    caller(HOST_RELAYS);
    response.onNext(tasks.issue(request));
    response.onCompleted();
  }

  @Override
  @AuthenticatedOnly
  public void finishSharedTask(
      FinishSharedTaskRequest request, StreamObserver<FinishSharedTaskResponse> response) {
    caller(HOST_RELAYS);
    response.onNext(tasks.finish(request));
    response.onCompleted();
  }

  @Override
  @AuthenticatedOnly
  public void resolveSharedTask(
      ResolveSharedTaskRequest request, StreamObserver<ResolveSharedTaskResponse> response) {
    caller(READERS);
    response.onNext(tasks.resolve(request));
    response.onCompleted();
  }

  @Override
  @AuthenticatedOnly
  public void authorizeSharedCapability(
      AuthorizeSharedCapabilityRequest request,
      StreamObserver<AuthorizeSharedCapabilityResponse> response) {
    caller(READERS);
    response.onNext(tasks.authorize(request));
    response.onCompleted();
  }

  @Override
  @AuthenticatedOnly
  public void readSharedCredentials(
      ReadSharedCredentialsRequest request,
      StreamObserver<ReadSharedCredentialsResponse> response) {
    caller(HOST_RELAYS);
    response.onNext(tasks.credentials(request));
    response.onCompleted();
  }

  @Override
  @AuthenticatedOnly
  public void executeSharedOutbound(
      ExecuteSharedOutboundRequest request,
      StreamObserver<ExecuteSharedOutboundResponse> response) {
    caller(HOST_RELAYS);
    response.onNext(outbound.execute(request));
    response.onCompleted();
  }
}
