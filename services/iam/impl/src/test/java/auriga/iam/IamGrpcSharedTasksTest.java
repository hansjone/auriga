package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.context.RequestContext;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.security.*;
import auriga.contracts.iam.v1.*;
import auriga.iam.api.CredentialInstallationScope;
import com.nimbusds.jose.jwk.Curve;
import com.nimbusds.jose.jwk.JWKSet;
import com.nimbusds.jose.jwk.gen.ECKeyGenerator;
import com.nimbusds.jose.jwk.source.ImmutableJWKSet;
import com.nimbusds.jwt.SignedJWT;
import io.grpc.stub.StreamObserver;
import java.net.URI;
import java.time.Clock;
import java.util.*;
import org.junit.jupiter.api.Test;

class IamGrpcSharedTasksTest {
  private final UUID tenant = UUID.randomUUID();
  private final SharedTasks tasks = mock(SharedTasks.class);
  private final IamGrpcSharedTasks rpc =
      new IamGrpcSharedTasks(tasks, new CredentialInstallationScope(tenant));

  @Test
  void onlyRuntimeRelayMayIssueFinishOrReadRawMaterial() {
    var calls =
        List.<Runnable>of(
            () -> rpc.beginSharedTask(BeginSharedTaskRequest.getDefaultInstance(), new Capture<>()),
            () ->
                rpc.issueSharedCapability(
                    IssueSharedCapabilityRequest.getDefaultInstance(), new Capture<>()),
            () ->
                rpc.finishSharedTask(FinishSharedTaskRequest.getDefaultInstance(), new Capture<>()),
            () ->
                rpc.readSharedCredentials(
                    ReadSharedCredentialsRequest.getDefaultInstance(), new Capture<>()));
    for (String caller : List.of("svc:edge", "svc:ai-gateway", "svc:collab", "member"))
      as(
          caller,
          tenant,
          Set.of("auriga.internal"),
          () -> calls.forEach(call -> assertThrows(AurigaException.class, call::run)));
    as(
        "svc:agent",
        UUID.randomUUID(),
        Set.of("auriga.internal"),
        () -> calls.forEach(call -> assertThrows(AurigaException.class, call::run)));
    as(
        "svc:agent",
        TenantContext.PLATFORM_TENANT,
        Set.of("auriga.internal"),
        () -> calls.forEach(call -> assertThrows(AurigaException.class, call::run)));
    verifyNoInteractions(tasks);
  }

  @Test
  void realInternalServiceTokenWithoutPermissionsCanReachRuntimeRelay() throws Exception {
    String secret = Secrets.random();
    var key = new ECKeyGenerator(Curve.P_256).keyID("fixture").generate();
    var keys = mock(SigningKeys.class);
    when(keys.current()).thenReturn(key);
    String issuer = "https://portal.example.com";
    var tokens =
        new TokenService(
            keys,
            new ContractSchemas(),
            (t, s, c) -> Set.of(),
            Clock.systemUTC(),
            issuer,
            Map.of("agent", Secrets.hash(secret)));
    String token = tokens.issue("agent", secret);
    assertNull(SignedJWT.parse(token).getJWTClaimsSet().getClaim("perms"));
    var verifier =
        new InternalTokenVerifier(
            new AccessTokenVerifier(
                AccessTokenDecoders.createInternal(
                    AccessTokenSettings.of(issuer, URI.create(issuer + "/oauth2/jwks")),
                    new ImmutableJWKSet<>(new JWKSet(key.toPublicJWK())))));
    var principal = verifier.verifier().verify(token);
    assertTrue(principal.isInternalService());
    assertTrue(principal.permissions().isEmpty());
    when(tasks.begin(any())).thenReturn(BeginSharedTaskResponse.getDefaultInstance());
    when(tasks.issue(any())).thenReturn(IssueSharedCapabilityResponse.getDefaultInstance());
    when(tasks.credentials(any())).thenReturn(ReadSharedCredentialsResponse.getDefaultInstance());
    RequestContext.start("shared-test", null)
        .withPrincipal(principal)
        .run(
            () ->
                TenantContext.run(
                    tenant,
                    () -> {
                      rpc.beginSharedTask(
                          BeginSharedTaskRequest.getDefaultInstance(), new Capture<>());
                      rpc.issueSharedCapability(
                          IssueSharedCapabilityRequest.getDefaultInstance(), new Capture<>());
                      rpc.finishSharedTask(
                          FinishSharedTaskRequest.getDefaultInstance(), new Capture<>());
                      rpc.readSharedCredentials(
                          ReadSharedCredentialsRequest.getDefaultInstance(), new Capture<>());
                    }));
    verify(tasks).begin(any());
    verify(tasks).issue(any());
    verify(tasks).finish(any());
    verify(tasks).credentials(any());
    verifyNoMoreInteractions(tasks);
  }

  @Test
  void downstreamReadersOnlyReachOnlineAuthorityAndCannotReadRaw() {
    when(tasks.resolve(any())).thenReturn(ResolveSharedTaskResponse.getDefaultInstance());
    when(tasks.authorize(any())).thenReturn(AuthorizeSharedCapabilityResponse.getDefaultInstance());
    for (String caller : List.of("svc:ai-gateway", "svc:collab"))
      as(
          caller,
          tenant,
          Set.of(),
          () -> {
            rpc.resolveSharedTask(ResolveSharedTaskRequest.getDefaultInstance(), new Capture<>());
            rpc.authorizeSharedCapability(
                AuthorizeSharedCapabilityRequest.getDefaultInstance(), new Capture<>());
            assertThrows(
                AurigaException.class,
                () ->
                    rpc.readSharedCredentials(
                        ReadSharedCredentialsRequest.getDefaultInstance(), new Capture<>()));
          });
    verify(tasks, times(2)).resolve(any());
    verify(tasks, times(2)).authorize(any());
    verifyNoMoreInteractions(tasks);
  }

  private void as(String subject, UUID delegated, Set<String> permissions, Runnable work) {
    var principal =
        new AurigaPrincipal(
            subject,
            TenantContext.PLATFORM_TENANT,
            subject.startsWith("svc:") ? PrincipalType.INTERNAL_SERVICE : PrincipalType.USER,
            subject.startsWith("svc:") ? TokenContext.SERVICE : TokenContext.PORTAL,
            null,
            null,
            null,
            List.of(),
            permissions,
            null,
            null,
            null);
    RequestContext.start("shared-test", null)
        .withPrincipal(principal)
        .run(() -> TenantContext.run(delegated, work));
  }

  private static final class Capture<T> implements StreamObserver<T> {
    public void onNext(T value) {}

    public void onError(Throwable error) {
      fail(error);
    }

    public void onCompleted() {}
  }
}
