package auriga.org;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.events.EventPublisher;
import auriga.common.persistence.CommonMigrations;
import auriga.common.persistence.TenantTransactionManager;
import auriga.common.persistence.UuidV7;
import auriga.common.security.AurigaAuthentication;
import auriga.common.security.AurigaPrincipal;
import auriga.common.security.PrincipalType;
import auriga.common.security.TokenContext;
import auriga.testsupport.PostgresExtension;
import java.time.Clock;
import java.time.Instant;
import java.util.List;
import java.util.Set;
import java.util.UUID;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.Executors;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.security.core.context.SecurityContextHolder;
import org.springframework.transaction.TransactionDefinition;
import org.springframework.transaction.support.TransactionTemplate;
import tools.jackson.databind.node.ObjectNode;

@ExtendWith(PostgresExtension.class)
class OrgGroupHierarchyTest {
  private static final UUID TENANT = OrgIds.DEFAULT_TENANT;
  private static OrgStore store;
  private static OrgService svc;
  private static TenantTransactionManager manager;
  private static JdbcTemplate observer;
  private static final AtomicInteger eventCount = new AtomicInteger();
  private UUID owner;

  @BeforeAll
  static void migrate() {
    var roles =
        PostgresExtension.serviceRoles(OrgGroupHierarchyTest.class, "org_owner", "org_app", "org");
    CommonMigrations.migrate(roles.owner().dataSource(), "org");
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas("org")
                .locations("classpath:db/org")
                .load())
        .migrate();
    var jdbc = new JdbcTemplate(roles.runtime().dataSource());
    manager = new TenantTransactionManager(roles.runtime().dataSource());
    store = new OrgStore(jdbc, manager);
    observer =
        new JdbcTemplate(PostgresExtension.database(OrgGroupHierarchyTest.class).dataSource());
    EventPublisher events =
        (type, subject, data, version) -> {
          eventCount.incrementAndGet();
          return UuidV7.next();
        };
    svc = new OrgService(store, events, Clock.systemUTC());
  }

  @BeforeEach
  void seed() {
    owner = UuidV7.next();
    store.tenant(
        TENANT,
        () -> {
          store.jdbc().update("DELETE FROM role_binding WHERE tenant_id=?", TENANT);
          store.jdbc().update("DELETE FROM \"group\" WHERE tenant_id=?", TENANT);
          store.jdbc().update("DELETE FROM member WHERE tenant_id=?", TENANT);
          insertMember(owner);
          bindRole(owner, OrgIds.ROLE_OWNER);
          svc.getEffectivePermissions(owner); // Commit catalog registration before racing writers.
          return null;
        });
    eventCount.set(0);
  }

  @Test
  void rejectsThreeLevelCycleAndSelfParentWithoutWriting() {
    UUID a = create("a", null), b = create("b", a), c = create("c", b);
    assertError("common.conflict", () -> update(a, null, null, c));
    assertError("common.conflict", () -> update(c, null, null, c));
    assertFalse(get(a).has("parentId"));
    assertEquals(b.toString(), get(c).path("parentId").asString());
    assertEquals(0, eventCount.get());
  }

  @Test
  void concurrentOppositeReparentsCannotBothCommit() throws Exception {
    UUID a = create("a", null), b = create("b", null);
    assertEquals(
        "common.conflict", race(() -> update(a, null, null, b), () -> update(b, null, null, a)));
    assertEquals(b.toString(), get(a).path("parentId").asString());
    assertFalse(get(b).has("parentId"));
    assertEquals(0, eventCount.get());
  }

  @Test
  void concurrentCreateAndDeleteCannotLeaveDanglingParent() throws Exception {
    UUID parent = create("parent", null);
    assertEquals(
        "org.group_not_empty",
        race(
            () -> create("child", parent),
            () -> TenantContext.run(TENANT, () -> svc.deleteGroup(parent))));
    UUID deleted = create("deleted", null);
    assertEquals(
        "common.not_found",
        race(
            () -> TenantContext.run(TENANT, () -> svc.deleteGroup(deleted)),
            () -> create("orphan", deleted)));
    assertEquals(
        2, TenantContext.call(TENANT, () -> svc.listGroups(null, null).path("items").size()));
  }

  @Test
  void repeatableReadSnapshotCannotCommitOppositeReparent() throws Exception {
    UUID a = create("a", null), b = create("b", null);
    var tenantBefore = TenantContext.call(TENANT, () -> svc.tenantJson(svc.getTenant()));
    assertEquals(
        "common.conflict",
        race(() -> update(a, null, null, b), () -> update(b, null, null, a), true));
    assertEquals(b.toString(), get(a).path("parentId").asString());
    assertFalse(get(b).has("parentId"));
    assertEquals(tenantBefore, TenantContext.call(TENANT, () -> svc.tenantJson(svc.getTenant())));
    assertEquals(0, eventCount.get());
  }

  private String race(Runnable firstMutation, Runnable secondMutation) throws Exception {
    return race(firstMutation, secondMutation, false);
  }

  private String race(Runnable firstMutation, Runnable secondMutation, boolean repeatableRead)
      throws Exception {
    var firstWritten = new CountDownLatch(1);
    var releaseFirst = new CountDownLatch(1);
    var secondStarted = new CountDownLatch(1);
    var secondPid = new AtomicInteger();
    var secondTransaction = new TransactionTemplate(manager);
    if (repeatableRead) {
      secondTransaction.setIsolationLevel(TransactionDefinition.ISOLATION_REPEATABLE_READ);
    }
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
                    return TenantContext.call(
                        TENANT,
                        () ->
                            secondTransaction.execute(
                                status -> {
                                  store.jdbc().execute("SET LOCAL statement_timeout='10s'");
                                  secondPid.set(
                                      store
                                          .jdbc()
                                          .queryForObject(
                                              "SELECT pg_backend_pid()", Integer.class));
                                  secondStarted.countDown();
                                  secondMutation.run();
                                  return "committed";
                                }));
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
              System.nanoTime() < deadline, "second writer neither finished nor waited for lock");
        }
        releaseFirst.countDown();
        assertEquals("committed", first.get(10, TimeUnit.SECONDS));
        return second.get(10, TimeUnit.SECONDS);
      } finally {
        releaseFirst.countDown();
      }
    }
  }

  @Test
  void missingAndOtherTenantParentsAreIndistinguishableForCreateAndUpdate() {
    UUID child = create("child", null);
    UUID foreignParent = UuidV7.next(), otherTenant = UuidV7.next();
    store.tenant(
        otherTenant,
        () -> {
          store
              .jdbc()
              .update(
                  "INSERT INTO \"group\" (id, tenant_id, name) VALUES (?,?,?)",
                  foreignParent,
                  otherTenant,
                  "foreign");
          return null;
        });
    for (UUID parent : List.of(UuidV7.next(), foreignParent)) {
      assertError("common.not_found", () -> create("missing", parent));
      assertError("common.not_found", () -> update(child, "changed", null, parent));
    }
    assertEquals("child", get(child).path("name").asString());
    assertFalse(get(child).has("parentId"));
    assertEquals(
        1, TenantContext.call(TENANT, () -> svc.listGroups(null, null).path("items").size()));
  }

  @Test
  void existingCycleAndDanglingAncestorFailWithinDatabaseTimeout() {
    UUID a = create("a", null), b = create("b", a), healthy = create("healthy", null);
    store.tenant(
        TENANT,
        () -> {
          rawParent(a, b);
          return null;
        });
    for (Runnable work :
        List.<Runnable>of(
            () -> create("under-cycle", a),
            () -> update(healthy, null, null, a),
            () -> update(a, "rename-cycle", null, null),
            () -> store.groupEffectivePermissions(a))) {
      assertError(
          "common.conflict",
          () ->
              store.tenant(
                  TENANT,
                  () -> {
                    store.jdbc().execute("SET LOCAL statement_timeout='2s'");
                    work.run();
                    return null;
                  }));
    }
    store.tenant(
        TENANT,
        () -> {
          rawParent(a, UuidV7.next());
          return null;
        });
    assertError("common.not_found", () -> create("under-dangling", b));
    assertError("common.not_found", () -> update(healthy, null, null, b));
  }

  @Test
  void legalReparentAndMetadataUpdatesKeepNullAsOmitted() {
    UUID a = create("a", null), b = create("b", a), c = create("c", null);
    var moved = update(b, "renamed", "description", c);
    assertEquals(c.toString(), moved.path("parentId").asString());
    var metadata = update(b, "renamed-again", null, null);
    assertEquals(c.toString(), metadata.path("parentId").asString());
    assertEquals("description", metadata.path("description").asString());
    assertFalse(update(a, "root", "root description", null).has("parentId"));
    assertEquals(0, eventCount.get());
  }

  @Test
  void reparentStillEnforcesGrantorCeilingForParentAndCurrentGroup() {
    UUID admin = UuidV7.next();
    UUID privileged = create("privileged", null), ordinary = create("ordinary", null);
    store.tenant(
        TENANT,
        () -> {
          insertMember(admin);
          bindRole(admin, OrgIds.ROLE_ADMIN);
          store
              .jdbc()
              .update(
                  "INSERT INTO role_binding (id, tenant_id, role_id, subject_type, subject_id, granted_by)"
                      + " VALUES (?,?,?,'group',?,?)",
                  UuidV7.next(),
                  TENANT,
                  OrgIds.ROLE_OWNER,
                  privileged,
                  owner);
          return null;
        });
    assertError(
        "org.grant_exceeds_own",
        () -> asActor(admin, () -> svc.createGroup("denied", null, privileged)));
    assertError(
        "org.grant_exceeds_own",
        () -> asActor(admin, () -> svc.updateGroup(ordinary, null, null, privileged)));
    assertError(
        "org.grant_exceeds_own",
        () -> asActor(admin, () -> svc.updateGroup(privileged, null, null, ordinary)));
  }

  private UUID create(String name, UUID parent) {
    return UUID.fromString(
        asActor(owner, () -> svc.createGroup(name, null, parent)).path("id").asString());
  }

  private ObjectNode update(UUID id, String name, String description, UUID parent) {
    return asActor(owner, () -> svc.updateGroup(id, name, description, parent));
  }

  private ObjectNode get(UUID id) {
    return TenantContext.call(TENANT, () -> svc.getGroup(id));
  }

  private static <T> T asActor(UUID actor, java.util.function.Supplier<T> work) {
    var previous = SecurityContextHolder.getContext();
    var context = SecurityContextHolder.createEmptyContext();
    context.setAuthentication(
        new AurigaAuthentication(
            new AurigaPrincipal(
                actor.toString(),
                TENANT,
                PrincipalType.USER,
                TokenContext.ADMIN,
                null,
                null,
                "member",
                List.of(),
                Set.of(),
                Instant.now(),
                Instant.now(),
                UuidV7.next().toString())));
    SecurityContextHolder.setContext(context);
    try {
      return TenantContext.call(TENANT, work::get);
    } finally {
      SecurityContextHolder.setContext(previous);
    }
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

  private static void rawParent(UUID group, UUID parent) {
    store
        .jdbc()
        .update(
            "UPDATE \"group\" SET parent_id=? WHERE tenant_id=? AND id=?", parent, TENANT, group);
  }

  private static void insertMember(UUID id) {
    store.insertMember(
        new OrgStore.MemberRow(id, "user", null, "member", "active", 1, Instant.now(), null, null));
  }

  private static void bindRole(UUID member, UUID role) {
    store
        .jdbc()
        .update(
            "INSERT INTO role_binding (id, tenant_id, role_id, subject_type, subject_id, granted_by)"
                + " VALUES (?,?,?,'member',?,?)",
            UuidV7.next(),
            TENANT,
            role,
            member,
            member);
  }
}
