package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import auriga.common.audit.*;
import auriga.common.context.TenantContext;
import auriga.common.events.*;
import auriga.common.persistence.UuidV7;
import auriga.contracts.iam.v1.*;
import auriga.iam.api.IdentityPolicyHooks;
import com.nimbusds.jwt.SignedJWT;
import java.net.*;
import java.nio.charset.StandardCharsets;
import java.time.*;
import java.util.*;
import java.util.function.Supplier;
import org.junit.jupiter.api.*;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** Real crypto/core services; the test repository only replaces PostgreSQL I/O, not policies. */
class IamFlowTest {
  static final class Time extends Clock {
    Instant now = Instant.parse("2026-01-01T00:00:00Z");

    public java.time.ZoneId getZone() {
      return ZoneOffset.UTC;
    }

    public Clock withZone(java.time.ZoneId zone) {
      return this;
    }

    public Instant instant() {
      return now;
    }

    void advance(long seconds) {
      now = now.plusSeconds(seconds);
    }
  }

  private final UUID tenant = UUID.randomUUID();
  private final Time clock = new Time();
  private final IamStore store = mock(IamStore.class);
  private final Map<String, ObjectNode> rows = new LinkedHashMap<>();
  private final ContractSchemas schemas = new ContractSchemas();
  private final AuditLogger audit = mock(AuditLogger.class);
  private final List<String> eventTypes = new ArrayList<>();
  private final EventPublisher events =
      (type, subject, data, version) -> {
        JsonNode node = Json.MAPPER.valueToTree(data);
        new EventSchemas()
            .validate(
                Json.MAPPER.valueToTree(
                    new EventEnvelope(
                        "1.0",
                        UuidV7.next().toString(),
                        "auriga/iam",
                        type,
                        clock.instant().toString(),
                        subject,
                        "application/json",
                        "1",
                        TenantContext.require().toString(),
                        null,
                        node)));
        eventTypes.add(type);
        return UuidV7.next();
      };
  private Secrets secrets;
  private SigningKeys keys;
  private TokenService tokens;
  private SessionManager sessions;
  private EmergencyAccounts emergency;
  private LoginManager login;
  private OAuthProvider oauth;
  private ConnectorRegistry registry;
  private EmergencyAccounts.Enrollment enrollment;

  private String key(String table, String lookup) {
    return TenantContext.require() + ":" + table + ":" + lookup;
  }

