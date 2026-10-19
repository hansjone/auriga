package auriga.common.grpc;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.context.RequestContext;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.grpc.TenantDirectoryExample.GrpcTenantDirectory;
import auriga.common.grpc.TenantDirectoryExample.LocalTenantDirectory;
import auriga.common.grpc.TenantDirectoryExample.TenantDirectory;
import auriga.common.grpc.TenantDirectoryExample.TenantGrpcService;
import auriga.common.security.AccessTokenDecoders;
import auriga.common.security.AccessTokenSettings;
import auriga.common.security.AccessTokenVerifier;
import auriga.common.security.AuthorizationAdvisor;
import auriga.common.testing.TestTokens;
import auriga.contracts.org.v1.GetTenantRequest;
import auriga.contracts.org.v1.Tenant;
import auriga.contracts.org.v1.TenantServiceGrpc;
import io.grpc.BindableService;
import io.grpc.ManagedChannel;
import io.grpc.Metadata;
import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import io.grpc.stub.MetadataUtils;
import java.time.Clock;
import java.time.Duration;
import java.util.List;
import java.util.Set;
import java.util.UUID;
import java.util.function.Consumer;
import org.junit.jupiter.api.AfterAll;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.Test;
import org.springframework.aop.framework.ProxyFactory;
import org.springframework.boot.test.context.runner.ApplicationContextRunner;

class GrpcTest {
  private static TestTokens tokens;
  private static GrpcServer server;
  private static final List<ManagedChannel> channels = new java.util.ArrayList<>();

  @BeforeAll
  static void start() throws Exception {
    tokens = new TestTokens();
    AccessTokenVerifier verifier =
        new AccessTokenVerifier(
            AccessTokenDecoders.createInternal(
                AccessTokenSettings.of(TestTokens.ISSUER, tokens.jwksUri())));
    // Spring-style CGLIB proxy: the final bindService() binds the proxy, so annotations apply.
    ProxyFactory factory = new ProxyFactory(new TenantGrpcService());
    factory.setProxyTargetClass(true);
    factory.addAdvisor(new AuthorizationAdvisor(Clock.systemUTC()));
    server =
        new GrpcServer(
            "127.0.0.1",
            0,
            List.of((BindableService) factory.getProxy()),
            new AurigaServerInterceptor(verifier, Set.of(), Set.of("svc:org")),
            null,
            false,
            Set.of());
    server.start();
  }

  @AfterAll
  static void stop() {
    channels.forEach(ManagedChannel::shutdownNow);
    server.stop();
    tokens.close();
  }

  private static String serviceToken(Consumer<com.nimbusds.jwt.JWTClaimsSet.Builder> extra) {
    return tokens.token(
        c -> {
          TestTokens.internalService(c).claim("perms", List.of("org.tenants.read"));
          extra.accept(c);
        });
  }

  private static TenantServiceGrpc.TenantServiceBlockingStub stub(String token, Duration timeout) {
    ManagedChannel channel =
        token == null
            ? GrpcChannels.open("127.0.0.1:" + server.port())
            : GrpcChannels.open(
                "127.0.0.1:" + server.port(), new AurigaClientInterceptor(() -> token, timeout));
    channels.add(channel);
    return TenantServiceGrpc.newBlockingStub(channel);
  }

  private static Tenant get(TenantServiceGrpc.TenantServiceBlockingStub stub, String id) {
    return stub.getTenant(GetTenantRequest.newBuilder().setTenantId(id).build()).getTenant();
  }

  private static void assertStatus(Status.Code code, Runnable call) {
    StatusRuntimeException e = assertThrows(StatusRuntimeException.class, call::run);
    assertEquals(code, e.getStatus().getCode(), e.toString());
  }

