package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.audit.OutboxAuditLogger;
import auriga.common.context.*;
import auriga.common.events.*;
import auriga.common.persistence.*;
import auriga.common.security.*;
import auriga.contracts.iam.v1.*;
import auriga.testsupport.PostgresExtension;
import java.time.*;
import java.util.*;
import java.util.function.Supplier;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;

@ExtendWith(PostgresExtension.class)
class IamAuditPublicationTest {
  static final UUID TENANT = UUID.fromString("00000000-0000-0000-0000-000000000001");
  static final Clock CLOCK = Clock.systemUTC();
  static IamStore store;
  static JdbcTemplate jdbc;
  static TeamAccounts accounts;
  static SessionManager sessions;
  UUID account;

  @BeforeAll
  static void setup() {
    var role =
        PostgresExtension.serviceRole(IamAuditPublicationTest.class, "iam_audit_owner", "iam");
    CommonMigrations.migrate(role.dataSource(), "iam");
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(role.dataSource())
                .schemas("iam")
                .locations("classpath:db/iam")
                .load())
        .migrate();
    jdbc = new JdbcTemplate(role.dataSource());
    store = new IamStore(jdbc, new TenantTransactionManager(role.dataSource()));
    var events =
        new OutboxEventPublisher(jdbc, "iam", "iam", new EventSchemas(), Json.MAPPER, CLOCK, null);
    byte[] key = new byte[32];
    new java.security.SecureRandom().nextBytes(key);
    accounts =
        new TeamAccounts(
            store, new Secrets(Base64.getEncoder().encodeToString(key)), CLOCK, events);
    sessions =
        new SessionManager(
            store,
            mock(SessionKv.class),
            mock(TokenService.class),
            CLOCK,
            events,
            new OutboxAuditLogger(events));
  }

  @BeforeEach
  void seed() {
    account = UuidV7.next();
    TenantContext.run(
        TENANT,
        () ->
            store.tx(
                () ->
                    store.insert(
                        "account",
                        account.toString(),
                        Json.object("status", "active", "memberType", "member"),
                        null)));
  }

  <T> T request(Supplier<T> action) {
    var principal =
        new AurigaPrincipal(
            account.toString(),
            TENANT,
            PrincipalType.USER,
            TokenContext.ADMIN,
            UUID.randomUUID().toString(),
            null,
            "member",
            List.of(),
            Set.of(),
            Instant.now(),
            Instant.now(),
            "audit");
    return TenantContext.call(
        TENANT,
        () ->
            RequestContext.start(
                    "iam-audit", "00-12345678901234567890123456789012-1234567890123456-01")
                .withClient("192.0.2.20", "prompt=must-not-appear")
                .withPrincipal(principal)
                .call(action::get));
  }

  List<String> records(String action, String target) {
    return store.tx(
        () ->
            jdbc.query(
                "SELECT envelope::text FROM iam.outbox_event WHERE event_type='audit.recorded' AND envelope::jsonb->'data'->>'action'=? AND envelope::jsonb->'data'->'target'->>'id'=?",
                (rs, n) -> rs.getString(1),
                action,
                target));
  }

  @Test
  void totpChangesContainNoCredentialsAndRollbackTogether() {
    request(
        () -> {
          var enrollment = store.tx(() -> accounts.beginTotpEnrollment(account, "example.com"));
          String secret = enrollment.path("secret").asString();
          String code = Totp.code(secret, CLOCK.instant().getEpochSecond() / 30);
          store.tx(
              () -> {
                accounts.confirmTotpEnrollment(
                    account, enrollment.path("enrollmentId").asString(), code);
                return null;
              });
          assertThrows(
              IllegalStateException.class,
              () ->
                  store.tx(
                      () -> {
                        accounts.deleteTotp(account, false);
                        throw new IllegalStateException("rollback");
                      }));
          assertTrue(store.tx(() -> accounts.hasTotp(account)));
          assertTrue(records("iam.mfa.totp.remove", account.toString()).isEmpty());
          store.tx(
              () -> {
                accounts.deleteTotp(account, false);
                return null;
              });
          for (String action : List.of("add", "remove")) {
            var entries = records("iam.mfa.totp." + action, account.toString());
            assertEquals(1, entries.size());
            assertFalse(entries.getFirst().contains(secret));
            assertFalse(entries.getFirst().contains("must-not-appear"));
            assertFalse(entries.getFirst().contains(enrollment.path("enrollmentId").asString()));
            var envelope = Json.MAPPER.readTree(entries.getFirst());
            assertEquals("192.0.2.20", envelope.path("data").path("actor").path("ip").asString());
            assertEquals(
                account.toString(), envelope.path("data").path("actor").path("id").asString());
          }
          return null;
        });
  }

  @Test
  void adminStepUpAndRepeatedDeviceRevocationAreTransactional() {
    request(
        () -> {
          UUID provider = UuidV7.next();
          store.tx(
              () ->
                  store.insert(
                      "idp_provider",
                      provider.toString(),
                      Json.object("enabled", true, "connectorType", "team-account"),
                      null));
          var issued =
              store.tx(
                  () ->
                      sessions.create(
                          account,
                          provider,
                          "admin",
                          false,
                          Duration.ofHours(1),
                          Duration.ofMinutes(30)));
          sessions.stepUp(issued.session());
          assertEquals(
              1,
              records("iam.session.step_up", issued.session().path("sessionId").asString()).size());
          UUID device = UuidV7.next();
          store.tx(
              () ->
                  store.insert(
                      "device",
                      device.toString(),
                      Json.object("accountId", account.toString(), "revoked", false),
                      null));
          var revoke =
              RevokeSessionsRequest.newBuilder()
                  .setTenantId(TENANT.toString())
                  .setAccountId(account.toString())
                  .setIncludeDevices(true)
                  .setReason(RevokeReason.REVOKE_REASON_ADMIN)
                  .build();
          sessions.revoke(revoke);
          sessions.revoke(revoke);
          assertEquals(1, records("iam.device.revoke", device.toString()).size());
          return null;
        });
  }
}
