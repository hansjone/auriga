package auriga.org;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.persistence.*;
import auriga.testsupport.PostgresExtension;
import java.sql.Timestamp;
import java.time.*;
import java.util.*;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;

@ExtendWith(PostgresExtension.class)
class UacInvitationMigrationPostgresTest {
  @Test
  void upgradeReleasesHistoricalReservationsAndRestoresForceRls() {
    var roles = PostgresExtension.serviceRoles(getClass(), "invite_owner", "invite_app", "org");
    CommonMigrations.migrate(roles.owner().dataSource(), "org");
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas("org")
                .locations("classpath:db/org")
                .target("4")
                .load())
        .migrate();
    var jdbc = new JdbcTemplate(roles.runtime().dataSource());
    var store = new OrgStore(jdbc, new TenantTransactionManager(roles.runtime().dataSource()));
    UUID tenant = UUID.randomUUID(), provider = UUID.randomUUID(), actor = UUID.randomUUID();
    store.tenant(
        tenant,
        () -> {
          jdbc.update(
              "INSERT INTO tenant(id,tenant_id,slug,display_name) VALUES (?,?,?,'Migration test')",
              tenant,
              tenant,
              tenant.toString());
          for (String kind : List.of("expired", "used", "live")) {
            UUID id = UUID.randomUUID();
            jdbc.update(
                "INSERT INTO invitation(id,tenant_id,code_hash,status,display_name,member_type,max_uses,expires_at,created_by) VALUES (?,?,?,?,'Migration test','external',1,?,?)",
                id,
                tenant,
                OrgService.hashCode(UUID.randomUUID().toString()),
                kind.equals("used") ? "used" : "active",
                Timestamp.from(Instant.now().plusSeconds(kind.equals("expired") ? -60 : 600)),
                actor);
            jdbc.update(
                "INSERT INTO uac_invitation_employee(id,tenant_id,invitation_id,provider_id,employee,purpose,verified_by,verified_at) VALUES (?,?,?,?,?,'uac_login',?,?)",
                UUID.randomUUID(),
                tenant,
                id,
                provider,
                kind,
                actor,
                Timestamp.from(Instant.now()));
          }
          return null;
        });
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas("org")
                .locations("classpath:db/org")
                .load())
        .migrate();
    store.tenant(
        tenant,
        () -> {
          assertEquals(
              List.of("live"),
              jdbc.queryForList(
                  "SELECT employee FROM uac_invitation_employee WHERE active", String.class));
          assertEquals(
              3,
              jdbc.queryForObject("SELECT count(*) FROM uac_invitation_employee", Integer.class));
          return null;
        });
    var owner = new JdbcTemplate(roles.owner().dataSource());
    assertEquals(
        0, owner.queryForObject("SELECT count(*) FROM org.uac_invitation_employee", Integer.class));
    assertEquals(
        2,
        owner.queryForObject(
            "SELECT count(*) FROM pg_class WHERE relnamespace='org'::regnamespace AND relname IN ('invitation','uac_invitation_employee') AND relrowsecurity AND relforcerowsecurity",
            Integer.class));
  }
}
