package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.persistence.*;
import auriga.testsupport.PostgresExtension;
import java.time.*;
import java.util.*;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;

@ExtendWith(PostgresExtension.class)
class UacRetentionMigrationPostgresTest {
  @Test
  void upgradeBackfillsExistingTenantsAndRestoresForceRls() {
    var roles =
        PostgresExtension.serviceRoles(getClass(), "retention_owner", "retention_app", "iam");
    CommonMigrations.migrate(roles.owner().dataSource(), "iam");
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas("iam")
                .locations("classpath:db/iam")
                .target("16")
                .load())
        .migrate();
    var jdbc = new JdbcTemplate(roles.runtime().dataSource());
    var store = new IamStore(jdbc, new TenantTransactionManager(roles.runtime().dataSource()));
    var tenants = List.of(UUID.randomUUID(), UUID.randomUUID());
    for (UUID tenant : tenants)
      store.tenant(
          tenant,
          () -> {
            String provider = UUID.randomUUID().toString();
            store.insert("idp_provider", provider, Json.object("providerId", provider), null);
            store.insert(
                "uac_credential",
                "retired",
                Json.object(
                    "providerId",
                    provider,
                    "kind",
                    "credential",
                    "invalid",
                    true,
                    "retryDeadline",
                    Instant.now().minusSeconds(60).toString()),
                null);
            store.insert(
                "uac_job",
                "finished",
                Json.object("done", true, "nextAt", Instant.now().toString()),
                null);
            store.insert(
                "oauth_grant",
                "code",
                Json.object("expiresAt", Instant.now().plusSeconds(600).toString()),
                Instant.now().minusSeconds(60));
            return null;
          });
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas("iam")
                .locations("classpath:db/iam")
                .load())
        .migrate();
    for (UUID tenant : tenants)
      store.tenant(
          tenant,
          () -> {
            assertTrue(store.get("uac_credential", "retired", false).has("expiresAt"));
            assertTrue(store.get("uac_job", "finished", false).has("expiresAt"));
            assertEquals(
                1,
                jdbc.queryForObject(
                    "SELECT count(*) FROM iam.uac_credential WHERE retry_deadline IS NOT NULL AND expires_at IS NOT NULL",
                    Integer.class));
            assertEquals(
                1,
                jdbc.queryForObject(
                    "SELECT count(*) FROM iam.oauth_grant WHERE expires_at>now()", Integer.class));
            return null;
          });
    assertEquals(0, jdbc.queryForObject("SELECT count(*) FROM iam.uac_credential", Integer.class));
    var owner = new JdbcTemplate(roles.owner().dataSource());
    assertEquals(0, owner.queryForObject("SELECT count(*) FROM iam.uac_credential", Integer.class));
    assertEquals(
        3,
        owner.queryForObject(
            "SELECT count(*) FROM pg_class WHERE relnamespace='iam'::regnamespace AND relname IN ('uac_credential','uac_job','oauth_grant') AND relrowsecurity AND relforcerowsecurity",
            Integer.class));
  }
}
