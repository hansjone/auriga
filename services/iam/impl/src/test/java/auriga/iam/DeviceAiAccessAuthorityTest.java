package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.context.TenantContext;
import auriga.common.error.*;
import auriga.contracts.org.v1.*;
import auriga.iam.api.InstanceAiAuthorization;
import com.nimbusds.jose.*;
import com.nimbusds.jose.crypto.ECDSASigner;
import com.nimbusds.jose.jwk.*;
import com.nimbusds.jose.jwk.gen.ECKeyGenerator;
import com.nimbusds.jwt.*;
import java.time.*;
import java.util.*;
import java.util.function.Consumer;
import org.junit.jupiter.api.*;
import tools.jackson.databind.node.ObjectNode;

class DeviceAiAccessAuthorityTest {
  final UUID tenant = UUID.randomUUID(), account = UUID.randomUUID(), device = UUID.randomUUID();
  final AdminCsrfPostgresTest.Time clock = new AdminCsrfPostgresTest.Time();
  final DeviceAccessAuthority devices = mock(DeviceAccessAuthority.class);
  final InstanceAiAuthorization authorization = mock(InstanceAiAuthorization.class);
  ECKey key;
  TokenService tokens;
  DeviceAiAccessAuthority authority;

  @BeforeEach
  void setup() throws Exception {
    clock.now = Instant.now().truncatedTo(java.time.temporal.ChronoUnit.SECONDS);
    key = new ECKeyGenerator(Curve.P_256).keyID(UUID.randomUUID().toString()).generate();
    var keys = mock(SigningKeys.class);
    when(keys.current()).thenReturn(key);
    when(keys.jwks())
        .thenReturn(Json.object("keys", List.of(Json.read(key.toPublicJWK().toJSONString()))));
    tokens =
        new TokenService(
            keys,
            new ContractSchemas(),
            (t, s, c) -> Set.of(),
            clock,
            "https://portal.example.com",
            Map.of());
    when(devices.credential(tenant.toString(), account.toString(), device.toString()))
        .thenReturn(
            new DeviceAccessAuthority.Identity(
                tenant.toString(),
                account.toString(),
                device.toString(),
                clock.instant().plusSeconds(600)));
    when(authorization.getEffectivePermissions(any())).thenReturn(member());
    authority = new DeviceAiAccessAuthority(devices, tokens, authorization, clock);
  }

  static GetEffectivePermissionsResponse member() {
    return GetEffectivePermissionsResponse.newBuilder()
        .setStatus(MemberStatus.MEMBER_STATUS_ACTIVE)
        .setMemberType(MemberType.MEMBER_TYPE_MEMBER)
        .addPermissions(DeviceAiResource.INVOKE)
        .build();
  }

  ObjectNode claims() {
    return Json.object(
        "iss",
        tokens.issuer(),
        "sub",
        account.toString(),
        "tid",
        tenant.toString(),
        "did",
        device.toString(),
        "aud",
        "auriga.ai-gateway",
        "ptyp",
        "device",
        "ctx",
        "device",
        "iat",
        clock.instant().getEpochSecond(),
        "nbf",
        clock.instant().getEpochSecond(),
        "exp",
        clock.instant().plusSeconds(300).getEpochSecond(),
        "jti",
        Secrets.random(),
        "perms",
        List.of(DeviceAiResource.INVOKE));
  }

  String sign(ObjectNode claims) throws Exception {
    return sign(claims, key, "at+jwt", key.getKeyID());
  }

  String sign(ObjectNode claims, ECKey signing, String typ, String kid) throws Exception {
    var jwt =
        new SignedJWT(
            new JWSHeader.Builder(JWSAlgorithm.ES256)
                .type(new JOSEObjectType(typ))
                .keyID(kid)
                .build(),
            JWTClaimsSet.parse(claims.toString()));
    jwt.sign(new ECDSASigner(signing));
    return jwt.serialize();
  }

  DeviceAccessAuthority.Identity access(String raw) {
    return TenantContext.call(tenant, () -> authority.access(raw));
  }

