package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.audit.AuditLogger;
import auriga.common.context.TenantContext;
import auriga.common.error.*;
import auriga.common.events.EventPublisher;
import auriga.common.persistence.*;
import auriga.contracts.iam.v1.*;
import auriga.contracts.org.v1.*;
import auriga.iam.api.InstanceAiAuthorization;
import auriga.testsupport.PostgresExtension;
import com.nimbusds.jose.crypto.ECDSAVerifier;
import com.nimbusds.jose.jwk.*;
import com.nimbusds.jose.jwk.gen.ECKeyGenerator;
import com.nimbusds.jwt.SignedJWT;
import java.time.*;
import java.util.*;
import java.util.concurrent.*;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;
import tools.jackson.databind.node.ObjectNode;

@ExtendWith(PostgresExtension.class)
class DeviceAiResourcePostgresTest {
  static IamStore store;
  static JdbcTemplate ownerJdbc;
  final UUID tenant = UUID.randomUUID(),
      account = UUID.randomUUID(),
      device = UUID.randomUUID(),
      provider = UUID.randomUUID();
  final AdminCsrfPostgresTest.Time clock = new AdminCsrfPostgresTest.Time();
  final InstanceAiAuthorization authorization = mock(InstanceAiAuthorization.class);
  final Set<String> effective =
      new HashSet<>(
          Set.of(
              "auriga.connect.models.invoke",
              "auriga.connect.collab.use",
              "auriga.connect",
              "ai.models.invoke",
              "iam.accounts.admin"));
  TokenService tokens;
  SessionManager sessions;
  OAuthProvider oauth;
  ECKey key;
  String refresh, sid;

