package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.audit.AuditLogger;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.events.EventPublisher;
import auriga.common.persistence.*;
import auriga.iam.api.IdentityPolicyHooks;
import auriga.testsupport.PostgresExtension;
import java.time.*;
import java.util.*;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;
import tools.jackson.databind.node.ObjectNode;

@ExtendWith(PostgresExtension.class)
class NativeConnectPostgresTest {
  static IamStore store;
  final UUID tenant = UUID.randomUUID(), provider = UUID.randomUUID(), account = UUID.randomUUID();
  final AdminCsrfPostgresTest.Time clock = new AdminCsrfPostgresTest.Time();
  LoginManager login;
  OAuthProvider oauth;
  SessionManager sessions;
  TokenService tokens;
  SigningKeys keys;
  Secrets secrets;
  final Set<String> effective =
      new HashSet<>(Set.of("auriga.connect", "auriga.connect.collab.use"));
  final String verifier = "v".repeat(43);
  final String issuer = "https://portal.example.com";
  final String totp = "JBSWY3DPEHPK3PXP";

  @BeforeAll
  static void migrate() {
    var roles =
        PostgresExtension.serviceRoles(
            NativeConnectPostgresTest.class, "native_owner", "native_app", "iam");
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
  void setup() {
    clock.now = Instant.now();
    TenantContext.run(
        tenant,
        () ->
            store.tx(
                () -> {
                  byte[] bytes = new byte[32];
                  new java.security.SecureRandom().nextBytes(bytes);
                  secrets = new Secrets(Base64.getEncoder().encodeToString(bytes));
                  ContractSchemas schemas = new ContractSchemas();
                  keys = new SigningKeys(store, secrets, clock, Duration.ofSeconds(30));
                  String kid = keys.publishNext();
                  clock.now = clock.now.plusSeconds(31);
                  keys.activate(kid);
                  tokens =
                      new TokenService(
                          keys, schemas, (t, s, c) -> effective, clock, issuer, Map.of());
                  SessionKv kv = mock(SessionKv.class);
                  EventPublisher events = mock(EventPublisher.class);
                  AuditLogger audit = mock(AuditLogger.class);
                  sessions = new SessionManager(store, kv, tokens, clock, events, audit);
                  TeamAccounts team = new TeamAccounts(store, secrets, clock, events);
                  TeamPasskeys passkeys = new TeamPasskeys(store, secrets, clock, events);
                  QrChallenges qr = new QrChallenges(store, clock);
                  ConnectorRegistry registry =
                      new ConnectorRegistry(
                          schemas, List.of(new TeamAccountConnector(team, passkeys, qr, issuer)));
                  login =
                      new LoginManager(
                          store,
                          secrets,
                          schemas,
                          registry,
                          new EmergencyAccounts(store, secrets, clock, false),
                          team,
                          sessions,
                          mock(IdentityPolicyHooks.class),
                          events,
                          audit,
                          clock,
                          issuer);
                  oauth =
                      new OAuthProvider(
                          store,
                          sessions,
                          tokens,
                          clock,
                          List.of(
                              new OAuthClient(
                                  "native-example",
                                  tenant,
                                  null,
                                  Set.of(),
                                  Set.of("auriga.connect"),
                                  Set.of(OAuthProvider.DEVICE, "refresh_token"),
                                  true,
                                  true)));
                  login.nativeDevices(oauth);
                  store.insert(
                      "idp_provider",
                      provider.toString(),
                      Json.object(
                          "providerId",
                          provider,
                          "connectorType",
                          "team-account",
                          "enabled",
                          true,
                          "display",
                          Json.object("name", Json.object("en", "Team")),
                          "config",
                          secrets.encrypt(
                              Json.object(
                                      "rpId",
                                      "portal.example.com",
                                      "origins",
                                      List.of(issuer),
                                      "primaryStep",
                                      "form")
                                  .toString(),
                              "provider:" + tenant + ":" + provider),
                          "admission",
                          Json.object(
                              "mode",
                              "invite_only",
                              "mfa",
                              "never",
                              "sessionTtlSeconds",
                              3600,
                              "idleTimeoutSeconds",
                              120)),
                      null);
                  team.register(provider.toString(), "alice", "correct-horse", "Alice");
                  store.insert(
                      "account",
                      account.toString(),
                      Json.object(
                          "accountId",
                          account,
                          "status",
                          "active",
                          "memberType",
                          "member",
                          "createdAt",
                          clock.instant().toString()),
                      null);
                  store.insert(
                      "identity",
                      provider + ":alice",
                      Json.object("providerId", provider, "accountId", account, "subject", "alice"),
                      null);
                  store.insert(
                      "team_totp",
                      account.toString(),
                      Json.object(
                          "accountId",
                          account,
                          "totp",
                          secrets.encrypt(totp, "totp:" + tenant + ":" + account),
                          "lastCounter",
                          -1),
                      null);
                  return null;
                }));
  }

  ObjectNode request() {
    return Json.object(
        "providerId",
        provider,
        "purpose",
        "login",
        "returnTo",
        "/",
        "nativeConnect",
        Json.object(
            "clientId",
            "native-example",
            "codeChallenge",
            OAuthProvider.challenge(verifier),
            "codeChallengeMethod",
            "S256",
            "deviceName",
            "Example DSH"));
  }

  LoginManager.Result begin(ObjectNode request) {
    return login.begin(request, "portal", "192.0.2.10", "native-test", issuer, null);
  }

  LoginManager.Result complete(LoginManager.Result started) {
    String tx = started.transaction().path("id").asString();
    var password =
        login.advance(
            tx,
            started.bindingCookie(),
            Json.object(
                "type",
                "form",
                "formId",
                "password",
                "values",
                Json.object("username", "alice", "password", "correct-horse")),
            false);
    assertEquals("totp", password.transaction().path("step").path("formId").asString());
    var done =
        login.advance(
            tx,
            started.bindingCookie(),
            Json.object(
                "type",
                "form",
                "formId",
                "totp",
                "values",
                Json.object("code", Totp.code(totp, clock.instant().getEpochSecond() / 30))),
            false);
    assertEquals("completed", done.transaction().path("status").asString());
    assertTrue(done.session().session().path("mfa").asBoolean());
    return done;
  }

  Map<String, String> exchange(String code, String proof) {
    return Map.of(
        "grant_type",
        OAuthProvider.DEVICE,
        "client_id",
        "native-example",
        "device_code",
        code,
        "code_verifier",
        proof);
  }

  ObjectNode connected() {
    var started = begin(request());
    complete(started);
    return oauth.token(
        exchange(
            started.transaction().path("nativeDevice").path("deviceCode").asString(), verifier));
  }

  ObjectNode rotate(ObjectNode token) {
    return oauth.token(
        Map.of(
            "grant_type",
            "refresh_token",
            "client_id",
            "native-example",
            "refresh_token",
            token.path("refresh_token").asString()));
  }

  @Test
  void nativeLoginRefreshCanAlternateAiAndPlatformWithoutAnotherLogin() {
    TenantContext.run(
        tenant,
        () -> {
          effective.add(DeviceAiResource.INVOKE);
          ObjectNode original = connected();
          var aiOauth =
              new OAuthProvider(
                  store,
                  sessions,
                  tokens,
                  clock,
                  List.of(
                      new OAuthClient(
                          "native-example",
                          tenant,
                          null,
                          Set.of(),
                          Set.of("auriga.connect"),
                          Set.of(OAuthProvider.DEVICE, "refresh_token"),
                          true,
                          true)),
                  request ->
                      auriga.contracts.org.v1.GetEffectivePermissionsResponse.newBuilder()
                          .setStatus(auriga.contracts.org.v1.MemberStatus.MEMBER_STATUS_ACTIVE)
                          .setMemberType(auriga.contracts.org.v1.MemberType.MEMBER_TYPE_MEMBER)
                          .addAllPermissions(effective)
                          .build());
          var input =
              new HashMap<>(
                  Map.of(
                      "grant_type",
                      "refresh_token",
                      "client_id",
                      "native-example",
                      "refresh_token",
                      original.path("refresh_token").asString(),
                      "resource",
                      DeviceAiResource.RESOURCE));
          ObjectNode ai = aiOauth.token(input);
          assertTrue(ai.has("access_token"), ai.toString());
          var initialClaims = tokens.verify(original.path("access_token").asString());
          var aiClaims = tokens.verify(ai.path("access_token").asString());
          assertEquals("auriga.ai-gateway", aiClaims.path("aud").asString());
          assertEquals(initialClaims.path("did"), aiClaims.path("did"));
          ObjectNode platform = rotate(ai);
          assertTrue(platform.has("access_token"), platform.toString());
          assertEquals(
              "auriga",
              tokens.verify(platform.path("access_token").asString()).path("aud").asString());
          input.put("refresh_token", platform.path("refresh_token").asString());
          assertTrue(aiOauth.token(input).has("access_token"));
          assertEquals(1, store.tx(() -> store.all("device").size()));
        });
  }

  @Test
  void samePasswordAndTotpCreatesExactlyOneDeviceCredential() {
    TenantContext.run(
        tenant,
        () -> {
          var started = begin(request());
          String code = started.transaction().path("nativeDevice").path("deviceCode").asString();
          assertFalse(code.isEmpty());
          assertFalse(
              login
                  .get(started.transaction().path("id").asString(), started.bindingCookie())
                  .transaction()
                  .has("nativeDevice"));
          var done = complete(started);
          assertFalse(done.transaction().has("nativeDevice"));
          ObjectNode response = oauth.token(exchange(code, verifier));
          assertFalse(response.has("error"), response.toString());
          ObjectNode claims = tokens.verify(response.path("access_token").asString());
          assertEquals("device", claims.path("ptyp").asString());
          assertEquals("device", claims.path("ctx").asString());
          assertEquals(account.toString(), claims.path("sub").asString());
          assertFalse(claims.has("sid"));
          assertTrue(claims.has("did"));
          assertEquals("auriga.connect", claims.path("scope").asString());
          assertTrue(response.has("refresh_token"));
          assertEquals(
              "invalid_grant", oauth.token(exchange(code, verifier)).path("error").asString());
          var authority = new DeviceAccessAuthority(store, sessions, tokens);
          assertNotNull(authority.access(response.path("access_token").asString()));
          assertEquals(1, store.tx(() -> store.all("device").size()));
        });
  }

  @Test
  void wrongVerifierCannotConsumeOrDelayTheNativeGrant() {
    TenantContext.run(
        tenant,
        () -> {
          var started = begin(request());
          String code = started.transaction().path("nativeDevice").path("deviceCode").asString();
          var before = store.tx(() -> store.get("oauth_grant", Secrets.hash(code), false));
          assertEquals(
              "invalid_grant",
              oauth.token(exchange(code, "x".repeat(43))).path("error").asString());
          var after = store.tx(() -> store.get("oauth_grant", Secrets.hash(code), false));
          assertEquals(before.path("nextPoll"), after.path("nextPoll"));
          assertFalse(after.path("used").asBoolean());
          complete(started);
          assertTrue(oauth.token(exchange(code, verifier)).has("access_token"));
        });
  }

  @Test
  void cancelRejectsItsOwnGrantAndCannotBeApprovedByManualDeviceEndpoint() {
    TenantContext.run(
        tenant,
        () -> {
          var started = begin(request());
          String code = started.transaction().path("nativeDevice").path("deviceCode").asString();
          assertThrows(AurigaException.class, () -> oauth.decideDevice("", "other-session", true));
          login.cancel(started.transaction().path("id").asString(), started.bindingCookie());
          assertEquals(
              "access_denied", oauth.token(exchange(code, verifier)).path("error").asString());
        });
  }

  @Test
  void nativeRefreshTouchesIdleButCannotReviveExpiredBacking() {
    TenantContext.run(
        tenant,
        () -> {
          var started = begin(request());
          complete(started);
          String code = started.transaction().path("nativeDevice").path("deviceCode").asString();
          ObjectNode token = oauth.token(exchange(code, verifier));
          String did = tokens.verify(token.path("access_token").asString()).path("did").asString();
          clock.now = clock.now.plusSeconds(60);
          ObjectNode refreshed =
              oauth.token(
                  Map.of(
                      "grant_type",
                      "refresh_token",
                      "client_id",
                      "native-example",
                      "refresh_token",
                      token.path("refresh_token").asString()));
          assertTrue(refreshed.has("access_token"), refreshed.toString());
          ObjectNode device = store.tx(() -> store.get("device", did, false));
          ObjectNode backing =
              store.tx(() -> sessions.activeById(device.path("sessionId").asString()));
          assertEquals(
              clock.instant().plusSeconds(120).toString(),
              backing.path("idleExpiresAt").asString());
          clock.now = clock.now.plusSeconds(121);
          assertEquals(
              "invalid_grant",
              oauth
                  .token(
                      Map.of(
                          "grant_type",
                          "refresh_token",
                          "client_id",
                          "native-example",
                          "refresh_token",
                          refreshed.path("refresh_token").asString()))
                  .path("error")
                  .asString());
        });
  }

  @Test
  void concurrentExchangesHaveOneWinnerAndLostResponseCannotIssueAnotherDevice() throws Exception {
    var code =
        TenantContext.call(
            tenant,
            () -> {
              var started = begin(request());
              complete(started);
              return started.transaction().path("nativeDevice").path("deviceCode").asString();
            });
    try (var pool = java.util.concurrent.Executors.newVirtualThreadPerTaskExecutor()) {
      var go = new java.util.concurrent.CountDownLatch(1);
      java.util.concurrent.Callable<ObjectNode> exchange =
          () -> {
            go.await();
            return TenantContext.call(tenant, () -> oauth.token(exchange(code, verifier)));
          };
      var a = pool.submit(exchange);
      var b = pool.submit(exchange);
      go.countDown();
      var results =
          List.of(
              a.get(10, java.util.concurrent.TimeUnit.SECONDS),
              b.get(10, java.util.concurrent.TimeUnit.SECONDS));
      assertEquals(1, results.stream().filter(n -> n.has("access_token")).count());
      assertEquals(1, TenantContext.call(tenant, () -> store.tx(() -> store.all("device").size())));
    }
  }

  @Test
  void rejectsCallerSuppliedGrantAndIdentity() {
    TenantContext.run(
        tenant,
        () -> {
          ObjectNode request = request();
          ((ObjectNode) request.path("nativeConnect"))
              .put("deviceCode", "foreign-code")
              .put("accountId", account.toString());
          assertThrows(AurigaException.class, () -> begin(request));
        });
  }

  @Test
  void nativeInputCannotChangePurposeAudienceOrTrustBoundary() {
    TenantContext.run(
        tenant,
        () -> {
          for (String field : List.of("purpose", "returnTo")) {
            var malformed = request();
            malformed.putNull(field);
            assertThrows(AurigaException.class, () -> begin(malformed));
          }
          for (String field :
              List.of(
                  "accountId", "sid", "deviceCode", "userCode", "permissions", "invitationCode")) {
            var malformed = request();
            malformed.put(field, "foreign-value");
            assertThrows(AurigaException.class, () -> begin(malformed));
          }
          assertThrows(
              AurigaException.class,
              () -> login.begin(request(), "admin", "192.0.2.10", "test", issuer, null));
          var link = request();
          link.put("purpose", "link");
          assertThrows(AurigaException.class, () -> begin(link));
          var redirect = request();
          redirect.put("returnTo", "/apps");
          assertThrows(AurigaException.class, () -> begin(redirect));
          var proof = request();
          ((ObjectNode) proof.path("nativeConnect")).put("codeChallengeMethod", "plain");
          assertThrows(AurigaException.class, () -> begin(proof));
          assertEquals(0, store.tx(() -> store.all("oauth_grant").size()));
        });
  }

  @Test
  void passwordAloneCannotApproveAndFirstTotpIsRealEnrollment() {
    TenantContext.run(
        tenant,
        () -> {
          store.tx(
              () -> {
                store.delete("team_totp", account.toString());
                return null;
              });
          var started = begin(request());
          String id = started.transaction().path("id").asString();
          var pending =
              login.advance(
                  id,
                  started.bindingCookie(),
                  Json.object(
                      "type",
                      "form",
                      "formId",
                      "password",
                      "values",
                      Json.object("username", "alice", "password", "correct-horse")),
                  false);
          assertEquals("first_totp", pending.transaction().path("step").path("formId").asString());
          String code = started.transaction().path("nativeDevice").path("deviceCode").asString();
          assertEquals(
              "authorization_pending",
              oauth.token(exchange(code, verifier)).path("error").asString());
          String label =
              pending
                  .transaction()
                  .path("step")
                  .path("fields")
                  .get(0)
                  .path("label")
                  .path("en")
                  .asString();
          String enrollment = label.substring(label.lastIndexOf(' ') + 1);
          var done =
              login.advance(
                  id,
                  started.bindingCookie(),
                  Json.object(
                      "type",
                      "form",
                      "formId",
                      "first_totp",
                      "values",
                      Json.object(
                          "otp", Totp.code(enrollment, clock.instant().getEpochSecond() / 30))),
                  false);
          assertEquals(
              "completed",
              done.transaction().path("status").asString(),
              done.transaction().toString());
          clock.now = clock.now.plusSeconds(5);
          assertTrue(oauth.token(exchange(code, verifier)).has("access_token"));
        });
  }

  @Test
  void cancellationAndReplacementDenyAlreadyApprovedUnexchangedGrant() {
    TenantContext.run(
        tenant,
        () -> {
          var started = begin(request());
          complete(started);
          String code = started.transaction().path("nativeDevice").path("deviceCode").asString();
          login.cancel(started.transaction().path("id").asString(), started.bindingCookie());
          assertEquals(
              "access_denied", oauth.token(exchange(code, verifier)).path("error").asString());
          var next =
              login.begin(
                  request(), "portal", "192.0.2.10", "test", issuer, started.bindingCookie());
          assertFalse(
              next.transaction().path("nativeDevice").path("deviceCode").asString().equals(code));
          assertThrows(
              AurigaException.class,
              () ->
                  login.begin(
                      request(), "portal", "192.0.2.10", "test", issuer, started.bindingCookie()));
          assertEquals(0, store.tx(() -> store.all("device").size()));
        });
  }

  @Test
  void selfRevokeInvalidatesAllRefreshGenerationsAndOnlyThatDevice() {
    TenantContext.run(
        tenant,
        () -> {
          var first = connected();
          var rotated = rotate(first);
          clock.now = clock.now.plusSeconds(30);
          var other = connected();
          String firstDid =
              tokens.verify(first.path("access_token").asString()).path("did").asString();
          String sid =
              store.tx(() -> store.get("device", firstDid, false)).path("sessionId").asString();
          assertEquals(
              Json.object(),
              oauth.revoke(
                  Map.of(
                      "client_id",
                      "native-example",
                      "token",
                      first.path("refresh_token").asString())));
          var authority = new DeviceAccessAuthority(store, sessions, tokens);
          assertNull(authority.access(first.path("access_token").asString()));
          assertEquals("invalid_grant", rotate(rotated).path("error").asString());
          assertEquals("invalid_grant", rotate(first).path("error").asString());
          assertNotNull(
              store.tx(() -> sessions.activeById(sid)),
              "old refresh replay after logout must not revoke backing session");
          assertNotNull(authority.access(other.path("access_token").asString()));
          assertTrue(rotate(other).has("access_token"));
          assertEquals(
              Json.object(),
              oauth.revoke(
                  Map.of(
                      "client_id",
                      "native-example",
                      "token",
                      first.path("access_token").asString())));
        });
  }

  @Test
  void revokeRejectsArbitraryDeviceAndUserTokensButAccessPossessionWorks() {
    TenantContext.run(
        tenant,
        () -> {
          var token = connected();
          String access = token.path("access_token").asString();
          var authority = new DeviceAccessAuthority(store, sessions, tokens);
          String did = tokens.verify(access).path("did").asString();
          String user =
              tokens.access(
                  tenant,
                  account.toString(),
                  "auriga",
                  "user",
                  "portal",
                  UUID.randomUUID(),
                  null,
                  null,
                  null,
                  "");
          assertEquals(
              Json.object(), oauth.revoke(Map.of("client_id", "native-example", "token", user)));
          assertEquals(
              Json.object(),
              oauth.revoke(Map.of("client_id", "native-example", "token", "unknown")));
          assertEquals(
              "invalid_request",
              oauth
                  .revoke(Map.of("client_id", "native-example", "token", access, "device_id", did))
                  .path("error")
                  .asString());
          assertNotNull(authority.access(access));
          assertEquals(
              Json.object(), oauth.revoke(Map.of("client_id", "native-example", "token", access)));
          assertNull(authority.access(access));
        });
  }

  @Test
  void concurrentRefreshAndRevokeCannotResurrectDevice() throws Exception {
    ObjectNode token = TenantContext.call(tenant, this::connected);
    try (var pool = java.util.concurrent.Executors.newVirtualThreadPerTaskExecutor()) {
      var go = new java.util.concurrent.CountDownLatch(1);
      var refresh =
          pool.submit(
              () -> {
                go.await();
                return TenantContext.call(tenant, () -> rotate(token));
              });
      var revoke =
          pool.submit(
              () -> {
                go.await();
                return TenantContext.call(
                    tenant,
                    () ->
                        oauth.revoke(
                            Map.of(
                                "client_id",
                                "native-example",
                                "token",
                                token.path("refresh_token").asString())));
              });
      go.countDown();
      var refreshed = refresh.get(10, java.util.concurrent.TimeUnit.SECONDS);
      revoke.get(10, java.util.concurrent.TimeUnit.SECONDS);
      TenantContext.run(
          tenant,
          () -> {
            assertNull(
                new DeviceAccessAuthority(store, sessions, tokens)
                    .access(token.path("access_token").asString()));
            assertEquals(
                "invalid_grant",
                rotate(refreshed.has("refresh_token") ? refreshed : token)
                    .path("error")
                    .asString());
          });
    }
  }

  @Test
  void authorityRequiresDevicePermissionAndLiveAccountBackingAndTenant() {
    TenantContext.run(
        tenant,
        () -> {
          var token = connected();
          String access = token.path("access_token").asString();
          var claims = tokens.verify(access);
          String did = claims.path("did").asString();
          var authority = new DeviceAccessAuthority(store, sessions, tokens);
          assertNotNull(authority.access(access));
          effective.remove("auriga.connect.collab.use");
          String noTeam =
              tokens.access(
                  tenant,
                  account.toString(),
                  "auriga",
                  "device",
                  "device",
                  null,
                  UUID.fromString(did),
                  null,
                  null,
                  "auriga.connect");
          assertNull(authority.access(noTeam));
          String user =
              tokens.access(
                  tenant,
                  account.toString(),
                  "auriga",
                  "user",
                  "portal",
                  UUID.randomUUID(),
                  null,
                  null,
                  null,
                  "");
          assertNull(authority.access(user));
          String wrongAudience =
              tokens.access(
                  tenant,
                  account.toString(),
                  "client-example",
                  "device",
                  "device",
                  null,
                  UUID.fromString(did),
                  null,
                  null,
                  "auriga.connect");
          assertNull(authority.access(wrongAudience));
          assertNull(authority.credential(tenant.toString(), UUID.randomUUID().toString(), did));
          assertThrows(
              AurigaException.class,
              () -> TenantContext.call(UUID.randomUUID(), () -> authority.access(access)));
          store.tx(
              () -> {
                var row = store.get("account", account.toString(), true);
                row.put("status", "disabled");
                store.save("account", row);
                return null;
              });
          assertNull(authority.access(access));
        });
  }

  @Test
  void actualGrpcTransportSeparatesGatewayAndCollabAndRequiresDelegatedTenant() {
    String access = TenantContext.call(tenant, () -> connected().path("access_token").asString());
    String did = TenantContext.call(tenant, () -> tokens.verify(access).path("did").asString());
    var service = new IamGrpcDevices(new DeviceAccessAuthority(store, sessions, tokens));
    var settings =
        new auriga.common.security.AccessTokenSettings(
            issuer,
            java.net.URI.create(issuer + "/oauth2/jwks"),
            null,
            null,
            null,
            null,
            clock,
            false);
    var jwks =
        new com.nimbusds.jose.jwk.source.ImmutableJWKSet<com.nimbusds.jose.proc.SecurityContext>(
            new com.nimbusds.jose.jwk.JWKSet(keys.current().toPublicJWK()));
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
    var channels = new ArrayList<io.grpc.ManagedChannel>();
    String secret = Secrets.random(),
        collabSecret = Secrets.random(),
        edgeSecret = Secrets.random();
    var internal =
        new TokenService(
            keys,
            new ContractSchemas(),
            (t, s, c) -> Set.of(),
            clock,
            issuer,
            Map.of(
                "ai-gateway",
                Secrets.hash(secret),
                "collab",
                Secrets.hash(collabSecret),
                "edge",
                Secrets.hash(edgeSecret)));
    server.start();
    try {
      java.util.function.Function<
              String,
              auriga.contracts.iam.v1.DeviceAccessServiceGrpc.DeviceAccessServiceBlockingStub>
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
                return auriga.contracts.iam.v1.DeviceAccessServiceGrpc.newBlockingStub(channel)
                    .withDeadlineAfter(5, java.util.concurrent.TimeUnit.SECONDS);
              };
      var gateway = stub.apply(internal.issue("ai-gateway", secret));
      var collab = stub.apply(internal.issue("collab", collabSecret));
      var edge = stub.apply(internal.issue("edge", edgeSecret));
      var request =
          auriga.contracts.iam.v1.VerifyDeviceAccessRequest.newBuilder()
              .setAccessToken(access)
              .build();
      var credential =
          auriga.contracts.iam.v1.VerifyDeviceCredentialRequest.newBuilder()
              .setTenantId(tenant.toString())
              .setAccountId(account.toString())
              .setDeviceId(did)
              .build();
      var verified = TenantContext.call(tenant, () -> gateway.verifyDeviceAccess(request));
      assertTrue(verified.getActive());
      assertEquals(account.toString(), verified.getAccountId());
      assertEquals(did, verified.getDeviceId());
      assertEquals(tenant.toString(), verified.getTenantId());
      assertTrue(
          TenantContext.call(tenant, () -> collab.verifyDeviceCredential(credential)).getActive());
      assertGrpc(
          io.grpc.Status.Code.PERMISSION_DENIED,
          () -> TenantContext.call(tenant, () -> gateway.verifyDeviceCredential(credential)));
      assertGrpc(
          io.grpc.Status.Code.PERMISSION_DENIED,
          () -> TenantContext.call(tenant, () -> collab.verifyDeviceAccess(request)));
      assertGrpc(
          io.grpc.Status.Code.PERMISSION_DENIED,
          () -> TenantContext.call(tenant, () -> edge.verifyDeviceAccess(request)));
      assertGrpc(io.grpc.Status.Code.PERMISSION_DENIED, () -> gateway.verifyDeviceAccess(request));
      assertGrpc(
          io.grpc.Status.Code.PERMISSION_DENIED,
          () -> TenantContext.call(UUID.randomUUID(), () -> gateway.verifyDeviceAccess(request)));
      assertGrpc(
          io.grpc.Status.Code.PERMISSION_DENIED,
          () ->
              TenantContext.call(
                  UUID.randomUUID(), () -> collab.verifyDeviceCredential(credential)));
      assertGrpc(
          io.grpc.Status.Code.UNAUTHENTICATED,
          () -> TenantContext.call(tenant, () -> stub.apply(null).verifyDeviceAccess(request)));
      assertGrpc(
          io.grpc.Status.Code.UNAUTHENTICATED,
          () -> TenantContext.call(tenant, () -> stub.apply(access).verifyDeviceAccess(request)));
      var inactive =
          TenantContext.call(
              tenant,
              () ->
                  gateway.verifyDeviceAccess(
                      request.toBuilder().setAccessToken("unknown").build()));
      assertFalse(inactive.getActive());
      assertEquals("", inactive.getTenantId());
      assertEquals("", inactive.getAccountId());
      assertEquals("", inactive.getDeviceId());
      assertFalse(inactive.hasExpiresAt());
      TenantContext.run(
          tenant, () -> oauth.revoke(Map.of("client_id", "native-example", "token", access)));
      assertFalse(
          TenantContext.call(tenant, () -> gateway.verifyDeviceAccess(request)).getActive());
      assertFalse(
          TenantContext.call(tenant, () -> collab.verifyDeviceCredential(credential)).getActive());
    } finally {
      channels.forEach(io.grpc.ManagedChannel::shutdownNow);
      server.stop();
    }
  }

