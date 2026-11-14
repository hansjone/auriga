package auriga.audit;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.context.*;
import auriga.common.error.AurigaException;
import auriga.common.events.ReceivedEvent;
import auriga.common.persistence.CommonMigrations;
import auriga.common.security.*;
import auriga.testsupport.PostgresExtension;
import java.time.*;
import java.util.*;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import tools.jackson.databind.json.JsonMapper;

@ExtendWith(PostgresExtension.class)
class UacAuditEventsTest {
  static AuditStore store;
  static final JsonMapper JSON = JsonMapper.builder().build();
  final UUID tenant = UUID.randomUUID(),
      provider = UUID.randomUUID(),
      correlation = UUID.randomUUID();
  final Instant now = Instant.parse("2026-01-01T00:00:00Z");

  @BeforeAll
  static void setup() {
    var roles =
        PostgresExtension.serviceRoles(
            UacAuditEventsTest.class, "uac_audit_owner", "uac_audit_app", "audit");
    CommonMigrations.migrate(roles.owner().dataSource(), "audit");
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas("audit")
                .locations("classpath:db/audit")
                .load())
        .migrate();
    store = new AuditStore(roles.runtime().dataSource(), false);
  }

  AurigaPrincipal admin(UUID tenant, Set<String> permissions) {
    return new AurigaPrincipal(
        UUID.randomUUID().toString(),
        tenant,
        PrincipalType.USER,
        TokenContext.ADMIN,
        UUID.randomUUID().toString(),
        null,
        "member",
        List.of(),
        permissions,
        now,
        null,
        UUID.randomUUID().toString());
  }

  @Test
  void actualConsumerRetainsOnlyMinimalSafeFieldsAndReadCannotCrossTenantOrProvider() {
    String secret = UUID.randomUUID().toString();
    String hash = UUID.randomUUID().toString().replace("-", "").substring(0, 12);
    var details =
        JSON.createObjectNode()
            .put("correlationId", correlation.toString())
            .put("providerId", provider.toString())
            .put("purpose", "login_info")
            .put("chain", "login")
            .put("outcome", "failure")
            .put("departmentPresent", false)
            .put("strategy", "token-verify")
            .put("credentialVersion", 3)
            .put("maxRetries", 2)
            .put("oldTokenHashPrefix", hash)
            .put("newTokenHashPrefix", hash)
            .put("identityComparisonVersion", "strip-ascii-fold-v1")
            .put("identityMatched", false)
            .put("retry_pending", true)
            .put("retryAt", now.plusSeconds(5).toString())
            .put("token", secret)
            .put("department", secret)
            .put("udsMessage", secret);
    for (var t : List.of(tenant, UUID.randomUUID())) {
      var data =
          JSON.createObjectNode()
              .put("action", "iam.uac.department_lookup")
              .put("result", "failure");
      data.set("details", details);
      store.accept(
          new ReceivedEvent(
              UUID.randomUUID(), "audit.recorded", "auriga/iam", null, now, t, "1", null, data));
    }
    RequestContext.start(null, null)
        .withPrincipal(admin(tenant, Set.of("audit.events.read")))
        .run(
            () ->
                TenantContext.run(
                    tenant,
                    () -> {
                      var result =
                          store.readUac(
                              now.minusSeconds(1), now.plusSeconds(1), provider, correlation, 100);
                      assertEquals(1, result.events().size());
                      assertFalse(result.truncated());
                      String raw = result.events().getFirst();
                      assertFalse(raw.contains(secret));
                      var retained = JSON.readTree(raw).path("details");
                      assertEquals("login_info", retained.path("purpose").asString());
                      assertEquals("failure", retained.path("outcome").asString());
                      assertFalse(retained.path("departmentPresent").asBoolean());
                      assertEquals("token-verify", retained.path("strategy").asString());
                      assertEquals(3, retained.path("credentialVersion").asInt());
                      assertEquals(hash, retained.path("oldTokenHashPrefix").asString());
                      assertEquals(hash, retained.path("newTokenHashPrefix").asString());
                      assertTrue(retained.path("retry_pending").asBoolean());
                      assertEquals(
                          now.plusSeconds(5).toString(), retained.path("retryAt").asString());
                      assertTrue(
                          store
                              .readUac(
                                  now.minusSeconds(1),
                                  now.plusSeconds(1),
                                  UUID.randomUUID(),
                                  null,
                                  100)
                              .events()
                              .isEmpty());
                      assertTrue(
                          store
                              .readUac(
                                  now.minusSeconds(1),
                                  now.plusSeconds(1),
                                  provider,
                                  UUID.randomUUID(),
                                  100)
                              .events()
                              .isEmpty());
                      TenantContext.run(
                          UUID.randomUUID(),
                          () ->
                              assertEquals(
                                  "common.forbidden",
                                  assertThrows(
                                          AurigaException.class,
                                          () ->
                                              store.readUac(
                                                  now.minusSeconds(1),
                                                  now.plusSeconds(1),
                                                  provider,
                                                  null,
                                                  100))
                                      .errorCode()
                                      .code()));
                    }));
    RequestContext.start(null, null)
        .withPrincipal(admin(tenant, Set.of()))
        .run(
            () ->
                TenantContext.run(
                    tenant,
                    () ->
                        assertEquals(
                            "common.forbidden",
                            assertThrows(
                                    AurigaException.class,
                                    () ->
                                        store.readUac(
                                            now.minusSeconds(1),
                                            now.plusSeconds(1),
                                            provider,
                                            null,
                                            100))
                                .errorCode()
                                .code())));
  }
}
