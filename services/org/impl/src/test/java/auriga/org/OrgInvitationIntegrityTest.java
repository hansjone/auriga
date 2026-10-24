package auriga.org;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.context.RequestContext;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.events.EventSchemas;
import auriga.common.events.OutboxEventPublisher;
import auriga.common.persistence.CommonMigrations;
import auriga.common.persistence.TenantTransactionManager;
import auriga.common.persistence.UuidV7;
import auriga.common.security.AurigaPrincipal;
import auriga.common.security.PrincipalType;
import auriga.common.security.TokenContext;
import auriga.contracts.deploymentconfig.DeploymentAccess;
import auriga.testsupport.PostgresExtension;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Clock;
import java.time.Instant;
import java.util.List;
import java.util.Set;
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
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.CsvSource;
import org.junit.jupiter.params.provider.ValueSource;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.transaction.TransactionDefinition;
import org.springframework.transaction.support.TransactionTemplate;
import tools.jackson.databind.node.ObjectNode;

@ExtendWith(PostgresExtension.class)
class OrgInvitationIntegrityTest {
  private static final UUID TENANT = OrgIds.DEFAULT_TENANT;
  private static final UUID FOREIGN = UuidV7.next();
  private static OrgStore store;
  private static OrgService svc;
  private static JdbcTemplate observer;
  private static TenantTransactionManager manager;
  private UUID actor;
  private UUID role;
  private UUID group;

  @BeforeAll
  static void migrate() throws Exception {
    var roles =
        PostgresExtension.serviceRoles(
            OrgInvitationIntegrityTest.class, "org_owner", "org_app", "org");
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
        new JdbcTemplate(PostgresExtension.database(OrgInvitationIntegrityTest.class).dataSource());
    var data =
        (ObjectNode)
            OrgJson.MAPPER.readTree(
                Files.readString(
                    Path.of("../../../contracts/deployment/fixtures/domain-proxy.json")));
    data.put("tenantId", TENANT.toString());
    var events =
        new OutboxEventPublisher(
            store.jdbc(),
            "org",
            "org",
            new EventSchemas(),
            OrgJson.MAPPER,
            Clock.systemUTC(),
            null);
    svc =
        new OrgService(
            store,
            events,
            Clock.systemUTC(),
            DeploymentAccess.load(OrgJson.MAPPER.writeValueAsBytes(data)));
    store.tenant(
        FOREIGN,
        () -> {
          store
              .jdbc()
              .update(
                  "INSERT INTO tenant (id,tenant_id,slug,display_name) VALUES (?,?,'foreign','foreign')",
                  FOREIGN,
                  FOREIGN);
          return null;
        });
  }

  @BeforeEach
  void clean() {
    for (UUID tenant : List.of(TENANT, FOREIGN))
      store.tenant(
          tenant,
          () -> {
            for (String table :
                List.of(
                    "outbox_event",
                    "invitation_redemption",
                    "invitation",
                    "role_binding",
                    "group_member",
                    "\"group\"",
                    "member"))
              store.jdbc().update("DELETE FROM " + table + " WHERE tenant_id=?", tenant);
            store.jdbc().update("DELETE FROM role WHERE tenant_id=? AND builtin=false", tenant);
            return null;
          });
    actor = UuidV7.next();
    store.tenant(
        TENANT,
        () -> {
          store.insertMember(
              new OrgStore.MemberRow(
                  actor,
                  "owner",
                  "owner@example.com",
                  "member",
                  "active",
                  1,
                  Instant.now(),
                  null,
                  null));
          store
              .jdbc()
              .update(
                  "INSERT INTO role_binding (id,tenant_id,role_id,subject_type,subject_id) VALUES (?,?,?,'member',?)",
                  UuidV7.next(),
                  TENANT,
                  OrgIds.ROLE_OWNER,
                  actor);
          role = insertTarget("role");
          group = insertTarget("group");
          return null;
        });
  }

  @ParameterizedTest
  @CsvSource({"role,false", "group,false", "role,true", "group,true"})
  void creationRejectsMissingAndForeignTargets(String kind, boolean foreign) {
    UUID id = foreign ? store.tenant(FOREIGN, () -> insertTarget(kind)) : UuidV7.next();
    assertError("org.not_found", () -> create(kind, id));
    assertEquals(0, count("invitation", "true"));
    assertEquals(0, count("outbox_event", "true"));
  }

