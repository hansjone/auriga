package auriga.platform;

import auriga.collab.api.CollabAdmission;
import auriga.collab.app.CollabService;
import auriga.collab.grpc.CollabGrpcService;
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

/** Explicit combined collab opt-in with separate migration and runtime database roles. */
@Configuration(proxyBeanMethods = false)
@ConditionalOnExpression(
    "${auriga.platform.combined:false} && ${auriga.services.collab.enabled:false}")
public class PlatformCollabConfiguration {
  @Bean(destroyMethod = "close")
  DataSource collabDataSource(Environment env) {
    String prefix = "auriga.platform.collab.";
    String url = env.getRequiredProperty(prefix + "jdbc-url");
    try (var migration = new HikariDataSource()) {
      migration.setJdbcUrl(url);
      migration.setUsername(env.getRequiredProperty(prefix + "migration-username"));
      migration.setPassword(env.getRequiredProperty(prefix + "migration-password"));
      migration.setMaximumPoolSize(2);
      migration.setSchema("collab");
      rejectPrivilegedRole(migration, false);
      CommonMigrations.migrate(migration, "collab");
      CommonMigrations.serviceFlyway(
              Flyway.configure()
                  .dataSource(migration)
                  .schemas("collab")
                  .defaultSchema("collab")
                  .locations("classpath:db/collab")
                  .load())
          .migrate();
    }
    var runtime = new HikariDataSource();
    runtime.setJdbcUrl(url);
    runtime.setUsername(env.getRequiredProperty(prefix + "username"));
    runtime.setPassword(env.getRequiredProperty(prefix + "password"));
    runtime.setSchema("collab");
    runtime.setMaximumPoolSize(10);
    runtime.setPoolName("auriga-collab");
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
            ? " OR EXISTS (SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='collab' AND pg_has_role(r.oid,c.relowner,'MEMBER') AND c.relkind IN ('r','p'))"
            : "";
    if (Boolean.TRUE.equals(
        new JdbcTemplate(source)
            .queryForObject(
                "SELECT r.rolsuper OR r.rolbypassrls"
                    + ownsTables
                    + " FROM pg_roles r WHERE r.rolname=current_user",
                Boolean.class)))
      throw new IllegalStateException("Collab database roles must preserve tenant isolation");
  }

  @Bean
  TenantTransactionManager collabTransactionManager(
      @Qualifier("collabDataSource") DataSource source) {
    return new TenantTransactionManager(source);
  }

  @Bean
  CollabService collabService(
      @Qualifier("collabDataSource") DataSource source,
      @Qualifier("collabTransactionManager") TenantTransactionManager manager,
      CollabAdmission admission) {
    return new CollabService(new JdbcTemplate(source), manager, admission);
  }

  @Bean
  CollabGrpcService collabGrpcService(CollabService service) {
    return new CollabGrpcService(service);
  }
}
