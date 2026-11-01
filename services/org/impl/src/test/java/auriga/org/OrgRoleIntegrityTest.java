package auriga.org;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.events.EventSchemas;
import auriga.common.events.OutboxEventPublisher;
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
import java.util.function.Supplier;
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
import org.springframework.security.core.context.SecurityContextHolder;
import org.springframework.transaction.TransactionDefinition;
import org.springframework.transaction.support.TransactionTemplate;
import tools.jackson.databind.node.ObjectNode;

@ExtendWith(PostgresExtension.class)
class OrgRoleIntegrityTest {
  private static final UUID TENANT = OrgIds.DEFAULT_TENANT;
  private static OrgStore store;
  private static OrgService svc;
  private static TenantTransactionManager manager;
  private static JdbcTemplate observer;
  private UUID firstOwner;
  private UUID secondOwner;
  private UUID firstBinding;
  private UUID secondBinding;
  private UUID operator;

  @BeforeAll
  static void migrate() {
    var roles =
        PostgresExtension.serviceRoles(OrgRoleIntegrityTest.class, "org_owner", "org_app", "org");
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
        new JdbcTemplate(PostgresExtension.database(OrgRoleIntegrityTest.class).dataSource());
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
  void seed() {
    firstOwner = UuidV7.next();
    secondOwner = UuidV7.next();
    operator = UuidV7.next();
    store.tenant(
        TENANT,
        () -> {
          store.jdbc().update("DELETE FROM outbox_event WHERE tenant_id=?", TENANT);
          store.jdbc().update("DELETE FROM role_binding WHERE tenant_id=?", TENANT);
          store
              .jdbc()
              .update("DELETE FROM role WHERE tenant_id=? AND key='integrity_operator'", TENANT);
          store.jdbc().update("DELETE FROM group_member WHERE tenant_id=?", TENANT);
          store.jdbc().update("DELETE FROM \"group\" WHERE tenant_id=?", TENANT);
          store.jdbc().update("DELETE FROM member WHERE tenant_id=?", TENANT);
          insertMember(firstOwner);
          insertMember(secondOwner);
          insertMember(operator);
          firstBinding = bind(firstOwner, OrgIds.ROLE_OWNER);
          secondBinding = bind(secondOwner, OrgIds.ROLE_OWNER);
          bind(operator, OrgIds.ROLE_ADMIN);
          svc.getEffectivePermissions(firstOwner);
          return null;
        });
  }

  static Stream<Arguments> missingTargets() {
    return Stream.of(
        Arguments.of("member", false), Arguments.of("group", false),
        Arguments.of("member", true), Arguments.of("group", true));
  }

  @ParameterizedTest
  @MethodSource("missingTargets")
  void missingAndForeignTargetsCannotBeBound(String type, boolean foreign) {
    UUID target = UuidV7.next();
    if (foreign) {
      UUID other = UuidV7.next();
      store.tenant(
          other,
          () -> {
            if (type.equals("member")) insertMember(target);
            else
              store
                  .jdbc()
                  .update(
                      "INSERT INTO \"group\" (id,tenant_id,name) VALUES (?,?,?)",
                      target,
                      other,
                      "foreign");
            return null;
          });
    }
    assertAll(
        () -> assertError("org.not_found", () -> create(OrgIds.ROLE_MEMBER, type, target)),
        () -> assertEquals(0, bindingsFor(target)),
        () -> assertEquals(0, eventCount()));
  }

  static Stream<Arguments> ownerRaces() {
    return Stream.of("revoke", "disable", "offboard")
        .flatMap(
            first ->
                Stream.of("revoke", "disable", "offboard")
                    .flatMap(
                        second ->
                            Stream.of(false, true).map(rr -> Arguments.of(first, second, rr))));
  }

  static Stream<Arguments> memberLifecycleCeilings() {
    return Stream.of("disable", "offboard", "enable")
        .flatMap(action -> Stream.of(false, true).map(group -> Arguments.of(action, group)));
  }

  @ParameterizedTest
  @MethodSource("memberLifecycleCeilings")
  void memberLifecycleRejectsHigherDirectAndInheritedPermissions(String action, boolean inherited) {
    UUID target = inherited ? member() : firstOwner;
    if (inherited) {
      UUID group = group();
      create(OrgIds.ROLE_OWNER, "group", group);
      store.tenant(
          TENANT,
          () -> {
            store
                .jdbc()
                .update(
                    "INSERT INTO group_member (id,tenant_id,group_id,account_id) VALUES (?,?,?,?)",
                    UuidV7.next(),
                    TENANT,
                    group,
                    target);
            return null;
          });
    }
    if (action.equals("enable")) setStatus(target, "disabled");
    var before = TenantContext.call(TENANT, () -> svc.memberJson(target));
    int events = eventCount();
    assertError("org.grant_exceeds_own", () -> asActor(operator, () -> lifecycle(action, target)));
    assertEquals(before, TenantContext.call(TENANT, () -> svc.memberJson(target)));
    assertEquals(events, eventCount());
  }

  @ParameterizedTest
  @MethodSource("memberLifecycleCeilings")
  void memberLifecycleAllowsEqualPermissionsAndOwner(String action, boolean ownerActor) {
    UUID target = ownerActor ? secondOwner : member();
    if (!ownerActor)
      store.tenant(
          TENANT,
          () -> {
            bind(target, OrgIds.ROLE_ADMIN);
            return null;
          });
    if (action.equals("enable")) setStatus(target, "disabled");
    var result = asActor(ownerActor ? firstOwner : operator, () -> lifecycle(action, target));
    assertEquals(
        action.equals("enable") ? "active" : action.equals("offboard") ? "offboarded" : "disabled",
        result.path("status").asString());
    assertEquals(1, eventCount());
  }

  @ParameterizedTest
  @ValueSource(strings = {"disable", "offboard", "enable"})
  void memberLifecycleRequiresExplicitActor(String action) {
    if (action.equals("enable")) setStatus(firstOwner, "disabled");
    assertError(
        "org.not_found", () -> TenantContext.call(TENANT, () -> lifecycle(action, firstOwner)));
    assertEquals(0, eventCount());
  }

  @Test
  void bindingRevocationRequiresExplicitActor() {
    assertError(
        "org.not_found",
        () -> TenantContext.run(TENANT, () -> svc.deleteRoleBinding(firstBinding)));
    assertEquals(1, bindingsFor(firstOwner));
    assertEquals(0, eventCount());
  }

  private ObjectNode lifecycle(String action, UUID member) {
    return switch (action) {
      case "disable" -> svc.disableMember(member, "ceiling test");
      case "offboard" -> svc.offboardMember(member, null, "ceiling test");
      case "enable" -> svc.enableMember(member);
      default -> throw new AssertionError(action);
    };
  }

  private void setStatus(UUID member, String status) {
    store.tenant(
        TENANT,
        () -> {
          store
              .jdbc()
              .update(
                  "UPDATE member SET status=? WHERE tenant_id=? AND id=?", status, TENANT, member);
          return null;
        });
  }

  @ParameterizedTest
  @ValueSource(strings = {"member", "group", "service_account"})
  void revokeCeilingRejectsHigherPermissionsWithoutWrites(String type) {
    UUID subject =
        type.equals("member") ? member() : type.equals("group") ? group() : UuidV7.next();
    var binding = create(OrgIds.ROLE_OWNER, type, subject);
    int before = eventCount();
    assertError(
        "org.grant_exceeds_own",
        () ->
            asActor(
                operator,
                () -> {
                  svc.deleteRoleBinding(UUID.fromString(binding.path("id").asString()));
                  return null;
                }));
    assertEquals(1, bindingsFor(subject));
    assertEquals(before, eventCount());
  }

  @Test
  void revokeCeilingAllowsEqualPermissionsAndOwner() {
    UUID target = member();
    var equal = create(OrgIds.ROLE_ADMIN, "member", target);
    asActor(
        operator,
        () -> {
          svc.deleteRoleBinding(UUID.fromString(equal.path("id").asString()));
          return null;
        });
    assertEquals(0, bindingsFor(target));
    asActor(
        firstOwner,
        () -> {
          svc.deleteRoleBinding(secondBinding);
          return null;
        });
    assertEquals(1, owners());
    assertEquals(3, eventCount());
  }

  @ParameterizedTest
  @ValueSource(strings = {"disabled", "offboarded"})
  void activeTargetRequiredForDirectGrant(String status) {
    UUID target = member();
    store.tenant(
        TENANT,
        () -> {
          store
              .jdbc()
              .update(
                  "UPDATE member SET status=? WHERE tenant_id=? AND id=?", status, TENANT, target);
          return null;
        });
    assertError("org.conflict", () -> create(OrgIds.ROLE_MEMBER, "member", target));
    assertEquals(0, bindingsFor(target));
    assertEquals(0, eventCount());
  }

  @ParameterizedTest
  @MethodSource("ownerRaces")
  void twoOwnersCannotBothDisappear(String first, String second, boolean repeatableRead)
      throws Exception {
    giveOperatorOwnerPermissions();
    String outcome =
        race(
            () -> mutate(first, firstOwner, firstBinding),
            () -> {
              store
                  .jdbc()
                  .update(
                      "UPDATE member SET display_name='must roll back' WHERE tenant_id=? AND id=?",
                      TENANT,
                      operator);
              mutate(second, secondOwner, secondBinding);
            },
            repeatableRead);
    assertAll(
        () -> assertEquals(repeatableRead ? "common.conflict" : "org.last_owner", outcome),
        () -> assertEquals(1, owners(), "active direct owners after " + first + "/" + second),
        () -> assertEquals(1, eventCount(), "rejected writer cannot publish an event"),
        () -> assertEquals(1, bindingsFor(secondOwner)),
        () ->
            assertEquals(
                "active",
                TenantContext.call(TENANT, () -> svc.memberJson(secondOwner))
                    .path("status")
                    .asString()),
        () ->
            assertEquals(
                "member",
                TenantContext.call(TENANT, () -> svc.memberJson(operator))
                    .path("displayName")
                    .asString()),
        () ->
            assertEquals(operator.toString(), lastEvent().path("data").path("actorId").asString()));
  }

  @Test
  void deletingGroupBeforeBindingCannotLeaveOrphan() throws Exception {
    UUID group = group();
    assertEquals(
        "org.not_found",
        race(
            () -> svc.deleteGroup(group), () -> create(OrgIds.ROLE_MEMBER, "group", group), false));
    assertEquals(0, bindingsFor(group));
    assertEquals(0, eventCount());
  }

  @Test
  void bindingBeforeDeletingGroupIsRemoved() throws Exception {
    UUID group = group();
    assertEquals(
        "committed",
        race(
            () -> create(OrgIds.ROLE_MEMBER, "group", group), () -> svc.deleteGroup(group), false));
    assertEquals(0, bindingsFor(group));
  }

  @Test
  void runtimeIsNotTableOwnerAndRlsFailsClosed() {
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
                      "SELECT pg_get_userbyid(relowner)=current_user FROM pg_class WHERE oid='org.member'::regclass",
                      Boolean.class));
          return null;
        });
    assertEquals(0, store.jdbc().queryForObject("SELECT count(*) FROM member", Integer.class));
    assertEquals(
        0,
        store.tenant(
            UuidV7.next(),
            () -> store.jdbc().queryForObject("SELECT count(*) FROM member", Integer.class)));
  }

  @ParameterizedTest
  @ValueSource(strings = {"revoke", "disable", "offboard"})
  void serialLastOwnerKeepsExistingErrorAndDoesNotWrite(String mutation) {
    giveOperatorOwnerPermissions();
    TenantContext.run(TENANT, () -> mutate(mutation, firstOwner, firstBinding));
    var before = TenantContext.call(TENANT, () -> svc.memberJson(secondOwner));
    assertError(
        "org.last_owner",
        () -> TenantContext.run(TENANT, () -> mutate(mutation, secondOwner, secondBinding)));
    assertEquals(before, TenantContext.call(TENANT, () -> svc.memberJson(secondOwner)));
    assertEquals(1, bindingsFor(secondOwner));
    assertEquals(1, owners());
    assertEquals(1, eventCount());
    assertError(
        "org.not_found",
        () -> TenantContext.run(TENANT, () -> svc.deleteRoleBinding(UuidV7.next())));
  }

  @Test
  void legalMemberAndGroupBindingsKeepPermissionEventsAndRevoke() {
    UUID member = member(), group = group();
    store.tenant(
        TENANT,
        () -> {
          store
              .jdbc()
              .update(
                  "INSERT INTO group_member (id,tenant_id,group_id,account_id) VALUES (?,?,?,?)",
                  UuidV7.next(),
                  TENANT,
                  group,
                  member);
          return null;
        });
    var direct = create(OrgIds.ROLE_MEMBER, "member", member);
    var inherited = create(OrgIds.ROLE_ADMIN, "group", group);
    assertEquals(member.toString(), direct.path("subject").path("id").asString());
    assertEquals("group", inherited.path("subject").path("type").asString());
    assertEquals(2, eventCount());
    var event = lastEvent();
    assertEquals("user.permissions_changed", event.path("type").asString());
    assertEquals(firstOwner.toString(), event.path("data").path("actorId").asString());
    assertEquals(member.toString(), event.path("data").path("userId").asString());
    assertTrue(event.path("data").path("roleKeys").toString().contains("admin"));
    asActor(
        firstOwner,
        () -> {
          svc.deleteRoleBinding(UUID.fromString(direct.path("id").asString()));
          svc.deleteRoleBinding(UUID.fromString(inherited.path("id").asString()));
          return null;
        });
    assertEquals(4, eventCount());
    assertEquals(0, bindingsFor(member));
    assertEquals(0, bindingsFor(group));
    assertEquals(2, owners());
  }

  @Test
  void grantCeilingBuiltinRolesAndInheritedOwnerPolicyRemainUnchanged() {
    UUID member = member(), group = group();
    create(OrgIds.ROLE_OWNER, "group", group);
    store.tenant(
        TENANT,
        () -> {
          store
              .jdbc()
              .update(
                  "INSERT INTO group_member (id,tenant_id,group_id,account_id) VALUES (?,?,?,?)",
                  UuidV7.next(),
                  TENANT,
                  group,
                  member);
          return null;
        });
    assertError(
        "org.grant_exceeds_own",
        () -> asActor(operator, () -> svc.createRoleBinding(OrgIds.ROLE_OWNER, "member", member)));
    assertError(
        "org.grant_exceeds_own",
        () -> asActor(operator, () -> svc.createRoleBinding(OrgIds.ROLE_MEMBER, "group", group)));
    assertError(
        "org.role_builtin",
        () -> TenantContext.run(TENANT, () -> svc.deleteRole(OrgIds.ROLE_OWNER)));
    assertError(
        "org.role_builtin",
        () ->
            TenantContext.call(
                TENANT, () -> svc.updateRole(OrgIds.ROLE_OWNER, null, null, List.of())));
    giveOperatorOwnerPermissions();
    asActor(
        firstOwner,
        () -> {
          svc.deleteRoleBinding(firstBinding);
          return null;
        });
    assertEquals(1, owners());
    assertError(
        "org.last_owner",
        () ->
            asActor(
                operator, () -> svc.disableMember(secondOwner, "inherited owner does not count")));
    assertEquals(1, owners());
    assertEquals(1, eventCount());
  }

  @Test
  void ordinaryMemberLifecycleKeepsVersionsIfMatchAndEvents() {
    UUID member = member();
    asActor(
        operator,
        () -> {
          assertEquals("disabled", svc.disableMember(member, "reason").path("status").asString());
          assertEquals("active", svc.enableMember(member).path("status").asString());
          assertError(
              "org.precondition_failed", () -> svc.updateMember(member, "bad", null, null, 1L));
          var updated = svc.updateMember(member, "renamed", null, "external", 3L);
          assertEquals(4, updated.path("version").asLong());
          assertEquals("active", updated.path("status").asString());
          assertEquals(
              "offboarded",
              svc.offboardMember(member, firstOwner, "left").path("status").asString());
          return null;
        });
    assertEquals(4, eventCount());
    assertEquals("user.offboarded", lastEvent().path("type").asString());
    assertEquals(operator.toString(), lastEvent().path("data").path("actorId").asString());
    assertEquals(2, owners());
  }

  @ParameterizedTest
  @ValueSource(strings = {"revoke", "disable", "offboard", "create"})
  void callerRollbackRemovesMutationAndOutbox(String mutation) {
    if (!mutation.equals("create")) giveOperatorOwnerPermissions();
    UUID member = member();
    var tenantBefore = TenantContext.call(TENANT, () -> svc.tenantJson(svc.getTenant()));
    var memberBefore = TenantContext.call(TENANT, () -> svc.memberJson(firstOwner));
    assertThrows(
        IllegalStateException.class,
        () ->
            store.tenant(
                TENANT,
                () -> {
                  if (mutation.equals("create")) create(OrgIds.ROLE_ADMIN, "member", member);
                  else mutate(mutation, firstOwner, firstBinding);
                  assertEquals(1, eventCount());
                  throw new IllegalStateException("caller rollback");
                }));
    assertEquals(2, owners());
    assertEquals(1, bindingsFor(firstOwner));
    assertEquals(0, bindingsFor(member));
    assertEquals(memberBefore, TenantContext.call(TENANT, () -> svc.memberJson(firstOwner)));
    assertEquals(tenantBefore, TenantContext.call(TENANT, () -> svc.tenantJson(svc.getTenant())));
    assertEquals(0, eventCount());
  }

  @ParameterizedTest
  @ValueSource(booleans = {false, true})
  void metadataCannotRestoreOldOwnerStatus(boolean repeatableRead) throws Exception {
    giveOperatorOwnerPermissions();
    assertEquals(
        repeatableRead ? "common.conflict" : "committed",
        race(
            () -> mutate("disable", firstOwner, firstBinding),
            () -> svc.updateMember(firstOwner, "renamed", null, null, null),
            repeatableRead));
    var member = TenantContext.call(TENANT, () -> svc.memberJson(firstOwner));
    assertEquals("disabled", member.path("status").asString());
    assertEquals(repeatableRead ? 2 : 3, member.path("version").asLong());
    assertEquals(1, owners());
    assertEquals(repeatableRead ? 1 : 2, eventCount());
  }

  @ParameterizedTest
  @ValueSource(booleans = {false, true})
  void metadataCannotUndoEnableFromOldSnapshot(boolean repeatableRead) throws Exception {
    UUID member = member();
    asActor(firstOwner, () -> svc.disableMember(member, "before race"));
    store.tenant(
        TENANT,
        () -> {
          store.jdbc().update("DELETE FROM outbox_event WHERE tenant_id=?", TENANT);
          return null;
        });
    assertEquals(
        repeatableRead ? "common.conflict" : "committed",
        race(
            () -> asActor(firstOwner, () -> svc.enableMember(member)),
            () -> svc.updateMember(member, "renamed", null, null, null),
            repeatableRead));
    assertEquals(
        "active",
        TenantContext.call(TENANT, () -> svc.memberJson(member)).path("status").asString());
    assertEquals(2, owners());
    assertEquals(repeatableRead ? 1 : 2, eventCount());
  }

  @Test
  void roleDeletionAndBindingUseSameLockInBothOrders() throws Exception {
    UUID member = member();
    UUID role =
        UUID.fromString(
            asActor(
                    firstOwner,
                    () ->
                        svc.createRole(
                            "integrity-a", OrgJson.object("en", "A", "zh", "A"), null, List.of()))
                .path("id")
                .asString());
    assertEquals(
        "org.not_found",
        race(() -> svc.deleteRole(role), () -> create(role, "member", member), false));
    assertEquals(0, bindingsFor(member));
    UUID secondRole =
        UUID.fromString(
            asActor(
                    firstOwner,
                    () ->
                        svc.createRole(
                            "integrity-b", OrgJson.object("en", "B", "zh", "B"), null, List.of()))
                .path("id")
                .asString());
    assertEquals(
        "committed",
        race(() -> create(secondRole, "member", member), () -> svc.deleteRole(secondRole), false));
    assertEquals(0, bindingsFor(member));
    assertEquals(2, eventCount());
  }

  private void mutate(String mutation, UUID member, UUID binding) {
    asActor(
        operator,
        () -> {
          switch (mutation) {
            case "revoke" -> svc.deleteRoleBinding(binding);
            case "disable" -> svc.disableMember(member, "integrity test");
            case "offboard" -> svc.offboardMember(member, null, "integrity test");
            default -> throw new AssertionError(mutation);
          }
          return null;
        });
  }

  private void giveOperatorOwnerPermissions() {
    // Exercise last-owner serialization with a fully authorized actor who is not counted as an
    // owner.
    var role =
        asActor(
            firstOwner,
            () ->
                svc.createRole(
                    "integrity_operator",
                    OrgJson.object("en", "Integrity operator"),
                    null,
                    svc.getEffectivePermissions(firstOwner).permissions()));
    store.tenant(
        TENANT,
        () -> {
          bind(operator, UUID.fromString(role.path("id").asString()));
          return null;
        });
  }

  private String race(Runnable firstMutation, Runnable secondMutation, boolean repeatableRead)
      throws Exception {
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
                            transaction.execute(
                                status -> {
                                  store.jdbc().execute("SET LOCAL statement_timeout='10s'");
                                  secondPid.set(
                                      store
                                          .jdbc()
                                          .queryForObject(
                                              "SELECT pg_backend_pid()", Integer.class));
                                  // This SELECT fixes the RR snapshot before the first writer
                                  // commits.
                                  assertEquals(2, store.countOwners());
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

  private ObjectNode create(UUID role, String type, UUID subject) {
    return asActor(firstOwner, () -> svc.createRoleBinding(role, type, subject));
  }

  private static <T> T asActor(UUID actor, Supplier<T> work) {
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

  private static void insertMember(UUID member) {
    store.insertMember(
        new OrgStore.MemberRow(
            member, "member", null, "member", "active", 1, Instant.now(), null, null));
  }

  private static UUID bind(UUID member, UUID role) {
    UUID id = UuidV7.next();
    store
        .jdbc()
        .update(
            "INSERT INTO role_binding (id,tenant_id,role_id,subject_type,subject_id) VALUES (?,?,?,'member',?)",
            id,
            store.tenantId(),
            role,
            member);
    return id;
  }

  private static UUID group() {
    return store.tenant(
        TENANT,
        () -> {
          UUID id = UuidV7.next();
          store
              .jdbc()
              .update(
                  "INSERT INTO \"group\" (id,tenant_id,name) VALUES (?,?,?)",
                  id,
                  TENANT,
                  id.toString());
          return id;
        });
  }

  private static long owners() {
    return store.tenant(TENANT, store::countOwners);
  }

  private static int bindingsFor(UUID subject) {
    return store.tenant(
        TENANT,
        () ->
            store
                .jdbc()
                .queryForObject(
                    "SELECT count(*) FROM role_binding WHERE tenant_id=? AND subject_id=?",
                    Integer.class,
                    TENANT,
                    subject));
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

  private static tools.jackson.databind.JsonNode lastEvent() {
    return store.tenant(
        TENANT,
        () ->
            OrgJson.MAPPER.readTree(
                store
                    .jdbc()
                    .queryForObject(
                        "SELECT envelope::text FROM outbox_event WHERE tenant_id=? ORDER BY id DESC LIMIT 1",
                        String.class,
                        TENANT)));
  }

  private static UUID member() {
    return store.tenant(
        TENANT,
        () -> {
          UUID id = UuidV7.next();
          insertMember(id);
          return id;
        });
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