  @Test
  void runtimeRoleReallyEnforcesRls() {
    UUID foreignRole = store.tenant(FOREIGN, () -> insertTarget("role"));
    UUID foreignGroup = store.tenant(FOREIGN, () -> insertTarget("group"));
    store.tenant(
        TENANT,
        () -> {
          assertEquals("org_app", store.jdbc().queryForObject("SELECT current_user", String.class));
          assertFalse(
              store
                  .jdbc()
                  .queryForObject(
                      "SELECT rolsuper OR rolbypassrls FROM pg_roles WHERE rolname=current_user",
                      Boolean.class));
          assertFalse(
              store
                  .jdbc()
                  .queryForObject(
                      "SELECT tableowner=current_user FROM pg_tables WHERE schemaname='org' AND tablename='invitation'",
                      Boolean.class));
          assertEquals(
              0,
              store
                  .jdbc()
                  .queryForObject(
                      "SELECT count(*) FROM role WHERE id=?", Integer.class, foreignRole));
          assertEquals(
              0,
              store
                  .jdbc()
                  .queryForObject(
                      "SELECT count(*) FROM \"group\" WHERE id=?", Integer.class, foreignGroup));
          return null;
        });
  }

  @ParameterizedTest
  @ValueSource(strings = {"role", "group"})
  void everyCreationReferenceMustExistEvenAfterAValidReference(String kind) {
    UUID missing = UuidV7.next();
    assertError(
        "org.not_found",
        () ->
            createBoth(
                kind.equals("role") ? List.of(role, missing) : List.of(role),
                kind.equals("group") ? List.of(group, missing) : List.of(group),
                null));
    assertEquals(0, count("invitation", "true"));
  }

  @ParameterizedTest
  @ValueSource(strings = {"role", "group"})
  void legacyForeignReferencesCannotBeRedeemed(String kind) {
    var invitation = create(kind, target(kind));
    UUID foreignTarget = store.tenant(FOREIGN, () -> insertTarget(kind));
    store.tenant(
        TENANT,
        () -> {
          OrgArrays.update(
              store.jdbc(),
              "UPDATE invitation SET "
                  + (kind.equals("role") ? "role_ids" : "group_ids")
                  + "=? WHERE id=?",
              new UUID[] {foreignTarget},
              UUID.fromString(invitation.path("id").asString()));
          return null;
        });
    UUID account = UuidV7.next();
    assertError("org.not_found", () -> redeem(invitation, account));
    assertNoRedemption(invitation, account);
    assertNoOrphans();
  }

  @ParameterizedTest
  @ValueSource(strings = {"role", "group"})
  void invalidTargetCannotChangeExistingMemberOrConsumeAUse(String kind) {
    var invitation = create(kind, target(kind));
    delete(kind);
    var before = TenantContext.call(TENANT, () -> svc.memberJson(actor));
    assertError("org.not_found", () -> redeem(invitation, actor));
    assertEquals(before, TenantContext.call(TENANT, () -> svc.memberJson(actor)));
    assertEquals(0, count("invitation_redemption", "true"));
    assertEquals(0, count("invitation", "uses<>0"));
    assertEquals(0, count("outbox_event", "true"));
    assertNoOrphans();
  }

  @ParameterizedTest
  @ValueSource(strings = {"role", "group"})
  void targetDeletedAfterCreationRejectsWithoutAnyRedemptionWrites(String kind) {
    var invitation = create(kind, target(kind));
    delete(kind);
    UUID account = UuidV7.next();
    assertError("org.not_found", () -> redeem(invitation, account));
    assertNoRedemption(invitation, account);
  }

  @ParameterizedTest
  @ValueSource(strings = {"role", "group"})
  void deletionThenRedemptionSerializesAndRejects(String kind) throws Exception {
    var invitation = create(kind, target(kind));
    UUID account = UuidV7.next();
    assertEquals("org.not_found", race(() -> delete(kind), () -> redeem(invitation, account)));
    assertNoRedemption(invitation, account);
    assertNoOrphans();
  }

  @ParameterizedTest
  @ValueSource(strings = {"role", "group"})
  void redemptionThenDeletionSerializesAndRemovesCommittedReferences(String kind) throws Exception {
    var invitation = create(kind, target(kind));
    UUID account = UuidV7.next();
    assertEquals("committed", race(() -> redeem(invitation, account), () -> delete(kind)));
    assertEquals(1, count("invitation_redemption", "account_id='" + account + "'"));
    assertEquals(
        1, count("outbox_event", "event_type='user.created' AND subject_key='" + account + "'"));
    assertNoOrphans();
  }

