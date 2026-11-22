package auriga.platform;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.persistence.CommonMigrations;
import auriga.testsupport.PostgresExtension;
import org.flywaydb.core.api.FlywayException;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;

/** PlatformCombinedConfiguration / PlatformCollabConfiguration 自建 Flyway 入口的 N-1 单测。 */
@ExtendWith(PostgresExtension.class)
class PlatformFlywayFutureTest {
  @Test
  void combinedMigrateToleratesFutureAndRejectsTamper() {
    var roles =
        PostgresExtension.serviceRoles(getClass(), "iam_future_owner", "iam_future_app", "iam");
    PlatformCombinedConfiguration.migrateSchema(
        "iam", roles.owner().dataSource(), "classpath:db/iam");

    var jdbc = new JdbcTemplate(roles.owner().dataSource());
    insertFuture(jdbc, "iam", "flyway_schema_history");
    assertDoesNotThrow(
        () ->
            PlatformCombinedConfiguration.migrateSchema(
                "iam", roles.owner().dataSource(), "classpath:db/iam"));

    jdbc.update("UPDATE iam.flyway_schema_history SET checksum = checksum + 1 WHERE version = '1'");
    assertThrows(
        FlywayException.class,
        () ->
            PlatformCombinedConfiguration.migrateSchema(
                "iam", roles.owner().dataSource(), "classpath:db/iam"));
  }

  @Test
  void collabMigrateToleratesFutureAndRejectsTamper() {
    var roles =
        PostgresExtension.serviceRoles(
            getClass(), "collab_future_owner", "collab_future_app", "collab");
    PlatformCollabConfiguration.migrateCollab(roles.owner().dataSource());

    var jdbc = new JdbcTemplate(roles.owner().dataSource());
    insertFuture(jdbc, "collab", "flyway_schema_history");
    assertDoesNotThrow(() -> PlatformCollabConfiguration.migrateCollab(roles.owner().dataSource()));

    jdbc.update(
        "UPDATE collab.flyway_schema_history SET checksum = checksum + 1 WHERE version = '1'");
    assertThrows(
        FlywayException.class,
        () -> PlatformCollabConfiguration.migrateCollab(roles.owner().dataSource()));
  }

  private static void insertFuture(JdbcTemplate jdbc, String schema, String table) {
    Integer rank =
        jdbc.queryForObject(
            "SELECT coalesce(max(installed_rank),0) FROM " + schema + "." + table, Integer.class);
    assertNotNull(rank);
    jdbc.update(
        "INSERT INTO "
            + schema
            + "."
            + table
            + " (installed_rank, version, description, type, script, checksum, installed_by,"
            + " installed_on, execution_time, success) VALUES (?,?,?,?,?,?,?,now(),?,true)",
        rank + 1,
        "999",
        "future",
        "SQL",
        "V999__future.sql",
        0,
        "test",
        0);
    // common 历史表也插一条未来版本，覆盖 CommonMigrations.migrate 路径
    Integer commonRank =
        jdbc.queryForObject(
            "SELECT coalesce(max(installed_rank),0) FROM "
                + schema
                + "."
                + CommonMigrations.HISTORY_TABLE,
            Integer.class);
    assertNotNull(commonRank);
    jdbc.update(
        "INSERT INTO "
            + schema
            + "."
            + CommonMigrations.HISTORY_TABLE
            + " (installed_rank, version, description, type, script, checksum, installed_by,"
            + " installed_on, execution_time, success) VALUES (?,?,?,?,?,?,?,now(),?,true)",
        commonRank + 1,
        "999",
        "future",
        "SQL",
        "V999__common_future.sql",
        0,
        "test",
        0);
  }
}