  @BeforeEach
  void setup() {
    when(store.tx(any())).thenAnswer(i -> ((Supplier<?>) i.getArgument(0)).get());
    when(store.atomic(any())).thenAnswer(i -> ((Supplier<?>) i.getArgument(0)).get());
    when(store.tenant(any(), any()))
        .thenAnswer(
            i ->
                TenantContext.call(i.getArgument(0), () -> ((Supplier<?>) i.getArgument(1)).get()));
    when(store.isolated(any(), any()))
        .thenAnswer(
            i ->
                TenantContext.call(i.getArgument(0), () -> ((Supplier<?>) i.getArgument(1)).get()));
    when(store.insert(anyString(), anyString(), any(), nullable(Instant.class)))
        .thenAnswer(
            i -> {
              String table = i.getArgument(0), lookup = i.getArgument(1);
              ObjectNode data = i.getArgument(2);
              assertFalse(rows.containsKey(key(table, lookup)));
              data.put(
                  "id", lookup.matches("[0-9a-fA-F-]{36}") ? lookup : UuidV7.next().toString());
              rows.put(key(table, lookup), data.deepCopy());
              return data;
            });
    when(store.get(anyString(), anyString(), anyBoolean()))
        .thenAnswer(
            i -> {
              ObjectNode n = rows.get(key(i.getArgument(0), i.getArgument(1)));
              return n == null ? null : n.deepCopy();
            });
    when(store.byId(anyString(), nullable(String.class), anyBoolean()))
        .thenAnswer(
            i ->
                rows.entrySet().stream()
                    .filter(
                        e ->
                            e.getKey()
                                    .startsWith(
                                        TenantContext.require() + ":" + i.getArgument(0) + ":")
                                && e.getValue().path("id").asString().equals(i.getArgument(1)))
                    .map(e -> e.getValue().deepCopy())
                    .findFirst()
                    .orElse(null));
    when(store.all(anyString()))
        .thenAnswer(
            i ->
                rows.entrySet().stream()
                    .filter(
                        e ->
                            e.getKey()
                                .startsWith(TenantContext.require() + ":" + i.getArgument(0) + ":"))
                    .map(e -> e.getValue().deepCopy())
                    .toList());
    doAnswer(
            i -> {
              ObjectNode n = i.getArgument(1);
              rows.entrySet().stream()
                  .filter(
                      e ->
                          e.getKey()
                                  .startsWith(
                                      TenantContext.require() + ":" + i.getArgument(0) + ":")
                              && e.getValue().path("id").equals(n.path("id")))
                  .forEach(e -> e.setValue(n.deepCopy()));
              return null;
            })
        .when(store)
        .save(anyString(), any());
    doAnswer(
            i -> {
              rows.remove(key(i.getArgument(0), i.getArgument(1)));
              return null;
            })
        .when(store)
        .delete(anyString(), anyString());
    byte[] raw = new byte[32];
    new java.security.SecureRandom().nextBytes(raw);
    secrets = new Secrets(Base64.getEncoder().encodeToString(raw));
    keys = new SigningKeys(store, secrets, clock, Duration.ofSeconds(30));
    String kid = keys.publishNext();
    clock.advance(31);
    keys.activate(kid);
    tokens =
        new TokenService(
            keys,
            schemas,
            (t, s, c) ->
                Set.of(
                    "auriga.connect",
                    "auriga.connect.read",
                    "auriga.connect.admin",
                    "org.members.write",
                    "workspace.read"),
            clock,
            "https://iam.example.com",
            Map.of("edge", Secrets.hash("test-installation-credential")));
    sessions =
        new SessionManager(
            store, new PostgresSessionKv(store, schemas, clock), tokens, clock, events, audit);
    emergency = new EmergencyAccounts(store, secrets, clock, false);
    enrollment = emergency.install(tenant);
    TeamAccounts teamAccounts = new TeamAccounts(store, secrets, clock, events);
    TeamPasskeys teamPasskeys = new TeamPasskeys(store, secrets, clock, events);
    QrChallenges qr = new QrChallenges(store, clock);
    registry =
        new ConnectorRegistry(
            schemas,
            List.of(
                new EmergencyConnector(),
                new TeamAccountConnector(teamAccounts, teamPasskeys, qr, tokens.issuer())));
    login =
        new LoginManager(
            store,
            secrets,
            schemas,
            registry,
            emergency,
            teamAccounts,
            sessions,
            new IdentityPolicyHooks() {
              @Override
              public boolean verifyMfa(java.util.UUID account, JsonNode input) {
                return teamAccounts.verifyMfaInput(account, input);
              }

              @Override
              public boolean consumeInvitation(
                  String code, java.util.UUID provider, JsonNode profile) {
                return code != null && !code.isBlank() && code.equals("invite-ok");
              }
            },
            events,
            audit,
            clock,
            tokens.issuer());
    oauth =
        new OAuthProvider(
            store,
            sessions,
            tokens,
            clock,
            List.of(
                new OAuthClient(
                    "example-client",
                    tenant,
                    null,
                    Set.of("https://client.example.com/callback"),
                    Set.of("openid", "profile", "email", "offline_access", "auriga.connect"),
                    Set.of("authorization_code", "refresh_token", OAuthProvider.DEVICE),
                    false),
                new OAuthClient(
                    "example-service",
                    tenant,
                    Secrets.hash("service-test-credential"),
                    Set.of(),
                    Set.of("example.read"),
                    Set.of("client_credentials"),
                    false)));
  }

  private LoginManager.Result begin(String audience) {
    return TenantContext.call(
        tenant,
        () ->
            login.begin(
                Json.object("providerId", enrollment.providerId(), "returnTo", "/"),
                audience,
                "127.0.0.1",
                "test",
                "https://portal.example.com",
                null));
  }