  @Test
  void propagatesTenantRequestIdAndTrace() {
    UUID tenant = UUID.randomUUID();
    var stub = stub(serviceToken(c -> {}), null);
    RequestContext request =
        RequestContext.start("req-42", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01");
    Tenant result = request.call(() -> TenantContext.call(tenant, () -> get(stub, "t1")));
    assertEquals(tenant.toString(), result.getTenantId());
    assertEquals("req-42", result.getSlug());
    assertEquals("4bf92f3577b34da6a3ce929d0e0e4736", result.getLocale());
    assertEquals("tenant t1", result.getDisplayName());
    // internal service without a tenant: nothing bound
    assertEquals("none", get(stub, "t1").getTenantId());
  }

  @Test
  void rejectsMissingAndNonInternalTokens() {
    assertStatus(Status.Code.UNAUTHENTICATED, () -> get(stub(null, null), "t1"));
    assertStatus(Status.Code.UNAUTHENTICATED, () -> get(stub("garbage", null), "t1"));
    assertStatus(
        Status.Code.UNAUTHENTICATED,
        () -> get(stub(serviceToken(c -> c.audience("auriga.ai-gateway")), null), "t1"));
    // user token
    assertStatus(Status.Code.UNAUTHENTICATED, () -> get(stub(tokens.token(), null), "t1"));
    // external client credentials token (service account) with the permission it needs
    String externalServiceAccount =
        tokens.token(
            c ->
                c.subject("0192a1b2-0000-7000-8000-0000000000s1")
                    .claim("ctx", "service")
                    .claim("ptyp", "service_account")
                    .claim("sid", null)
                    .claim("perms", List.of("org.tenants.read")));
    assertStatus(Status.Code.UNAUTHENTICATED, () -> get(stub(externalServiceAccount, null), "t1"));
  }

  @Test
  void platformServiceAccountCannotActForTenants() {
    String platformServiceAccount =
        tokens.token(
            c ->
                c.subject("svc:org")
                    .claim("ctx", "service")
                    .claim("ptyp", "service_account")
                    .claim("sid", null)
                    .claim("tid", TenantContext.PLATFORM_TENANT.toString())
                    .claim("perms", List.of("org.tenants.read")));
    var stub = stub(platformServiceAccount, null);
    assertStatus(Status.Code.UNAUTHENTICATED, () -> get(stub, "t1"));
    assertStatus(
        Status.Code.UNAUTHENTICATED,
        () -> TenantContext.call(TestTokens.TENANT_A, () -> get(stub, "t1")));
  }

  @Test
  void onlyAllowlistedInternalServicesActForTenants() {
    var allowed = stub(serviceToken(c -> {}), null);
    assertEquals(
        TestTokens.TENANT_B.toString(),
        TenantContext.call(TestTokens.TENANT_B, () -> get(allowed, "t1")).getTenantId());
    var other = stub(serviceToken(c -> c.subject("svc:billing")), null);
    assertEquals("none", get(other, "t1").getTenantId());
    StatusRuntimeException e =
        assertThrows(
            StatusRuntimeException.class,
            () -> TenantContext.call(TestTokens.TENANT_A, () -> get(other, "t1")));
    assertEquals(Status.Code.PERMISSION_DENIED, e.getStatus().getCode());
    assertEquals("common.forbidden", e.getTrailers().get(GrpcMetadata.ERROR_CODE));
  }

  @Test
  void misconfiguredPlatformVerifierStillRefusesNonInternalPrincipals() {
    AccessTokenVerifier platform =
        new AccessTokenVerifier(
            AccessTokenDecoders.create(
                AccessTokenSettings.of(TestTokens.ISSUER, tokens.jwksUri())));
    GrpcServer wrong =
        new GrpcServer(
            "127.0.0.1",
            0,
            List.of(new TenantGrpcService()),
            new AurigaServerInterceptor(platform, Set.of(), Set.of()),
            null,
            false,
            Set.of());
    wrong.start();
    try {
      ManagedChannel channel =
          GrpcChannels.open(
              "127.0.0.1:" + wrong.port(), new AurigaClientInterceptor(tokens::token, null));
      channels.add(channel);
      assertStatus(
          Status.Code.PERMISSION_DENIED,
          () -> get(TenantServiceGrpc.newBlockingStub(channel), "t1"));
    } finally {
      wrong.stop();
    }
  }

  @Test
  void refusesPlaintextNonLoopbackBind() {
    AurigaServerInterceptor interceptor =
        new AurigaServerInterceptor(new AccessTokenVerifier(t -> null), Set.of(), Set.of());
    GrpcServer exposed =
        new GrpcServer(
            "0.0.0.0", 0, List.of(new TenantGrpcService()), interceptor, null, false, Set.of());
    IllegalStateException e = assertThrows(IllegalStateException.class, exposed::start);
    assertTrue(e.getMessage().contains("TLS"), e.getMessage());
    assertFalse(exposed.isRunning());
  }

  /** Implements an RPC without declaring how it is authorized. */
  static class UndeclaredService extends TenantServiceGrpc.TenantServiceImplBase {
    @Override
    public void getTenant(
        GetTenantRequest request,
        io.grpc.stub.StreamObserver<auriga.contracts.org.v1.GetTenantResponse> response) {
      response.onCompleted();
    }
  }

  @Test
  void rpcWithoutAuthorizationAnnotationFailsStartup() {
    AurigaServerInterceptor interceptor =
        new AurigaServerInterceptor(new AccessTokenVerifier(t -> null), Set.of(), Set.of());
    GrpcServer server =
        new GrpcServer(
            "127.0.0.1", 0, List.of(new UndeclaredService()), interceptor, null, false, Set.of());
    IllegalStateException e = assertThrows(IllegalStateException.class, server::start);
    assertTrue(e.getMessage().contains("GetTenant"), e.getMessage());
    // explicitly public methods (health checks) are exempt
    GrpcServer exempt =
        new GrpcServer(
            "127.0.0.1",
            0,
            List.of(new UndeclaredService()),
            interceptor,
            null,
            false,
            Set.of(TenantServiceGrpc.getGetTenantMethod().getFullMethodName()));
    exempt.start();
    exempt.stop();
  }

  @Test
  void malformedTenantMetadataIsInvalid() {
    Metadata md = new Metadata();
    md.put(GrpcMetadata.TENANT_ID, "not-a-uuid");
    var stub =
        stub(serviceToken(c -> {}), null)
            .withInterceptors(MetadataUtils.newAttachHeadersInterceptor(md));
    assertStatus(Status.Code.INVALID_ARGUMENT, () -> get(stub, "t1"));
  }

  @Test
  void permissionAnnotationAppliesToGrpcServices() {
    var stub = stub(serviceToken(c -> c.claim("perms", List.of())), null);
    StatusRuntimeException e = assertThrows(StatusRuntimeException.class, () -> get(stub, "t1"));
    assertEquals(Status.Code.PERMISSION_DENIED, e.getStatus().getCode());
    AurigaException mapped = GrpcErrors.toAurigaException(e);
    assertEquals(CommonErrors.FORBIDDEN.code(), mapped.errorCode().code());
    assertEquals("org.tenants.read", mapped.params().get("permission"));
  }

  @Test
  void businessErrorsRoundTripAsAurigaException() {
    TenantDirectory remote = new GrpcTenantDirectory(channelFor(serviceToken(c -> {})));
    TenantDirectory local = new LocalTenantDirectory();
    for (TenantDirectory directory : List.of(local, remote)) {
      assertEquals("tenant t9", directory.displayName("t9"));
      AurigaException e =
          assertThrows(AurigaException.class, () -> directory.displayName("missing"));
      assertEquals("common.not_found", e.errorCode().code());
      assertEquals(404, e.errorCode().httpStatus());
      assertEquals("missing", e.params().get("tenantId"));
    }
  }

  @Test
  void unexpectedErrorsDoNotLeakDetails() {
    StatusRuntimeException e =
        assertThrows(
            StatusRuntimeException.class, () -> get(stub(serviceToken(c -> {}), null), "crash"));
    assertEquals(Status.Code.INTERNAL, e.getStatus().getCode());
    assertFalse(String.valueOf(e.getStatus().getDescription()).contains("secret"));
    assertEquals("common.internal", e.getTrailers().get(GrpcMetadata.ERROR_CODE));
  }

  @Test
  void defaultDeadlineIsApplied() {
    var stub = stub(serviceToken(c -> {}), Duration.ofMillis(300));
    assertStatus(Status.Code.DEADLINE_EXCEEDED, () -> get(stub, "slow"));
    AurigaException mapped =
        assertThrows(AurigaException.class, () -> GrpcErrors.call(() -> get(stub, "slow")));
    assertEquals(CommonErrors.UNAVAILABLE, mapped.errorCode());
  }

  private static ManagedChannel channelFor(String token) {
    ManagedChannel channel =
        GrpcChannels.open(
            "127.0.0.1:" + server.port(), new AurigaClientInterceptor(() -> token, null));
    channels.add(channel);
    return channel;
  }

  @Test
  void configurationSelectsLocalBeanOrGrpcClient() {
    ApplicationContextRunner runner =
        new ApplicationContextRunner().withUserConfiguration(TenantDirectoryExample.Wiring.class);
    runner.run(
        ctx -> assertInstanceOf(LocalTenantDirectory.class, ctx.getBean(TenantDirectory.class)));
    runner
        .withPropertyValues("auriga.remote.org.mode=local")
        .run(
            ctx ->
                assertInstanceOf(LocalTenantDirectory.class, ctx.getBean(TenantDirectory.class)));
    runner
        .withPropertyValues("auriga.remote.org.mode=grpc", "auriga.remote.org.target=127.0.0.1:1")
        .run(
            ctx -> assertInstanceOf(GrpcTenantDirectory.class, ctx.getBean(TenantDirectory.class)));
    runner
        .withPropertyValues("auriga.remote.org.mode=grpc")
        .run(ctx -> assertNotNull(ctx.getStartupFailure()));
    runner
        .withPropertyValues("auriga.remote.org.mode=carrier-pigeon")
        .run(ctx -> assertNotNull(ctx.getStartupFailure()));
  }
}
