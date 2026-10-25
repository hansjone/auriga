package auriga.collab;

import static org.junit.jupiter.api.Assertions.*;

import auriga.collab.api.CollabAdmission;
import auriga.collab.api.CollabAdmission.Operation;
import auriga.collab.config.CollabControlPlaneConfiguration;
import auriga.common.context.RequestContext;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.grpc.AurigaServerInterceptor;
import auriga.common.security.*;
import auriga.contracts.collab.v1.Actor;
import auriga.contracts.iam.v1.*;
import auriga.contracts.org.v1.*;
import com.google.protobuf.Timestamp;
import com.nimbusds.jose.*;
import com.nimbusds.jose.crypto.ECDSASigner;
import com.nimbusds.jose.jwk.*;
import com.nimbusds.jose.jwk.gen.ECKeyGenerator;
import com.nimbusds.jwt.*;
import com.sun.net.httpserver.HttpServer;
import io.grpc.Server;
import io.grpc.Status;
import io.grpc.netty.shaded.io.grpc.netty.NettyServerBuilder;
import io.grpc.stub.StreamObserver;
import java.net.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.time.Instant;
import java.util.*;
import java.util.concurrent.atomic.AtomicBoolean;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;
import org.springframework.context.annotation.AnnotationConfigApplicationContext;
import org.springframework.core.env.MapPropertySource;

/** External authority is a local service; common JWT and production client/bean are real. */
class CollabControlPlaneTest {
  @TempDir Path temporary;