  private String external(boolean verified, String mfaPolicy) {
    auriga.iam.api.IdentityConnector connector = mock(auriga.iam.api.IdentityConnector.class);
    ObjectNode descriptor = (ObjectNode) new EmergencyConnector().descriptor();
    descriptor.put("type", "example-connector");
    when(connector.descriptor()).thenReturn(descriptor);
    when(connector.begin(any(), any()))
        .thenReturn(
            Json.object(
                "step",
                Json.object(
                    "type",
                    "done",
                    "profile",
                    Json.object(
                        "subject",
                        "example-subject",
                        "email",
                        "alice@example.com",
                        "emailVerified",
                        verified,
                        "displayName",
                        "Alice"))));
    registry.register(connector);
    ProviderManager providers =
        new ProviderManager(store, registry, secrets, audit, clock, tokens.issuer());
    return TenantContext.call(
        tenant,
        () ->
            providers
                .create(
                    Json.object(
                        "connectorType",
                        "example-connector",
                        "config",
                        Json.object(),
                        "display",
                        Json.object("name", Json.object("en", "Example")),
                        "admission",
                        Json.object(
                            "mode",
                            "auto_provision",
                            "mfa",
                            mfaPolicy,
                            "defaultMemberType",
                            "external",
                            "autoProvisionRules",
                            List.of(
                                Json.object(
                                    "attribute",
                                    "email",
                                    "operator",
                                    "suffix",
                                    "value",
                                    "@example.com")))))
                .path("id")
                .asString());
  }

  @Test
  void externalFirstLoginProvisionsAndSubsequentLoginMatchesSameIdentity() throws Exception {
    String provider = external(true, "never");
    var first =
        TenantContext.call(
            tenant,
            () ->
                login.begin(
                    Json.object("providerId", provider),
                    "portal",
                    "192.0.2.10",
                    "test",
                    "https://portal.example.com",
                    null));
    var second =
        TenantContext.call(
            tenant,
            () ->
                login.begin(
                    Json.object("providerId", provider),
                    "portal",
                    "192.0.2.10",
                    "test",
                    "https://portal.example.com",
                    null));
    assertNotNull(first.session());
    assertEquals(
        first.session().session().path("accountId"), second.session().session().path("accountId"));
    assertEquals(
        "external",
        SignedJWT.parse(first.session().session().path("accessToken").asString())
            .getJWTClaimsSet()
            .getStringClaim("mtyp"));
    assertEquals(1, eventTypes.stream().filter(t -> t.equals("identity.linked")).count());
    TenantContext.run(tenant, () -> assertEquals(2, store.all("account").size()));
  }

  @Test
  void unverifiedExternalEmailCannotProvisionAndFailureIsAudited() {
    String provider = external(false, "never");
    var result =
        TenantContext.call(
            tenant,
            () ->
                login.begin(
                    Json.object("providerId", provider),
                    "portal",
                    "192.0.2.10",
                    "test",
                    "https://portal.example.com",
                    null));
    assertEquals("failed", result.transaction().path("status").asString());
    assertNull(result.session());
    assertTrue(eventTypes.contains("identity.login_failed"));
    TenantContext.run(tenant, () -> assertEquals(1, store.all("account").size()));
  }

  @Test
  void pendingMfaDoesNotCreateAnOrphanAccount() {
    String provider = external(true, "always");
    var result =
        TenantContext.call(
            tenant,
            () ->
                login.begin(
                    Json.object("providerId", provider),
                    "portal",
                    "192.0.2.10",
                    "test",
                    "https://portal.example.com",
                    null));
    assertEquals("platform_totp", result.transaction().path("step").path("formId").asString());
    TenantContext.run(
        tenant,
        () -> {
          assertEquals(1, store.all("account").size());
          var failed =
              login.advance(
                  result.transaction().path("id").asString(),
                  result.bindingCookie(),
                  Json.object(
                      "type",
                      "form",
                      "formId",
                      "platform_totp",
                      "values",
                      Json.object("otp", "000000")),
                  false);
          assertEquals("failed", failed.transaction().path("status").asString());
          assertEquals(1, store.all("account").size());
        });
  }

