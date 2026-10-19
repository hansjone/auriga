package io.github.hansjone.auriga.iam;

import io.github.hansjone.auriga.common.security.*;
import io.github.hansjone.auriga.contracts.iam.v1.*;
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
            .orElseThrow(() -> IamError.error("unauthenticated", 401));
    IamError.require(p.isInternalService() && allowed.contains(p.subject()), "forbidden", 403);
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
  public void revokeSessions(
      RevokeSessionsRequest request, StreamObserver<RevokeSessionsResponse> observer) {
    caller(Set.of("svc:org", "svc:ops"));
    observer.onNext(sessions.revoke(request));
    observer.onCompleted();
  }
}