  static void assertGrpc(io.grpc.Status.Code code, org.junit.jupiter.api.function.Executable work) {
    assertEquals(
        code, assertThrows(io.grpc.StatusRuntimeException.class, work).getStatus().getCode());
  }

  @Test
  void controllerKeepsOriginalJsonAndRevokeDoesNotUseCookieIdentity() throws Exception {
    var configTree =
        Json.read(
            java.nio.file.Files.readString(
                java.nio.file.Path.of("../../../contracts/deployment/fixtures/domain-proxy.json")));
    configTree.put("tenantId", tenant.toString());
    var config =
        auriga.contracts.deploymentconfig.DeploymentAccess.load(
            configTree.toString().getBytes(java.nio.charset.StandardCharsets.UTF_8));
    var identity =
        new RequestIdentity(config, new RequestIdentity.Hop(Set.of("127.0.0.1"), null, true));
    try (var controller =
        new ControllerClose(
            new IamLoginController(
                login, identity, store, sessions, mock(QrChallenges.class), secrets))) {
      var http = HttpBoundaryTest.request(config, "portal");
      http.addHeader("Origin", config.portalUrl());
      var malformed = request();
      ((ObjectNode) malformed.path("nativeConnect")).put("accountId", account.toString());
      assertThrows(
          AurigaException.class,
          () ->
              controller.value.begin(
                  Json.MAPPER.readTree(malformed.toString()),
                  http,
                  new org.springframework.mock.web.MockHttpServletResponse()));
      assertEquals(
          0, TenantContext.call(tenant, () -> store.tx(() -> store.all("oauth_grant").size())));
      var token = TenantContext.call(tenant, this::connected);
      String access = token.path("access_token").asString();
      http.setCookies(new jakarta.servlet.http.Cookie("__Host-auriga_session", "untrusted-cookie"));
      var endpoint = new OAuthController(oauth, keys, identity, store, sessions);
      assertEquals(
          200,
          endpoint
              .revoke(Map.of("client_id", "native-example", "token", "unknown"), http)
              .getStatusCode()
              .value());
      assertNotNull(
          TenantContext.call(
              tenant, () -> new DeviceAccessAuthority(store, sessions, tokens).access(access)));
      assertEquals(
          200,
          endpoint
              .revoke(Map.of("client_id", "native-example", "token", access), http)
              .getStatusCode()
              .value());
      assertNull(
          TenantContext.call(
              tenant, () -> new DeviceAccessAuthority(store, sessions, tokens).access(access)));
    }
  }

