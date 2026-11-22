package auriga.audit;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.persistence.CommonMigrations;
import auriga.testsupport.PostgresExtension;
import org.flywaydb.core.api.FlywayException;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;

/** AuditStore 自建 Flyway 入口：容忍未来迁移，拒绝被改过的旧迁移。 */
@ExtendWith(PostgresExtension.class)
class AuditStoreFutureTest {
  @Test
  void toleratesFutureHistoryAndRejectsTamperedChecksum() {
    var ds = PostgresExtension.serviceRole(getClass(), "audit_future", "audit").dataSource();
    new AuditStore(ds);

    var jdbc = new JdbcTemplate(ds);
    Integer rank =
        jdbc.queryForObject(
            "SELECT coalesce(max(installed_rank),0) FROM audit.flyway_schema_history",
            Integer.class);
    assertNotNull(rank);
    jdbc.update(
        "INSERT INTO audit.flyway_schema_history"
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
    Integer commonRank =
        jdbc.queryForObject(
            "SELECT coalesce(max(installed_rank),0) FROM audit." + CommonMigrations.HISTORY_TABLE,
            Integer.class);
    assertNotNull(commonRank);
    jdbc.update(
        "INSERT INTO audit."
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

    assertDoesNotThrow(() -> new AuditStore(ds));

    jdbc.update(
        "UPDATE audit.flyway_schema_history SET checksum = checksum + 1 WHERE version = '1'");
    assertThrows(FlywayException.class, () -> new AuditStore(ds));
  }
}
