package io.github.hansjone.auriga.org;

import static org.junit.jupiter.api.Assertions.*;

import io.github.hansjone.auriga.common.context.TenantContext;
import io.github.hansjone.auriga.common.error.AurigaException;
import io.github.hansjone.auriga.common.events.EventPublisher;
import io.github.hansjone.auriga.common.persistence.CommonMigrations;
import io.github.hansjone.auriga.common.persistence.TenantTransactionManager;
import io.github.hansjone.auriga.common.persistence.UuidV7;
import io.github.hansjone.auriga.common.security.AurigaAuthentication;
import io.github.hansjone.auriga.common.security.AurigaPrincipal;
import io.github.hansjone.auriga.common.security.PrincipalType;
import io.github.hansjone.auriga.common.security.TokenContext;
import io.github.hansjone.auriga.org.api.PendingAccount;
import io.github.hansjone.auriga.testsupport.PostgresExtension;
import java.time.Clock;
import java.time.Instant;
import java.time.ZoneOffset;
import java.util.List;
import java.util.Set;
import java.util.UUID;
import java.util.concurrent.CopyOnWriteArrayList;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.security.core.context.SecurityContext;
import org.springframework.security.core.context.SecurityContextHolder;
import tools.jackson.databind.node.ObjectNode;

@ExtendWith(PostgresExtension.class)
class OrgCoreTest {
  private static final UUID TENANT = OrgIds.DEFAULT_TENANT;
  private static OrgStore store;
  private static OrgService svc;
  private static RecordingEvents events;
  private static Clock clock;