  record ControllerClose(IamLoginController value) implements AutoCloseable {
    public void close() {
      value.destroy();
    }
  }

  List<ObjectNode> race(
      java.util.function.Supplier<ObjectNode> a, java.util.function.Supplier<ObjectNode> b)
      throws Exception {
    try (var pool = java.util.concurrent.Executors.newVirtualThreadPerTaskExecutor()) {
      var go = new java.util.concurrent.CountDownLatch(1);
      var first =
          pool.submit(
              () -> {
                go.await();
                return TenantContext.call(tenant, a::get);
              });
      var second =
          pool.submit(
              () -> {
                go.await();
                return TenantContext.call(tenant, b::get);
              });
      go.countDown();
      return List.of(
          first.get(10, java.util.concurrent.TimeUnit.SECONDS),
          second.get(10, java.util.concurrent.TimeUnit.SECONDS));
    }
  }

  @Test
  void concurrentCancelAndMfaCompletionCannotLeaveAnExchangeableGrant() throws Exception {
    var started =
        TenantContext.call(
            tenant,
            () -> {
              var tx = begin(request());
              login.advance(
                  tx.transaction().path("id").asString(),
                  tx.bindingCookie(),
                  Json.object(
                      "type",
                      "form",
                      "formId",
                      "password",
                      "values",
                      Json.object("username", "alice", "password", "correct-horse")),
                  false);
              return tx;
            });
    String id = started.transaction().path("id").asString();
    race(
        () -> {
          try {
            return login
                .advance(
                    id,
                    started.bindingCookie(),
                    Json.object(
                        "type",
                        "form",
                        "formId",
                        "totp",
                        "values",
                        Json.object(
                            "code", Totp.code(totp, clock.instant().getEpochSecond() / 30))),
                    false)
                .transaction();
          } catch (AurigaException finished) {
            assertEquals(409, finished.errorCode().httpStatus());
            return Json.object();
          }
        },
        () -> {
          login.cancel(id, started.bindingCookie());
          return Json.object();
        });
    TenantContext.run(
        tenant,
        () -> {
          assertFalse(
              oauth
                  .token(
                      exchange(
                          started.transaction().path("nativeDevice").path("deviceCode").asString(),
                          verifier))
                  .has("access_token"));
          assertEquals(0, store.tx(() -> store.all("device").size()));
        });
  }