  @BeforeAll
  static void migrate() {
    var roles =
        PostgresExtension.serviceRoles(
            DeviceAiResourcePostgresTest.class, "device_ai_owner", "device_ai_app", "iam");
    ownerJdbc = new JdbcTemplate(roles.owner().dataSource());
    CommonMigrations.migrate(roles.owner().dataSource(), "iam");
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas("iam")
                .locations("classpath:db/iam")
                .load())
        .migrate();
    store =
        new IamStore(
            new JdbcTemplate(roles.runtime().dataSource()),
            new TenantTransactionManager(roles.runtime().dataSource()));
  }

  @BeforeEach
  void setup() throws Exception {
    clock.now = Instant.now().truncatedTo(java.time.temporal.ChronoUnit.SECONDS);
    key = new ECKeyGenerator(Curve.P_256).keyID(UUID.randomUUID().toString()).generate();
    var keys = mock(SigningKeys.class);
    when(keys.current()).thenReturn(key);
    when(keys.jwks())
        .thenReturn(Json.object("keys", List.of(Json.read(key.toPublicJWK().toJSONString()))));
    tokens =
        spy(
            new TokenService(
                keys,
                new ContractSchemas(),
                (t, s, c) -> effective,
                clock,
                "https://portal.example.com",
                Map.of()));
    sessions =
        new SessionManager(
            store,
            mock(SessionKv.class),
            tokens,
            clock,
            mock(EventPublisher.class),
            mock(AuditLogger.class));
    doAnswer(
            i -> {
              GetEffectivePermissionsRequest r = i.getArgument(0);
              assertEquals(tenant.toString(), r.getTenantId());
              assertEquals(account.toString(), r.getAccountId());
              return GetEffectivePermissionsResponse.newBuilder()
                  .setStatus(MemberStatus.MEMBER_STATUS_ACTIVE)
                  .setMemberType(MemberType.MEMBER_TYPE_MEMBER)
                  .addAllPermissions(effective)
                  .build();
            })
        .when(authorization)
        .getEffectivePermissions(any());
    oauth =
        new OAuthProvider(
            store, sessions, tokens, clock, List.of(client("native-example", true)), authorization);
    TenantContext.run(
        tenant,
        () ->
            store.tx(
                () -> {
                  store.insert(
                      "account",
                      account.toString(),
                      Json.object("accountId", account, "status", "active"),
                      null);
                  store.insert(
                      "idp_provider",
                      provider.toString(),
                      Json.object(
                          "providerId", provider, "connectorType", "team-account", "enabled", true),
                      null);
                  var issued =
                      sessions.create(
                          account,
                          provider,
                          "portal",
                          true,
                          Duration.ofSeconds(600),
                          Duration.ofSeconds(120));
                  sid = issued.session().path("sessionId").asString();
                  store.insert(
                      "device",
                      device.toString(),
                      Json.object(
                          "deviceId",
                          device,
                          "accountId",
                          account,
                          "sessionId",
                          sid,
                          "clientId",
                          "native-example",
                          "scope",
                          "auriga.connect",
                          "authorizationMode",
                          "native_tx",
                          "revoked",
                          false),
                      null);
                  refresh = Secrets.random();
                  store.insert(
                      "oauth_grant",
                      Secrets.hash(refresh),
                      Json.object(
                          "kind",
                          "refresh",
                          "clientId",
                          "native-example",
                          "sessionId",
                          sid,
                          "deviceId",
                          device,
                          "scope",
                          "auriga.connect",
                          "authorizationMode",
                          "native_tx",
                          "used",
                          false,
                          "expiresAt",
                          clock.instant().plusSeconds(600).toString()),
                      clock.instant().plusSeconds(600));
                  return null;
                }));
  }

  OAuthClient client(String id, boolean nativeConnect) {
    return new OAuthClient(
        id,
        tenant,
        null,
        Set.of(),
        Set.of("auriga.connect"),
        Set.of(OAuthProvider.DEVICE, "refresh_token"),
        true,
        nativeConnect);
  }

  Map<String, String> input(String raw, boolean ai) {
    var input =
        new HashMap<>(
            Map.of(
                "grant_type",
                "refresh_token",
                "client_id",
                "native-example",
                "refresh_token",
                raw));
    if (ai) input.put("resource", "urn:auriga:ai-gateway");
    return input;
  }

  ObjectNode exchange(String raw, boolean ai) {
    return TenantContext.call(tenant, () -> oauth.token(input(raw, ai)));
  }

  ObjectNode grant() {
    return TenantContext.call(
        tenant, () -> store.tx(() -> store.get("oauth_grant", Secrets.hash(refresh), false)));
  }

  ObjectNode session() {
    return TenantContext.call(tenant, () -> store.tx(() -> store.byId("session", sid, false)));
  }

  ObjectNode otherAccountSession() {
    return TenantContext.call(
        tenant,
        () ->
            store.tx(
                () -> {
                  UUID otherAccount = UUID.randomUUID();
                  store.insert(
                      "account",
                      otherAccount.toString(),
                      Json.object("accountId", otherAccount, "status", "active"),
                      null);
                  return sessions
                      .create(
                          otherAccount,
                          provider,
                          "portal",
                          true,
                          Duration.ofSeconds(600),
                          Duration.ofSeconds(120))
                      .session();
                }));
  }

  @Test
  void realRpcAiAdmissionKeepsPlatformTeamSeparateAndDoesNotTouchIdleOrRefresh() {
    ObjectNode issued = exchange(refresh, true);
    String ai = issued.path("access_token").asString();
    String platform =
        exchange(issued.path("refresh_token").asString(), false).path("access_token").asString();
    var before = session();
    var grants = TenantContext.call(tenant, () -> store.tx(() -> store.all("oauth_grant")));
    withRpc(
        ai,
        authorization,
        rpc -> {
          var result = rpc.ai(ai);
          assertTrue(result.getActive());
          assertEquals(tenant.toString(), result.getTenantId());
          assertEquals(account.toString(), result.getAccountId());
          assertEquals(device.toString(), result.getDeviceId());
          assertEquals(tokens.verify(ai).path("exp").asLong(), result.getExpiresAt().getSeconds());
          assertInactive(rpc.ai(platform));
          assertInactive(
              TenantContext.call(tenant, () -> rpc.gateway().verifyDeviceAccess(request(ai))));
          assertTrue(
              TenantContext.call(tenant, () -> rpc.gateway().verifyDeviceAccess(request(platform)))
                  .getActive());
          assertTrue(
              TenantContext.call(
                      tenant,
                      () ->
                          rpc.collab()
                              .verifyDeviceCredential(
                                  VerifyDeviceCredentialRequest.newBuilder()
                                      .setTenantId(tenant.toString())
                                      .setAccountId(account.toString())
                                      .setDeviceId(device.toString())
                                      .build()))
                  .getActive());
          assertInactive(rpc.ai("ait_example"));
          assertGrpc(
              io.grpc.Status.Code.PERMISSION_DENIED,
              () ->
                  TenantContext.call(
                      tenant, () -> rpc.collab().verifyDeviceAiAccess(aiRequest(ai))));
          assertGrpc(
              io.grpc.Status.Code.PERMISSION_DENIED,
              () ->
                  TenantContext.call(tenant, () -> rpc.edge().verifyDeviceAiAccess(aiRequest(ai))));
          assertGrpc(
              io.grpc.Status.Code.PERMISSION_DENIED,
              () -> rpc.gateway().verifyDeviceAiAccess(aiRequest(ai)));
          assertGrpc(
              io.grpc.Status.Code.INVALID_ARGUMENT,
              () ->
                  TenantContext.call(
                      TenantContext.PLATFORM_TENANT,
                      () -> rpc.gateway().verifyDeviceAiAccess(aiRequest(ai))));
          assertGrpc(
              io.grpc.Status.Code.PERMISSION_DENIED,
              () ->
                  TenantContext.call(
                      UUID.randomUUID(), () -> rpc.gateway().verifyDeviceAiAccess(aiRequest(ai))));
          assertGrpc(
              io.grpc.Status.Code.UNAUTHENTICATED,
              () ->
                  TenantContext.call(
                      tenant, () -> rpc.anonymous().verifyDeviceAiAccess(aiRequest(ai))));
          assertGrpc(
              io.grpc.Status.Code.UNAUTHENTICATED,
              () ->
                  TenantContext.call(
                      tenant, () -> rpc.external().verifyDeviceAiAccess(aiRequest(ai))));
          assertGrpc(
              io.grpc.Status.Code.PERMISSION_DENIED,
              () ->
                  TenantContext.call(
                      tenant,
                      () ->
                          rpc.gateway()
                              .verifyDeviceCredential(
                                  VerifyDeviceCredentialRequest.getDefaultInstance())));
        });
    assertEquals(before, session());
    assertEquals(
        grants, TenantContext.call(tenant, () -> store.tx(() -> store.all("oauth_grant"))));
  }

  @Test
  void realRpcRechecksMembershipAndPermissionsOnEveryAdmission() {
    String ai = exchange(refresh, true).path("access_token").asString();
    withRpc(
        ai,
        authorization,
        rpc -> {
          assertTrue(rpc.ai(ai).getActive());
          effective.remove(DeviceAiResource.INVOKE);
          assertInactive(rpc.ai(ai));
          effective.add(DeviceAiResource.INVOKE);
          assertTrue(rpc.ai(ai).getActive());
          for (var status :
              List.of(
                  MemberStatus.MEMBER_STATUS_DISABLED,
                  MemberStatus.MEMBER_STATUS_OFFBOARDED,
                  MemberStatus.MEMBER_STATUS_UNSPECIFIED)) {
            doReturn(DeviceAiAccessAuthorityTest.member().toBuilder().setStatus(status).build())
                .when(authorization)
                .getEffectivePermissions(any());
            assertInactive(rpc.ai(ai));
          }
          for (var type : List.of(MemberType.MEMBER_TYPE_UNSPECIFIED, MemberType.UNRECOGNIZED)) {
            var member = DeviceAiAccessAuthorityTest.member().toBuilder();
            if (type == MemberType.UNRECOGNIZED) member.setMemberTypeValue(99);
            else member.setMemberType(type);
            doReturn(member.build()).when(authorization).getEffectivePermissions(any());
            assertInactive(rpc.ai(ai));
          }
          doReturn(
                  DeviceAiAccessAuthorityTest.member().toBuilder()
                      .setMemberType(MemberType.MEMBER_TYPE_EXTERNAL)
                      .build())
              .when(authorization)
              .getEffectivePermissions(any());
          assertTrue(rpc.ai(ai).getActive());
          for (var error : List.of(CommonErrors.NOT_FOUND, CommonErrors.FORBIDDEN)) {
            doThrow(new AurigaException(error)).when(authorization).getEffectivePermissions(any());
            assertInactive(rpc.ai(ai));
          }
        });
  }

  @Test
  void realRpcControlPlaneNullMissingExceptionsAndDatabaseFaultAreUnavailableWithoutDetails() {
    String ai = exchange(refresh, true).path("access_token").asString();
    withRpc(
        ai,
        authorization,
        rpc -> {
          doReturn(null).when(authorization).getEffectivePermissions(any());
          unavailable(() -> rpc.ai(ai));
          doThrow(new IllegalStateException("private-org-detail " + ai))
              .when(authorization)
              .getEffectivePermissions(any());
          unavailable(() -> rpc.ai(ai));
          doThrow(AurigaException.of(CommonErrors.UNAVAILABLE, "private", ai))
              .when(authorization)
              .getEffectivePermissions(any());
          unavailable(() -> rpc.ai(ai));
          ownerJdbc.execute("REVOKE SELECT ON iam.device FROM device_ai_app");
          try {
            unavailable(() -> rpc.ai(ai));
          } finally {
            ownerJdbc.execute("GRANT SELECT ON iam.device TO device_ai_app");
          }
        });
    withRpc(ai, null, rpc -> unavailable(() -> rpc.ai(ai)));
  }

  @Test
  void realRpcRevocationAccountSessionAndOwnershipChangesReturnEmptyInactive() {
    String ai = exchange(refresh, true).path("access_token").asString();
    var otherSession = otherAccountSession();
    String otherAccount = otherSession.path("accountId").asString();
    var originalSession = session();
    var originalDevice =
        TenantContext.call(
            tenant, () -> store.tx(() -> store.get("device", device.toString(), false)));
    withRpc(
        ai,
        authorization,
        rpc -> {
          for (String table : List.of("device", "account", "session")) {
            String lookup =
                table.equals("device")
                    ? device.toString()
                    : table.equals("account") ? account.toString() : sid;
            var original =
                TenantContext.call(
                    tenant,
                    () ->
                        store.tx(
                            () ->
                                table.equals("session")
                                    ? store.byId(table, lookup, false)
                                    : store.get(table, lookup, false)));
            Map<String, Object> changes =
                switch (table) {
                  case "device" ->
                      Map.of(
                          "revoked",
                          true,
                          "accountId",
                          otherAccount,
                          "sessionId",
                          otherSession.path("sessionId").asString(),
                          "scope",
                          "openid");
                  case "account" -> Map.of("status", "disabled");
                  default ->
                      Map.of(
                          "revoked",
                          true,
                          "audience",
                          "admin",
                          "accountId",
                          otherAccount,
                          "expiresAt",
                          clock.instant().toString(),
                          "idleExpiresAt",
                          clock.instant().toString());
                };
            for (var change : changes.entrySet()) {
              TenantContext.run(
                  tenant,
                  () ->
                      store.tx(
                          () -> {
                            var row = original.deepCopy();
                            row.set(change.getKey(), Json.MAPPER.valueToTree(change.getValue()));
                            store.save(table, row);
                            return null;
                          }));
              var persisted =
                  TenantContext.call(
                      tenant,
                      () ->
                          store.tx(
                              () ->
                                  table.equals("session")
                                      ? store.byId(table, lookup, false)
                                      : store.get(table, lookup, false)));
              assertEquals(
                  Json.MAPPER.valueToTree(change.getValue()),
                  persisted.path(change.getKey()),
                  table + "." + change.getKey());
              assertInactive(rpc.ai(ai));
              TenantContext.run(
                  tenant,
                  () ->
                      store.tx(
                          () -> {
                            store.save(table, original);
                            return null;
                          }));
              assertTrue(rpc.ai(ai).getActive());
            }
            TenantContext.run(
                tenant,
                () ->
                    store.tx(
                        () -> {
                          if (table.equals("account")) {
                            // Move both FK dependants to the real other account before deletion.
                            var ownedDevice = originalDevice.deepCopy();
                            ownedDevice.put("accountId", otherAccount);
                            store.save("device", ownedDevice);
                            var ownedSession = originalSession.deepCopy();
                            ownedSession.put("accountId", otherAccount);
                            store.save("session", ownedSession);
                          }
                          store.delete(
                              table,
                              table.equals("session")
                                  ? original.path("cookieHash").asString()
                                  : lookup);
                          return null;
                        }));
            assertNull(
                TenantContext.call(
                    tenant,
                    () ->
                        store.tx(
                            () ->
                                table.equals("session")
                                    ? store.byId(table, lookup, false)
                                    : store.get(table, lookup, false))));
            assertInactive(rpc.ai(ai));
            if (table.equals("session")) continue;
            // Restore the deleted row through the production insert path.
            TenantContext.run(
                tenant,
                () ->
                    store.tx(
                        () -> {
                          store.insert(table, lookup, original, null);
                          if (table.equals("account")) {
                            store.save("session", originalSession);
                            store.save("device", originalDevice);
                          }
                          return null;
                        }));
            assertTrue(rpc.ai(ai).getActive());
          }
        });
  }

  @Test
  void realRpcReleasesIamLocksBeforeOrgAndRechecksChangesAtCompletion() {
    String ai = exchange(refresh, true).path("access_token").asString();
    var original = session();
    withRpc(
        ai,
        authorization,
        rpc -> {
          doAnswer(
                  invocation -> {
                    assertFalse(
                        org.springframework.transaction.support.TransactionSynchronizationManager
                            .isActualTransactionActive());
                    // A separate connection must be able to acquire both locks during the org
                    // query.
                    try (var pool = Executors.newVirtualThreadPerTaskExecutor()) {
                      pool.submit(
                              () ->
                                  TenantContext.call(
                                      tenant,
                                      () ->
                                          store.tx(
                                              () -> {
                                                store
                                                    .networkJdbc()
                                                    .execute("SET LOCAL lock_timeout='1s'");
                                                var row = store.byId("session", sid, true);
                                                store.get("device", device.toString(), true);
                                                row.put(
                                                    "expiresAt",
                                                    clock.instant().plusSeconds(23).toString());
                                                row.put(
                                                    "idleExpiresAt",
                                                    clock.instant().plusSeconds(41).toString());
                                                store.save("session", row);
                                                return null;
                                              })))
                          .get(3, TimeUnit.SECONDS);
                    }
                    return DeviceAiAccessAuthorityTest.member();
                  })
              .when(authorization)
              .getEffectivePermissions(any());
          assertEquals(
              clock.instant().plusSeconds(23).getEpochSecond(),
              rpc.ai(ai).getExpiresAt().getSeconds());
          doAnswer(
                  invocation -> {
                    update("session", sid, "expiresAt", clock.instant().plusSeconds(60).toString());
                    update(
                        "session",
                        sid,
                        "idleExpiresAt",
                        clock.instant().plusSeconds(17).toString());
                    return DeviceAiAccessAuthorityTest.member();
                  })
              .when(authorization)
              .getEffectivePermissions(any());
          assertEquals(
              clock.instant().plusSeconds(17).getEpochSecond(),
              rpc.ai(ai).getExpiresAt().getSeconds());
          doAnswer(
                  invocation -> {
                    update("device", device.toString(), "revoked", true);
                    return DeviceAiAccessAuthorityTest.member();
                  })
              .when(authorization)
              .getEffectivePermissions(any());
          assertInactive(rpc.ai(ai));
          update("device", device.toString(), "revoked", false);
          TenantContext.run(
              tenant,
              () ->
                  store.tx(
                      () -> {
                        store.save("session", original);
                        return null;
                      }));
          doAnswer(
                  invocation -> {
                    // Keep backing valid beyond this AI token, so completion must check the token
                    // deadline.
                    update(
                        "session",
                        sid,
                        "idleExpiresAt",
                        clock.instant().plusSeconds(600).toString());
                    clock.now = Instant.ofEpochSecond(tokens.verify(ai).path("exp").asLong());
                    return DeviceAiAccessAuthorityTest.member();
                  })
              .when(authorization)
              .getEffectivePermissions(any());
          assertInactive(rpc.ai(ai));
        });
  }

  void update(String table, String lookup, String field, Object value) {
    TenantContext.run(
        tenant,
        () ->
            store.tx(
                () -> {
                  var row =
                      table.equals("session")
                          ? store.byId(table, lookup, true)
                          : store.get(table, lookup, true);
                  row.set(field, Json.MAPPER.valueToTree(value));
                  store.save(table, row);
                  return null;
                }));
  }

  static VerifyDeviceAccessRequest request(String raw) {
    return VerifyDeviceAccessRequest.newBuilder().setAccessToken(raw).build();
  }

  static VerifyDeviceAiAccessRequest aiRequest(String raw) {
    return VerifyDeviceAiAccessRequest.newBuilder().setAccessToken(raw).build();
  }

  static void assertInactive(VerifyDeviceAccessResponse response) {
    assertEquals(VerifyDeviceAccessResponse.getDefaultInstance(), response);
  }

  static void assertInactive(VerifyDeviceAiAccessResponse response) {
    assertEquals(VerifyDeviceAiAccessResponse.getDefaultInstance(), response);
  }

  static io.grpc.StatusRuntimeException assertGrpc(io.grpc.Status.Code code, Runnable work) {
    var failure = assertThrows(io.grpc.StatusRuntimeException.class, work::run);
    assertEquals(code, failure.getStatus().getCode());
    return failure;
  }

  static void unavailable(Runnable work) {
    var failure = assertGrpc(io.grpc.Status.Code.UNAVAILABLE, work);
    assertEquals("common.unavailable", failure.getStatus().getDescription());
    assertEquals(
        "common.unavailable",
        failure.getTrailers().get(auriga.common.grpc.GrpcMetadata.ERROR_CODE));
    assertNull(failure.getTrailers().get(auriga.common.grpc.GrpcMetadata.ERROR_PARAMS));
  }

  record Rpc(
      DeviceAccessServiceGrpc.DeviceAccessServiceBlockingStub gateway,
      DeviceAccessServiceGrpc.DeviceAccessServiceBlockingStub collab,
      DeviceAccessServiceGrpc.DeviceAccessServiceBlockingStub edge,
      DeviceAccessServiceGrpc.DeviceAccessServiceBlockingStub anonymous,
      DeviceAccessServiceGrpc.DeviceAccessServiceBlockingStub external,
      UUID tenant) {
    VerifyDeviceAiAccessResponse ai(String raw) {
      return TenantContext.call(tenant, () -> gateway.verifyDeviceAiAccess(aiRequest(raw)));
    }
  }

  static io.grpc.ClientInterceptor perCallDeadline() {
    return new io.grpc.ClientInterceptor() {
      @Override
      public <Q, R> io.grpc.ClientCall<Q, R> interceptCall(
          io.grpc.MethodDescriptor<Q, R> method,
          io.grpc.CallOptions options,
          io.grpc.Channel next) {
        // Allocate the bounded deadline for this call, never for the shared stub.
        return next.newCall(method, options.withDeadlineAfter(5, TimeUnit.SECONDS));
      }
    };
  }

  void withRpc(
      String externalToken, InstanceAiAuthorization auth, java.util.function.Consumer<Rpc> work) {
    var provider = new org.springframework.beans.factory.support.StaticListableBeanFactory();
    if (auth != null) provider.addBean("authorization", auth);
    var service =
        new IamRuntimeConfiguration()
            .iamGrpcDevices(
                store,
                sessions,
                tokens,
                new IamGrpcSessions(sessions),
                clock,
                provider.getBeanProvider(InstanceAiAuthorization.class));
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
    var verifier =
        new auriga.common.security.AccessTokenVerifier(
            auriga.common.security.AccessTokenDecoders.createInternal(settings, jwks));
    var server =
        new auriga.common.grpc.GrpcServer(
            "127.0.0.1",
            0,
            List.of(service),
            new auriga.common.grpc.AurigaServerInterceptor(
                verifier, Set.of(), Set.of("svc:ai-gateway", "svc:collab", "svc:edge")),
            null,
            false,
            Set.of());
    var signing = mock(SigningKeys.class);
    when(signing.current()).thenReturn(key);
    var secrets =
        Map.of(
            "ai-gateway", Secrets.random(), "collab", Secrets.random(), "edge", Secrets.random());
    var hashes = new HashMap<String, String>();
    secrets.forEach((name, secret) -> hashes.put(name, Secrets.hash(secret)));
    var internal =
        new TokenService(
            signing, new ContractSchemas(), (t, s, c) -> Set.of(), clock, tokens.issuer(), hashes);
    var channels = new ArrayList<io.grpc.ManagedChannel>();
    server.start();
    try {
      java.util.function.Function<String, DeviceAccessServiceGrpc.DeviceAccessServiceBlockingStub>
          stub =
              raw -> {
                var channel =
                    raw == null
                        ? auriga.common.grpc.GrpcChannels.open(
                            "127.0.0.1:" + server.port(), perCallDeadline())
                        : auriga.common.grpc.GrpcChannels.open(
                            "127.0.0.1:" + server.port(),
                            new auriga.common.grpc.AurigaClientInterceptor(
                                () -> raw, Duration.ofSeconds(5)),
                            perCallDeadline());
                channels.add(channel);
                return DeviceAccessServiceGrpc.newBlockingStub(channel);
              };
      work.accept(
          new Rpc(
              stub.apply(internal.issue("ai-gateway", secrets.get("ai-gateway"))),
              stub.apply(internal.issue("collab", secrets.get("collab"))),
              stub.apply(internal.issue("edge", secrets.get("edge"))),
              stub.apply(null),
              stub.apply(externalToken),
              tenant));
    } finally {
      channels.forEach(io.grpc.ManagedChannel::shutdownNow);
      server.stop();
    }
  }

  @Test
  void signedAiClaimsAreNarrowBoundedAndRotationWorksInBothDirections() throws Exception {
    ObjectNode before = session();
    clock.now = clock.now.plusSeconds(50);
    ObjectNode ai = exchange(refresh, true);
    assertTrue(ai.has("access_token"), ai.toString());
    SignedJWT jwt = SignedJWT.parse(ai.path("access_token").asString());
    assertTrue(jwt.verify(new ECDSAVerifier(key.toPublicJWK())));
    assertEquals("at+jwt", jwt.getHeader().getType().toString());
    ObjectNode claims = tokens.verify(ai.path("access_token").asString());
    assertEquals("auriga.ai-gateway", claims.path("aud").asString());
    assertEquals("device", claims.path("ptyp").asString());
    assertEquals("device", claims.path("ctx").asString());
    assertEquals(device.toString(), claims.path("did").asString());
    assertEquals(account.toString(), claims.path("sub").asString());
    assertEquals(tenant.toString(), claims.path("tid").asString());
    assertFalse(claims.has("sid"));
    assertFalse(claims.has("roles"));
    assertFalse(claims.has("instanceId"));
    assertEquals(
        Json.MAPPER.valueToTree(List.of("auriga.connect.models.invoke")), claims.path("perms"));
    assertEquals("auriga.connect", claims.path("scope").asString());
    assertEquals(70, ai.path("expires_in").asInt());
    assertEquals(
        Instant.parse(before.path("idleExpiresAt").asString()).getEpochSecond(),
        claims.path("exp").asLong());
    assertEquals(before.path("idleExpiresAt"), session().path("idleExpiresAt"));
    assertEquals(before.path("expiresAt"), session().path("expiresAt"));
    ObjectNode platform = exchange(ai.path("refresh_token").asString(), false);
    assertEquals(
        "auriga", tokens.verify(platform.path("access_token").asString()).path("aud").asString());
    ObjectNode next = exchange(platform.path("refresh_token").asString(), true);
    assertTrue(next.has("access_token"), next.toString());
    assertEquals(
        device.toString(),
        tokens.verify(next.path("access_token").asString()).path("did").asString());
    assertNull(
        TenantContext.call(
            tenant,
            () ->
                new DeviceAccessAuthority(store, sessions, tokens)
                    .access(next.path("access_token").asString())));
  }

  @Test
  void invalidTargetsDoNotConsumeRefreshOrRevokeItsSession() {
    for (String resource :
        List.of("", "auriga.ai-gateway", "urn:other", "urn:auriga:ai-gateway urn:other")) {
      var input = input(refresh, true);
      input.put("resource", resource);
      assertEquals(
          "invalid_target",
          TenantContext.call(tenant, () -> oauth.token(input)).path("error").asString());
      assertFalse(grant().path("used").asBoolean());
    }
    var audience = input(refresh, false);
    audience.put("audience", "auriga.ai-gateway");
    assertEquals(
        "invalid_target",
        TenantContext.call(tenant, () -> oauth.token(audience)).path("error").asString());
    assertTrue(exchange(refresh, false).has("access_token"));
    assertEquals(
        "invalid_target",
        TenantContext.call(tenant, () -> oauth.token(audience)).path("error").asString());
    assertFalse(session().path("revoked").asBoolean());
  }

  @Test
  void noModelPermissionIsRejectedWithoutConsumingRefresh() {
    effective.remove("auriga.connect.models.invoke");
    assertEquals("invalid_grant", exchange(refresh, true).path("error").asString());
    assertFalse(grant().path("used").asBoolean());
    assertTrue(exchange(refresh, false).has("access_token"));
  }

  @Test
  void inactiveUnknownOrUnavailableMembershipCannotIssueOrConsume() {
    for (MemberStatus status :
        List.of(
            MemberStatus.MEMBER_STATUS_DISABLED,
            MemberStatus.MEMBER_STATUS_OFFBOARDED,
            MemberStatus.MEMBER_STATUS_UNSPECIFIED)) {
      doReturn(
              GetEffectivePermissionsResponse.newBuilder()
                  .setStatus(status)
                  .setMemberType(MemberType.MEMBER_TYPE_MEMBER)
                  .addAllPermissions(effective)
                  .build())
          .when(authorization)
          .getEffectivePermissions(any());
      assertEquals("invalid_grant", exchange(refresh, true).path("error").asString());
      assertFalse(grant().path("used").asBoolean());
    }
    doThrow(new IllegalStateException("offline"))
        .when(authorization)
        .getEffectivePermissions(any());
    assertThrows(auriga.common.error.AurigaException.class, () -> exchange(refresh, true));
    assertFalse(grant().path("used").asBoolean());
    verify(authorization, times(4))
        .getEffectivePermissions(
            GetEffectivePermissionsRequest.newBuilder()
                .setTenantId(tenant.toString())
                .setAccountId(account.toString())
                .build());
    verifyNoMoreInteractions(authorization);
  }

  @Test
  void deviceBindingAndAccountRevocationFailClosed() {
    TenantContext.run(
        tenant,
        () -> {
          ObjectNode otherSession = otherAccountSession();
          oauth =
              new OAuthProvider(
                  store,
                  sessions,
                  tokens,
                  clock,
                  List.of(client("native-example", true), client("other-native", true)),
                  authorization);
          ObjectNode original = store.tx(() -> store.get("device", device.toString(), false));
          Map<String, Object> changes =
              Map.of(
                  "clientId",
                  "other-native",
                  "sessionId",
                  otherSession.path("sessionId").asString(),
                  "accountId",
                  otherSession.path("accountId").asString(),
                  "scope",
                  "openid",
                  "authorizationMode",
                  "manual",
                  "revoked",
                  true);
          for (var change : changes.entrySet()) {
            String field = change.getKey();
            store.tx(
                () -> {
                  ObjectNode row = original.deepCopy();
                  row.set(field, Json.MAPPER.valueToTree(change.getValue()));
                  store.save("device", row);
                  return null;
                });
            assertEquals(
                Json.MAPPER.valueToTree(change.getValue()),
                store.tx(() -> store.get("device", device.toString(), false)).path(field),
                field);
            assertEquals("invalid_grant", exchange(refresh, true).path("error").asString(), field);
            assertFalse(grant().path("used").asBoolean());
          }
          store.tx(
              () -> {
                store.save("device", original);
                ObjectNode row = store.get("account", account.toString(), true);
                row.put("status", "disabled");
                store.save("account", row);
                return null;
              });
          assertEquals("invalid_grant", exchange(refresh, true).path("error").asString());
          assertFalse(grant().path("used").asBoolean());
        });
  }

  @Test
  void forgedCrossTenantClientAndNonNativeRefreshAreDenied() {
    assertEquals("invalid_grant", exchange("forged", true).path("error").asString());
    TenantContext.run(
        UUID.randomUUID(),
        () ->
            assertThrows(
                auriga.common.error.AurigaException.class,
                () -> oauth.token(input(refresh, true))));
    var other =
        new OAuthProvider(
            store,
            sessions,
            tokens,
            clock,
            List.of(client("native-example", true), client("other-native", true)));
    var wrong = input(refresh, true);
    wrong.put("client_id", "other-native");
    assertEquals(
        "invalid_grant",
        TenantContext.call(tenant, () -> other.token(wrong)).path("error").asString());
    TenantContext.run(
        tenant,
        () ->
            store.tx(
                () -> {
                  ObjectNode row = store.get("oauth_grant", Secrets.hash(refresh), true);
                  row.put("authorizationMode", "manual");
                  store.save("oauth_grant", row);
                  return null;
                }));
    assertEquals("invalid_grant", exchange(refresh, true).path("error").asString());
    assertFalse(grant().path("used").asBoolean());
  }

  @Test
  void idleOrAbsoluteExpiryCannotBeExtendedByAi() {
    clock.now = clock.now.plusSeconds(121);
    assertEquals("invalid_grant", exchange(refresh, true).path("error").asString());
    assertFalse(grant().path("used").asBoolean());
  }

  @Test
  void absoluteDeadlineCapsAiEvenWhenIdleDeadlineIsLater() {
    TenantContext.run(
        tenant,
        () ->
            store.tx(
                () -> {
                  ObjectNode row = store.byId("session", sid, true);
                  row.put("expiresAt", clock.instant().plusSeconds(31).toString());
                  store.save("session", row);
                  return null;
                }));
    ObjectNode result = exchange(refresh, true);
    assertTrue(result.has("access_token"), result.toString());
    assertEquals(31, result.path("expires_in").asInt());
    assertEquals(
        clock.instant().plusSeconds(31).getEpochSecond(),
        tokens.verify(result.path("access_token").asString()).path("exp").asLong());
  }

  @Test
  void invalidScopeAndIdentityParametersLeaveRefreshAvailable() {
    for (String scope : List.of("", "openid", "auriga.connect other")) {
      var input = input(refresh, true);
      input.put("scope", scope);
      assertEquals(
          "invalid_scope",
          TenantContext.call(tenant, () -> oauth.token(input)).path("error").asString());
      assertFalse(grant().path("used").asBoolean());
    }
    for (String field : List.of("did", "sid", "tenant_id", "permissions")) {
      var input = input(refresh, true);
      input.put(field, "forged");
      assertEquals(
          "invalid_request",
          TenantContext.call(tenant, () -> oauth.token(input)).path("error").asString());
      assertFalse(grant().path("used").asBoolean());
    }
    assertTrue(exchange(refresh, true).has("access_token"));
  }

  @Test
  void responseTransactionRollbackRestoresConsumedRefreshAndSuccessor() {
    TenantContext.run(
        tenant,
        () -> {
          int before = store.tx(() -> store.all("oauth_grant").size());
          assertThrows(
              IllegalStateException.class,
              () ->
                  store.tx(
                      () -> {
                        assertTrue(oauth.token(input(refresh, true)).has("access_token"));
                        throw new IllegalStateException("response transaction failed");
                      }));
          assertFalse(grant().path("used").asBoolean());
          assertEquals(before, store.tx(() -> store.all("oauth_grant").size()));
          assertTrue(exchange(refresh, true).has("access_token"));
        });
  }

  @Test
  void signingFailureLeavesTheRefreshUsable() {
    doThrow(new IllegalStateException("signer unavailable"))
        .when(tokens)
        .deviceAi(any(), any(), anyString(), anySet());
    assertThrows(IllegalStateException.class, () -> exchange(refresh, true));
    assertFalse(grant().path("used").asBoolean());
    doCallRealMethod().when(tokens).deviceAi(any(), any(), anyString(), anySet());
    assertTrue(exchange(refresh, true).has("access_token"));
  }

  @Test
  void parallelAiAndPlatformRotationHasOneWinnerAndReplayRevokesFamily() throws Exception {
    try (var pool = Executors.newVirtualThreadPerTaskExecutor()) {
      var go = new CountDownLatch(1);
      var a =
          pool.submit(
              () -> {
                go.await();
                return exchange(refresh, true);
              });
      var b =
          pool.submit(
              () -> {
                go.await();
                return exchange(refresh, false);
              });
      go.countDown();
      var results = List.of(a.get(10, TimeUnit.SECONDS), b.get(10, TimeUnit.SECONDS));
      assertEquals(1, results.stream().filter(r -> r.has("access_token")).count());
      assertTrue(session().path("revoked").asBoolean());
      String next =
          results.stream()
              .filter(r -> r.has("access_token"))
              .findFirst()
              .orElseThrow()
              .path("refresh_token")
              .asString();
      assertEquals("invalid_grant", exchange(next, true).path("error").asString());
    }
  }
}