  @BeforeAll
  static void migrate() {
    var roles = PostgresExtension.serviceRoles(OrgCoreTest.class, "org_owner", "org_app", "org");
    CommonMigrations.migrate(roles.owner().dataSource(), "org");
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas("org")
                .locations("classpath:db/org")
                .load())
        .migrate();
    JdbcTemplate jdbc = new JdbcTemplate(roles.runtime().dataSource());
    store = new OrgStore(jdbc, new TenantTransactionManager(roles.runtime().dataSource()));
    events = new RecordingEvents();
    clock = Clock.fixed(Instant.parse("2026-06-01T00:00:00Z"), ZoneOffset.UTC);
    svc = new OrgService(store, events, clock);
  }

  @BeforeEach
  void cleanBusiness() {
    events.clear();
    store.tenant(
        TENANT,
        () -> {
          store.jdbc().update("DELETE FROM invitation_redemption WHERE tenant_id=?", TENANT);
          store.jdbc().update("DELETE FROM invitation WHERE tenant_id=?", TENANT);
          store.jdbc().update("DELETE FROM role_binding WHERE tenant_id=?", TENANT);
          store.jdbc().update("DELETE FROM group_member WHERE tenant_id=?", TENANT);
          store.jdbc().update("DELETE FROM quota WHERE tenant_id=? AND scope<>'default'", TENANT);
          store.jdbc().update("DELETE FROM \"group\" WHERE tenant_id=?", TENANT);
          store.jdbc().update("DELETE FROM member WHERE tenant_id=?", TENANT);
          store.jdbc().update("DELETE FROM role WHERE tenant_id=? AND builtin=false", TENANT);
          return null;
        });
  }

  @Test
  void resolveTenantByHostFallsBackToDefault() {
    var byMiss = svc.resolveTenantByHost("unknown.example.com");
    assertEquals("default", byMiss.slug());
    store.tenant(
        TENANT,
        () -> {
          svc.updateTenant(null, null, List.of("portal.example.com"));
          return null;
        });
    var hit = svc.resolveTenantByHost("portal.example.com");
    assertEquals(TENANT, hit.id());
  }

  @Test
  void effectivePermissionsUnionDirectAndGroupRoles() {
    UUID owner = seedOwner();
    UUID user = UuidV7.next();
    store.tenant(
        TENANT,
        () -> {
          insertMember(user, "alice");
          UUID group = createGroup("eng");
          bindRole(OrgIds.ROLE_ADMIN, "group", group, owner);
          addToGroup(group, user);
          bindRole(OrgIds.ROLE_MEMBER, "member", user, owner);
          return null;
        });
    var eff = TenantContext.call(TENANT, () -> svc.getEffectivePermissions(user));
    assertTrue(eff.roleKeys().contains("admin"));
    assertTrue(eff.roleKeys().contains("member"));
    assertTrue(eff.permissions().contains("org.members.write"));
  }

  @Test
  void grantExceedsOwnWhenAddingToPowerfulGroup() {
    UUID owner = seedOwner();
    UUID admin = UuidV7.next();
    store.tenant(
        TENANT,
        () -> {
          insertMember(admin, "admin");
          bindRole(OrgIds.ROLE_ADMIN, "member", admin, owner);
          UUID ownersGroup = createGroup("owners-group");
          bindRole(OrgIds.ROLE_OWNER, "group", ownersGroup, owner);
          var r = svc.checkGrant(admin, null, ownersGroup);
          assertFalse(r.allowed());
          assertTrue(r.exceeding().contains("org.tenant.write"));
          AurigaException ex =
              assertThrows(
                  AurigaException.class, () -> svc.ensureGrantAllowed(admin, null, ownersGroup));
          assertEquals("org.grant_exceeds_own", ex.errorCode().code());
          return null;
        });
  }

  @Test
  void invitationRedeemIsIdempotent() {
    UUID owner = seedOwner();
    UUID account = UuidV7.next();
    ObjectNode created =
        store.tenant(
            TENANT,
            () ->
                withActor(
                    owner,
                    () ->
                        svc.createInvitation(
                            "alice@example.com",
                            "Alice",
                            "member",
                            List.of(OrgIds.ROLE_MEMBER),
                            List.of(),
                            List.of(),
                            clock.instant().plusSeconds(3600),
                            1)));
    String code = created.path("code").asString();
    ObjectNode first = svc.redeemInvitation(code, account, "Alice", "alice@example.com", null);
    assertEquals("active", first.path("status").asString());
    assertEquals(1, events.ofType("user.created").size());
    ObjectNode second = svc.redeemInvitation(code, account, "Alice", "alice@example.com", null);
    assertEquals(account.toString(), second.path("id").asString());
    assertEquals(1, events.ofType("user.created").size());
  }

  @Test
  void disablePublishesUserDisabled() {
    UUID owner = seedOwner();
    UUID user = UuidV7.next();
    store.tenant(
        TENANT,
        () -> {
          insertMember(user, "carol");
          bindRole(OrgIds.ROLE_MEMBER, "member", user, owner);
          withActor(owner, () -> svc.disableMember(user, "left"));
          return null;
        });
    assertEquals(1, events.ofType("user.disabled").size());
    assertEquals(user.toString(), events.ofType("user.disabled").getFirst().subject());
  }

  @Test
  void rlsIsolatesTenants() {
    UUID other = UUID.fromString("00000000-0000-0000-0000-000000000099");
    store.tenant(
        other,
        () -> {
          OrgArrays.update(
              store.jdbc(),
              "INSERT INTO tenant (id, tenant_id, slug, display_name, locale, hosts, created_at)"
                  + " VALUES (?,?,?,?,?,?,?)",
              other,
              other,
              "other",
              "Other",
              "en",
              new String[0],
              java.sql.Timestamp.from(clock.instant()));
          insertMember(UuidV7.next(), "x");
          return null;
        });
    assertEquals(
        0,
        store.tenant(
            TENANT,
            () ->
                store
                    .jdbc()
                    .queryForObject(
                        "SELECT count(*) FROM member WHERE display_name='x'", Integer.class)));
    assertEquals(0, store.jdbc().queryForObject("SELECT count(*) FROM member", Integer.class));
  }

  @Test
  void permissionResolverReturnsNonEmptyForOwner() {
    UUID owner = seedOwner();
    var resolver = new OrgPermissionResolver(svc);
    var perms = resolver.resolve(TENANT, owner.toString(), "admin");
    assertFalse(perms.isEmpty());
    assertTrue(perms.contains("org.members.write"));
  }

  @Test
  void identityPolicyHooksRedeemViaPendingAccount() {
    UUID owner = seedOwner();
    ObjectNode created =
        store.tenant(
            TENANT,
            () ->
                withActor(
                    owner,
                    () ->
                        svc.createInvitation(
                            null,
                            "Dave",
                            "member",
                            List.of(OrgIds.ROLE_MEMBER),
                            List.of(),
                            List.of(),
                            clock.instant().plusSeconds(3600),
                            1)));
    UUID account = UuidV7.next();
    String code = created.path("code").asString();
    assertFalse(code.isBlank());
    ObjectNode member = svc.redeemInvitation(code, account, "Dave", "dave@example.com", null);
    assertEquals(account.toString(), member.path("id").asString());
    assertTrue(store.tenant(TENANT, () -> store.member(account)).isPresent());
    var hooks = new OrgIdentityPolicyHooks(svc);
    assertTrue(
        hooks.consumeInvitation(
            code,
            null,
            OrgJson.object("accountId", account.toString(), "displayName", "Dave")));
    assertTrue(
        PendingAccount.call(
            account,
            () -> hooks.consumeInvitation(code, null, OrgJson.object("displayName", "Dave"))));
  }

  private UUID seedOwner() {
    UUID owner = UuidV7.next();
    store.tenant(
        TENANT,
        () -> {
          insertMember(owner, "owner");
          bindRole(OrgIds.ROLE_OWNER, "member", owner, owner);
          return null;
        });
    return owner;
  }

  private void insertMember(UUID id, String name) {
    store.insertMember(
        new OrgStore.MemberRow(
            id, name, name + "@example.com", "member", "active", 1, clock.instant(), null, null));
  }

  private UUID createGroup(String name) {
    UUID id = UuidV7.next();
    store
        .jdbc()
        .update(
            "INSERT INTO \"group\" (id, tenant_id, name, source, created_at) VALUES (?,?,?,'manual',?)",
            id,
            TENANT,
            name,
            java.sql.Timestamp.from(clock.instant()));
    return id;
  }

  private void bindRole(UUID roleId, String subjectType, UUID subjectId, UUID grantedBy) {
    store
        .jdbc()
        .update(
            "INSERT INTO role_binding (id, tenant_id, role_id, subject_type, subject_id, granted_by, created_at)"
                + " VALUES (?,?,?,?,?,?,?)",
            UuidV7.next(),
            TENANT,
            roleId,
            subjectType,
            subjectId,
            grantedBy,
            java.sql.Timestamp.from(clock.instant()));
  }

  private void addToGroup(UUID groupId, UUID accountId) {
    store
        .jdbc()
        .update(
            "INSERT INTO group_member (id, tenant_id, group_id, account_id, created_at) VALUES (?,?,?,?,?)",
            UuidV7.next(),
            TENANT,
            groupId,
            accountId,
            java.sql.Timestamp.from(clock.instant()));
  }

  private static <T> T withActor(UUID actor, java.util.concurrent.Callable<T> work) {
    var auth =
        new AurigaAuthentication(
            new AurigaPrincipal(
                actor.toString(),
                TENANT,
                PrincipalType.USER,
                TokenContext.ADMIN,
                null,
                null,
                "member",
                List.of("owner"),
                Set.of(
                    "org.invitations.write",
                    "org.members.write",
                    "org.roles.grant",
                    "org.groups.write",
                    "org.tenant.write",
                    "org.members.offboard",
                    "org.roles.write",
                    "org.roles.read",
                    "org.groups.read",
                    "org.members.read",
                    "org.policies.read",
                    "org.policies.write",
                    "org.quotas.read",
                    "org.quotas.write",
                    "org.invitations.read"),
                clock.instant(),
                clock.instant(),
                UuidV7.next().toString()));
    SecurityContext ctx = SecurityContextHolder.createEmptyContext();
    ctx.setAuthentication(auth);
    SecurityContext previous = SecurityContextHolder.getContext();
    SecurityContextHolder.setContext(ctx);
    try {
      return work.call();
    } catch (Exception e) {
      if (e instanceof RuntimeException re) throw re;
      throw new RuntimeException(e);
    } finally {
      SecurityContextHolder.setContext(previous);
    }
  }

  private record Rec(String type, String subject, Object data) {}

  private static final class RecordingEvents implements EventPublisher {
    private final List<Rec> all = new CopyOnWriteArrayList<>();

    @Override
    public UUID publish(String type, String subject, Object data, String dataVersion) {
      all.add(new Rec(type, subject, data));
      return UuidV7.next();
    }

    List<Rec> ofType(String type) {
      return all.stream().filter(r -> r.type().equals(type)).toList();
    }

    void clear() {
      all.clear();
    }
  }
}
