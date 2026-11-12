package auriga.org;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.context.TenantContext;
import auriga.common.persistence.CommonMigrations;
import auriga.common.persistence.TenantTransactionManager;
import auriga.iam.api.M3PermissionCatalog;
import auriga.testsupport.PostgresExtension;
import java.util.Arrays;
import java.util.Set;
import java.util.UUID;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;

@ExtendWith(PostgresExtension.class)
class OrgBuiltinPermissionsUpgradeTest {
  @Test
  void legacyBuiltinRolesGainCurrentPermissionsWithoutChangingCustomRoles() {
    assertTrue(M3PermissionCatalog.adminOnly("iam.network.manage"));
    assertEquals(
        Set.of("owner", "admin"),
        M3PermissionCatalog.all().stream()
            .filter(permission -> permission.key().equals("iam.network.manage"))
            .findFirst()
            .orElseThrow()
            .builtinRoles());
    var roles = PostgresExtension.serviceRoles(getClass(), "org_owner", "org_app", "org");
    CommonMigrations.migrate(roles.owner().dataSource(), "org");
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas("org")
                .locations("classpath:db/org")
                .load())
        .migrate();
    var store =
        new OrgStore(
            new JdbcTemplate(roles.runtime().dataSource()),
            new TenantTransactionManager(roles.runtime().dataSource()));
    TenantContext.run(
        OrgIds.DEFAULT_TENANT,
        () ->
            store.tx(
                () -> {
                  store
                      .jdbc()
                      .update(
                          "INSERT INTO role(id,tenant_id,key,name_en,name_zh,builtin,permissions) VALUES(?,?,'custom-support','Support','支持',false,ARRAY['org.members.read'])",
                          UUID.randomUUID(),
                          OrgIds.DEFAULT_TENANT);
                  new OrgService(
                          store,
                          org.mockito.Mockito.mock(auriga.common.events.EventPublisher.class),
                          java.time.Clock.systemUTC())
                      .initializeBuiltinPermissions();
                  for (String role : new String[] {"owner", "admin", "member", "external"}) {
                    String[] permissions =
                        store
                            .jdbc()
                            .queryForObject(
                                "SELECT permissions FROM role WHERE key=?",
                                (rs, n) -> (String[]) rs.getArray(1).getArray(),
                                role);
                    var actual = Set.copyOf(Arrays.asList(permissions));
                    assertTrue(actual.containsAll(M3PermissionCatalog.forRole(role)), role);
                    assertEquals(
                        Set.of("owner", "admin").contains(role),
                        actual.contains("iam.network.manage"),
                        role);
                  }
                  assertArrayEquals(
                      new String[] {"org.members.read"},
                      store
                          .jdbc()
                          .queryForObject(
                              "SELECT permissions FROM role WHERE key='custom-support'",
                              (rs, n) -> (String[]) rs.getArray(1).getArray()));
                  return null;
                }));
  }
}