  @Test
  void concurrentReplacementAndExchangeHaveOnlyOneLinearizedOutcome() throws Exception {
    var started =
        TenantContext.call(
            tenant,
            () -> {
              var tx = begin(request());
              complete(tx);
              return tx;
            });
    String code = started.transaction().path("nativeDevice").path("deviceCode").asString();
    var result =
        race(
            () -> oauth.token(exchange(code, verifier)),
            () ->
                login
                    .begin(
                        request(), "portal", "192.0.2.10", "test", issuer, started.bindingCookie())
                    .transaction());
    TenantContext.run(
        tenant,
        () -> {
          assertFalse(oauth.token(exchange(code, verifier)).has("access_token"));
          assertEquals(
              result.getFirst().has("access_token") ? 1 : 0,
              store.tx(() -> store.all("device").size()));
          assertNotNull(result.get(1).get("nativeDevice"));
        });
  }

  @Test
  void concurrentBackingRevocationAndExchangeCannotReturnLiveAuthorityAfterRevoke()
      throws Exception {
    var started =
        TenantContext.call(
            tenant,
            () -> {
              var tx = begin(request());
              var done = complete(tx);
              return new LoginManager.Result(tx.transaction(), tx.bindingCookie(), done.session());
            });
    String sid = started.session().session().path("sessionId").asString();
    var result =
        race(
            () ->
                oauth.token(
                    exchange(
                        started.transaction().path("nativeDevice").path("deviceCode").asString(),
                        verifier)),
            () ->
                store.tx(
                    () -> {
                      sessions.revokeOne(sessions.activeById(sid), "security");
                      return Json.object();
                    }));
    TenantContext.run(
        tenant,
        () -> {
          assertNull(store.tx(() -> sessions.activeById(sid)));
          if (result.getFirst().has("access_token")) {
            assertNull(
                new DeviceAccessAuthority(store, sessions, tokens)
                    .access(result.getFirst().path("access_token").asString()));
            assertEquals("invalid_grant", rotate(result.getFirst()).path("error").asString());
          }
        });
  }

  @Test
  void nativeIdleTouchIsCappedByOriginalAbsoluteExpiry() {
    TenantContext.run(
        tenant,
        () -> {
          var token = connected();
          String did = tokens.verify(token.path("access_token").asString()).path("did").asString();
          String sid = store.tx(() -> store.get("device", did, false)).path("sessionId").asString();
          Instant absolute = clock.instant().plusSeconds(150);
          store.tx(
              () -> {
                var row = sessions.activeById(sid);
                row.put("expiresAt", absolute.toString());
                store.save("session", row);
                return null;
              });
          clock.now = clock.now.plusSeconds(90);
          var rotated = rotate(token);
          assertTrue(rotated.has("access_token"));
          assertEquals(
              absolute.toString(),
              store.tx(() -> sessions.activeById(sid)).path("idleExpiresAt").asString());
          clock.now = absolute;
          assertEquals("invalid_grant", rotate(rotated).path("error").asString());
        });
  }
}
