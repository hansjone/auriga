package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import auriga.common.audit.AuditLogger;
import auriga.common.context.RequestContext;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.events.*;
import auriga.common.persistence.UuidV7;
import auriga.common.security.*;
import auriga.iam.api.IdentityPolicyHooks;
import java.time.*;
import java.util.*;
import java.util.function.Supplier;
import org.junit.jupiter.api.*;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

class TeamAccountFlowTest {
  static final class Time extends Clock {
    Instant now = Instant.parse("2026-06-01T00:00:00Z");

    public ZoneId getZone() {
      return ZoneOffset.UTC;
    }

    public Clock withZone(ZoneId zone) {
      return this;
    }

    public Instant instant() {
      return now;
    }
  }

  private final UUID tenant = UUID.randomUUID();
  private final Time clock = new Time();
  private final IamStore store = mock(IamStore.class);
  private final Map<String, ObjectNode> rows = new LinkedHashMap<>();
  private final ContractSchemas schemas = new ContractSchemas();
  private final AuditLogger audit = mock(AuditLogger.class);
  private final List<String> eventTypes = new ArrayList<>();
  private Secrets secrets;
  private TeamAccounts team;
  private TeamPasskeys passkeys;
  private QrChallenges qr;
  private LoginManager login;
  private String providerId;

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
    EventPublisher events =
        (type, subject, data, version) -> {
          eventTypes.add(type);
          return UuidV7.next();
        };
    var keys = new SigningKeys(store, secrets, clock, Duration.ofSeconds(30));
    String kid = keys.publishNext();
    clock.now = clock.now.plusSeconds(31);
    keys.activate(kid);
    var tokens =
        new TokenService(
            keys,
            schemas,
            (t, s, c) -> Set.of("workspace.read"),
            clock,
            "https://portal.example.com",
            Map.of());
    var sessions =
        new SessionManager(
            store, new PostgresSessionKv(store, schemas, clock), tokens, clock, events, audit);
    team = new TeamAccounts(store, secrets, clock, events);
    passkeys = new TeamPasskeys(store, secrets, clock, events);
    qr = new QrChallenges(store, clock);
    var registry =
        new ConnectorRegistry(
            schemas,
            List.of(
                new EmergencyConnector(),
                new TeamAccountConnector(team, passkeys, qr, tokens.issuer())));
    login =
        new LoginManager(
            store,
            secrets,
            schemas,
            registry,
            new EmergencyAccounts(store, secrets, clock, false),
            team,
            sessions,
            new IdentityPolicyHooks() {
              @Override
              public boolean validateInvitation(String code, UUID provider) {
                return "invite-ok".equals(code);
              }

              @Override
              public boolean consumeInvitation(String code, UUID provider, JsonNode profile) {
                return "invite-ok".equals(code) && profile.path("accountId").isTextual();
              }

              @Override
              public boolean verifyMfa(UUID account, JsonNode input) {
                return team.verifyMfaInput(account, input);
              }
            },
            events,
            audit,
            clock,
            tokens.issuer());
    providerId =
        TenantContext.call(
            tenant,
            () -> {
              String id = UuidV7.next().toString();
              store.insert(
                  "idp_provider",
                  id,
                  Json.object(
                      "providerId",
                      id,
                      "connectorType",
                      "team-account",
                      "builtin",
                      true,
                      "enabled",
                      true,
                      "display",
                      Json.object("name", Json.object("en", "Team", "zh-CN", "团队")),
                      "config",
                      secrets.encrypt(
                          Json.object(
                                  "rpId",
                                  "portal.example.com",
                                  "origins",
                                  List.of("https://portal.example.com"),
                                  "primaryStep",
                                  "form")
                              .toString(),
                          "provider:" + tenant + ":" + id),
                      "admission",
                      Json.object("mode", "invite_only", "mfa", "never"),
                      "createdAt",
                      clock.instant().toString(),
                      "updatedAt",
                      clock.instant().toString()),
                  null);
              return id;
            });
  }

  @Test
  void inviteRegistrationSucceedsAndMissingInviteFails() {
    TenantContext.call(
        tenant,
        () -> {
          var begin =
              login.begin(
                  Json.object(
                      "providerId", providerId, "returnTo", "/", "invitationCode", "invite-ok"),
                  "portal",
                  "192.0.2.10",
                  "test",
                  "https://portal.example.com",
                  null);
          assertEquals("register", begin.transaction().path("step").path("formId").asString());
          var advanced =
              login.advance(
                  begin.transaction().path("id").asString(),
                  begin.bindingCookie(),
                  Json.object(
                      "type",
                      "form",
                      "formId",
                      "register",
                      "values",
                      Json.object(
                          "username",
                          "alice",
                          "password",
                          "correct-horse",
                          "displayName",
                          "Alice")),
                  false);
          assertEquals("complete", advanced.transaction().path("step").path("type").asString());
          assertNotNull(advanced.session());
          assertTrue(eventTypes.contains("identity.login"));

          assertEquals(
              "iam.admission_denied",
              assertThrows(
                      AurigaException.class,
                      () ->
                          login.begin(
                              Json.object("providerId", providerId, "invitationCode", "invite-bad"),
                              "portal",
                              "192.0.2.10",
                              "test",
                              "https://portal.example.com",
                              null))
                  .errorCode()
                  .code());

          var noInvite =
              login.begin(
                  Json.object("providerId", providerId, "returnTo", "/"),
                  "portal",
                  "192.0.2.10",
                  "test",
                  "https://portal.example.com",
                  null);
          assertEquals("password", noInvite.transaction().path("step").path("formId").asString());
          var fail =
              login.advance(
                  noInvite.transaction().path("id").asString(),
                  noInvite.bindingCookie(),
                  Json.object(
                      "type",
                      "form",
                      "formId",
                      "password",
                      "values",
                      Json.object("username", "nobody", "password", "correct-horse")),
                  false);
          assertEquals("fail", fail.transaction().path("step").path("type").asString());
          return null;
        });
  }

  @Test
  void passwordAndTotpLogin() {
    TenantContext.call(
        tenant,
        () -> {
          team.register(providerId, "alice", "correct-horse", "Alice");
          var begin =
              login.begin(
                  Json.object(
                      "providerId", providerId, "returnTo", "/", "invitationCode", "invite-ok"),
                  "portal",
                  "192.0.2.10",
                  "test",
                  "https://portal.example.com",
                  null);
          // already registered username path: use password login after first invite register
          login.advance(
              begin.transaction().path("id").asString(),
              begin.bindingCookie(),
              Json.object(
                  "type",
                  "form",
                  "formId",
                  "register",
                  "values",
                  Json.object(
                      "username", "carol", "password", "correct-horse", "displayName", "Carol")),
              false);
          UUID account = team.accountIdFor(providerId, "carol");
          assertNotNull(account);
          var enroll =
              recent(account, clock.instant())
                  .call(() -> team.beginTotpEnrollment(account, "portal.example.com"));
          recent(account, clock.instant())
              .run(
                  () ->
                      team.confirmTotpEnrollment(
                          account,
                          enroll.path("enrollmentId").asString(),
                          Totp.code(
                              enroll.path("secret").asString(),
                              clock.instant().getEpochSecond() / 30)));

          var loginBegin =
              login.begin(
                  Json.object("providerId", providerId, "returnTo", "/"),
                  "portal",
                  "192.0.2.10",
                  "test",
                  "https://portal.example.com",
                  null);
          assertEquals("password", loginBegin.transaction().path("step").path("formId").asString());
          var afterPassword =
              login.advance(
                  loginBegin.transaction().path("id").asString(),
                  loginBegin.bindingCookie(),
                  Json.object(
                      "type",
                      "form",
                      "formId",
                      "password",
                      "values",
                      Json.object("username", "carol", "password", "correct-horse")),
                  false);
          assertEquals("totp", afterPassword.transaction().path("step").path("formId").asString());
          ObjectNode totpRow = store.get("team_totp", account.toString(), false);
          String secret =
              secrets.decrypt(totpRow.path("totp").asString(), "totp:" + tenant + ":" + account);
          // Advance past the enrollment counter window so the same TOTP slot is not reused.
          clock.now = clock.now.plusSeconds(60);
          var done =
              login.advance(
                  loginBegin.transaction().path("id").asString(),
                  loginBegin.bindingCookie(),
                  Json.object(
                      "type",
                      "form",
                      "formId",
                      "totp",
                      "values",
                      Json.object(
                          "code", Totp.code(secret, clock.instant().getEpochSecond() / 30))),
                  false);
          assertEquals("complete", done.transaction().path("step").path("type").asString());
          assertTrue(done.session().session().path("mfa").asBoolean());
          return null;
        });
  }

  @Test
  void qrChallengeStateMachine() {
    TenantContext.call(
        tenant,
        () -> {
          ObjectNode created =
              qr.create(
                  UuidV7.next().toString(),
                  providerId,
                  "login",
                  "192.0.2.10",
                  "desktop",
                  "https://portal.example.com");
          String id = created.path("challengeId").asString();
          assertEquals("waiting", qr.row(id, false).path("status").asString());
          String account = UuidV7.next().toString();
          qr.markScanned(id, account);
          assertEquals("scanned", qr.row(id, false).path("status").asString());
          String code = created.path("matchCode").asString();
          qr.confirm(id, code, account, null);
          assertEquals("confirmed", qr.row(id, false).path("status").asString());
          ObjectNode other =
              qr.create(
                  UuidV7.next().toString(),
                  providerId,
                  "login",
                  "192.0.2.10",
                  "desktop",
                  "https://portal.example.com");
          String wrong =
              String.format(
                  "%02d", (Integer.parseInt(other.path("matchCode").asString()) + 1) % 100);
          qr.markScanned(other.path("challengeId").asString(), account);
          assertEquals(
              "iam.qr_match_mismatch",
              assertThrows(
                      AurigaException.class,
                      () -> qr.confirm(other.path("challengeId").asString(), wrong, account, null))
                  .errorCode()
                  .code());
          return null;
        });
  }

  private RequestContext recent(UUID account, Instant time) {
    return RequestContext.start("test", null)
        .withPrincipal(
            new AurigaPrincipal(
                account.toString(),
                tenant,
                PrincipalType.USER,
                TokenContext.PORTAL,
                "test-session",
                null,
                "member",
                List.of(),
                Set.of(),
                clock.instant(),
                time,
                "test-token"));
  }

  private UUID registeredAccount() {
    var begin =
        login.begin(
            Json.object("providerId", providerId, "invitationCode", "invite-ok"),
            "portal",
            "192.0.2.10",
            "test",
            "https://portal.example.com",
            null);
    var done =
        login.advance(
            begin.transaction().path("id").asString(),
            begin.bindingCookie(),
            Json.object(
                "type",
                "form",
                "formId",
                "register",
                "values",
                Json.object(
                    "username", "alice", "password", "correct-horse", "displayName", "Alice")),
            false);
    assertNotNull(done.session());
    return team.accountIdFor(providerId, "alice");
  }

  @Test
  void registrationAndPasswordLoginUseTheSameNormalizedUsername() {
    TenantContext.run(
        tenant,
        () -> {
          var begin =
              login.begin(
                  Json.object("providerId", providerId, "invitationCode", "invite-ok"),
                  "portal",
                  "192.0.2.10",
                  "test",
                  "https://portal.example.com",
                  null);
          var registered =
              login.advance(
                  begin.transaction().path("id").asString(),
                  begin.bindingCookie(),
                  Json.object(
                      "type",
                      "form",
                      "formId",
                      "register",
                      "values",
                      Json.object(
                          "username",
                          " Alice ",
                          "password",
                          "correct-horse",
                          "displayName",
                          "Alice")),
                  false);
          assertNotNull(registered.session());
          UUID account = team.accountIdFor(providerId, "alice");
          assertNotNull(account);
          var password =
              login.begin(
                  Json.object("providerId", providerId),
                  "portal",
                  "192.0.2.10",
                  "test",
                  "https://portal.example.com",
                  null);
          var done =
              login.advance(
                  password.transaction().path("id").asString(),
                  password.bindingCookie(),
                  Json.object(
                      "type",
                      "form",
                      "formId",
                      "password",
                      "values",
                      Json.object("username", " ALICE ", "password", "correct-horse")),
                  false);
          assertNotNull(done.session());
          assertEquals(account.toString(), done.session().session().path("accountId").asString());
        });
  }

  @Test
  void mandatoryMfaCannotBeSkippedWithoutEnrollment() {
    TenantContext.run(
        tenant,
        () -> {
          registeredAccount();
          var p = store.get("idp_provider", providerId, true);
          ((ObjectNode) p.path("admission")).put("mfa", "always");
          store.save("idp_provider", p);
          var begin =
              login.begin(
                  Json.object("providerId", providerId),
                  "portal",
                  "192.0.2.10",
                  "test",
                  "https://portal.example.com",
                  null);
          var denied =
              login.advance(
                  begin.transaction().path("id").asString(),
                  begin.bindingCookie(),
                  Json.object(
                      "type",
                      "form",
                      "formId",
                      "password",
                      "values",
                      Json.object("username", "alice", "password", "correct-horse")),
                  false);
          assertNull(denied.session());
          assertEquals("first_totp", denied.transaction().path("step").path("formId").asString());
        });
  }

  @Test
  void passkeyOnlyAccountMustCompleteAssertionAfterPassword() {
    TenantContext.run(
        tenant,
        () -> {
          UUID account = registeredAccount();
          store.insert(
              "team_passkey",
              UuidV7.next().toString(),
              Json.object("accountId", account.toString(), "credentialId", "AQ"),
              null);
          var begin =
              login.begin(
                  Json.object("providerId", providerId),
                  "portal",
                  "192.0.2.10",
                  "test",
                  "https://portal.example.com",
                  null);
          var assertion =
              login.advance(
                  begin.transaction().path("id").asString(),
                  begin.bindingCookie(),
                  Json.object(
                      "type",
                      "form",
                      "formId",
                      "password",
                      "values",
                      Json.object("username", "alice", "password", "correct-horse")),
                  false);
          assertNull(assertion.session());
          assertEquals("webauthn", assertion.transaction().path("step").path("type").asString());
          assertEquals(
              "required",
              assertion
                  .transaction()
                  .path("step")
                  .path("publicKey")
                  .path("userVerification")
                  .asString());
          var denied =
              login.advance(
                  begin.transaction().path("id").asString(),
                  begin.bindingCookie(),
                  Json.object("type", "webauthn", "credential", Json.object()),
                  false);
          assertEquals(
              "iam.invalid_credentials", denied.transaction().path("step").path("code").asString());
        });
  }

  @Test
  void normalizedUsernamesShareRateLimitAndTransactionsDoNotShareAnonymousBucket() {
    TenantContext.run(
        tenant,
        () -> {
          for (String name : List.of("Alice", " aLiCe ")) {
            var begin =
                login.begin(
                    Json.object("providerId", providerId),
                    "portal",
                    "192.0.2.10",
                    "test",
                    "https://portal.example.com",
                    null);
            login.advance(
                begin.transaction().path("id").asString(),
                begin.bindingCookie(),
                Json.object(
                    "type",
                    "form",
                    "formId",
                    "password",
                    "values",
                    Json.object("username", name, "password", "incorrect")),
                false);
          }
          verify(store, times(2))
              .limit(eq("login-provider:" + providerId + ":user:alice"), any(), eq(10));
          var p = store.get("idp_provider", providerId, true);
          p.put(
              "config",
              secrets.encrypt(
                  Json.object(
                          "rpId",
                          "portal.example.com",
                          "origins",
                          List.of("https://portal.example.com"),
                          "primaryStep",
                          "webauthn")
                      .toString(),
                  "provider:" + tenant + ":" + providerId));
          store.save("idp_provider", p);
          for (int i = 0; i < 2; i++) {
            var begin =
                login.begin(
                    Json.object("providerId", providerId),
                    "portal",
                    "192.0.2.10",
                    "test",
                    "https://portal.example.com",
                    null);
            login.advance(
                begin.transaction().path("id").asString(),
                begin.bindingCookie(),
                Json.object("type", "webauthn", "credential", Json.object()),
                false);
            verify(store)
                .limit(
                    eq(
                        "login-provider:"
                            + providerId
                            + ":tx:"
                            + begin.transaction().path("id").asString()),
                    any(),
                    eq(10));
          }
          verify(store, never()).limit(eq("login-provider:" + providerId + ":"), any(), anyInt());
        });
  }

  @Test
  void credentialEnrollmentRejectsMissingStaleAndWrongAccountStepUp() {
    TenantContext.run(
        tenant,
        () -> {
          UUID account = UUID.randomUUID();
          var config =
              Json.object(
                  "rpId", "portal.example.com", "origins", List.of("https://portal.example.com"));
          List<Runnable> operations =
              List.of(
                  () -> team.beginTotpEnrollment(account, "Auriga"),
                  () -> team.confirmTotpEnrollment(account, "unknown", "123456"),
                  () -> passkeys.beginRegistration(account, "Alice", config),
                  () ->
                      passkeys.completeRegistration(
                          account, "unknown", "key", Json.object(), config));
          for (Runnable operation : operations) {
            assertEquals(
                "iam.step_up_required",
                assertThrows(AurigaException.class, operation::run).errorCode().code());
            recent(account, clock.instant().minusSeconds(601))
                .run(
                    () ->
                        assertEquals(
                            "iam.step_up_required",
                            assertThrows(AurigaException.class, operation::run)
                                .errorCode()
                                .code()));
            recent(UUID.randomUUID(), clock.instant())
                .run(
                    () ->
                        assertEquals(
                            "iam.step_up_required",
                            assertThrows(AurigaException.class, operation::run)
                                .errorCode()
                                .code()));
          }
        });
  }

  @Test
  void totpCannotBeOverwrittenAndPasskeyRegistrationRequiresUv() {
    TenantContext.run(
        tenant,
        () -> {
          UUID account = UUID.randomUUID();
          recent(account, clock.instant())
              .run(
                  () -> {
                    var config =
                        Json.object(
                            "rpId",
                            "portal.example.com",
                            "origins",
                            List.of("https://portal.example.com"));
                    var registration = passkeys.beginRegistration(account, "Alice", config);
                    assertEquals(
                        "required",
                        registration
                            .path("publicKey")
                            .path("authenticatorSelection")
                            .path("userVerification")
                            .asString());
                    store.insert(
                        "team_totp",
                        account.toString(),
                        Json.object("accountId", account.toString()),
                        null);
                    assertEquals(
                        "common.conflict",
                        assertThrows(
                                AurigaException.class,
                                () -> team.beginTotpEnrollment(account, "Auriga"))
                            .errorCode()
                            .code());
                    assertEquals(
                        "common.conflict",
                        assertThrows(
                                AurigaException.class,
                                () -> team.confirmTotpEnrollment(account, "unknown", "123456"))
                            .errorCode()
                            .code());
                  });
        });
  }

  @Test
  void qrRequiresSameScannerAndTerminalStateCannotBeRevived() {
    TenantContext.run(
        tenant,
        () -> {
          String account = UUID.randomUUID().toString();
          var created =
              qr.create(
                  "tx", providerId, "login", "192.0.2.10", "test", "https://portal.example.com");
          String id = created.path("challengeId").asString(),
              code = created.path("matchCode").asString();
          assertEquals(6, code.length());
          assertFalse(qr.get(id).has("matchCode"));
          assertEquals(
              "iam.external_denied",
              assertThrows(AurigaException.class, () -> qr.confirm(id, code, account, null))
                  .errorCode()
                  .code());
          qr.markScanned(id, account);
          assertEquals(
              "iam.external_denied",
              assertThrows(AurigaException.class, () -> qr.confirm(id, code, "other", null))
                  .errorCode()
                  .code());
          String wrong = code.equals("000000") ? "000001" : "000000";
          assertEquals(
              "iam.qr_match_mismatch",
              assertThrows(AurigaException.class, () -> qr.confirm(id, wrong, account, null))
                  .errorCode()
                  .code());
          assertEquals(
              "iam.external_denied",
              assertThrows(AurigaException.class, () -> qr.confirm(id, code, account, null))
                  .errorCode()
                  .code());
          clock.now = clock.now.plusSeconds(301);
          assertNull(qr.row(id, false));
          assertEquals(
              "common.not_found",
              assertThrows(AurigaException.class, () -> qr.confirm(id, code, account, null))
                  .errorCode()
                  .code());
        });
  }

  @Test
  void invalidDisplayNameAndDuplicateUsernameDoNotExposeNewCodes() {
    TenantContext.run(
        tenant,
        () -> {
          for (String name : List.of("x".repeat(65), "Alice\nAdmin", "Alice\u202e"))
            assertEquals(
                "common.validation_failed",
                assertThrows(
                        AurigaException.class,
                        () -> team.register(providerId, "alice", "correct-horse", name))
                    .errorCode()
                    .code());
          var begin =
              login.begin(
                  Json.object("providerId", providerId, "invitationCode", "invite-ok"),
                  "portal",
                  "192.0.2.10",
                  "test",
                  "https://portal.example.com",
                  null);
          var invalid =
              login.advance(
                  begin.transaction().path("id").asString(),
                  begin.bindingCookie(),
                  Json.object(
                      "type",
                      "form",
                      "formId",
                      "register",
                      "values",
                      Json.object("username", "!", "password", "correct-horse")),
                  false);
          assertEquals(
              "iam.invalid_credentials",
              invalid.transaction().path("step").path("code").asString());
          team.register(providerId, "alice", "correct-horse", "Alice");
          assertEquals(
              "iam.invalid_credentials",
              assertThrows(
                      AurigaException.class,
                      () -> team.register(providerId, "alice", "correct-horse", "Alice"))
                  .errorCode()
                  .code());
          assertEquals(
              "iam.invalid_credentials",
              assertThrows(
                      AurigaException.class,
                      () -> team.authenticatePassword(providerId, "nobody", "correct-horse"))
                  .errorCode()
                  .code());
          assertEquals(
              "iam.invalid_credentials",
              assertThrows(
                      AurigaException.class,
                      () -> team.authenticatePassword(providerId, "alice", "incorrect"))
                  .errorCode()
                  .code());
        });
  }

  @Test
  void normalizedUsernameAttemptsActuallyThrottle() {
    TenantContext.run(
        tenant,
        () -> {
          var attempts = new java.util.concurrent.atomic.AtomicInteger();
          doAnswer(
                  i -> {
                    if (i.getArgument(0).equals("login-provider:" + providerId + ":user:alice")
                        && attempts.incrementAndGet() > 10)
                      throw IamError.error("rate_limited", 429);
                    return null;
                  })
              .when(store)
              .limit(anyString(), any(), anyInt());
          for (int n = 0; n < 10; n++) {
            var begin =
                login.begin(
                    Json.object("providerId", providerId),
                    "portal",
                    "192.0.2.10",
                    "test",
                    "https://portal.example.com",
                    null);
            login.advance(
                begin.transaction().path("id").asString(),
                begin.bindingCookie(),
                Json.object(
                    "type",
                    "form",
                    "formId",
                    "password",
                    "values",
                    Json.object("username", n % 2 == 0 ? "ALICE" : " alice ", "password", "wrong")),
                false);
          }
          var begin =
              login.begin(
                  Json.object("providerId", providerId),
                  "portal",
                  "192.0.2.10",
                  "test",
                  "https://portal.example.com",
                  null);
          assertEquals(
              "common.rate_limited",
              assertThrows(
                      AurigaException.class,
                      () ->
                          login.advance(
                              begin.transaction().path("id").asString(),
                              begin.bindingCookie(),
                              Json.object(
                                  "type",
                                  "form",
                                  "formId",
                                  "password",
                                  "values",
                                  Json.object("username", "AlIcE", "password", "wrong")),
                              false))
                  .errorCode()
                  .code());
        });
  }

  @Test
  void qrPollingDoesNotConsumeLoginAttemptBuckets() {
    TenantContext.run(
        tenant,
        () -> {
          var begin =
              login.begin(
                  Json.object("providerId", providerId),
                  "portal",
                  "192.0.2.10",
                  "test",
                  "https://portal.example.com",
                  null);
          String id = begin.transaction().path("id").asString();
          var challenge =
              qr.create(
                  id, providerId, "login", "192.0.2.10", "test", "https://portal.example.com");
          // Contract-shaped pending transaction; new QR initiation is disabled.
          var challengeRow = qr.row(challenge.path("challengeId").asString(), true);
          challengeRow.put("matchCode", "12");
          store.save("qr_challenge", challengeRow);
          var tx = store.get("login_tx", id, true);
          tx.set(
              "step",
              Json.object(
                  "type",
                  "qr",
                  "content",
                  challenge.path("content").asString(),
                  "expiresAt",
                  challenge.path("expiresAt").asString()));
          tx.put(
              "state",
              secrets.encrypt(
                  Json.object(
                          "phase",
                          "qr",
                          "challengeId",
                          challenge.path("challengeId").asString(),
                          "content",
                          challenge.path("content").asString())
                      .toString(),
                  "login:" + tenant + ":" + id));
          store.save("login_tx", tx);
          clearInvocations(store);
          for (int n = 0; n < 50; n++)
            assertEquals(
                "qr",
                login
                    .advance(id, begin.bindingCookie(), Json.object("type", "poll"), false)
                    .transaction()
                    .path("step")
                    .path("type")
                    .asString());
          verify(store, never()).limit(anyString(), any(), anyInt());
        });
  }

  @Test
  void qrReadDoesNotScanAndPasswordSessionCannotConfirm() {
    TenantContext.run(
        tenant,
        () -> {
          UUID account = UUID.randomUUID();
          SessionManager sessions = mock(SessionManager.class);
          when(sessions.activeById("test-session"))
              .thenReturn(Json.object("accountId", account.toString(), "mfa", false));
          var controller = new IamLoginController(login, null, store, sessions, qr, secrets);
          try {
            var created =
                qr.create(
                    "tx", providerId, "login", "192.0.2.10", "test", "https://portal.example.com");
            String id = created.path("challengeId").asString();
            recent(account, clock.instant())
                .run(
                    () -> {
                      controller.qrChallenge(id);
                      assertEquals("waiting", qr.row(id, false).path("status").asString());
                      qr.markScanned(id, account.toString());
                      assertEquals(
                          "iam.step_up_required",
                          assertThrows(
                                  AurigaException.class,
                                  () ->
                                      controller.confirmQr(
                                          id,
                                          Json.object(
                                              "matchCode", created.path("matchCode").asString())))
                              .errorCode()
                              .code());
                      assertEquals("scanned", qr.row(id, false).path("status").asString());
                    });
          } finally {
            controller.destroy();
          }
        });
  }

  @Test
  void validTotpEnrollmentRevokesOtherSessionsAndPublishesEvent() {
    TenantContext.run(
        tenant,
        () -> {
          UUID account = UUID.randomUUID();
          SessionManager sessions = mock(SessionManager.class);
          TokenService tokens = mock(TokenService.class);
          when(tokens.issuer()).thenReturn("https://portal.example.com");
          var controller =
              new IamMeController(store, sessions, team, passkeys, null, clock, tokens);
          store.insert(
              "session",
              UUID.randomUUID().toString(),
              Json.object(
                  "accountId", account.toString(), "sessionId", "other-session", "revoked", false),
              null);
          store.insert(
              "session",
              UUID.randomUUID().toString(),
              Json.object(
                  "accountId", account.toString(), "sessionId", "test-session", "revoked", false),
              null);
          recent(account, clock.instant())
              .run(
                  () -> {
                    var enrollment = controller.beginTotp();
                    controller.confirmTotp(
                        enrollment.path("enrollmentId").asString(),
                        Json.object(
                            "code",
                            Totp.code(
                                enrollment.path("secret").asString(),
                                clock.instant().getEpochSecond() / 30)));
                  });
          verify(sessions)
              .revokeOne(
                  argThat(r -> r.path("sessionId").asString().equals("other-session")),
                  eq("security"));
          verify(sessions, never())
              .revokeOne(
                  argThat(r -> r.path("sessionId").asString().equals("test-session")), anyString());
          assertTrue(eventTypes.contains("identity.mfa_changed"));
        });
  }

  private ObjectNode registrationResponse(JsonNode options, boolean uv) throws Exception {
    var generator = java.security.KeyPairGenerator.getInstance("EC");
    generator.initialize(new java.security.spec.ECGenParameterSpec("secp256r1"));
    var key = (java.security.interfaces.ECPublicKey) generator.generateKeyPair().getPublic();
    byte[] x = coordinate(key.getW().getAffineX()), y = coordinate(key.getW().getAffineY());
    var cose =
        com.upokecenter.cbor.CBORObject.NewMap()
            .Add(1, 2)
            .Add(3, -7)
            .Add(-1, 1)
            .Add(-2, x)
            .Add(-3, y);
    byte[] credentialId = new byte[32];
    new java.security.SecureRandom().nextBytes(credentialId);
    var data = new java.io.ByteArrayOutputStream();
    data.write(
        java.security.MessageDigest.getInstance("SHA-256")
            .digest("portal.example.com".getBytes(java.nio.charset.StandardCharsets.UTF_8)));
    data.write(uv ? 0x45 : 0x41);
    data.write(new byte[4 + 16]);
    data.write(new byte[] {0, 32});
    data.write(credentialId);
    data.write(cose.EncodeToBytes());
    var attestation =
        com.upokecenter.cbor.CBORObject.NewMap()
            .Add("fmt", "none")
            .Add("attStmt", com.upokecenter.cbor.CBORObject.NewMap())
            .Add("authData", data.toByteArray());
    String client =
        Json.object(
                "type",
                "webauthn.create",
                "challenge",
                options.path("publicKey").path("challenge").asString(),
                "origin",
                "https://portal.example.com",
                "crossOrigin",
                false)
            .toString();
    var b64 = Base64.getUrlEncoder().withoutPadding();
    return Json.object(
        "id",
        b64.encodeToString(credentialId),
        "rawId",
        b64.encodeToString(credentialId),
        "type",
        "public-key",
        "response",
        Json.object(
            "attestationObject",
            b64.encodeToString(attestation.EncodeToBytes()),
            "clientDataJSON",
            b64.encodeToString(client.getBytes(java.nio.charset.StandardCharsets.UTF_8))),
        "clientExtensionResults",
        Json.object());
  }

  private byte[] coordinate(java.math.BigInteger n) {
    byte[] b = n.toByteArray(), out = new byte[32];
    System.arraycopy(
        b, Math.max(0, b.length - 32), out, Math.max(0, 32 - b.length), Math.min(32, b.length));
    return out;
  }

  @Test
  void registrationWithoutUvFlagIsRejectedAndVerifiedRegistrationSucceeds() throws Exception {
    TenantContext.call(
        tenant,
        () -> {
          UUID account = UUID.randomUUID();
          return recent(account, clock.instant())
              .call(
                  () -> {
                    var config =
                        Json.object(
                            "rpId",
                            "portal.example.com",
                            "origins",
                            List.of("https://portal.example.com"));
                    var registration = passkeys.beginRegistration(account, "Alice", config);
                    var noUv = registrationResponse(registration, false);
                    assertEquals(
                        "iam.invalid_credentials",
                        assertThrows(
                                AurigaException.class,
                                () ->
                                    passkeys.completeRegistration(
                                        account,
                                        registration.path("registrationId").asString(),
                                        "key",
                                        noUv,
                                        config))
                            .errorCode()
                            .code());
                    assertTrue(passkeys.list(account).isEmpty());
                    var verified = passkeys.beginRegistration(account, "Alice", config);
                    passkeys.completeRegistration(
                        account,
                        verified.path("registrationId").asString(),
                        "key",
                        registrationResponse(verified, true),
                        config);
                    assertEquals(1, passkeys.list(account).size());
                    return null;
                  });
        });
  }

  private String firstTotpSecret(LoginManager.Result pending) {
    String id = pending.transaction().path("id").asString();
    var row = store.get("login_tx", id, false);
    String secret =
        secrets.decrypt(
            row.path("firstTotpSecret").asString(), "login:" + tenant + ":" + id + ":first-totp");
    assertFalse(row.toString().contains(secret));
    assertTrue(pending.transaction().path("step").toString().contains(secret));
    assertNull(pending.session());
    assertEquals("first_totp", pending.transaction().path("step").path("formId").asString());
    return secret;
  }

  private LoginManager.Result passwordForFirstTotp() {
    var provider = store.get("idp_provider", providerId, true);
    ((ObjectNode) provider.path("admission")).put("mfa", "always");
    store.save("idp_provider", provider);
    var begin =
        login.begin(
            Json.object("providerId", providerId),
            "portal",
            "192.0.2.10",
            "test",
            "https://portal.example.com",
            null);
    var result =
        login.advance(
            begin.transaction().path("id").asString(),
            begin.bindingCookie(),
            Json.object(
                "type",
                "form",
                "formId",
                "password",
                "values",
                Json.object("username", "alice", "password", "correct-horse")),
            false);
    return new LoginManager.Result(result.transaction(), begin.bindingCookie(), result.session());
  }

  private LoginManager.Result confirmFirst(LoginManager.Result pending, String code) {
    return login.advance(
        pending.transaction().path("id").asString(),
        pending.bindingCookie(),
        Json.object("type", "form", "formId", "first_totp", "values", Json.object("otp", code)),
        false);
  }

  private LoginManager.Result registerForFirstTotp() {
    var provider = store.get("idp_provider", providerId, true);
    ((ObjectNode) provider.path("admission")).put("mfa", "new_device");
    store.save("idp_provider", provider);
    var begin =
        login.begin(
            Json.object("providerId", providerId, "invitationCode", "invite-ok"),
            "portal",
            "192.0.2.10",
            "test",
            "https://portal.example.com",
            null);
    var pending =
        login.advance(
            begin.transaction().path("id").asString(),
            begin.bindingCookie(),
            Json.object(
                "type",
                "form",
                "formId",
                "register",
                "values",
                Json.object("username", "alice", "password", "correct-horse")),
            false);
    assertEquals("first_totp", pending.transaction().path("step").path("formId").asString());
    return new LoginManager.Result(pending.transaction(), begin.bindingCookie(), null);
  }

  @Test
  void failedAndCancelledFirstRegistrationCanBeRestarted() {
    TenantContext.run(
        tenant,
        () -> {
          var first = registerForFirstTotp();
          assertEquals(
              "iam.invalid_credentials",
              confirmFirst(first, "invalid").transaction().path("step").path("code").asString());
          assertNull(
              store.get("team_account", TeamAccounts.accountKey(providerId, "alice"), false));
          var retry = registerForFirstTotp();
          login.cancel(retry.transaction().path("id").asString(), retry.bindingCookie());
          assertNull(
              store.get("team_account", TeamAccounts.accountKey(providerId, "alice"), false));
          var finalAttempt = registerForFirstTotp();
          String secret = firstTotpSecret(finalAttempt);
          assertNotNull(
              confirmFirst(finalAttempt, Totp.code(secret, clock.instant().getEpochSecond() / 30))
                  .session());
        });
  }

  @Test
  void expiredFirstRegistrationCannotDeleteReplacementCredentials() {
    TenantContext.run(
        tenant,
        () -> {
          var old = registerForFirstTotp();
          clock.now = clock.now.plusSeconds(301);
          var replacement = registerForFirstTotp();
          var expired = login.get(old.transaction().path("id").asString(), old.bindingCookie());
          assertEquals(
              "iam.login_tx_expired", expired.transaction().path("step").path("code").asString());
          var row = store.get("team_account", TeamAccounts.accountKey(providerId, "alice"), false);
          assertEquals(
              replacement.transaction().path("id").asString(), row.path("pendingTxId").asString());
          String secret = firstTotpSecret(replacement);
          assertNotNull(
              confirmFirst(replacement, Totp.code(secret, clock.instant().getEpochSecond() / 30))
                  .session());
        });
  }

  @Test
  void invitedNewDeviceRegistrationEnrollsFirstTotpBeforeIssuingSession() {
    TenantContext.run(
        tenant,
        () -> {
          var provider = store.get("idp_provider", providerId, true);
          ((ObjectNode) provider.path("admission")).put("mfa", "new_device");
          store.save("idp_provider", provider);
          var begin =
              login.begin(
                  Json.object("providerId", providerId, "invitationCode", "invite-ok"),
                  "portal",
                  "192.0.2.10",
                  "test",
                  "https://portal.example.com",
                  null);
          var pending =
              login.advance(
                  begin.transaction().path("id").asString(),
                  begin.bindingCookie(),
                  Json.object(
                      "type",
                      "form",
                      "formId",
                      "register",
                      "values",
                      Json.object("username", "alice", "password", "correct-horse")),
                  false);
          pending =
              new LoginManager.Result(
                  pending.transaction(), begin.bindingCookie(), pending.session());
          String secret = firstTotpSecret(pending);
          assertTrue(store.all("session").isEmpty());
          assertNull(team.accountIdFor(providerId, "alice"));
          var done =
              confirmFirst(pending, Totp.code(secret, clock.instant().getEpochSecond() / 30));
          assertNotNull(done.session());
          assertTrue(done.session().session().path("mfa").asBoolean());
          assertTrue(team.hasTotp(team.accountIdFor(providerId, "alice")));
          assertEquals("complete", done.transaction().path("step").path("type").asString());
          assertFalse(
              store
                  .get("login_tx", begin.transaction().path("id").asString(), false)
                  .has("firstTotpSecret"));
          assertTrue(eventTypes.contains("identity.mfa_changed"));
          var finished = pending;
          assertEquals(
              "iam.login_tx_finished",
              assertThrows(AurigaException.class, () -> confirmFirst(finished, "123456"))
                  .errorCode()
                  .code());
        });
  }

  @Test
  void existingPasswordAccountCanEnrollFirstFactorWithoutRecentStepUp() {
    TenantContext.run(
        tenant,
        () -> {
          UUID account = registeredAccount();
          var pending = passwordForFirstTotp();
          String secret = firstTotpSecret(pending);
          var done =
              confirmFirst(pending, Totp.code(secret, clock.instant().getEpochSecond() / 30));
          assertNotNull(done.session());
          assertTrue(team.hasTotp(account));
          assertTrue(done.session().session().path("mfa").asBoolean());
        });
  }

  @Test
  void firstEnrollmentCannotReplaceAnExistingFactor() {
    TenantContext.run(
        tenant,
        () -> {
          UUID account = registeredAccount();
          var pending = passwordForFirstTotp();
          String secret = firstTotpSecret(pending);
          store.insert(
              "team_passkey",
              UUID.randomUUID().toString(),
              Json.object("accountId", account.toString()),
              null);
          var denied =
              confirmFirst(pending, Totp.code(secret, clock.instant().getEpochSecond() / 30));
          assertNull(denied.session());
          assertEquals(
              "common.conflict", denied.transaction().path("step").path("code").asString());
          assertFalse(team.hasTotp(account));
        });
  }

  @Test
  void firstEnrollmentRejectsWrongBindingExpiredTransactionAndInvalidCode() {
    TenantContext.run(
        tenant,
        () -> {
          registeredAccount();
          var pending = passwordForFirstTotp();
          assertEquals(
              "common.not_found",
              assertThrows(
                      AurigaException.class,
                      () ->
                          login.advance(
                              pending.transaction().path("id").asString(),
                              "wrong-binding",
                              Json.object(
                                  "type",
                                  "form",
                                  "formId",
                                  "first_totp",
                                  "values",
                                  Json.object("otp", "123456")),
                              false))
                  .errorCode()
                  .code());
          clock.now = clock.now.plusSeconds(301);
          var tx = store.get("login_tx", pending.transaction().path("id").asString(), true);
          var expired = confirmFirst(pending, "123456");
          assertEquals(
              "iam.login_tx_expired", expired.transaction().path("step").path("code").asString());
          assertFalse(
              store.get("login_tx", tx.path("id").asString(), false).has("firstTotpSecret"));
          var fresh = passwordForFirstTotp();
          var denied = confirmFirst(fresh, "invalid");
          assertNull(denied.session());
          assertEquals(
              "iam.invalid_credentials", denied.transaction().path("step").path("code").asString());
          assertFalse(team.hasTotp(team.accountIdFor(providerId, "alice")));
        });
  }

  @Test
  void deviceRevokeBlocksRefreshGrant() {
    TenantContext.call(
        tenant,
        () -> {
          String deviceId = UuidV7.next().toString();
          String accountId = UuidV7.next().toString();
          store.insert(
              "account",
              accountId,
              Json.object(
                  "accountId",
                  accountId,
                  "status",
                  "active",
                  "displayName",
                  "D",
                  "createdAt",
                  clock.instant().toString(),
                  "memberType",
                  "member"),
              null);
          store.insert(
              "device",
              deviceId,
              Json.object(
                  "deviceId",
                  deviceId,
                  "accountId",
                  accountId,
                  "name",
                  "CLI",
                  "kind",
                  "cli",
                  "revoked",
                  false,
                  "createdAt",
                  clock.instant().toString()),
              null);
          ObjectNode device = store.get("device", deviceId, true);
          device.put("revoked", true);
          store.save("device", device);
          assertTrue(store.get("device", deviceId, false).path("revoked").asBoolean());
          return null;
        });
  }
}
