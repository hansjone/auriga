package auriga.org;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.persistence.CommonMigrations;
import auriga.common.persistence.TenantTransactionManager;
import auriga.common.persistence.UuidV7;
import auriga.contracts.org.v1.GetEffectiveQuotaRequest;
import auriga.contracts.org.v1.GetEffectiveQuotaResponse;
import auriga.testsupport.PostgresExtension;
import io.grpc.stub.StreamObserver;
import java.math.BigInteger;
import java.time.Clock;
import java.time.Instant;
import java.util.ArrayList;
import java.util.List;
import java.util.UUID;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.Executors;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.function.Supplier;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

@ExtendWith(PostgresExtension.class)
class OrgQuotaIntegrityTest {
  private static final UUID TENANT = OrgIds.DEFAULT_TENANT;
  private static OrgStore store;
  private static OrgService svc;
  private static JdbcTemplate observer;
  private UUID group;
  private UUID member;

  @BeforeAll
  static void migrate() {
    var roles =
        PostgresExtension.serviceRoles(OrgQuotaIntegrityTest.class, "org_owner", "org_app", "org");
    CommonMigrations.migrate(roles.owner().dataSource(), "org");
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas("org")
                .locations("classpath:db/org")
                .load())
        .migrate();
    store =
        new OrgStore(
            new JdbcTemplate(roles.runtime().dataSource()),
            new TenantTransactionManager(roles.runtime().dataSource()));
    observer =
        new JdbcTemplate(PostgresExtension.database(OrgQuotaIntegrityTest.class).dataSource());
    svc = new OrgService(store, (type, subject, data, version) -> UuidV7.next(), Clock.systemUTC());
  }

  @BeforeEach
  void clean() {
    store.tenant(
        TENANT,
        () -> {
          store.jdbc().update("DELETE FROM quota WHERE tenant_id=?", TENANT);
          store.jdbc().update("DELETE FROM group_member WHERE tenant_id=?", TENANT);
          store.jdbc().update("DELETE FROM \"group\" WHERE tenant_id=?", TENANT);
          store.jdbc().update("DELETE FROM member WHERE tenant_id=?", TENANT);
          group = insertGroup();
          member = UuidV7.next();
          insertMember(member);
          return null;
        });
  }

  @Test
  void missingTargetsCannotCreateQuota() {
    for (String scope : List.of("group", "member")) {
      UUID missing = UuidV7.next();
      assertError("common.not_found", () -> get(scope, missing));
      assertError("common.not_found", () -> put(scope, missing, "{}"));
      assertError("common.not_found", () -> delete(scope, missing));
      assertEquals(0, count(scope, missing));
    }
  }

  @Test
  void unsetCoverageKeepsLegacyCodesDistinctFromMissingTargets() {
    for (var target : List.of(new Target("group", group), new Target("member", member))) {
      assertError("org.not_found", () -> get(target.scope(), target.id()));
      assertError("org.not_found", () -> delete(target.scope(), target.id()));
      UUID missing = UuidV7.next();
      assertError("common.not_found", () -> get(target.scope(), missing));
      assertError("common.not_found", () -> delete(target.scope(), missing));
    }
    assertError("org.conflict", () -> delete("default", null));
    assertError(
        "org.not_found",
        () -> TenantContext.call(TENANT, () -> svc.getEffectiveQuota(UuidV7.next())));
  }

  @Test
  void missingAndForeignTargetsCannotReadWriteOrDeleteStaleOverrides() {
    UUID other = UuidV7.next(), foreignGroup = UuidV7.next(), foreignMember = UuidV7.next();
    store.tenant(
        other,
        () -> {
          store
              .jdbc()
              .update(
                  "INSERT INTO \"group\" (id,tenant_id,name) VALUES (?,?,?)",
                  foreignGroup,
                  other,
                  "foreign");
          insertMember(foreignMember);
          return null;
        });
    for (var target :
        List.of(
            new Target("group", UuidV7.next()),
            new Target("member", UuidV7.next()),
            new Target("group", foreignGroup),
            new Target("member", foreignMember))) {
      store.tenant(
          TENANT,
          () -> {
            rawQuota(target.scope(), target.id(), "{}");
            return null;
          });
      assertError("common.not_found", () -> get(target.scope(), target.id()));
      assertError(
          "common.not_found",
          () -> put(target.scope(), target.id(), "{\"instance\":{\"maxInstances\":0}}"));
      assertError("common.not_found", () -> delete(target.scope(), target.id()));
      assertEquals(1, count(target.scope(), target.id()));
    }
    assertEquals(0, store.jdbc().queryForObject("SELECT count(*) FROM quota", Integer.class));
  }

  @Test
  void knownSchemaInvalidQuotaIsRejectedWithoutChangingPreviousLayer() {
    var before =
        put("default", null, "{\"instance\":{\"maxInstances\":0,\"alwaysOnAllowed\":false}}");
    List<String> invalid =
        new ArrayList<>(
            List.of(
                "{\"instance\":false}",
                "null",
                "[]",
                "5",
                "\"value\"",
                "{\"instance\":null}",
                "{\"instance\":[]}",
                "{\"model\":false}"));
    for (var field : List.of("cpuMillis", "memoryMb", "diskGb")) {
      for (var value : List.of("null", "0", "-1", "1.5", "\"1000\"", "true"))
        invalid.add("{\"instance\":{\"" + field + "\":" + value + "}}");
    }
    invalid.add("{\"instance\":{\"cpuMillis\":99}}");
    invalid.add("{\"instance\":{\"memoryMb\":255}}");
    for (var value : List.of("-1", "0.5", "\"0\"", "false"))
      invalid.add("{\"instance\":{\"maxInstances\":" + value + "}}");
    for (var value : List.of("null", "0", "\"false\""))
      invalid.add("{\"instance\":{\"alwaysOnAllowed\":" + value + "}}");
    for (var value : List.of("null", "0", "{}", "[]", "{\"removedLimit\":0}"))
      invalid.add("{\"model\":" + value + "}");
    for (String value : invalid) {
      assertError("common.validation_failed", () -> put("default", null, value));
      assertEquals(before, get("default", null), value);
    }
    assertError(
        "common.validation_failed",
        () -> TenantContext.call(TENANT, () -> svc.putQuota("default", null, null)));
  }

  @Test
  void legalLayersRoundTripReplaceAndDeleteWithoutLosingUnknownProperties() {
    assertEquals(OrgJson.object(), get("default", null));
    assertError("org.conflict", () -> delete("default", null));
    String json =
        """
        {"instance":{"cpuMillis":100,"memoryMb":256,"diskGb":1,"maxInstances":null,
          "alwaysOnAllowed":false,"futureInstance":{"mode":[null,0,false]}},
         "futureRoot":[null,0,false,{"nested":1}]}
        """;
    for (var target :
        List.of(
            new Target("default", null),
            new Target("group", group),
            new Target("member", member))) {
      if (!target.scope().equals("default"))
        assertError("org.not_found", () -> get(target.scope(), target.id()));
      assertEquals(tree(json), put(target.scope(), target.id(), json));
      assertEquals(tree(json), get(target.scope(), target.id()));
      assertEquals(OrgJson.object(), put(target.scope(), target.id(), "{}"));
      assertEquals(OrgJson.object(), get(target.scope(), target.id()));
      if (!target.scope().equals("default")) {
        delete(target.scope(), target.id());
        assertError("org.not_found", () -> get(target.scope(), target.id()));
        assertError("org.not_found", () -> delete(target.scope(), target.id()));
      }
    }
  }

  @Test
  void concurrentFirstWritesAreCompleteLayerLastWriterWins() throws Exception {
    for (var target :
        List.of(
            new Target("default", null),
            new Target("group", group),
            new Target("member", member))) {
      assertEquals(
          "committed",
          race(
              () -> put(target.scope(), target.id(), "{\"instance\":{\"maxInstances\":1}}"),
              () -> put(target.scope(), target.id(), "{\"instance\":{\"maxInstances\":0}}")));
      assertEquals(1, count(target.scope(), target.id()));
      assertEquals(tree("{\"instance\":{\"maxInstances\":0}}"), get(target.scope(), target.id()));
    }
  }

  @Test
  void deleteThenPutCannotLeaveOrphan() throws Exception {
    assertEquals(
        "common.not_found",
        race(
            () -> TenantContext.run(TENANT, () -> svc.deleteGroup(group)),
            () -> put("group", group, "{}")));
    assertEquals(0, count("group", group));
  }

  @Test
  void putThenDeleteRemovesCommittedOverride() throws Exception {
    assertEquals(
        "committed",
        race(
            () -> put("group", group, "{}"),
            () -> TenantContext.run(TENANT, () -> svc.deleteGroup(group))));
    assertEquals(0, count("group", group));
    assertError("common.not_found", () -> get("group", group));
  }

  @Test
  void callerRollbackLeavesNoQuota() {
    assertThrows(
        IllegalStateException.class,
        () ->
            store.tenant(
                TENANT,
                () -> {
                  put("group", group, "{}");
                  throw new IllegalStateException("rollback");
                }));
    assertEquals(0, count("group", group));
  }

  @Test
  void effectiveQuotaPreservesMemberGroupDefaultPriorityAndNullZeroFalse() {
    assertEquals(OrgJson.object(), TenantContext.call(TENANT, () -> svc.getEffectiveQuota(member)));
    UUID second = store.tenant(TENANT, OrgQuotaIntegrityTest::insertGroup);
    addMembership(group);
    addMembership(second);
    put(
        "default",
        null,
        "{\"instance\":{\"cpuMillis\":100,\"memoryMb\":256,\"diskGb\":1,\"maxInstances\":10,\"alwaysOnAllowed\":true}}");
    put(
        "group",
        group,
        "{\"instance\":{\"cpuMillis\":200,\"maxInstances\":0,\"alwaysOnAllowed\":false}}");
    put(
        "group",
        second,
        "{\"instance\":{\"cpuMillis\":300,\"maxInstances\":null,\"alwaysOnAllowed\":true}}");
    put("member", member, "{\"instance\":{\"maxInstances\":0,\"alwaysOnAllowed\":false}}");
    var effective = TenantContext.call(TENANT, () -> svc.getEffectiveQuota(member));
    assertEquals(
        tree(
            "{\"instance\":{\"cpuMillis\":300,\"memoryMb\":256,\"diskGb\":1,\"maxInstances\":0,\"alwaysOnAllowed\":false}}"),
        effective);
    UUID ungrouped = UuidV7.next();
    store.tenant(
        TENANT,
        () -> {
          insertMember(ungrouped);
          return null;
        });
    assertEquals(
        get("default", null), TenantContext.call(TENANT, () -> svc.getEffectiveQuota(ungrouped)));
  }

  @Test
  void httpIntegersBeyondDoubleAndInt64RemainExactThroughStorageAndGroupMerge() {
    UUID second = store.tenant(TENANT, OrgQuotaIntegrityTest::insertGroup);
    addMembership(group);
    addMembership(second);
    String lower = "9223372036854775808", upper = "9223372036854775809";
    String json =
        "{\"instance\":{\"cpuMillis\":9007199254740993,\"maxInstances\":"
            + lower
            + ",\"diskGb\":"
            + lower
            + "}}";
    assertEquals(tree(json), put("group", group, json));
    assertEquals(
        new BigInteger("9007199254740993"),
        get("group", group).path("instance").path("cpuMillis").bigIntegerValue());
    put(
        "group",
        second,
        "{\"instance\":{\"maxInstances\":" + upper + ",\"diskGb\":" + upper + "}}");
    var effective = TenantContext.call(TENANT, () -> svc.getEffectiveQuota(member));
    assertEquals(
        new BigInteger(upper), effective.path("instance").path("maxInstances").bigIntegerValue());
    assertEquals(
        new BigInteger(upper), effective.path("instance").path("diskGb").bigIntegerValue());
    assertEquals(
        new BigInteger("9007199254740993"),
        effective.path("instance").path("cpuMillis").bigIntegerValue());
  }

  @Test
  @SuppressWarnings("deprecation")
  void deprecatedModelContractDoesNotRestoreModelSettingsOrGrpcValues() {
    put("member", member, "{\"instance\":{\"maxInstances\":2}}");
    assertError(
        "common.validation_failed", () -> put("member", member, "{\"model\":{\"dailyTokens\":0}}"));
    var output = new QuotaObserver();
    new OrgGrpcServices.QuotaGrpc(svc)
        .getEffectiveQuota(
            GetEffectiveQuotaRequest.newBuilder()
                .setTenantId(TENANT.toString())
                .setAccountId(member.toString())
                .build(),
            output);
    assertTrue(output.completed);
    assertFalse(output.responses.getFirst().getQuota().hasModel());
    assertEquals(2, output.responses.getFirst().getQuota().getInstance().getMaxInstances());
  }

  @Test
  void grpcInt64BoundaryIsExactAndOverflowFailsWithoutPublishingResponse() {
    var service = new OrgGrpcServices.QuotaGrpc(svc);
    var request =
        GetEffectiveQuotaRequest.newBuilder()
            .setTenantId(TENANT.toString())
            .setAccountId(member.toString())
            .build();
    put(
        "member",
        member,
        "{\"instance\":{\"cpuMillis\":9223372036854775807,\"maxInstances\":0,\"memoryMb\":9223372036854775807}}");
    var output = new QuotaObserver();
    service.getEffectiveQuota(request, output);
    assertTrue(output.completed);
    assertEquals(
        Long.MAX_VALUE, output.responses.getFirst().getQuota().getInstance().getCpuMillis());
    assertEquals(
        Long.MAX_VALUE, output.responses.getFirst().getQuota().getInstance().getMemoryMb());
    assertTrue(output.responses.getFirst().getQuota().getInstance().hasMaxInstances());
    assertEquals(0, output.responses.getFirst().getQuota().getInstance().getMaxInstances());
    for (String field : List.of("cpuMillis", "memoryMb", "diskGb", "maxInstances")) {
      put("member", member, "{\"instance\":{\"" + field + "\":9223372036854775808}}");
      var rejected = new QuotaObserver();
      assertError("common.validation_failed", () -> service.getEffectiveQuota(request, rejected));
      assertTrue(rejected.responses.isEmpty());
      assertFalse(rejected.completed);
    }
  }

  @Test
  void unknownGroupFieldsWithDifferentTypesRemainValuesWithoutCoercion() {
    UUID second = store.tenant(TENANT, OrgQuotaIntegrityTest::insertGroup);
    addMembership(group);
    addMembership(second);
    String opaque = "{\"instance\":{\"futureFlag\":{\"mode\":\"opaque\"}}}";
    String flag = "{\"instance\":{\"futureFlag\":true}}";
    for (boolean reverse : List.of(false, true)) {
      String first = reverse ? flag : opaque;
      String last = reverse ? opaque : flag;
      assertEquals(tree(first), put("group", group, first));
      assertEquals(tree(last), put("group", second, last));
      assertEquals(tree(first), get("group", group));
      assertEquals(tree(last), get("group", second));
      var value =
          TenantContext.call(TENANT, () -> svc.getEffectiveQuota(member))
              .path("instance")
              .path("futureFlag");
      assertTrue(value.isObject() || value.isBoolean());
      if (value.isObject()) assertEquals(tree("{\"mode\":\"opaque\"}"), value);
      else assertTrue(value.booleanValue());
    }
  }

  @Test
  void oldMalformedQuotaFailsSafelyOnDirectAndEffectiveReads() {
    for (String invalid :
        List.of(
            "[]",
            "null",
            "{\"instance\":5}",
            "{\"model\":null}",
            "{\"instance\":{\"cpuMillis\":1.5}}")) {
      store.tenant(
          TENANT,
          () -> {
            store.jdbc().update("DELETE FROM quota WHERE tenant_id=?", TENANT);
            rawQuota("default", null, invalid);
            return null;
          });
      assertError("common.validation_failed", () -> get("default", null));
      assertError(
          "common.validation_failed",
          () -> TenantContext.call(TENANT, () -> svc.getEffectiveQuota(member)));
    }
  }

  private String race(Runnable firstMutation, Runnable secondMutation) throws Exception {
    var firstWritten = new CountDownLatch(1);
    var releaseFirst = new CountDownLatch(1);
    var secondStarted = new CountDownLatch(1);
    var secondPid = new AtomicInteger();
    try (var executor = Executors.newFixedThreadPool(2)) {
      var first =
          executor.submit(
              () ->
                  store.tenant(
                      TENANT,
                      () -> {
                        store.jdbc().execute("SET LOCAL statement_timeout='10s'");
                        firstMutation.run();
                        firstWritten.countDown();
                        await(releaseFirst);
                        return "committed";
                      }));
      try {
        assertTrue(firstWritten.await(10, TimeUnit.SECONDS));
        var second =
            executor.submit(
                () -> {
                  try {
                    return store.tenant(
                        TENANT,
                        () -> {
                          store.jdbc().execute("SET LOCAL statement_timeout='10s'");
                          secondPid.set(
                              store
                                  .jdbc()
                                  .queryForObject("SELECT pg_backend_pid()", Integer.class));
                          secondStarted.countDown();
                          secondMutation.run();
                          return "committed";
                        });
                  } catch (AurigaException e) {
                    return e.errorCode().code();
                  }
                });
        assertTrue(secondStarted.await(10, TimeUnit.SECONDS));
        long deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(10);
        while (!second.isDone()
            && !Boolean.TRUE.equals(
                observer.queryForObject(
                    "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE pid=? AND wait_event_type='Lock')",
                    Boolean.class,
                    secondPid.get()))) {
          assertTrue(
              System.nanoTime() < deadline,
              "writer neither completed nor waited for a database lock");
        }
        releaseFirst.countDown();
        assertEquals("committed", first.get(10, TimeUnit.SECONDS));
        return second.get(10, TimeUnit.SECONDS);
      } finally {
        releaseFirst.countDown();
      }
    }
  }

  private ObjectNode put(String scope, UUID id, String json) {
    return tenant(() -> svc.putQuota(scope, id, tree(json)));
  }

  private ObjectNode get(String scope, UUID id) {
    return tenant(() -> svc.getQuota(scope, id));
  }

  private void delete(String scope, UUID id) {
    TenantContext.run(TENANT, () -> svc.deleteQuota(scope, id));
  }

  private static <T> T tenant(Supplier<T> work) {
    return TenantContext.call(TENANT, work::get);
  }

  private static JsonNode tree(String json) {
    return OrgJson.MAPPER.readTree(json);
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

  private int count(String scope, UUID id) {
    return store.tenant(
        TENANT,
        () ->
            store
                .jdbc()
                .queryForObject(
                    "SELECT count(*) FROM quota WHERE tenant_id=? AND scope=? AND scope_id IS NOT DISTINCT FROM ?::uuid",
                    Integer.class,
                    TENANT,
                    scope,
                    id));
  }

  private static UUID insertGroup() {
    UUID id = UuidV7.next();
    store
        .jdbc()
        .update(
            "INSERT INTO \"group\" (id,tenant_id,name) VALUES (?,?,?)",
            id,
            store.tenantId(),
            id.toString());
    return id;
  }

  private static void insertMember(UUID id) {
    store.insertMember(
        new OrgStore.MemberRow(
            id, "member", null, "member", "active", 1, Instant.now(), null, null));
  }

  private static void rawQuota(String scope, UUID id, String json) {
    store
        .jdbc()
        .update(
            "INSERT INTO quota (id,tenant_id,scope,scope_id,value) VALUES (?,?,?,?,?::jsonb)",
            UuidV7.next(),
            TENANT,
            scope,
            id,
            json);
  }

  private void addMembership(UUID id) {
    store.tenant(
        TENANT,
        () -> {
          store
              .jdbc()
              .update(
                  "INSERT INTO group_member (id,tenant_id,group_id,account_id) VALUES (?,?,?,?)",
                  UuidV7.next(),
                  TENANT,
                  id,
                  member);
          return null;
        });
  }

  private record Target(String scope, UUID id) {}

  private static class QuotaObserver implements StreamObserver<GetEffectiveQuotaResponse> {
    final List<GetEffectiveQuotaResponse> responses = new ArrayList<>();
    boolean completed;

    @Override
    public void onNext(GetEffectiveQuotaResponse response) {
      responses.add(response);
    }

    @Override
    public void onError(Throwable t) {
      fail(t);
    }

    @Override
    public void onCompleted() {
      completed = true;
    }
  }
}