  @Test
  void acceptsRealSignedAiWithNoPlatformScopeAndSingletonAudience() throws Exception {
    for (Object audience : List.of("auriga.ai-gateway", List.of("auriga.ai-gateway"))) {
      var c = claims();
      c.set("aud", Json.MAPPER.valueToTree(audience));
      var result = access(sign(c));
      assertNotNull(result);
      assertEquals(tenant.toString(), result.tenant());
      assertEquals(account.toString(), result.account());
      assertEquals(device.toString(), result.device());
      assertEquals(clock.instant().plusSeconds(300), result.expiresAt());
    }
    verify(authorization, times(2))
        .getEffectivePermissions(
            GetEffectivePermissionsRequest.newBuilder()
                .setTenantId(tenant.toString())
                .setAccountId(account.toString())
                .build());
    verify(devices, times(4)).credential(tenant.toString(), account.toString(), device.toString());
  }

  @Test
  void rejectsWideOrWrongIdentityAudienceAndPermissionsBeforeOnlineReads() throws Exception {
    List<Consumer<ObjectNode>> mutations =
        List.of(
            c -> c.put("aud", "auriga"),
            c -> c.put("aud", "auriga.internal"),
            c -> c.set("aud", Json.MAPPER.valueToTree(List.of("auriga.ai-gateway", "auriga"))),
            c ->
                c.set(
                    "aud",
                    Json.MAPPER.valueToTree(List.of("auriga.ai-gateway", "auriga.ai-gateway"))),
            c -> c.putArray("aud"),
            c -> c.put("ptyp", "user"),
            c -> c.put("ctx", "instance"),
            c -> {
              c.put("ptyp", "service_account");
              c.put("ctx", "service");
            },
            c -> c.put("ptyp", "internal_service"),
            c -> c.put("tid", tenant.toString().toUpperCase(Locale.ROOT)),
            c -> c.put("sub", "1-1-1-1-1"),
            c -> c.put("did", "not-a-uuid"),
            c -> c.remove("did"),
            c -> c.remove("perms"),
            c -> c.putArray("perms"),
            c -> c.putArray("perms").add("auriga.connect.collab.use"),
            c -> c.putArray("perms").add(DeviceAiResource.INVOKE).add("auriga.connect"),
            c -> c.putArray("perms").add(DeviceAiResource.INVOKE).add("ai.models.invoke"),
            c -> c.putArray("perms").add(DeviceAiResource.INVOKE).add("iam.accounts.admin"),
            c -> c.putArray("roles").add("owner"),
            c -> c.put("sid", UUID.randomUUID().toString()));
    for (var mutation : mutations) {
      var c = claims();
      mutation.accept(c);
      assertNull(access(sign(c)), c.toString());
    }
    verifyNoInteractions(devices, authorization);
  }

  @Test
  void rejectsBadSignatureIssuerTypeKeySchemaAndOpaqueCredentials() throws Exception {
    var other = new ECKeyGenerator(Curve.P_256).generate();
    assertNull(access(sign(claims(), other, "at+jwt", key.getKeyID())));
    assertNull(access(sign(claims(), key, "JWT", key.getKeyID())));
    assertNull(access(sign(claims(), key, "at+jwt", "unknown")));
    assertNull(access(sign(claims(), key, "at+jwt", null)));
    assertNull(
        access(
            tokens
                .instanceAi(tenant, account, device, 1, Set.of("ai.models.invoke"))
                .accessToken()));
    assertNull(
        access(
            tokens.access(
                tenant,
                account.toString(),
                "auriga",
                "device",
                "device",
                null,
                device,
                clock.instant(),
                null,
                "auriga.connect")));
    assertNull(
        access(
            tokens.access(
                tenant,
                account.toString(),
                "auriga",
                "service_account",
                "service",
                null,
                null,
                null,
                null,
                "")));
    assertNull(
        access(
            tokens.access(
                tenant,
                account.toString(),
                "auriga",
                "user",
                "portal",
                UUID.randomUUID(),
                null,
                clock.instant(),
                null,
                "openid")));
    var hmac =
        new SignedJWT(
            new JWSHeader.Builder(JWSAlgorithm.HS256)
                .type(new JOSEObjectType("at+jwt"))
                .keyID(key.getKeyID())
                .build(),
            JWTClaimsSet.parse(claims().toString()));
    hmac.sign(new com.nimbusds.jose.crypto.MACSigner(new byte[32]));
    assertNull(access(hmac.serialize()));
    var c = claims();
    c.put("iss", "https://other.example.com");
    assertNull(access(sign(c)));
    c = claims();
    c.remove("jti");
    assertNull(access(sign(c)));
    for (String raw : List.of("", "ait_example", "invalid", "x".repeat(16385)))
      assertNull(access(raw));
    assertNull(access(null));
    verifyNoInteractions(devices, authorization);
  }