  private JsonNode credentials(String password) {
    return Json.object(
        "type",
        "form",
        "formId",
        "emergency",
        "values",
        Json.object(
            "username",
            enrollment.username(),
            "password",
            password,
            "otp",
            Totp.code(enrollment.totpSecret(), clock.instant().getEpochSecond() / 30)));
  }

  private LoginManager.Result complete(String audience) {
    var tx = begin(audience);
    return TenantContext.call(
        tenant,
        () ->
            login.advance(
                tx.transaction().path("id").asString(),
                tx.bindingCookie(),
                credentials(enrollment.password()),
                false));
  }

  @Test
  void emergencyLoginIssuesContractSessionAndAudit() throws Exception {
    var result = complete("admin");
    assertEquals("completed", result.transaction().path("status").asString());
    assertNotNull(result.session());
    var issued = result.session();
    assertTrue(issued.session().path("mfa").asBoolean());
    assertEquals(Secrets.hash(issued.csrf()), issued.session().path("csrfHash").asString());
    var jwt = SignedJWT.parse(issued.session().path("accessToken").asString());
    assertEquals("at+jwt", jwt.getHeader().getType().toString());
    assertEquals("ES256", jwt.getHeader().getAlgorithm().getName());
    schemas.validate("auth/access-token.schema.json", Json.read(jwt.getJWTClaimsSet().toString()));
    assertEquals("admin", jwt.getJWTClaimsSet().getStringClaim("ctx"));
    assertNotNull(jwt.getJWTClaimsSet().getClaim("stepup_time"));
    assertTrue(eventTypes.contains("identity.login"));
    verify(audit).record(any(AuditEntry.class));
    TenantContext.run(
        tenant,
        () -> {
          assertTrue(
              sessions
                  .introspect(
                      IntrospectSessionRequest.newBuilder()
                          .setSessionToken(issued.cookie())
                          .setAudience(SessionAudience.SESSION_AUDIENCE_ADMIN)
                          .build())
                  .getActive());
          assertFalse(
              sessions
                  .introspect(
                      IntrospectSessionRequest.newBuilder()
                          .setSessionToken(issued.cookie())
                          .setAudience(SessionAudience.SESSION_AUDIENCE_PORTAL)
                          .build())
                  .getActive());
        });
  }

  @Test
  void browserBindingIsMandatoryAndTenantScoped() {
    var tx = begin("portal");
    String id = tx.transaction().path("id").asString();
    TenantContext.run(
        tenant, () -> assertThrows(RuntimeException.class, () -> login.get(id, "wrong")));
    TenantContext.run(
        UUID.randomUUID(),
        () -> assertThrows(RuntimeException.class, () -> login.get(id, tx.bindingCookie())));
  }

  @Test
  void expiredAndCompletedTransactionsCannotReplay() {
    var expired = begin("portal");
    clock.advance(301);
    TenantContext.run(
        tenant,
        () -> {
          var result =
              login.advance(
                  expired.transaction().path("id").asString(),
                  expired.bindingCookie(),
                  credentials(enrollment.password()),
                  false);
          assertEquals("expired", result.transaction().path("status").asString());
          assertThrows(
              RuntimeException.class,
              () ->
                  login.advance(
                      expired.transaction().path("id").asString(),
                      expired.bindingCookie(),
                      credentials(enrollment.password()),
                      false));
        });
    var tx = begin("portal");
    TenantContext.run(
        tenant,
        () -> {
          assertNotNull(
              login
                  .advance(
                      tx.transaction().path("id").asString(),
                      tx.bindingCookie(),
                      credentials(enrollment.password()),
                      false)
                  .session());
          assertThrows(
              RuntimeException.class,
              () ->
                  login.advance(
                      tx.transaction().path("id").asString(),
                      tx.bindingCookie(),
                      credentials(enrollment.password()),
                      false));
        });
  }

