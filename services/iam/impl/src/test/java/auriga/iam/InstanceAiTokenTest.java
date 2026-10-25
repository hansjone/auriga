package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import auriga.common.context.RequestContext;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.grpc.GrpcAuthorizationCheck;
import auriga.common.security.*;
import auriga.contracts.iam.v1.*;
import auriga.contracts.org.v1.*;
import auriga.iam.api.*;
import com.nimbusds.jose.JWSAlgorithm;
import com.nimbusds.jose.crypto.ECDSAVerifier;
import com.nimbusds.jose.jwk.*;
import com.nimbusds.jose.jwk.gen.ECKeyGenerator;
import com.nimbusds.jwt.SignedJWT;
import io.grpc.stub.StreamObserver;
import java.time.*;
import java.util.*;
import java.util.function.Supplier;
import org.junit.jupiter.api.*;

class InstanceAiTokenTest {
  private final UUID tenant = UUID.randomUUID(),
      owner = UUID.randomUUID(),
      instance = UUID.randomUUID();
  private final Clock clock = Clock.fixed(Instant.parse("2026-10-22T00:00:00Z"), ZoneOffset.UTC);
  private final ContractSchemas schemas = new ContractSchemas();
  private final AccountDirectory accounts = mock(AccountDirectory.class);
  private final InstanceAiAuthorization authorization = mock(InstanceAiAuthorization.class);
  private final PermissionResolver permissions = mock(PermissionResolver.class);
  private TokenService tokens;
  private IamInstanceAiTokens service;
  private ECKey key;
  private SigningKeys keys;

  @BeforeEach
  void setup() throws Exception {
    key = new ECKeyGenerator(Curve.P_256).keyID(UUID.randomUUID().toString()).generate();
    keys = mock(SigningKeys.class);
    when(keys.current()).thenReturn(key);
    when(keys.jwks())
        .thenReturn(Json.object("keys", List.of(Json.read(key.toPublicJWK().toJSONString()))));
    tokens =
        new TokenService(keys, schemas, permissions, clock, "https://iam.example.com/", Map.of());
    service = new IamInstanceAiTokens(tokens, accounts, authorization);
    when(accounts.get(any()))
        .thenReturn(account(tenant, owner, AccountStatus.ACCOUNT_STATUS_ACTIVE));
    when(authorization.getEffectivePermissions(any()))
        .thenReturn(
            effective(
                MemberStatus.MEMBER_STATUS_ACTIVE, Set.of("ai.models.invoke", "ai.usage.read")));
  }

  private GetAccountResponse account(UUID tid, UUID sub, AccountStatus status) {
    return GetAccountResponse.newBuilder()
        .setAccount(
            Account.newBuilder()
                .setTenantId(tid.toString())
                .setAccountId(sub.toString())
                .setStatus(status))
        .build();
  }

  private GetEffectivePermissionsResponse effective(MemberStatus status, Collection<String> perms) {
    return GetEffectivePermissionsResponse.newBuilder()
        .setStatus(status)
        .setMemberType(MemberType.MEMBER_TYPE_MEMBER)
        .addRoleKeys("owner")
        .addAllPermissions(perms)
        .build();
  }

  private IssueInstanceAiTokenRequest request() {
    return IssueInstanceAiTokenRequest.newBuilder()
        .setTenantId(tenant.toString())
        .setOwnerId(owner.toString())
        .setInstanceId(instance.toString())
        .setCredentialVersion(7)
        .build();
  }

  private AurigaPrincipal caller(String subject, PrincipalType type, TokenContext context) {
    return new AurigaPrincipal(
        subject,
        TenantContext.PLATFORM_TENANT,
        type,
        context,
        null,
        null,
        null,
        List.of(),
        Set.of(),
        null,
        null,
        "internal-test");
  }

  private <T> T workspace(Supplier<T> work) {
    return as(
        caller("svc:workspace", PrincipalType.INTERNAL_SERVICE, TokenContext.SERVICE),
        tenant,
        work);
  }

  private <T> T as(AurigaPrincipal principal, UUID delegated, Supplier<T> work) {
    return RequestContext.start("instance-ai-test", null)
        .withPrincipal(principal)
        .call(() -> delegated == null ? work.get() : TenantContext.call(delegated, work::get));
  }

