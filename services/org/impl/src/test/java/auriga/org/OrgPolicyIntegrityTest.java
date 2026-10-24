package auriga.org;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.events.EventSchemas;
import auriga.common.events.OutboxEventPublisher;
import auriga.common.persistence.CommonMigrations;
import auriga.common.persistence.TenantTransactionManager;
import auriga.common.persistence.UuidV7;
import auriga.testsupport.PostgresExtension;
import java.time.Clock;
import java.util.List;
import java.util.UUID;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.Executors;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.stream.Stream;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.Arguments;
import org.junit.jupiter.params.provider.MethodSource;
import org.junit.jupiter.params.provider.ValueSource;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.transaction.TransactionDefinition;
import org.springframework.transaction.support.TransactionTemplate;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

@ExtendWith(PostgresExtension.class)
class OrgPolicyIntegrityTest {
  private static final UUID TENANT = OrgIds.DEFAULT_TENANT;
  private static OrgStore store;
  private static OrgService svc;
  private static TenantTransactionManager manager;
  private static JdbcTemplate observer;

  @BeforeAll
  static void migrate() {
    var roles =
        PostgresExtension.serviceRoles(OrgPolicyIntegrityTest.class, "org_owner", "org_app", "org");
    CommonMigrations.migrate(roles.owner().dataSource(), "org");
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas("org")
                .locations("classpath:db/org")
                .load())
        .migrate();
    manager = new TenantTransactionManager(roles.runtime().dataSource());
    store = new OrgStore(new JdbcTemplate(roles.runtime().dataSource()), manager);
    observer =
        new JdbcTemplate(PostgresExtension.database(OrgPolicyIntegrityTest.class).dataSource());
    svc =
        new OrgService(
            store,
            new OutboxEventPublisher(
                store.jdbc(),
                "org",
                "org",
                new EventSchemas(),
                OrgJson.MAPPER,
                Clock.systemUTC(),
                null),
            Clock.systemUTC());
  }

  @BeforeEach
  void reset() {
    store.tenant(
        TENANT,
        () -> {
          store
              .jdbc()
              .update(
                  "UPDATE policy SET version=1,value='{}'::jsonb,updated_at='2026-01-01T00:00:00Z',updated_by=NULL WHERE tenant_id=?",
                  TENANT);
          store.jdbc().update("DELETE FROM outbox_event WHERE tenant_id=?", TENANT);
          return null;
        });
  }

  static Stream<Arguments> invalidWorkspace() {
    var values = Stream.<Arguments>builder();
    for (String field : List.of("idleMinutes", "maxConcurrentStarts")) {
      for (String raw : List.of("null", "true", "\"5\"", "[]", "{}", "5.5", "0", "-1"))
        values.add(Arguments.of("{\"" + field + "\":" + raw + "}"));
    }
    values.add(Arguments.of("{\"idleMinutes\":4}"));
    for (String field :
        List.of("wakeOnSchedule", "alwaysOnRequiresApproval", "allowPersonalPlugins")) {
      for (String raw : List.of("null", "0", "\"true\"", "[]", "{}"))
        values.add(Arguments.of("{\"" + field + "\":" + raw + "}"));
    }
    for (String raw : List.of("null", "0", "true", "[]", "{}"))
      values.add(Arguments.of("{\"defaultDshVersion\":" + raw + "}"));
    for (String raw : List.of("null", "[]", "true", "5", "\"x\"")) values.add(Arguments.of(raw));
    return values.build();
  }

  @ParameterizedTest
  @MethodSource("invalidWorkspace")
  void invalidWorkspaceDoesNotMutateAnything(String raw) {
    ObjectNode before = get("workspace");
    assertError(
        "common.validation_failed", () -> put("workspace", OrgJson.MAPPER.readTree(raw), 1L));
    assertEquals(before, get("workspace"));
    assertEquals(0, eventCount());
  }

  @Test
  void optionalAndUnknownFieldsArePreservedWithoutDefaultsOrPrecisionLoss() {
    JsonNode value =
        OrgJson.MAPPER.readTree(
            "{\"idleMinutes\":9007199254740993,\"maxConcurrentStarts\":18446744073709551616,\"wakeOnSchedule\":false,\"alwaysOnRequiresApproval\":true,\"allowPersonalPlugins\":false,\"defaultDshVersion\":\"\",\"unknown\":[null,{\"number\":9007199254740993}],\"scalar\":false}");
    assertEquals(value, put("workspace", value, 1L).path("value"));
    assertEquals(value, get("workspace").path("value"));
    assertEquals(OrgJson.object(), put("workspace", OrgJson.object(), 2L).path("value"));
    assertEquals(3, get("workspace").path("version").asLong());
    assertEquals(0, eventCount());
  }

  @ParameterizedTest
  @ValueSource(strings = {"workspace", "login", "egress"})
  void staleVersionAndCallerRollbackPreservePolicy(String key) {
    ObjectNode before = get(key);
    assertError("org.precondition_failed", () -> put(key, OrgJson.object("unknown", true), 7L));
    assertEquals(before, get(key));
    assertThrows(
        IllegalStateException.class,
        () ->
            store.tenant(
                TENANT,
                () -> {
                  svc.putPolicy(key, OrgJson.object("unknown", true), 1L);
                  throw new IllegalStateException("rollback");
                }));
    assertEquals(before, get(key));
    assertEquals(0, eventCount());
  }

  static Stream<Arguments> races() {
    return Stream.of("workspace", "login", "egress")
        .flatMap(
            key ->
                Stream.of(false, true)
                    .flatMap(
                        rr ->
                            Stream.of(false, true)
                                .map(withEtag -> Arguments.of(key, rr, withEtag))));
  }

  @ParameterizedTest
  @MethodSource("races")
  void oldSnapshotCannotOverwriteVersionCheck(String key, boolean repeatableRead, boolean withEtag)
      throws Exception {
    String result = race(key, repeatableRead, withEtag);
    String expected =
        repeatableRead ? "common.conflict" : withEtag ? "org.precondition_failed" : "committed";
    assertEquals(expected, result);
    boolean secondCommitted = expected.equals("committed");
    assertEquals(secondCommitted ? 3 : 2, get(key).path("version").asLong());
    assertEquals(
        secondCommitted ? "second" : "first", get(key).path("value").path("writer").asString());
    assertEquals(0, eventCount());
  }

  @Test
  void tenantScopeAndExistingKeyErrorsRemainIntact() {
    UUID foreign = UuidV7.next();
    assertError(
        "org.not_found", () -> TenantContext.call(foreign, () -> svc.getPolicy("workspace")));
    assertError(
        "org.not_found",
        () -> TenantContext.call(foreign, () -> svc.putPolicy("workspace", OrgJson.object(), 1L)));
    assertError("org.conflict", () -> put("unknown", OrgJson.object(), 1L));
    store.tenant(
        TENANT,
        () -> {
          store.jdbc().update("DELETE FROM policy WHERE tenant_id=? AND key='workspace'", TENANT);
          return null;
        });
    try {
      assertError("org.not_found", () -> put("workspace", OrgJson.object(), 1L));
    } finally {
      store.tenant(
          TENANT,
          () -> {
            store
                .jdbc()
                .update(
                    "INSERT INTO policy(id,tenant_id,key,version,value,updated_at) VALUES(?,?,'workspace',1,'{}'::jsonb,now())",
                    UuidV7.next(),
                    TENANT);
            return null;
          });
    }
  }

  @Test
  void httpIntegerParsingAndStorageRemainExact() throws Exception {
    var mvc =
        org.springframework.test.web.servlet.setup.MockMvcBuilders.standaloneSetup(
                new OrgAdminController(svc))
            .build();
    TenantContext.run(
        TENANT,
        () -> {
          try {
            var response =
                mvc.perform(
                        org.springframework.test.web.servlet.request.MockMvcRequestBuilders.put(
                                "/api/org/v1/admin/policies/workspace")
                            .header("If-Match", "\"1\"")
                            .contentType("application/json")
                            .content(
                                "{\"value\":{\"idleMinutes\":9007199254740993,\"maxConcurrentStarts\":18446744073709551616}}"))
                    .andReturn()
                    .getResponse();
            assertEquals(200, response.getStatus());
            assertEquals(
                "9007199254740993",
                OrgJson.MAPPER
                    .readTree(response.getContentAsString())
                    .path("value")
                    .path("idleMinutes")
                    .asString());
            assertEquals(
                "18446744073709551616",
                svc.getPolicy("workspace").path("value").path("maxConcurrentStarts").asString());
          } catch (Exception error) {
            throw new AssertionError(error);
          }
        });
  }

  @Test
  void realSecondTenantCannotUpdateFirstTenantsPolicy() {
    UUID foreign = UuidV7.next();
    store.tenant(
        foreign,
        () -> {
          store
              .jdbc()
              .update(
                  "INSERT INTO tenant(id,tenant_id,slug,display_name) VALUES(?,?,?,?)",
                  foreign,
                  foreign,
                  foreign.toString(),
                  "Other");
          store
              .jdbc()
              .update(
                  "INSERT INTO policy(id,tenant_id,key,version,value,updated_at) VALUES(?,?,'workspace',1,'{}'::jsonb,now())",
                  UuidV7.next(),
                  foreign);
          return null;
        });
    ObjectNode before = get("workspace");
    try {
      var updated =
          TenantContext.call(
              foreign, () -> svc.putPolicy("workspace", OrgJson.object("idleMinutes", 5), 1L));
      assertEquals(2, updated.path("version").asLong());
      assertEquals(before, get("workspace"));
      store.tenant(
          TENANT,
          () -> {
            assertEquals(
                0, store.jdbc().update("UPDATE policy SET version=99 WHERE tenant_id=?", foreign));
            return null;
          });
      assertEquals(
          2,
          TenantContext.call(foreign, () -> svc.getPolicy("workspace")).path("version").asLong());
    } finally {
      store.tenant(
          foreign,
          () -> {
            store.jdbc().update("DELETE FROM policy WHERE tenant_id=?", foreign);
            store.jdbc().update("DELETE FROM tenant WHERE tenant_id=?", foreign);
            return null;
          });
    }
  }

  private static String race(String key, boolean repeatableRead, boolean withEtag)
      throws Exception {
    String sibling = key.equals("login") ? "egress" : "login";
    put(sibling, OrgJson.object("rollback", "before"), 1L);
    var firstWritten = new CountDownLatch(1);
    var releaseFirst = new CountDownLatch(1);
    var secondStarted = new CountDownLatch(1);
    var secondPid = new AtomicInteger();
    var transaction = new TransactionTemplate(manager);
    if (repeatableRead)
      transaction.setIsolationLevel(TransactionDefinition.ISOLATION_REPEATABLE_READ);
    try (var executor = Executors.newFixedThreadPool(2)) {
      var first =
          executor.submit(
              () ->
                  store.tenant(
                      TENANT,
                      () -> {
                        store.jdbc().execute("SET LOCAL statement_timeout='12s'");
                        svc.putPolicy(key, OrgJson.object("writer", "first"), 1L);
                        firstWritten.countDown();
                        await(releaseFirst);
                        return "committed";
                      }));
      try {
        await(firstWritten);
        var second =
            executor.submit(
                () -> {
                  try {
                    return TenantContext.call(
                        TENANT,
                        () ->
                            transaction.execute(
                                status -> {
                                  store.jdbc().execute("SET LOCAL statement_timeout='12s'");
                                  secondPid.set(
                                      store
                                          .jdbc()
                                          .queryForObject(
                                              "SELECT pg_backend_pid()", Integer.class));
                                  assertEquals(1, svc.getPolicy(key).path("version").asLong());
                                  // A write made before the rejected policy update must roll back
                                  // too.
                                  store
                                      .jdbc()
                                      .update(
                                          "UPDATE policy SET value='{\"rollback\":\"after\"}'::jsonb WHERE tenant_id=? AND key=?",
                                          TENANT,
                                          key.equals("login") ? "egress" : "login");
                                  secondStarted.countDown();
                                  svc.putPolicy(
                                      key,
                                      OrgJson.object("writer", "second"),
                                      withEtag ? 1L : null);
                                  return "committed";
                                }));
                  } catch (AurigaException e) {
                    return e.errorCode().code();
                  }
                });
        await(secondStarted);
        long deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(10);
        while (!second.isDone()
            && !Boolean.TRUE.equals(
                observer.queryForObject(
                    "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE pid=? AND wait_event_type='Lock')",
                    Boolean.class,
                    secondPid.get())))
          assertTrue(System.nanoTime() < deadline, "second policy writer did not wait for lock");
        releaseFirst.countDown();
        assertEquals("committed", first.get(15, TimeUnit.SECONDS));
        String outcome = second.get(15, TimeUnit.SECONDS);
        assertEquals(
            outcome.equals("committed") ? "after" : "before",
            get(sibling).path("value").path("rollback").asString());
        return outcome;
      } finally {
        releaseFirst.countDown();
      }
    }
  }

  private static ObjectNode get(String key) {
    return TenantContext.call(TENANT, () -> svc.getPolicy(key));
  }

  private static ObjectNode put(String key, JsonNode value, Long etag) {
    return TenantContext.call(TENANT, () -> svc.putPolicy(key, value, etag));
  }

  private static int eventCount() {
    return store.tenant(
        TENANT,
        () ->
            store
                .jdbc()
                .queryForObject(
                    "SELECT count(*) FROM outbox_event WHERE tenant_id=?", Integer.class, TENANT));
  }

  private static void assertError(String code, Runnable work) {
    assertEquals(code, assertThrows(AurigaException.class, work::run).errorCode().code());
  }

  private static void await(CountDownLatch latch) {
    try {
      assertTrue(latch.await(15, TimeUnit.SECONDS));
    } catch (InterruptedException e) {
      Thread.currentThread().interrupt();
      throw new AssertionError(e);
    }
  }
}