  @Test
  void wrongPasswordAndTotpReplayFailAndProduceFailureAudit() {
    var tx = begin("portal");
    TenantContext.run(
        tenant,
        () ->
            assertEquals(
                "failed",
                login
                    .advance(
                        tx.transaction().path("id").asString(),
                        tx.bindingCookie(),
                        credentials("incorrect"),
                        false)
                    .transaction()
                    .path("status")
                    .asString()));
    assertNotNull(complete("portal").session());
    assertNull(complete("portal").session());
    assertTrue(eventTypes.contains("identity.login_failed"));
  }

  @Test
  void remoteEmergencyLoginIsDenied() {
    var tx =
        TenantContext.call(
            tenant,
            () ->
                login.begin(
                    Json.object("providerId", enrollment.providerId()),
                    "admin",
                    "192.0.2.10",
                    "test",
                    "https://portal.example.com",
                    null));
    TenantContext.run(
        tenant,
        () ->
            assertNull(
                login
                    .advance(
                        tx.transaction().path("id").asString(),
                        tx.bindingCookie(),
                        credentials(enrollment.password()),
                        false)
                    .session()));
  }

  @Test
  void newKeyIsPublishedBeforeActivationAndOldTokensVerify() {
    String token = complete("portal").session().session().path("accessToken").asString();
    String kid = keys.publishNext();
    assertTrue(keys.jwks().toString().contains(kid));
    assertThrows(RuntimeException.class, () -> keys.activate(kid));
    clock.advance(31);
    keys.activate(kid);
    assertEquals(enrollment.accountId().toString(), tokens.verify(token).path("sub").asString());
  }

  private Map<String, String> authorization() {
    String verifier = "a".repeat(43);
    return new HashMap<>(
        Map.of(
            "client_id",
            "example-client",
            "response_type",
            "code",
            "redirect_uri",
            "https://client.example.com/callback",
            "code_challenge_method",
            "S256",
            "code_challenge",
            OAuthProvider.challenge(verifier),
            "scope",
            "openid profile offline_access",
            "nonce",
            "example-nonce"));
  }

  private String code(SessionManager.Issued session) {
    String redirect =
        TenantContext.call(
            tenant, () -> oauth.authorize(authorization(), session.cookie(), "portal"));
    return URLDecoder.decode(
        URI.create(redirect).getQuery().split("code=")[1].split("&")[0], StandardCharsets.UTF_8);
  }

  private ObjectNode exchange(String code, String verifier) {
    return TenantContext.call(
        tenant,
        () ->
            oauth.token(
                Map.of(
                    "client_id",
                    "example-client",
                    "grant_type",
                    "authorization_code",
                    "code",
                    code,
                    "code_verifier",
                    verifier,
                    "redirect_uri",
                    "https://client.example.com/callback")));
  }

  @Test
  void pkceMismatchBurnsCodeAndRedirectIsExact() {
    var session = complete("portal").session();
    String code = code(session);
    assertEquals("invalid_grant", exchange(code, "b".repeat(43)).path("error").asString());
    assertEquals("invalid_grant", exchange(code, "a".repeat(43)).path("error").asString());
    var input = authorization();
    input.put("redirect_uri", "https://client.example.com/callback/other");
    TenantContext.run(
        tenant,
        () ->
            assertThrows(
                RuntimeException.class, () -> oauth.authorize(input, session.cookie(), "portal")));
  }

  @Test
  void oidcCodeIssuesSeparateIdTokenAndThirdPartyAudience() throws Exception {
    var session = complete("portal").session();
    ObjectNode result = exchange(code(session), "a".repeat(43));
    assertTrue(result.has("refresh_token"));
    var access = SignedJWT.parse(result.path("access_token").asString());
    assertEquals(List.of("example-client"), access.getJWTClaimsSet().getAudience());
    var id = SignedJWT.parse(result.path("id_token").asString());
    assertEquals("JWT", id.getHeader().getType().toString());
    assertEquals("example-nonce", id.getJWTClaimsSet().getStringClaim("nonce"));
    assertThrows(RuntimeException.class, () -> tokens.verify(result.path("id_token").asString()));
    assertEquals(
        enrollment.accountId().toString(),
        oauth.userinfo(result.path("access_token").asString()).path("sub").asString());
  }

