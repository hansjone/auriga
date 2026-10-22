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
import auriga.contracts.org.v1.*;
import auriga.iam.api.M3PermissionCatalog;
import auriga.org.api.PendingAccount;
import auriga.testsupport.PostgresExtension;
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

  private ObjectNode preflightInvitation(UUID owner, UUID provider) {
    return store.tenant(
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
                        provider == null ? List.of() : List.of(provider),
                        clock.instant().plusSeconds(3600),
                        1)));
  }

  @Test
  void invitationPreflightIsReadOnlyAndRedemptionStillWorks() {
    UUID owner = seedOwner(), provider = UuidV7.next(), account = UuidV7.next();
    var invitation = preflightInvitation(owner, provider);
    String code = invitation.path("code").asString();
    var hooks = new OrgIdentityPolicyHooks(svc);
    TenantContext.run(
        TENANT,
        () -> {
          assertTrue(hooks.validateInvitation(code, provider));
          assertTrue(hooks.validateInvitation(code, provider));
          assertFalse(hooks.validateInvitation(code, UuidV7.next()));
          store.tx(
              () -> {
                assertEquals(
                    0,
                    store
                        .jdbc()
                        .queryForObject(
                            "SELECT uses FROM invitation WHERE tenant_id=? AND id=?",
                            Integer.class,
                            TENANT,
                            UUID.fromString(invitation.path("id").asString())));
                return null;
              });
          assertTrue(
              hooks.consumeInvitation(
                  code,
                  provider,
                  OrgJson.object("accountId", account.toString(), "displayName", "Alice")));
          assertFalse(hooks.validateInvitation(code, provider));
        });
    assertTrue(store.tenant(TENANT, () -> store.member(account)).isPresent());
  }

  @Test
  void invitationPreflightRejectsInvalidStatesAndOtherTenant() {
    UUID owner = seedOwner(), provider = UuidV7.next();
    var hooks = new OrgIdentityPolicyHooks(svc);
    var active = preflightInvitation(owner, provider);
    String code = active.path("code").asString();
    assertFalse(hooks.validateInvitation(code, provider));
    TenantContext.run(
        UUID.randomUUID(), () -> assertFalse(hooks.validateInvitation(code, provider)));
    TenantContext.run(
        TENANT,
        () -> {
          assertFalse(hooks.validateInvitation("missing", provider));
          assertFalse(hooks.validateInvitation("", provider));
          assertFalse(hooks.validateInvitation(null, provider));
          for (String state : List.of("expired", "used", "revoked")) {
            var invitation = preflightInvitation(owner, provider);
            store.tx(
                () -> {
                  UUID id = UUID.fromString(invitation.path("id").asString());
                  if (state.equals("expired"))
                    store
                        .jdbc()
                        .update(
                            "UPDATE invitation SET expires_at=? WHERE tenant_id=? AND id=?",
                            java.sql.Timestamp.from(clock.instant()),
                            TENANT,
                            id);
                  else if (state.equals("used"))
                    store
                        .jdbc()
                        .update(
                            "UPDATE invitation SET uses=max_uses WHERE tenant_id=? AND id=?",
                            TENANT,
                            id);
                  else
                    store
                        .jdbc()
                        .update(
                            "UPDATE invitation SET status='revoked' WHERE tenant_id=? AND id=?",
                            TENANT,
                            id);
                  return null;
                });
            assertFalse(
                hooks.validateInvitation(invitation.path("code").asString(), provider), state);
          }
        });
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
  void m3CatalogRegistersExactMetadataAndBuiltinRolesWithoutGrantingExternalUsers() {
    UUID owner = seedOwner();
    store.tenant(
        TENANT,
        () -> {
          var items = svc.listPermissions().path("items");
          for (var permission : M3PermissionCatalog.all()) {
            var found =
                java.util.stream.StreamSupport.stream(items.spliterator(), false)
                    .filter(p -> p.path("key").asString().equals(permission.key()))
                    .findFirst()
                    .orElseThrow();
            assertEquals(permission.english(), found.path("description").path("en").asString());
            assertEquals(permission.chinese(), found.path("description").path("zh").asString());
            assertEquals(permission.sensitive(), found.path("sensitive").asBoolean());
          }
          for (var role :
              java.util.Map.of(
                      "owner",
                      OrgIds.ROLE_OWNER,
                      "admin",
                      OrgIds.ROLE_ADMIN,
                      "member",
                      OrgIds.ROLE_MEMBER,
                      "external",
                      OrgIds.ROLE_EXTERNAL)
                  .entrySet()) {
            UUID user = UuidV7.next();
            insertMember(user, "member");
            bindRole(role.getValue(), "member", user, owner);
            Set<String> permissions = Set.copyOf(svc.getEffectivePermissions(user).permissions());
            for (var permission : M3PermissionCatalog.all())
              assertEquals(
                  permission.builtinRoles().contains(role.getKey()),
                  permissions.contains(permission.key()),
                  role.getKey() + "/" + permission.key());
          }
          assertTrue(svc.getEffectivePermissions(owner).permissions().contains("org.tenant.write"));
          long before = items.size();
          assertEquals(before, svc.listPermissions().path("items").size());
          return null;
        });
    assertEquals(0, store.jdbc().queryForObject("SELECT count(*) FROM permission", Integer.class));
  }

  @Test
  void m3RoleCreationAndGrantCannotExceedActualGrantorPermissions() {
    UUID owner = seedOwner(), admin = UuidV7.next(), member = UuidV7.next();
    store.tenant(
        TENANT,
        () -> {
          insertMember(admin, "admin");
          insertMember(member, "member");
          bindRole(OrgIds.ROLE_ADMIN, "member", admin, owner);
          bindRole(OrgIds.ROLE_MEMBER, "member", member, owner);
          return null;
        });
    TenantContext.call(
        TENANT,
        () -> {
          withActor(
              admin,
              () -> {
                assertEquals(
                    "org.grant_exceeds_own",
                    assertThrows(
                            AurigaException.class,
                            () ->
                                svc.createRole(
                                    "escalation",
                                    OrgJson.object("en", "Escalation", "zh", "扩权"),
                                    null,
                                    List.of("resource.levels.admin")))
                        .errorCode()
                        .code());
                var result = svc.checkGrant(admin, List.of(OrgIds.ROLE_OWNER), null);
                assertFalse(result.allowed());
                assertTrue(result.exceeding().contains("resource.levels.admin"));
                return null;
              });
          withActor(
              member,
              () -> {
                for (var permission : M3PermissionCatalog.all()) {
                  if (!permission.adminOnly()) continue;
                  assertEquals(
                      "org.grant_exceeds_own",
                      assertThrows(
                              AurigaException.class,
                              () ->
                                  svc.createRole(
                                      "escalation",
                                      OrgJson.object("en", "Escalation", "zh", "扩权"),
                                      null,
                                      List.of(permission.key())))
                          .errorCode()
                          .code());
                }
                return null;
              });
          return null;
        });
  }

  @Test
  void instanceAiAuthorizationChecksActualMembershipAndTenant() {
    UUID owner = seedOwner();
    var resolver = new OrgPermissionResolver(svc);
    var request =
        GetEffectivePermissionsRequest.newBuilder()
            .setTenantId(TENANT.toString())
            .setAccountId(owner.toString())
            .build();
    assertEquals(
        "common.forbidden",
        assertThrows(AurigaException.class, () -> resolver.getEffectivePermissions(request))
            .errorCode()
            .code());
    TenantContext.call(
        TENANT,
        () -> {
          var response = resolver.getEffectivePermissions(request);
          assertEquals(MemberStatus.MEMBER_STATUS_ACTIVE, response.getStatus());
          assertTrue(response.getPermissionsList().contains("ai.models.invoke"));
          assertEquals(
              "common.not_found",
              assertThrows(
                      AurigaException.class,
                      () ->
                          resolver.getEffectivePermissions(
                              request.toBuilder().setAccountId(UuidV7.next().toString()).build()))
                  .errorCode()
                  .code());
          assertEquals(
              "common.forbidden",
              assertThrows(
                      AurigaException.class,
                      () ->
                          resolver.getEffectivePermissions(
                              request.toBuilder()
                                  .setTenantId(UUID.randomUUID().toString())
                                  .build()))
                  .errorCode()
                  .code());
          store.tx(
              () ->
                  store
                      .jdbc()
                      .update(
                          "UPDATE member SET status='disabled' WHERE tenant_id=? AND id=?",
                          TENANT,
                          owner));
          assertEquals(
              MemberStatus.MEMBER_STATUS_DISABLED,
              resolver.getEffectivePermissions(request).getStatus());
          return null;
        });
  }

  @Test
  void roleRequestsCannotSupplyServerOwnedPrivilegeMetadata() {
    var controller = new OrgAdminController(svc);
    for (String flag : List.of("adminOnly", "builtin")) {
      assertEquals(
          "common.validation_failed",
          assertThrows(
                  AurigaException.class, () -> controller.createRole(OrgJson.object(flag, true)))
              .errorCode()
              .code());
      assertEquals(
          "common.validation_failed",
          assertThrows(
                  AurigaException.class,
                  () -> controller.updateRole(OrgIds.ROLE_MEMBER, OrgJson.object(flag, true)))
              .errorCode()
              .code());
    }
    assertEquals(
        "org.role_builtin",
        assertThrows(
                AurigaException.class,
                () ->
                    svc.createRole(
                        "owner", OrgJson.object("en", "Fake", "zh", "伪造"), null, List.of()))
            .errorCode()
            .code());
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
            code, null, OrgJson.object("accountId", account.toString(), "displayName", "Dave")));
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