  @Test
  void rejectsExpiredAbnormalLifetimeAndFutureIssuanceBeyondThirtySeconds() throws Exception {
    long now = clock.instant().getEpochSecond();
    for (long[] times :
        List.of(
            new long[] {now, now},
            new long[] {now - 300, now - 1},
            new long[] {now, now + 301},
            new long[] {now + 31, now + 300},
            new long[] {now + 20, now + 19},
            new long[] {Long.MIN_VALUE, now + 1})) {
      var c = claims();
      c.put("iat", times[0]);
      c.put("exp", times[1]);
      assertNull(access(sign(c)));
    }
    var c = claims();
    c.put("nbf", now + 31);
    assertNull(access(sign(c)));
    c = claims();
    c.put("exp", now + 10);
    c.put("nbf", now + 20);
    assertNull(access(sign(c)));
    verifyNoInteractions(devices, authorization);
    c = claims();
    c.put("iat", now + 30);
    c.put("nbf", now + 30);
    assertNotNull(access(sign(c)));
  }

  @Test
  void completionUsesCurrentBackingDeadlineAndCannotApproveAnExpiredToken() throws Exception {
    String raw = sign(claims());
    var initial =
        new DeviceAccessAuthority.Identity(
            tenant.toString(),
            account.toString(),
            device.toString(),
            clock.instant().plusSeconds(600));
    var shortened =
        new DeviceAccessAuthority.Identity(
            tenant.toString(),
            account.toString(),
            device.toString(),
            clock.instant().plusSeconds(23));
    when(devices.credential(tenant.toString(), account.toString(), device.toString()))
        .thenReturn(initial, shortened);
    assertEquals(shortened.expiresAt(), access(raw).expiresAt());
    when(devices.credential(tenant.toString(), account.toString(), device.toString()))
        .thenReturn(initial, null);
    assertNull(access(raw));
    when(devices.credential(tenant.toString(), account.toString(), device.toString()))
        .thenReturn(initial);
    doAnswer(
            invocation -> {
              clock.now = clock.now.plusSeconds(300);
              return member();
            })
        .when(authorization)
        .getEffectivePermissions(any());
    assertNull(access(raw));
  }

  @Test
  void inactiveMembershipAndRevokedInvokeCannotFallBackToTokenPermissions() throws Exception {
    String raw = sign(claims());
    for (var effective :
        List.of(
            member().toBuilder().clearPermissions().build(),
            member().toBuilder().setStatus(MemberStatus.MEMBER_STATUS_DISABLED).build(),
            member().toBuilder().setMemberType(MemberType.MEMBER_TYPE_UNSPECIFIED).build())) {
      doReturn(effective).when(authorization).getEffectivePermissions(any());
      assertNull(access(raw));
    }
    doReturn(member().toBuilder().setMemberType(MemberType.MEMBER_TYPE_EXTERNAL).build())
        .when(authorization)
        .getEffectivePermissions(any());
    assertNotNull(access(raw));
  }

