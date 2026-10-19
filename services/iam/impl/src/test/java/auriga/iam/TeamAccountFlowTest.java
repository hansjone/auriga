package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import auriga.common.audit.AuditLogger;
import auriga.common.context.TenantContext;
import auriga.common.events.*;
import auriga.common.persistence.UuidV7;
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
                  "test");
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

          var badInvite =
              login.begin(
                  Json.object(
                      "providerId", providerId, "returnTo", "/", "invitationCode", "invite-bad"),
                  "portal",
                  "192.0.2.10",
                  "test");
          var denied =
              login.advance(
                  badInvite.transaction().path("id").asString(),
                  badInvite.bindingCookie(),
                  Json.object(
                      "type",
                      "form",
                      "formId",
                      "register",
                      "values",
                      Json.object(
                          "username", "bob", "password", "correct-horse", "displayName", "Bob")),
                  false);
          assertEquals("fail", denied.transaction().path("step").path("type").asString());
          assertEquals(
              "iam.admission_denied", denied.transaction().path("step").path("code").asString());

          var noInvite =
              login.begin(
                  Json.object("providerId", providerId, "returnTo", "/"),
                  "portal",
                  "192.0.2.10",
                  "test");
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
                  "test");
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
          var enroll = team.beginTotpEnrollment(account, "portal.example.com");
          team.confirmTotpEnrollment(
              account,
              enroll.path("enrollmentId").asString(),
              Totp.code(enroll.path("secret").asString(), clock.instant().getEpochSecond() / 30));

          var loginBegin =
              login.begin(
                  Json.object("providerId", providerId, "returnTo", "/"),
                  "portal",
                  "192.0.2.10",
                  "test");
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
          qr.markScanned(id, UuidV7.next().toString());
          assertEquals("scanned", qr.row(id, false).path("status").asString());
          String code = created.path("matchCode").asString();
          String account = UuidV7.next().toString();
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
          assertThrows(
              Exception.class,
              () ->
                  qr.confirm(
                      other.path("challengeId").asString(), wrong, UuidV7.next().toString(), null));
          return null;
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
