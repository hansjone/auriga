package auriga.org;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.context.*;
import auriga.common.events.*;
import auriga.common.persistence.*;
import auriga.common.security.*;
import auriga.contracts.deploymentconfig.DeploymentAccess;
import auriga.testsupport.PostgresExtension;
import java.nio.file.*;
import java.time.*;
import java.util.*;
import java.util.function.Supplier;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;

@ExtendWith(PostgresExtension.class)
class OrgAuditPublicationTest {
  static final UUID TENANT = OrgIds.DEFAULT_TENANT;
  static final String TRACE = "00-12345678901234567890123456789012-1234567890123456-01";
  static OrgStore store;
  static OrgService service;
  UUID actor;
  UUID member;

  @BeforeAll
  static void setup() throws Exception {
    var role =
        PostgresExtension.serviceRole(OrgAuditPublicationTest.class, "org_audit_owner", "org");
    CommonMigrations.migrate(role.dataSource(), "org");
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(role.dataSource())
                .schemas("org")
                .locations("classpath:db/org")
                .load())
        .migrate();
    store =
        new OrgStore(
            new JdbcTemplate(role.dataSource()), new TenantTransactionManager(role.dataSource()));
    var events =
        new OutboxEventPublisher(
            store.jdbc(),
            "org",
            "org",
            new EventSchemas(),
            OrgJson.MAPPER,
            Clock.systemUTC(),
            null);
    var access =
        DeploymentAccess.load(
            Files.readString(Path.of("../../../contracts/deployment/fixtures/domain-proxy.json"))
                .replace("019a0000-0000-7000-8000-000000000001", TENANT.toString())
                .getBytes(java.nio.charset.StandardCharsets.UTF_8));
    service = new OrgService(store, events, Clock.systemUTC(), access);
  }

  @BeforeEach
  void seed() {
    actor = UuidV7.next();
    member = UuidV7.next();
    TenantContext.run(
        TENANT,
        () ->
            store.tx(
                () -> {
                  for (UUID id : List.of(actor, member))
                    store.insertMember(
                        new OrgStore.MemberRow(
                            id,
                            "alice",
                            "alice@example.com",
                            "member",
                            "active",
                            1,
                            Instant.now(),
                            null,
                            null));
                  store
                      .jdbc()
                      .update(
                          "INSERT INTO org.role_binding(id,tenant_id,role_id,subject_type,subject_id,created_at) SELECT ?,?,id,'member',?,now() FROM org.role WHERE tenant_id=? AND key='owner'",
                          UuidV7.next(),
                          TENANT,
                          actor,
                          TENANT);
                  return null;
                }));
  }

  <T> T request(Supplier<T> action) {
    var principal =
        new AurigaPrincipal(
            actor.toString(),
            TENANT,
            PrincipalType.USER,
            TokenContext.ADMIN,
            null,
            null,
            "member",
            List.of("owner"),
            Set.of("audit.events.read"),
            Instant.now(),
            Instant.now(),
            "audit-test");
    return TenantContext.call(
        TENANT,
        () ->
            RequestContext.start("org-audit", TRACE)
                .withClient("192.0.2.20", "prompt=must-not-appear")
                .withPrincipal(principal)
                .call(action::get));
  }

  List<String> audit(String action, UUID id) {
    return store.tx(
        () ->
            store
                .jdbc()
                .query(
                    "SELECT envelope::text FROM org.outbox_event WHERE event_type='audit.recorded' AND envelope::jsonb->'data'->>'action'=? AND envelope::jsonb->'data'->'target'->>'id'=?",
                    (rs, n) -> rs.getString(1),
                    action,
                    id.toString()));
  }

  @Test
  void quotaChangesAreAuditedWithoutValuesAndRollbackTogether() {
    request(
        () -> {
          service.putQuota("default", null, OrgJson.object("monthlyBudgetMicros", 100L));
          service.putQuota("member", member, OrgJson.object("monthlyBudgetMicros", 5L));
          assertEquals(1, audit("org.quota.update", TENANT).size());
          assertEquals(1, audit("org.quota.update", member).size());
          assertThrows(
              IllegalStateException.class,
              () ->
                  store.tx(
                      () -> {
                        service.putQuota(
                            "member", member, OrgJson.object("monthlyBudgetMicros", 9L));
                        throw new IllegalStateException("rollback");
                      }));
          assertEquals(5L, service.getQuota("member", member).path("monthlyBudgetMicros").asLong());
          assertEquals(1, audit("org.quota.update", member).size());
          assertThrows(
              IllegalStateException.class,
              () ->
                  store.tx(
                      () -> {
                        service.deleteQuota("member", member);
                        throw new IllegalStateException("rollback");
                      }));
          assertTrue(audit("org.quota.delete", member).isEmpty());
          service.deleteQuota("member", member);
          var records = audit("org.quota.delete", member);
          assertEquals(1, records.size());
          assertFalse(records.getFirst().contains("monthlyBudgetMicros"));
          assertEquals(
              actor.toString(),
              OrgJson.MAPPER
                  .readTree(records.getFirst())
                  .path("data")
                  .path("actor")
                  .path("id")
                  .asString());
          return null;
        });
  }

  @Test
  void memberMutationsCarryOnlySafeMetadata() {
    request(
        () -> {
          service.updateMember(member, "password=must-not-appear", null, null, 1L);
          service.disableMember(member, "token=must-not-appear");
          service.enableMember(member);
          service.offboardMember(member, null, "prompt=must-not-appear");
          for (String action : List.of("update", "disable", "enable", "offboard")) {
            var records = audit("org.member." + action, member);
            assertEquals(1, records.size());
            var envelope = OrgJson.MAPPER.readTree(records.getFirst());
            var data = envelope.path("data");
            assertEquals(actor.toString(), data.path("actor").path("id").asString());
            assertEquals("192.0.2.20", data.path("actor").path("ip").asString());
            assertTrue(envelope.path("traceparent").asString().startsWith(TRACE.substring(0, 36)));
            assertEquals("success", data.path("result").asString());
            assertFalse(records.getFirst().contains("must-not-appear"));
          }
          return null;
        });
  }

  @Test
  void invitationRedemptionIsAuditedOnceWithoutCode() {
    request(
        () -> {
          var invitation =
              service.createInvitation(
                  null,
                  "secret=must-not-appear",
                  "member",
                  List.of(OrgIds.ROLE_MEMBER),
                  List.of(),
                  List.of(),
                  Instant.now().plusSeconds(600),
                  3);
          UUID id = UUID.fromString(invitation.path("id").asString());
          var code = invitation.path("code").asString();
          UUID newcomer = UuidV7.next();
          service.redeemInvitation(code, newcomer, "alice", "alice@example.com", null);
          service.redeemInvitation(code, newcomer, "alice", "alice@example.com", null);
          service.revokeInvitation(id);
          for (String action : List.of("create", "redeem", "revoke")) {
            var records = audit("org.invitation." + action, id);
            assertEquals(1, records.size());
            assertFalse(records.getFirst().contains(code));
            assertFalse(records.getFirst().contains("must-not-appear"));
          }
          assertEquals(1, audit("org.member.create", newcomer).size());
          UUID binding =
              store.tx(
                  () ->
                      store
                          .jdbc()
                          .queryForObject(
                              "SELECT id FROM org.role_binding WHERE tenant_id=? AND role_id=? AND subject_id=?",
                              UUID.class,
                              TENANT,
                              OrgIds.ROLE_MEMBER,
                              newcomer));
          assertEquals(1, audit("org.role_binding.create", binding).size());
          return null;
        });
  }

  @Test
  void rolesAndBindingsHaveOneRecordPerMutation() {
    request(
        () -> {
          var role =
              service.createRole(
                  "audit-" + member,
                  OrgJson.object("en", "Fixture", "zh", "Fixture"),
                  null,
                  List.of());
          UUID id = UUID.fromString(role.path("id").asString());
          service.updateRole(id, OrgJson.object("en", "Updated"), null, null);
          var binding = service.createRoleBinding(id, "member", member);
          UUID bid = UUID.fromString(binding.path("id").asString());
          service.deleteRoleBinding(bid);
          UUID group =
              UUID.fromString(
                  service.createGroup("audit-" + member, null, null).path("id").asString());
          UUID groupBinding =
              UUID.fromString(service.createRoleBinding(id, "group", group).path("id").asString());
          service.deleteGroup(group);
          assertEquals(1, audit("org.role_binding.create", groupBinding).size());
          assertEquals(1, audit("org.role_binding.delete", groupBinding).size());
          UUID cascaded =
              UUID.fromString(
                  service.createRoleBinding(id, "member", member).path("id").asString());
          service.deleteRole(id);
          assertEquals(1, audit("org.role_binding.create", cascaded).size());
          assertEquals(1, audit("org.role_binding.delete", cascaded).size());
          for (String action : List.of("create", "update", "delete"))
            assertEquals(1, audit("org.role." + action, id).size());
          assertEquals(1, audit("org.role_binding.create", bid).size());
          assertEquals(1, audit("org.role_binding.delete", bid).size());
          return null;
        });
  }

  @Test
  void rollbackAndOutboxFailureCannotSeparateMutationFromAudit() {
    request(
        () -> {
          assertThrows(
              IllegalStateException.class,
              () ->
                  store.tx(
                      () -> {
                        service.updateMember(member, "rolled-back", null, null, 1L);
                        throw new IllegalStateException("rollback");
                      }));
          assertTrue(audit("org.member.update", member).isEmpty());
          var admin =
              new JdbcTemplate(
                  PostgresExtension.database(OrgAuditPublicationTest.class).dataSource());
          admin.execute(
              "ALTER TABLE org.outbox_event ADD CONSTRAINT audit_failure CHECK (event_type <> 'audit.recorded') NOT VALID");
          try {
            assertThrows(
                RuntimeException.class,
                () -> service.updateMember(member, "must-rollback", null, null, 1L));
          } finally {
            admin.execute("ALTER TABLE org.outbox_event DROP CONSTRAINT audit_failure");
          }
          assertEquals(1, store.tx(() -> store.member(member).orElseThrow().version()));
          assertTrue(audit("org.member.update", member).isEmpty());
          return null;
        });
  }

  @Test
  void delegatedServiceActorUsesTheStandardSystemType() {
    var principal =
        new AurigaPrincipal(
            "svc:iam",
            TenantContext.PLATFORM_TENANT,
            PrincipalType.INTERNAL_SERVICE,
            TokenContext.SERVICE,
            null,
            null,
            null,
            List.of(),
            Set.of(),
            Instant.now(),
            null,
            "service-audit");
    TenantContext.run(
        TENANT,
        () ->
            RequestContext.start("delegated", TRACE)
                .withPrincipal(principal)
                .run(() -> service.updateMember(member, "updated", null, null, 1L)));
    var records = TenantContext.call(TENANT, () -> audit("org.member.update", member));
    assertEquals(1, records.size());
    var envelope = OrgJson.MAPPER.readTree(records.getFirst());
    assertEquals(TENANT.toString(), envelope.path("tenantid").asString());
    assertEquals("system", envelope.path("data").path("actor").path("type").asString());
    assertEquals("svc:iam", envelope.path("data").path("actor").path("id").asString());
  }
}