  private IssueInstanceAiTokenResponse issue(IssueInstanceAiTokenRequest request) {
    List<IssueInstanceAiTokenResponse> responses = new ArrayList<>();
    service.issueInstanceAiToken(
        request,
        new StreamObserver<>() {
          public void onNext(IssueInstanceAiTokenResponse response) {
            responses.add(response);
          }

          public void onError(Throwable error) {
            fail(error);
          }

          public void onCompleted() {}
        });
    assertEquals(1, responses.size());
    return responses.getFirst();
  }

  private void error(String code, Runnable work) {
    assertEquals(code, assertThrows(AurigaException.class, work::run).errorCode().code());
  }

  @Test
  void realSignatureSchemaAndExactClaimsForOwner() throws Exception {
    Set<String> all = new HashSet<>();
    M3PermissionCatalog.all().forEach(p -> all.add(p.key()));
    all.addAll(
        Set.of("iam.providers.write", "org.members.write", "ops.nodes.write", "auriga.connect"));
    when(authorization.getEffectivePermissions(any()))
        .thenReturn(effective(MemberStatus.MEMBER_STATUS_ACTIVE, all));
    var response = workspace(() -> issue(request()));
    SignedJWT jwt = SignedJWT.parse(response.getAccessToken());
    assertEquals(JWSAlgorithm.ES256, jwt.getHeader().getAlgorithm());
    assertEquals("at+jwt", jwt.getHeader().getType().toString());
    assertEquals(key.getKeyID(), jwt.getHeader().getKeyID());
    assertTrue(jwt.verify(new ECDSAVerifier(key.toPublicJWK())));
    var claims = tokens.verify(response.getAccessToken());
    schemas.validate("auth/access-token.schema.json", claims);
    assertEquals(
        Set.of(
            "iss",
            "sub",
            "aud",
            "iat",
            "nbf",
            "exp",
            "jti",
            "tid",
            "ptyp",
            "ctx",
            "instanceId",
            "credentialVersion",
            "perms"),
        claims.propertyNames());
    assertEquals("https://iam.example.com", claims.path("iss").asString());
    assertEquals("auriga.ai-gateway", claims.path("aud").asString());
    assertEquals("instance", claims.path("ctx").asString());
    assertEquals("user", claims.path("ptyp").asString());
    assertEquals(owner.toString(), claims.path("sub").asString());
    assertEquals(tenant.toString(), claims.path("tid").asString());
    assertEquals(instance.toString(), claims.path("instanceId").asString());
    assertEquals(7, claims.path("credentialVersion").asLong());
    assertEquals(
        List.of("ai.models.invoke", "ai.usage.read"),
        Json.MAPPER.convertValue(claims.path("perms"), List.class));
    assertEquals(300, claims.path("exp").asLong() - claims.path("iat").asLong());
    assertEquals(
        clock.instant().plusSeconds(300).getEpochSecond(), response.getExpiresAt().getSeconds());
    verify(accounts)
        .get(
            GetAccountRequest.newBuilder()
                .setTenantId(tenant.toString())
                .setAccountId(owner.toString())
                .build());
    verify(authorization)
        .getEffectivePermissions(
            GetEffectivePermissionsRequest.newBuilder()
                .setTenantId(tenant.toString())
                .setAccountId(owner.toString())
                .build());
    verifyNoInteractions(permissions);
  }

  @Test
  void callerAndDelegationCannotBeSelfDeclared() {
    error("common.unauthenticated", () -> issue(request()));
    for (var principal :
        List.of(
            caller("svc:edge", PrincipalType.INTERNAL_SERVICE, TokenContext.SERVICE),
            caller("svc:workspace", PrincipalType.SERVICE_ACCOUNT, TokenContext.SERVICE),
            caller("svc:workspace", PrincipalType.USER, TokenContext.ADMIN),
            caller("svc:workspace", PrincipalType.DEVICE, TokenContext.DEVICE))) {
      error("common.forbidden", () -> as(principal, tenant, () -> issue(request())));
    }
    var workspace = caller("svc:workspace", PrincipalType.INTERNAL_SERVICE, TokenContext.SERVICE);
    error("common.forbidden", () -> as(workspace, null, () -> issue(request())));
    error("common.forbidden", () -> as(workspace, UUID.randomUUID(), () -> issue(request())));
    verifyNoInteractions(accounts, authorization);
    GrpcAuthorizationCheck.verify(List.of(service), Set.of());
  }