  @Test
  void revokedSessionCannotRefreshAndRefreshRotates() {
    var session = complete("portal").session();
    ObjectNode result = exchange(code(session), "a".repeat(43));
    ObjectNode rotated =
        TenantContext.call(
            tenant,
            () ->
                oauth.token(
                    Map.of(
                        "client_id",
                        "example-client",
                        "grant_type",
                        "refresh_token",
                        "refresh_token",
                        result.path("refresh_token").asString())));
    assertTrue(rotated.has("refresh_token"));
    assertNotEquals(result.path("refresh_token"), rotated.path("refresh_token"));
    TenantContext.run(tenant, () -> sessions.revokeOne(session.session(), "logout"));
    ObjectNode failure =
        TenantContext.call(
            tenant,
            () ->
                oauth.token(
                    Map.of(
                        "client_id",
                        "example-client",
                        "grant_type",
                        "refresh_token",
                        "refresh_token",
                        rotated.path("refresh_token").asString())));
    assertEquals("invalid_grant", failure.path("error").asString());
  }

  @Test
  void clientCredentialsNeverIssueInternalAudience() throws Exception {
    ObjectNode response =
        TenantContext.call(
            tenant,
            () ->
                oauth.token(
                    Map.of(
                        "client_id",
                        "example-service",
                        "client_secret",
                        "service-test-credential",
                        "grant_type",
                        "client_credentials",
                        "scope",
                        "example.read")));
    var jwt = SignedJWT.parse(response.path("access_token").asString());
    assertEquals(List.of("example-service"), jwt.getJWTClaimsSet().getAudience());
    assertEquals("service_account", jwt.getJWTClaimsSet().getStringClaim("ptyp"));
    assertEquals(
        "invalid_target",
        TenantContext.call(
                tenant,
                () ->
                    oauth.token(
                        Map.of(
                            "client_id",
                            "example-service",
                            "client_secret",
                            "service-test-credential",
                            "grant_type",
                            "client_credentials",
                            "audience",
                            "auriga.internal")))
            .path("error")
            .asString());
    var internal = SignedJWT.parse(tokens.issue("edge", "test-installation-credential"));
    assertEquals(List.of("auriga.internal"), internal.getJWTClaimsSet().getAudience());
    assertEquals("svc:edge", internal.getJWTClaimsSet().getSubject());
    assertThrows(RuntimeException.class, () -> tokens.issue("edge", "incorrect"));
  }

  @Test
  void devicePollingApprovalAndPermissionCeiling() throws Exception {
    var session = complete("portal").session();
    ObjectNode device =
        TenantContext.call(
            tenant,
            () ->
                oauth.deviceAuthorization(
                    Map.of("client_id", "example-client", "scope", "auriga.connect")));
    Map<String, String> poll =
        Map.of(
            "client_id",
            "example-client",
            "grant_type",
            OAuthProvider.DEVICE,
            "device_code",
            device.path("device_code").asString());
    assertEquals(
        "authorization_pending",
        TenantContext.call(tenant, () -> oauth.token(poll)).path("error").asString());
    assertEquals(
        "slow_down", TenantContext.call(tenant, () -> oauth.token(poll)).path("error").asString());
    TenantContext.run(
        tenant,
        () ->
            oauth.decideDevice(
                device.path("user_code").asString(),
                session.session().path("sessionId").asString(),
                true));
    clock.advance(11);
    ObjectNode result = TenantContext.call(tenant, () -> oauth.token(poll));
    var jwt = SignedJWT.parse(result.path("access_token").asString());
    assertEquals("device", jwt.getJWTClaimsSet().getStringClaim("ctx"));
    assertNotNull(jwt.getJWTClaimsSet().getStringClaim("did"));
    assertFalse(jwt.getJWTClaimsSet().getStringListClaim("perms").contains("auriga.connect.admin"));
    assertTrue(result.has("refresh_token"));
    assertEquals(
        "invalid_grant",
        TenantContext.call(tenant, () -> oauth.token(poll)).path("error").asString());
  }