  @ParameterizedTest
  @CsvSource({
    "role,false,false",
    "group,false,false",
    "role,true,false",
    "group,true,false",
    "role,false,true",
    "group,false,true",
    "role,true,true",
    "group,true,true"
  })
  void outerSnapshotDeletionCannotAdmitStaleReferences(
      String kind, boolean repeatable, boolean creation) throws Exception {
    var invitation = creation ? null : create(kind, target(kind));
    UUID account = UuidV7.next();
    var snapshot = new CountDownLatch(1);
    var proceed = new CountDownLatch(1);
    try (var executor = Executors.newSingleThreadExecutor()) {
      var future =
          executor.submit(
              () ->
                  outcome(
                      () ->
                          TenantContext.call(
                              TENANT,
                              () -> {
                                var tx = new TransactionTemplate(manager);
                                tx.setIsolationLevel(
                                    repeatable
                                        ? TransactionDefinition.ISOLATION_REPEATABLE_READ
                                        : TransactionDefinition.ISOLATION_READ_COMMITTED);
                                return tx.execute(
                                    status -> {
                                      store.jdbc().execute("SET LOCAL statement_timeout='10s'");
                                      store
                                          .jdbc()
                                          .queryForObject(
                                              "SELECT count(*) FROM role", Integer.class);
                                      snapshot.countDown();
                                      await(proceed);
                                      if (creation) create(kind, target(kind));
                                      else redeem(invitation, account);
                                      return null;
                                    });
                              })));
      try {
        assertTrue(snapshot.await(10, TimeUnit.SECONDS));
        delete(kind);
        proceed.countDown();
        assertEquals(
            repeatable ? "common.conflict" : "org.not_found", future.get(15, TimeUnit.SECONDS));
      } finally {
        proceed.countDown();
      }
    }
    if (creation) assertEquals(0, count("invitation", "true"));
    else assertNoRedemption(invitation, account);
    assertNoOrphans();
  }

  @Test
  void legalTargetsCommitRealOutboxAndRetryAfterDeletionRemainsIdempotent() {
    var invitation = createBoth(List.of(role), List.of(group), null);
    String code = invitation.path("code").asString();
    assertEquals("https://portal.example.com/invite/" + code, invitation.path("url").asString());
    assertEquals(
        OrgService.hashCode(code),
        store.tenant(
            TENANT,
            () ->
                store
                    .jdbc()
                    .queryForObject(
                        "SELECT code_hash FROM invitation WHERE id=?",
                        String.class,
                        UUID.fromString(invitation.path("id").asString()))));
    UUID account = UuidV7.next();
    var first = redeem(invitation, account);
    assertEquals(1, first.path("groupIds").size());
    assertEquals(
        1, count("outbox_event", "event_type='user.created' AND subject_key='" + account + "'"));
    assertEquals(
        1, count("role_binding", "role_id='" + role + "' AND subject_id='" + account + "'"));
    delete("role");
    delete("group");
    var retry = redeem(invitation, account);
    assertEquals(account.toString(), retry.path("id").asString());
    assertEquals(1, count("invitation_redemption", "account_id='" + account + "'"));
    assertEquals(
        1, count("outbox_event", "event_type='user.created' AND subject_key='" + account + "'"));
    assertError("org.conflict", () -> redeem(invitation, UuidV7.next()));
    assertNoOrphans();
  }

  @Test
  void callerRollbackRemovesMemberUsesRedemptionAndOutbox() {
    var invitation = createBoth(List.of(role), List.of(group), null);
    UUID account = UuidV7.next();
    assertThrows(
        IllegalStateException.class,
        () ->
            store.tenant(
                TENANT,
                () -> {
                  redeem(invitation, account);
                  throw new IllegalStateException("rollback");
                }));
    assertNoRedemption(invitation, account);
  }