  @Test
  void realGrpcTransportUsesInternalJwtAndAuthorizedTenantGuard() {
    var settings =
        new AccessTokenSettings(
            "https://iam.example.com",
            java.net.URI.create("https://iam.example.com/jwks"),
            null,
            null,
            null,
            null,
            clock,
            false);
    var source =
        new com.nimbusds.jose.jwk.source.ImmutableJWKSet<com.nimbusds.jose.proc.SecurityContext>(
            new JWKSet(key.toPublicJWK()));
    var verifier = new AccessTokenVerifier(AccessTokenDecoders.createInternal(settings, source));
    var server =
        new auriga.common.grpc.GrpcServer(
            "127.0.0.1",
            0,
            List.of(service),
            new auriga.common.grpc.AurigaServerInterceptor(
                verifier, Set.of(), Set.of("svc:workspace")),
            null,
            false,
            Set.of());
    List<io.grpc.ManagedChannel> channels = new ArrayList<>();
    String secret = Secrets.random(), edgeSecret = Secrets.random();
    var issuer =
        new TokenService(
            keys,
            schemas,
            permissions,
            clock,
            "https://iam.example.com",
            Map.of("workspace", Secrets.hash(secret), "edge", Secrets.hash(edgeSecret)));
    server.start();
    try {
      java.util.function.Function<
              String, InstanceAiTokenServiceGrpc.InstanceAiTokenServiceBlockingStub>
          stub =
              token -> {
                var channel =
                    token == null
                        ? auriga.common.grpc.GrpcChannels.open("127.0.0.1:" + server.port())
                        : auriga.common.grpc.GrpcChannels.open(
                            "127.0.0.1:" + server.port(),
                            new auriga.common.grpc.AurigaClientInterceptor(
                                () -> token, Duration.ofSeconds(5)));
                channels.add(channel);
                return InstanceAiTokenServiceGrpc.newBlockingStub(channel)
                    .withDeadlineAfter(5, java.util.concurrent.TimeUnit.SECONDS);
              };
      var workspace = stub.apply(issuer.issue("workspace", secret));
      var response = TenantContext.call(tenant, () -> workspace.issueInstanceAiToken(request()));
      assertEquals(
          owner.toString(), tokens.verify(response.getAccessToken()).path("sub").asString());
      assertEquals(
          io.grpc.Status.Code.UNAUTHENTICATED,
          assertThrows(
                  io.grpc.StatusRuntimeException.class,
                  () -> stub.apply(null).issueInstanceAiToken(request()))
              .getStatus()
              .getCode());
      assertEquals(
          io.grpc.Status.Code.UNAUTHENTICATED,
          assertThrows(
                  io.grpc.StatusRuntimeException.class,
                  () -> stub.apply("invalid").issueInstanceAiToken(request()))
              .getStatus()
              .getCode());
      var user =
          stub.apply(
              tokens.access(
                  tenant,
                  owner.toString(),
                  "auriga",
                  "user",
                  "portal",
                  UUID.randomUUID(),
                  null,
                  null,
                  null,
                  ""));
      assertEquals(
          io.grpc.Status.Code.UNAUTHENTICATED,
          assertThrows(
                  io.grpc.StatusRuntimeException.class,
                  () -> TenantContext.call(tenant, () -> user.issueInstanceAiToken(request())))
              .getStatus()
              .getCode());
      var edge = stub.apply(issuer.issue("edge", edgeSecret));
      assertEquals(
          io.grpc.Status.Code.PERMISSION_DENIED,
          assertThrows(
                  io.grpc.StatusRuntimeException.class,
                  () -> TenantContext.call(tenant, () -> edge.issueInstanceAiToken(request())))
              .getStatus()
              .getCode());
      assertEquals(
          io.grpc.Status.Code.PERMISSION_DENIED,
          assertThrows(
                  io.grpc.StatusRuntimeException.class,
                  () -> workspace.issueInstanceAiToken(request()))
              .getStatus()
              .getCode());
      assertEquals(
          io.grpc.Status.Code.PERMISSION_DENIED,
          assertThrows(
                  io.grpc.StatusRuntimeException.class,
                  () ->
                      TenantContext.call(
                          UUID.randomUUID(), () -> workspace.issueInstanceAiToken(request())))
              .getStatus()
              .getCode());
    } finally {
      channels.forEach(io.grpc.ManagedChannel::shutdownNow);
      server.stop();
    }
  }

