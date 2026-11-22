package auriga.common.persistence;

import static org.junit.jupiter.api.Assertions.*;

import auriga.testsupport.PostgresExtension;
import org.flywaydb.core.Flyway;
import org.flywaydb.core.api.FlywayException;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;

/** CommonMigrations.migrate：库里有比代码更新的迁移记录时旧代码仍能启动；被改过的旧迁移仍拒绝。 */
@ExtendWith(PostgresExtension.class)
class CommonMigrationsFutureTest {
  @Test
  void toleratesFutureHistoryAndRejectsTamperedChecksum() {
    var role = PostgresExtension.serviceRole(getClass(), "common_future", "common_future");
    CommonMigrations.migrate(role.dataSource(), "common_future");
    // 幂等
    CommonMigrations.migrate(role.dataSource(), "common_future");

    var jdbc = new JdbcTemplate(role.dataSource());
    Integer rank =
        jdbc.queryForObject(
            "SELECT coalesce(max(installed_rank),0) FROM common_future."
                + CommonMigrations.HISTORY_TABLE,
            Integer.class);
    assertNotNull(rank);
    jdbc.update(
        "INSERT INTO common_future."
            + CommonMigrations.HISTORY_TABLE
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

    assertDoesNotThrow(() -> CommonMigrations.migrate(role.dataSource(), "common_future"));
    // 反证：同样配置但不忽略 future 时，这条未来记录会让校验失败——说明上面的通过靠的是
    // *:future，而不是测试数据没被识别为未来迁移。
    var strict =
        Flyway.configure()
            .dataSource(role.dataSource())
            .schemas("common_future")
            .defaultSchema("common_future")
            .table(CommonMigrations.HISTORY_TABLE)
            .locations(CommonMigrations.LOCATION)
            .ignoreMigrationPatterns("*:missing")
            .load();
    assertThrows(FlywayException.class, strict::validate);
    assertTrue(
        java.util.Arrays.stream(
                CommonMigrations.serviceFlyway(
                        Flyway.configure().dataSource(role.dataSource()).load())
                    .getConfiguration()
                    .getIgnoreMigrationPatterns())
            .anyMatch(
                p -> p.toString().equalsIgnoreCase(CommonMigrations.IGNORE_FUTURE_MIGRATIONS)),
        "serviceFlyway 必须保留 *:future");

    jdbc.update(
        "UPDATE common_future."
            + CommonMigrations.HISTORY_TABLE
            + " SET checksum = checksum + 1 WHERE version = '1'");
    assertThrows(
        FlywayException.class, () -> CommonMigrations.migrate(role.dataSource(), "common_future"));
  }
}
