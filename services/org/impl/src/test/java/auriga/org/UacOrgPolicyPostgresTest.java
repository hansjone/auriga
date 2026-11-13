package auriga.org;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.events.EventPublisher;
import auriga.common.persistence.*;
import auriga.testsupport.PostgresExtension;
import java.nio.file.*;
import java.sql.Timestamp;
import java.time.*;
import java.util.*;
import java.util.concurrent.*;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import org.junit.jupiter.api.io.TempDir;
import org.springframework.jdbc.core.JdbcTemplate;
import tools.jackson.databind.node.ObjectNode;

@ExtendWith(PostgresExtension.class)
class UacOrgPolicyPostgresTest {
  static OrgStore store;
  static OrgService org;
  static UacOrgPolicy policy;
  static final Clock CLOCK = Clock.fixed(Instant.parse("2026-01-01T00:00:00Z"), ZoneOffset.UTC);
  final UUID tenant = UUID.randomUUID(), provider = UUID.randomUUID(), actor = UUID.randomUUID();
  @TempDir Path temp;

  @BeforeEach
  void seedTenant() {
    store.tenant(
        tenant,
        () -> {
          store
              .jdbc()
              .update(
                  "INSERT INTO tenant(id,tenant_id,slug,display_name) VALUES (?,?,?,'UAC test tenant')",
                  tenant,
                  tenant,
                  tenant.toString());
          return null;
        });
  }

  @BeforeAll
  static void migrate() {
    var roles =
        PostgresExtension.serviceRoles(
            UacOrgPolicyPostgresTest.class, "uac_org_owner", "uac_org_app", "org");
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
    org = new OrgService(store, mock(EventPublisher.class), CLOCK);
    policy = new UacOrgPolicy(store, org, CLOCK);
  }

  ObjectNode record() {
    return OrgJson.object(
        "tenantId",
        tenant.toString(),
        "providerId",
        provider.toString(),
        "employee",
        "001aB",
        "verified",
        true,
        "verifiedBy",
        actor.toString(),
        "verifiedAt",
        CLOCK.instant().toString());
  }

  void provision(ObjectNode record) throws Exception {
    Path path = temp.resolve(UUID.randomUUID() + ".json");
    Files.writeString(path, OrgJson.MAPPER.writeValueAsString(List.of(record)));
    policy.provision(path);
  }

  UUID member(String status) {
    UUID id = UuidV7.next();
    store.tenant(
        tenant,
        () -> {
          store.insertMember(
              new OrgStore.MemberRow(
                  id,
                  "Verified member",
                  "shared@example.com",
                  "external",
                  status,
                  1,
                  CLOCK.instant(),
                  null,
                  null));
          return null;
        });
    return id;
  }

  UUID invite(String code, String status, int max) {
    UUID id = UuidV7.next();
    store.tenant(
        tenant,
        () -> {
          store
              .jdbc()
              .update(
                  "INSERT INTO invitation(id,tenant_id,code_hash,status,display_name,member_type,max_uses,expires_at,created_by) VALUES (?,?,?,?,'Verified invite','external',?,?,?)",
                  id,
                  tenant,
                  OrgService.hashCode(code),
                  status,
                  max,
                  Timestamp.from(CLOCK.instant().plusSeconds(300)),
                  actor);
          return null;
        });
    return id;
  }