  private String race(Runnable firstMutation, Runnable secondMutation) throws Exception {
    var firstWritten = new CountDownLatch(1);
    var release = new CountDownLatch(1);
    var started = new CountDownLatch(1);
    var pid = new AtomicInteger();
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
                        await(release);
                        return "committed";
                      }));
      try {
        assertTrue(firstWritten.await(10, TimeUnit.SECONDS));
        var second =
            executor.submit(
                () ->
                    outcome(
                        () ->
                            store.tenant(
                                TENANT,
                                () -> {
                                  store.jdbc().execute("SET LOCAL statement_timeout='10s'");
                                  pid.set(
                                      store
                                          .jdbc()
                                          .queryForObject(
                                              "SELECT pg_backend_pid()", Integer.class));
                                  started.countDown();
                                  secondMutation.run();
                                  return null;
                                })));
        assertTrue(started.await(10, TimeUnit.SECONDS));
        long deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(10);
        boolean blocked = false;
        while (!second.isDone()) {
          blocked =
              Boolean.TRUE.equals(
                  observer.queryForObject(
                      "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=? AND wait_event_type='Lock')",
                      Boolean.class,
                      pid.get()));
          if (blocked) break;
          assertTrue(
              System.nanoTime() < deadline, "second transaction neither finished nor blocked");
        }
        release.countDown();
        assertEquals("committed", first.get(15, TimeUnit.SECONDS));
        String result = second.get(15, TimeUnit.SECONDS);
        assertTrue(
            blocked,
            "second transaction did not wait for first; outcome="
                + result
                + "; orphan role bindings="
                + roleOrphans()
                + "; orphan group members="
                + groupOrphans());
        return result;
      } finally {
        release.countDown();
      }
    }
  }

  private static String outcome(Supplier<?> work) {
    try {
      work.get();
      return "committed";
    } catch (AurigaException e) {
      return e.errorCode().code();
    }
  }

  private ObjectNode create(String kind, UUID id) {
    return createBoth(
        kind.equals("role") ? List.of(id) : List.of(),
        kind.equals("group") ? List.of(id) : List.of(),
        null);
  }

  private ObjectNode createBoth(List<UUID> roles, List<UUID> groups, List<UUID> providers) {
    return TenantContext.call(
        TENANT,
        () ->
            RequestContext.start("invitation-integrity", null)
                .withPrincipal(
                    new AurigaPrincipal(
                        actor.toString(),
                        TENANT,
                        PrincipalType.USER,
                        TokenContext.ADMIN,
                        null,
                        null,
                        "member",
                        List.of("owner"),
                        Set.of("org.invitations.write"),
                        null,
                        null,
                        null))
                .call(
                    () ->
                        svc.createInvitation(
                            null, "member", "member", roles, groups, providers, null, 1)));
  }

  private ObjectNode redeem(ObjectNode invitation, UUID account) {
    return TenantContext.call(
        TENANT,
        () ->
            svc.redeemInvitation(
                invitation.path("code").asString(), account, "member", "member@example.com", null));
  }

  private void delete(String kind) {
    TenantContext.run(
        TENANT,
        () -> {
          if (kind.equals("role")) svc.deleteRole(role);
          else svc.deleteGroup(group);
        });
  }

  private UUID target(String kind) {
    return kind.equals("role") ? role : group;
  }

  private static UUID insertTarget(String kind) {
    UUID id = UuidV7.next();
    if (kind.equals("role"))
      store
          .jdbc()
          .update(
              "INSERT INTO role (id,tenant_id,key,name_en,name_zh) VALUES (?,?,?,?,?)",
              id,
              store.tenantId(),
              id.toString(),
              "role",
              "role");
    else
      store
          .jdbc()
          .update(
              "INSERT INTO \"group\" (id,tenant_id,name) VALUES (?,?,?)",
              id,
              store.tenantId(),
              id.toString());
    return id;
  }

  private void assertNoRedemption(ObjectNode invitation, UUID account) {
    assertEquals(0, count("member", "id='" + account + "'"));
    assertEquals(0, count("role_binding", "subject_id='" + account + "'"));
    assertEquals(0, count("group_member", "account_id='" + account + "'"));
    assertEquals(0, count("invitation_redemption", "account_id='" + account + "'"));
    assertEquals(0, count("outbox_event", "subject_key='" + account + "'"));
    store.tenant(
        TENANT,
        () -> {
          assertEquals(
              0,
              store
                  .jdbc()
                  .queryForObject(
                      "SELECT uses FROM invitation WHERE id=?",
                      Integer.class,
                      UUID.fromString(invitation.path("id").asString())));
          assertEquals(
              "active",
              store
                  .jdbc()
                  .queryForObject(
                      "SELECT status FROM invitation WHERE id=?",
                      String.class,
                      UUID.fromString(invitation.path("id").asString())));
          return null;
        });
  }

  private void assertNoOrphans() {
    assertEquals(0, roleOrphans());
    assertEquals(0, groupOrphans());
  }

  private int roleOrphans() {
    return count(
        "role_binding b",
        "NOT EXISTS (SELECT 1 FROM role r WHERE r.tenant_id=b.tenant_id AND r.id=b.role_id)");
  }

  private int groupOrphans() {
    return count(
        "group_member gm",
        "NOT EXISTS (SELECT 1 FROM \"group\" g WHERE g.tenant_id=gm.tenant_id AND g.id=gm.group_id)");
  }

  private int count(String table, String predicate) {
    return store.tenant(
        TENANT,
        () ->
            store
                .jdbc()
                .queryForObject(
                    "SELECT count(*) FROM " + table + " WHERE " + predicate, Integer.class));
  }

  private static void assertError(String code, Runnable work) {
    assertEquals(code, assertThrows(AurigaException.class, work::run).errorCode().code());
  }

  private static void await(CountDownLatch latch) {
    try {
      assertTrue(latch.await(20, TimeUnit.SECONDS));
    } catch (InterruptedException e) {
      Thread.currentThread().interrupt();
      throw new AssertionError(e);
    }
  }
}
