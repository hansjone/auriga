package auriga.iam;

import auriga.common.security.*;
import auriga.contracts.iam.v1.*;
import io.grpc.stub.StreamObserver;
import java.util.Set;

public class IamGrpcSessions extends SessionServiceGrpc.SessionServiceImplBase {
  private final SessionManager sessions;

  public IamGrpcSessions(SessionManager sessions) {
    this.sessions = sessions;
  }

  private static void caller(Set<String> allowed) {
    var p =
        AuthorizationRules.currentPrincipal()
            .orElseThrow(
                () ->
                    new auriga.common.error.AurigaException(
                        auriga.common.error.CommonErrors.UNAUTHENTICATED));
    if (!p.isInternalService() || !allowed.contains(p.subject()))
      throw new auriga.common.error.AurigaException(auriga.common.error.CommonErrors.FORBIDDEN);
  }

  @Override
  @AuthenticatedOnly
  public void introspectSession(
      IntrospectSessionRequest request, StreamObserver<IntrospectSessionResponse> observer) {
    caller(Set.of("svc:edge"));
    observer.onNext(sessions.introspect(request));
    observer.onCompleted();
  }

  @Override
  @AuthenticatedOnly
  public void touchSession(
      TouchSessionRequest request, StreamObserver<TouchSessionResponse> observer) {
    caller(Set.of("svc:edge"));
    observer.onNext(sessions.touch(request));
    observer.onCompleted();
  }

  @Override
  @AuthenticatedOnly
  public void verifyAdminSessionCsrf(
      VerifyAdminSessionCsrfRequest request,
      StreamObserver<VerifyAdminSessionCsrfResponse> observer) {
    caller(Set.of("svc:edge"));
    if (auriga.common.context.TenantContext.current().isEmpty())
      throw new auriga.common.error.AurigaException(auriga.common.error.CommonErrors.FORBIDDEN);
    observer.onNext(sessions.verifyAdminCsrf(request));
    observer.onCompleted();
  }

  @Override
  @AuthenticatedOnly
  public void revokeSessions(
      RevokeSessionsRequest request, StreamObserver<RevokeSessionsResponse> observer) {
    caller(Set.of("svc:org", "svc:ops"));
    observer.onNext(sessions.revoke(request));
    observer.onCompleted();
  }
}