  @Test
  void ownServiceIdentityAndTenantDelegationReachBothAuthoritiesOnEveryCall() throws Exception {
    var key = new ECKeyGenerator(Curve.P_256).keyID(UUID.randomUUID().toString()).generate();
    var jwks = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
    jwks.createContext(
        "/jwks",
        exchange -> {
          byte[] value = new JWKSet(key.toPublicJWK()).toString().getBytes(StandardCharsets.UTF_8);
          exchange.getResponseHeaders().set("Content-Type", "application/json");
          exchange.sendResponseHeaders(200, value.length);
          exchange.getResponseBody().write(value);
          exchange.close();
        });
    jwks.start();
    var verifier =
        new AccessTokenVerifier(
            AccessTokenDecoders.createInternal(
                AccessTokenSettings.of(
                    "https://auriga.example.com",
                    URI.create("http://127.0.0.1:" + jwks.getAddress().getPort() + "/jwks"))));
    UUID tenant = UUID.randomUUID();
    var active = new AtomicBoolean(true);
    var permission = new AtomicBoolean(true);
    Actor actor =
        Actor.newBuilder()
            .setTenantId(tenant.toString())
            .setAccountId(UUID.randomUUID().toString())
            .setCredentialKind("DEVICE")
            .setCredentialSubject(UUID.randomUUID().toString())
            .build();
    Server authority =
        NettyServerBuilder.forAddress(new InetSocketAddress("127.0.0.1", 0))
            .intercept(
                new AurigaServerInterceptor(
                    verifier, Set.of(), Set.of("svc:collab", "svc:ai-gateway")))
            .addService(
                new DeviceAccessServiceGrpc.DeviceAccessServiceImplBase() {
                  @Override
                  @AuthenticatedOnly
                  public void verifyDeviceCredential(
                      VerifyDeviceCredentialRequest request,
                      StreamObserver<VerifyDeviceCredentialResponse> observer) {
                    if (!ownCaller()
                        || !request.getTenantId().equals(TenantContext.require().toString())) {
                      observer.onError(Status.PERMISSION_DENIED.asRuntimeException());
                      return;
                    }
                    observer.onNext(
                        active.get()
                            ? VerifyDeviceCredentialResponse.newBuilder()
                                .setActive(true)
                                .setTenantId(request.getTenantId())
                                .setAccountId(request.getAccountId())
                                .setDeviceId(request.getDeviceId())
                                .setExpiresAt(
                                    Timestamp.newBuilder()
                                        .setSeconds(Instant.now().plusSeconds(60).getEpochSecond()))
                                .build()
                            : VerifyDeviceCredentialResponse.getDefaultInstance());
                    observer.onCompleted();
                  }
                })
            .addService(
                new AuthorizationServiceGrpc.AuthorizationServiceImplBase() {
                  @Override
                  @AuthenticatedOnly
                  public void getEffectivePermissions(
                      GetEffectivePermissionsRequest request,
                      StreamObserver<GetEffectivePermissionsResponse> observer) {
                    if (!ownCaller()
                        || !request.getTenantId().equals(TenantContext.require().toString())) {
                      observer.onError(Status.PERMISSION_DENIED.asRuntimeException());
                      return;
                    }
                    var result =
                        GetEffectivePermissionsResponse.newBuilder()
                            .setStatus(MemberStatus.MEMBER_STATUS_ACTIVE);
                    if (permission.get()) result.addPermissions("collab.messages.use");
                    observer.onNext(result.build());
                    observer.onCompleted();
                  }
                })
            .build()
            .start();
    Path token = temporary.resolve("collab.jwt");
    Files.writeString(token, token(key, "collab"));
    try (var context = new AnnotationConfigApplicationContext()) {
      String target = "127.0.0.1:" + authority.getPort();
      context
          .getEnvironment()
          .getPropertySources()
          .addFirst(
              new MapPropertySource(
                  "test",
                  Map.of(
                      "auriga.collab.control-plane.enabled",
                      "true",
                      "auriga.collab.control-plane.iam-target",
                      target,
                      "auriga.collab.control-plane.org-target",
                      target,
                      "auriga.collab.control-plane.service-token-file",
                      token.toString())));
      context.register(CollabControlPlaneConfiguration.class);
      context.refresh();
      CollabAdmission admission = context.getBean(CollabAdmission.class);
      TenantContext.run(
          tenant,
          () -> {
            admission.verify(actor, Operation.OPEN);
            admission.verify(actor, Operation.RECEIVE);
            active.set(false);
            denied(() -> admission.verify(actor, Operation.RECEIVE), "common.forbidden");
            active.set(true);
            permission.set(false);
            denied(() -> admission.verify(actor, Operation.READ), "common.forbidden");
            permission.set(true);
          });
      Files.writeString(token, token(key, "ai-gateway"));
      TenantContext.run(
          tenant, () -> denied(() -> admission.verify(actor, Operation.OPEN), "common.forbidden"));
      Files.writeString(token, token(key, "collab"));
      TenantContext.run(tenant, () -> admission.verify(actor, Operation.ACK));
      Files.delete(token);
      TenantContext.run(
          tenant,
          () -> denied(() -> admission.verify(actor, Operation.PULL), "common.unavailable"));
    } finally {
      authority.shutdownNow();
      authority.awaitTermination(5, java.util.concurrent.TimeUnit.SECONDS);
      jwks.stop(0);
    }
  }

  private static boolean ownCaller() {
    return RequestContext.currentPrincipal().orElseThrow().subject().equals("svc:collab");
  }

  private static String token(ECKey key, String service) throws Exception {
    Instant now = Instant.now();
    var claims =
        new JWTClaimsSet.Builder()
            .issuer("https://auriga.example.com")
            .subject("svc:" + service)
            .audience("auriga.internal")
            .claim("tid", TenantContext.PLATFORM_TENANT.toString())
            .claim("ptyp", "internal_service")
            .claim("ctx", "service")
            .issueTime(Date.from(now))
            .notBeforeTime(Date.from(now))
            .expirationTime(Date.from(now.plusSeconds(60)))
            .jwtID(UUID.randomUUID().toString())
            .build();
    var jwt =
        new SignedJWT(
            new JWSHeader.Builder(JWSAlgorithm.ES256)
                .type(new JOSEObjectType("at+jwt"))
                .keyID(key.getKeyID())
                .build(),
            claims);
    jwt.sign(new ECDSASigner(key));
    return jwt.serialize();
  }

  private static void denied(Runnable action, String error) {
    assertEquals(error, assertThrows(AurigaException.class, action::run).errorCode().code());
  }
}