  @Test
  void admissionRequiresVerifiedEmailAndRedirectCannotEscapeOrigin() {
    JsonNode policy =
        Json.object(
            "autoProvisionRules",
            List.of(
                Json.object("attribute", "email", "operator", "suffix", "value", "@example.com")));
    assertFalse(
        LoginManager.autoProvision(
            policy, Json.object("email", "alice@example.com", "emailVerified", false)));
    assertTrue(
        LoginManager.autoProvision(
            policy, Json.object("email", "alice@example.com", "emailVerified", true)));
    for (String path :
        List.of(
            "//example.com",
            "/\\example.com",
            "/%2fexample.com",
            "/a/../b",
            "/%252fexample.com",
            "/\r\n")) assertThrows(RuntimeException.class, () -> LoginManager.safeReturn(path));
  }

  @Test
  void encryptedValuesAreTenantBoundAndSignedRemoteResponsesAreMandatory() {
    String encrypted = secrets.encrypt("example-sensitive-value", "tenant-one");
    assertFalse(encrypted.contains("example-sensitive-value"));
    assertThrows(RuntimeException.class, () -> secrets.decrypt(encrypted, "tenant-two"));
    byte[] key = new byte[32];
    new java.security.SecureRandom().nextBytes(key);
    String time = Long.toString(clock.instant().getEpochSecond()),
        body = "{}",
        nonce = Secrets.random();
    String signature = "v1=" + RemoteConnector.hmac(key, time + "." + nonce + ".200." + body);
    assertDoesNotThrow(
        () ->
            RemoteConnector.verifyResponse(List.of(key), clock, nonce, 200, time, signature, body));
    assertThrows(
        RuntimeException.class,
        () ->
            RemoteConnector.verifyResponse(
                List.of(key), clock, nonce, 200, time, signature, "{\"step\":\"done\"}"));
    clock.advance(301);
    assertThrows(
        RuntimeException.class,
        () ->
            RemoteConnector.verifyResponse(List.of(key), clock, nonce, 200, time, signature, body));
  }

  @Test
  void audienceTransactionsAreIndependentAndReplacementCancelsOnlyItsOwnEntry() {
    var portal = begin("portal");
    var admin =
        TenantContext.call(
            tenant,
            () ->
                login.begin(
                    Json.object("providerId", enrollment.providerId()),
                    "admin",
                    "127.0.0.1",
                    "test",
                    "https://admin.example.com",
                    null));
    TenantContext.run(
        tenant,
        () -> {
          login.checkEntry(
              portal.transaction().path("id").asString(),
              portal.bindingCookie(),
              "portal",
              "https://portal.example.com");
          login.checkEntry(
              admin.transaction().path("id").asString(),
              admin.bindingCookie(),
              "admin",
              "https://admin.example.com");
          assertEquals(
              "common.not_found",
              assertThrows(
                      auriga.common.error.AurigaException.class,
                      () ->
                          login.checkEntry(
                              portal.transaction().path("id").asString(),
                              portal.bindingCookie(),
                              "admin",
                              "https://admin.example.com"))
                  .errorCode()
                  .code());
          var replacement =
              login.begin(
                  Json.object("providerId", enrollment.providerId()),
                  "portal",
                  "127.0.0.1",
                  "test",
                  "https://portal.example.com",
                  portal.bindingCookie());
          assertEquals(
              "cancelled",
              login
                  .get(portal.transaction().path("id").asString(), portal.bindingCookie())
                  .transaction()
                  .path("status")
                  .asString());
          assertEquals(
              "pending",
              login
                  .get(admin.transaction().path("id").asString(), admin.bindingCookie())
                  .transaction()
                  .path("status")
                  .asString());
          login.cancel(
              replacement.transaction().path("id").asString(), replacement.bindingCookie());
          assertEquals(
              "pending",
              login
                  .get(admin.transaction().path("id").asString(), admin.bindingCookie())
                  .transaction()
                  .path("status")
                  .asString());
        });
  }
}
