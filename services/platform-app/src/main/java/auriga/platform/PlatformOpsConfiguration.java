package auriga.platform;

import auriga.common.persistence.CommonMigrations;
import auriga.common.persistence.TenantTransactionManager;
import com.zaxxer.hikari.HikariDataSource;
import javax.sql.DataSource;
import org.flywaydb.core.Flyway;
import org.springframework.beans.factory.annotation.Qualifier;
import org.springframework.boot.autoconfigure.condition.ConditionalOnExpression;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.core.env.Environment;
import org.springframework.jdbc.core.JdbcTemplate;

/** Explicit combined ops opt-in with separate migration and runtime database roles. */
@Configuration(proxyBeanMethods = false)
@ConditionalOnExpression(
    "${auriga.platform.combined:false} && ${auriga.services.ops.enabled:false}")
public class PlatformOpsConfiguration {
  @Bean(destroyMethod = "close")
  DataSource opsDataSource(Environment env) {
    String prefix = "auriga.platform.ops.";
    String url = env.getRequiredProperty(prefix + "jdbc-url");
    try (var migration = new HikariDataSource()) {
      migration.setJdbcUrl(url);
      migration.setUsername(env.getRequiredProperty(prefix + "migration-username"));
      migration.setPassword(env.getRequiredProperty(prefix + "migration-password"));
      migration.setMaximumPoolSize(2);
      migration.setSchema("ops");
      rejectPrivilegedRole(migration, false);
      CommonMigrations.migrate(migration, "ops");
      CommonMigrations.serviceFlyway(
              Flyway.configure()
                  .dataSource(migration)
                  .schemas("ops")
                  .defaultSchema("ops")
                  .locations("classpath:db/ops")
                  .load())
          .migrate();
    }
    var runtime = new HikariDataSource();
    runtime.setJdbcUrl(url);
    runtime.setUsername(env.getRequiredProperty(prefix + "username"));
    runtime.setPassword(env.getRequiredProperty(prefix + "password"));
    runtime.setSchema("ops");
    runtime.setMaximumPoolSize(10);
    runtime.setPoolName("auriga-ops");
    try {
      rejectPrivilegedRole(runtime, true);
      return runtime;
    } catch (RuntimeException e) {
      runtime.close();
      throw e;
    }
  }

  private static void rejectPrivilegedRole(DataSource source, boolean runtime) {
    String ownsTables =
        runtime
            ? " OR EXISTS (SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='ops' AND pg_has_role(r.oid,c.relowner,'MEMBER') AND c.relkind IN ('r','p'))"
            : "";
    if (Boolean.TRUE.equals(
        new JdbcTemplate(source)
            .queryForObject(
                "SELECT r.rolsuper OR r.rolbypassrls"
                    + ownsTables
                    + " FROM pg_roles r WHERE r.rolname=current_user",
                Boolean.class)))
      throw new IllegalStateException("Ops database roles must preserve tenant isolation");
  }

  @Bean
  TenantTransactionManager opsTransactionManager(@Qualifier("opsDataSource") DataSource source) {
    return new TenantTransactionManager(source);
  }

  @Bean
  @org.springframework.boot.autoconfigure.condition.ConditionalOnExpression(
      "'${auriga.events.nats-url:}' != ''")
  auriga.common.events.NatsEventConsumer opsInstanceConsumer(
      @Qualifier("opsDataSource") DataSource source,
      @Qualifier("opsTransactionManager") TenantTransactionManager manager,
      @Qualifier("opsInstanceEvents") auriga.common.events.EventHandler handler,
      io.nats.client.Connection connection,
      auriga.common.observability.EventMetrics metrics,
      java.time.Clock clock) {
    var inbox =
        new auriga.common.events.InboxProcessor(
            manager,
            new JdbcTemplate(source),
            "ops",
            metrics,
            clock,
            5,
            auriga.common.events.EventProducers.of(java.util.Map.of()));
    return new auriga.common.events.NatsEventConsumer(
        connection,
        "ops",
        handler,
        inbox,
        java.time.Duration.ofSeconds(30),
        java.time.Duration.ofSeconds(1));
  }

  @Bean
  PlatformOpsNodeSignals opsNodeSignals(
      org.springframework.beans.factory.ObjectProvider<auriga.workspace.app.WorkspaceService>
          workspace,
      auriga.ops.api.OpsSignals signals) {
    return new PlatformOpsNodeSignals(workspace, signals);
  }
}