  @Test
  void actualGrpcTransportVerifiesSignedAiAndSanitizesControlPlaneFaults() throws Exception {
    String raw = sign(claims());
    var settings =
        new auriga.common.security.AccessTokenSettings(
            tokens.issuer(),
            java.net.URI.create(tokens.issuer() + "/oauth2/jwks"),
            null,
            null,
            null,
            null,
            clock,
            false);
    var jwks =
        new com.nimbusds.jose.jwk.source.ImmutableJWKSet<com.nimbusds.jose.proc.SecurityContext>(
            new JWKSet(key.toPublicJWK()));
    var server =
        new auriga.common.grpc.GrpcServer(
            "127.0.0.1",
            0,
            List.of(new IamGrpcDevices(devices, authority)),
            new auriga.common.grpc.AurigaServerInterceptor(
                new auriga.common.security.AccessTokenVerifier(
                    auriga.common.security.AccessTokenDecoders.createInternal(settings, jwks)),
                Set.of(),
                Set.of("svc:ai-gateway")),
            null,
            false,
            Set.of());
    var signing = mock(SigningKeys.class);
    when(signing.current()).thenReturn(key);
    String secret = Secrets.random();
    var internal =
        new TokenService(
            signing,
            new ContractSchemas(),
            (t, s, c) -> Set.of(),
            clock,
            tokens.issuer(),
            Map.of("ai-gateway", Secrets.hash(secret)));
    String serviceToken = internal.issue("ai-gateway", secret);
    server.start();
    io.grpc.ManagedChannel channel = null;
    try {
      channel =
          auriga.common.grpc.GrpcChannels.open(
              "127.0.0.1:" + server.port(),
              new auriga.common.grpc.AurigaClientInterceptor(
                  () -> serviceToken, Duration.ofSeconds(5)));
      var stub =
          auriga.contracts.iam.v1.DeviceAccessServiceGrpc.newBlockingStub(channel)
              .withDeadlineAfter(5, java.util.concurrent.TimeUnit.SECONDS);
      var request = DeviceAiResourcePostgresTest.aiRequest(raw);
      var result = TenantContext.call(tenant, () -> stub.verifyDeviceAiAccess(request));
      assertTrue(result.getActive());
      assertEquals(account.toString(), result.getAccountId());
      assertEquals(device.toString(), result.getDeviceId());
      assertEquals(tenant.toString(), result.getTenantId());
      assertEquals(
          clock.instant().plusSeconds(300).getEpochSecond(), result.getExpiresAt().getSeconds());
      DeviceAiResourcePostgresTest.assertInactive(
          TenantContext.call(
              tenant,
              () ->
                  stub.verifyDeviceAiAccess(DeviceAiResourcePostgresTest.aiRequest(serviceToken))));
      DeviceAiResourcePostgresTest.assertGrpc(
          io.grpc.Status.Code.PERMISSION_DENIED, () -> stub.verifyDeviceAiAccess(request));
      DeviceAiResourcePostgresTest.assertGrpc(
          io.grpc.Status.Code.PERMISSION_DENIED,
          () -> TenantContext.call(UUID.randomUUID(), () -> stub.verifyDeviceAiAccess(request)));
      doReturn(null).when(authorization).getEffectivePermissions(any());
      DeviceAiResourcePostgresTest.unavailable(
          () -> TenantContext.call(tenant, () -> stub.verifyDeviceAiAccess(request)));
      doThrow(AurigaException.of(CommonErrors.UNAVAILABLE, "private", "internal-detail"))
          .when(authorization)
          .getEffectivePermissions(any());
      DeviceAiResourcePostgresTest.unavailable(
          () -> TenantContext.call(tenant, () -> stub.verifyDeviceAiAccess(request)));
      doThrow(new IllegalStateException("internal-detail"))
          .when(authorization)
          .getEffectivePermissions(any());
      DeviceAiResourcePostgresTest.unavailable(
          () -> TenantContext.call(tenant, () -> stub.verifyDeviceAiAccess(request)));
    } finally {
      if (channel != null) channel.shutdownNow();
      server.stop();
    }
  }

  @Test
  void surroundingTransactionCannotRetainIamLocksAcrossOrg() throws Exception {
    String raw = sign(claims());
    org.springframework.transaction.support.TransactionSynchronizationManager
        .setActualTransactionActive(true);
    try {
      assertEquals(
          "common.unavailable",
          assertThrows(AurigaException.class, () -> access(raw)).errorCode().code());
      verifyNoInteractions(devices, authorization);
    } finally {
      org.springframework.transaction.support.TransactionSynchronizationManager
          .setActualTransactionActive(false);
    }
  }

  @Test
  void crossTenantIsForbiddenAndControlPlaneErrorsAreSanitizedUnavailable() throws Exception {
    String raw = sign(claims());
    assertEquals(
        "common.forbidden",
        assertThrows(
                AurigaException.class,
                () -> TenantContext.call(UUID.randomUUID(), () -> authority.access(raw)))
            .errorCode()
            .code());
    verifyNoInteractions(devices, authorization);
    when(authorization.getEffectivePermissions(any()))
        .thenThrow(
            new AurigaException(CommonErrors.NOT_FOUND),
            new AurigaException(CommonErrors.FORBIDDEN),
            AurigaException.of(CommonErrors.UNAVAILABLE, "private", raw),
            new IllegalStateException(raw));
    assertNull(access(raw));
    assertNull(access(raw));
    for (int i = 0; i < 2; i++) {
      var failure = assertThrows(AurigaException.class, () -> access(raw));
      assertEquals("common.unavailable", failure.errorCode().code());
      assertFalse(failure.toString().contains(raw));
      assertNull(failure.getCause());
    }
    doReturn(null).when(authorization).getEffectivePermissions(any());
    assertEquals(
        "common.unavailable",
        assertThrows(AurigaException.class, () -> access(raw)).errorCode().code());
  }
}