  @Test
  void malformedBindingsAndVersionFailBeforeLookingUpOwner() {
    for (var invalid :
        List.of(
            request().toBuilder().setTenantId("invalid").build(),
            request().toBuilder().setTenantId(TenantContext.PLATFORM_TENANT.toString()).build(),
            request().toBuilder().setOwnerId("alice").build(),
            request().toBuilder().setInstanceId("").build(),
            request().toBuilder().setCredentialVersion(0).build(),
            request().toBuilder().setCredentialVersion(9007199254740992L).build(),
            request().toBuilder().setCredentialVersion(-1L).build())) {
      error("common.validation_failed", () -> workspace(() -> issue(invalid)));
    }
    verifyNoInteractions(accounts, authorization);
  }

  @Test
  void mismatchedOrDisabledAccountAndUnavailableControlPlaneFailClosed() {
    for (var account :
        List.of(
            account(UUID.randomUUID(), owner, AccountStatus.ACCOUNT_STATUS_ACTIVE),
            account(tenant, UUID.randomUUID(), AccountStatus.ACCOUNT_STATUS_ACTIVE),
            account(tenant, owner, AccountStatus.ACCOUNT_STATUS_DISABLED),
            account(tenant, owner, AccountStatus.ACCOUNT_STATUS_UNSPECIFIED))) {
      when(accounts.get(any())).thenReturn(account);
      error("common.forbidden", () -> workspace(() -> issue(request())));
    }
    when(accounts.get(any())).thenReturn(GetAccountResponse.getDefaultInstance());
    error("common.not_found", () -> workspace(() -> issue(request())));
    when(accounts.get(any())).thenThrow(new IllegalStateException("unavailable"));
    error("common.unavailable", () -> workspace(() -> issue(request())));
    verifyNoInteractions(authorization);
  }

  @Test
  void memberMustBeActiveWithNonemptyEffectiveIntersection() {
    for (var status :
        List.of(
            MemberStatus.MEMBER_STATUS_DISABLED,
            MemberStatus.MEMBER_STATUS_OFFBOARDED,
            MemberStatus.MEMBER_STATUS_UNSPECIFIED)) {
      when(authorization.getEffectivePermissions(any()))
          .thenReturn(effective(status, Set.of("ai.models.invoke")));
      error("common.forbidden", () -> workspace(() -> issue(request())));
    }
    when(authorization.getEffectivePermissions(any()))
        .thenReturn(
            effective(
                MemberStatus.MEMBER_STATUS_ACTIVE,
                Set.of("ai.providers.write", "resource.versions.publish")));
    error("common.forbidden", () -> workspace(() -> issue(request())));
    when(authorization.getEffectivePermissions(any()))
        .thenReturn(effective(MemberStatus.MEMBER_STATUS_ACTIVE, Set.of("ai.usage.read")));
    var claims = tokens.verify(workspace(() -> issue(request())).getAccessToken());
    assertEquals(
        List.of("ai.usage.read"), Json.MAPPER.convertValue(claims.path("perms"), List.class));
    when(authorization.getEffectivePermissions(any())).thenReturn(null);
    error("common.unavailable", () -> workspace(() -> issue(request())));
    when(authorization.getEffectivePermissions(any()))
        .thenThrow(io.grpc.Status.UNAVAILABLE.asRuntimeException());
    error("common.unavailable", () -> workspace(() -> issue(request())));
  }

  @Test
  void missingAuthoritativeOrgPortIsUnavailableEvenWithConfiguredPermissions() {
    var beans = new org.springframework.beans.factory.support.DefaultListableBeanFactory();
    var accountsBean = mock(IamAccounts.class);
    when(accountsBean.get(any()))
        .thenReturn(account(tenant, owner, AccountStatus.ACCOUNT_STATUS_ACTIVE));
    service =
        new IamRuntimeConfiguration()
            .iamInstanceAiTokens(
                tokens, accountsBean, beans.getBeanProvider(InstanceAiAuthorization.class));
    when(permissions.resolve(any(), anyString(), anyString()))
        .thenReturn(Set.of("ai.models.invoke"));
    error("common.unavailable", () -> workspace(() -> issue(request())));
    verifyNoInteractions(permissions);
  }

