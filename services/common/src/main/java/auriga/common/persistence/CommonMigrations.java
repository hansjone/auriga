package auriga.common.persistence;

import java.util.Objects;
import java.util.regex.Pattern;
import javax.sql.DataSource;
import org.flywaydb.core.Flyway;

/**
 * Applies common's own migrations (RLS helpers, outbox, processed_event, dead letters) into a
 * service schema. They use a separate history table so their versions never interleave with the
 * service's migrations; run them before the service's Flyway (the auto-configuration does this
 * through a {@code FlywayMigrationStrategy}).
 */
public final class CommonMigrations {
  public static final String LOCATION = "classpath:auriga/db/common";
  public static final String HISTORY_TABLE = "auriga_common_schema_history";

  /** N-1：库里可以有比当前代码更新的迁移记录（新版本留下的），旧进程仍须能启动；校验仍拒绝被改过的旧迁移。 */
  public static final String IGNORE_FUTURE_MIGRATIONS = "*:future";

  private static final Pattern SCHEMA = Pattern.compile("^[a-z][a-z0-9_]{0,62}$");

  private CommonMigrations() {}

  public static void migrate(DataSource dataSource, String schema) {
    Objects.requireNonNull(dataSource, "dataSource");
    requireSchemaName(schema);
    Flyway.configure()
        .dataSource(dataSource)
        .schemas(schema)
        .defaultSchema(schema)
        .createSchemas(true)
        .table(HISTORY_TABLE)
        .locations(LOCATION)
        .ignoreMigrationPatterns(IGNORE_FUTURE_MIGRATIONS)
        .load()
        .migrate();
  }

  /**
   * The service's own Flyway, adjusted so that a schema already holding common's objects counts as
   * "baseline 0": every service migration (V1 and up) still runs. Also keeps {@link
   * #IGNORE_FUTURE_MIGRATIONS} so N-1 回滚后旧镜像能忽略库里更新的迁移记录。
   */
  public static Flyway serviceFlyway(Flyway configured) {
    return Flyway.configure(configured.getConfiguration().getClassLoader())
        .configuration(configured.getConfiguration())
        .baselineOnMigrate(true)
        .baselineVersion("0")
        .baselineDescription("auriga common objects")
        .ignoreMigrationPatterns(IGNORE_FUTURE_MIGRATIONS)
        .load();
  }

  /** Schema names are interpolated into SQL; only plain lowercase identifiers are accepted. */
  public static String requireSchemaName(String schema) {
    if (schema == null || !SCHEMA.matcher(schema).matches()) {
      throw new IllegalArgumentException("Invalid schema name: " + schema);
    }
    return schema;
  }

  /** SQL for a service migration that enables tenant RLS on {@code table}. */
  public static String enableTenantRls(String schema, String table) {
    requireSchemaName(schema);
    requireSchemaName(table);
    return "SELECT " + schema + ".auriga_enable_tenant_rls('" + schema + "." + table + "')";
  }
}