  @Test
  void verifiedMappingRequiresExactProviderTenantAndActiveMemberAndPreservesZeroes()
      throws Exception {
    UUID account = member("active");
    assertTrue(store.tenant(tenant, () -> policy.resolve(provider, "001ab")).isEmpty());
    provision(record().put("accountId", account.toString()));
    assertEquals(
        account, store.tenant(tenant, () -> policy.resolve(provider, " 001Ab ")).orElseThrow());
    assertTrue(store.tenant(tenant, () -> policy.resolve(provider, "1ab")).isEmpty());
    assertTrue(store.tenant(tenant, () -> policy.resolve(UUID.randomUUID(), "001ab")).isEmpty());
    assertTrue(store.tenant(UUID.randomUUID(), () -> policy.resolve(provider, "001ab")).isEmpty());
    assertThrows(
        RuntimeException.class,
        () -> provision(record().put("accountId", member("active").toString())));
    store.tenant(
        tenant,
        () -> {
          store
              .jdbc()
              .update(
                  "UPDATE member SET status='disabled' WHERE tenant_id=? AND id=?",
                  tenant,
                  account);
          return null;
        });
    assertTrue(store.tenant(tenant, () -> policy.resolve(provider, "001ab")).isEmpty());
    assertTrue(store.tenant(tenant, () -> policy.member(account)).isEmpty());
  }

  @Test
  void invitationIsEmployeeBoundSingleUseAndIdempotentAcrossConcurrentRetries() throws Exception {
    String code = UUID.randomUUID().toString();
    UUID invitation = invite(code, "active", 1), account = UuidV7.next();
    provision(record().put("invitationId", invitation.toString()).put("purpose", "uac_login"));
    assertEquals(
        "common.forbidden",
        assertThrows(
                AurigaException.class,
                () ->
                    store.tenant(
                        tenant,
                        () ->
                            org.redeemInvitation(
                                code, account, "Forged", "shared@example.com", provider)))
            .errorCode()
            .code());
    assertFalse(store.tenant(tenant, () -> policy.consume(code, provider, "Other", account)));
    assertFalse(
        store.tenant(tenant, () -> policy.consume(code, UUID.randomUUID(), "001Ab", account)));
    assertFalse(
        TenantContext.call(
            UUID.randomUUID(), () -> policy.consume(code, provider, "001Ab", account)));
    try (var pool = Executors.newFixedThreadPool(2)) {
      var a =
          pool.submit(
              () -> store.tenant(tenant, () -> policy.consume(code, provider, "001Ab", account)));
      var b =
          pool.submit(
              () -> store.tenant(tenant, () -> policy.consume(code, provider, "001AB", account)));
      assertTrue(a.get(20, TimeUnit.SECONDS));
      assertTrue(b.get(20, TimeUnit.SECONDS));
    }
    assertFalse(
        store.tenant(tenant, () -> policy.consume(code, provider, "001Ab", UUID.randomUUID())));
    assertEquals(
        account, store.tenant(tenant, () -> policy.resolve(provider, "001ab")).orElseThrow());
    assertEquals(
        "external", store.tenant(tenant, () -> policy.member(account)).orElseThrow().memberType());
    store.tenant(
        tenant,
        () -> {
          assertEquals(
              1,
              store
                  .jdbc()
                  .queryForObject(
                      "SELECT uses FROM invitation WHERE tenant_id=? AND id=?",
                      Integer.class,
                      tenant,
                      invitation));
          return null;
        });
  }

  @Test
  void genericOrExpiredOrUnverifiedInvitationCannotAdmitAnEmployee() throws Exception {
    UUID account = UuidV7.next();
    String generic = UUID.randomUUID().toString();
    invite(generic, "active", 1);
    assertFalse(store.tenant(tenant, () -> policy.consume(generic, provider, "001Ab", account)));
    String expired = UUID.randomUUID().toString();
    UUID id = invite(expired, "expired", 1);
    provision(record().put("invitationId", id.toString()).put("purpose", "uac_login"));
    assertFalse(
        TenantContext.call(tenant, () -> policy.consume(expired, provider, "001Ab", account)));
    assertTrue(store.tenant(tenant, () -> store.member(account)).isEmpty());
    assertThrows(
        IllegalArgumentException.class,
        () ->
            provision(
                record().put("verified", false).put("accountId", member("active").toString())));
    assertThrows(
        IllegalArgumentException.class,
        () -> provision(record().put("purpose", "team_signup").put("invitationId", id.toString())));
  }
}