  @Test
  void instanceSchemaRejectsPrincipalAudienceSessionAndManagementPermissions() {
    var claims = tokens.verify(workspace(() -> issue(request())).getAccessToken());
    for (String field : List.of("sid", "did")) {
      var invalid = claims.deepCopy().put(field, UUID.randomUUID().toString());
      error(
          "iam.contract_violation",
          () -> schemas.validate("auth/access-token.schema.json", invalid));
    }
    for (var invalid :
        List.of(
            claims.deepCopy().put("ptyp", "device"),
            claims.deepCopy().put("aud", "auriga"),
            claims.deepCopy().put("credentialVersion", 0),
            claims.deepCopy().put("credentialVersion", 9007199254740992L)))
      error(
          "iam.contract_violation",
          () -> schemas.validate("auth/access-token.schema.json", invalid));
    var invalidPermissions = claims.deepCopy();
    invalidPermissions.set("perms", Json.MAPPER.valueToTree(List.of("ai.providers.write")));
    error(
        "iam.contract_violation",
        () -> schemas.validate("auth/access-token.schema.json", invalidPermissions));
    var invalidRoles = claims.deepCopy();
    invalidRoles.set("roles", Json.MAPPER.valueToTree(List.of("owner")));
    error(
        "iam.contract_violation",
        () -> schemas.validate("auth/access-token.schema.json", invalidRoles));
  }

  @Test
  void ordinaryAccessCannotIssueInstanceOrMismatchedContexts() {
    for (String context : List.of("instance", "unknown", "device", "service")) {
      error(
          "common.validation_failed",
          () ->
              tokens.access(
                  tenant,
                  owner.toString(),
                  "auriga.ai-gateway",
                  "user",
                  context,
                  UUID.randomUUID(),
                  null,
                  null,
                  null,
                  ""));
    }
    error(
        "common.validation_failed",
        () ->
            tokens.access(
                tenant,
                owner.toString(),
                "auriga.ai-gateway",
                "user",
                "portal",
                UUID.randomUUID(),
                null,
                null,
                null,
                ""));
    assertFalse(TokenService.allowedPermission("ai.providers.write", "instance"));
    assertFalse(TokenService.allowedPermission("ai.models.invoke", "unknown"));
  }

  @Test
  void portalFiltersEntireCatalogAndDeviceStaysWithinConnectPermissions() {
    Set<String> effective =
        new HashSet<>(Set.of("iam.providers.write", "org.roles.read", "ops.nodes.write"));
    M3PermissionCatalog.all().forEach(p -> effective.add(p.key()));
    effective.addAll(
        Set.of(
            "auriga.connect.collab.write",
            "auriga.connect.collab.admin",
            "auriga.connect.collab.grant"));
    when(permissions.resolve(any(), anyString(), anyString())).thenReturn(effective);
    var portal =
        tokens.verify(
            tokens.access(
                tenant,
                owner.toString(),
                "auriga",
                "user",
                "portal",
                UUID.randomUUID(),
                null,
                null,
                null,
                ""));
    Set<String> portalPerms =
        Set.copyOf(Json.MAPPER.convertValue(portal.path("perms"), List.class));
    for (var permission : M3PermissionCatalog.all()) {
      assertEquals(
          !permission.adminOnly(), portalPerms.contains(permission.key()), permission.key());
    }
    assertFalse(
        portalPerms.stream()
            .anyMatch(p -> p.startsWith("iam.") || p.startsWith("org.") || p.startsWith("ops.")));
    var device =
        tokens.verify(
            tokens.access(
                tenant,
                owner.toString(),
                "auriga.ai-gateway",
                "device",
                "device",
                null,
                UUID.randomUUID(),
                null,
                null,
                ""));
    assertEquals(
        List.of("auriga.connect.collab.use", "auriga.connect.models.invoke"),
        Json.MAPPER.convertValue(device.path("perms"), List.class));
    assertEquals(
        Set.of("ai.models.invoke", "ai.usage.read"),
        M3PermissionCatalog.devicePermissions("auriga.connect.models.invoke"));
    var admin =
        tokens.verify(
            tokens.access(
                tenant,
                owner.toString(),
                "auriga",
                "user",
                "admin",
                UUID.randomUUID(),
                null,
                null,
                null,
                ""));
    assertEquals(effective, Set.copyOf(Json.MAPPER.convertValue(admin.path("perms"), List.class)));
  }
}
