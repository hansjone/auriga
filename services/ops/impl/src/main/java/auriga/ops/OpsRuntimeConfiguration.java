package auriga.ops;

import auriga.common.persistence.TenantTransactionManager;
import auriga.common.security.*;
import javax.sql.DataSource;
import org.springframework.beans.factory.ListableBeanFactory;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.boot.autoconfigure.condition.*;
import org.springframework.context.annotation.*;
import org.springframework.core.annotation.Order;
import org.springframework.core.env.Environment;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.security.config.annotation.web.builders.HttpSecurity;
import org.springframework.security.oauth2.jwt.JwtDecoder;
import org.springframework.security.web.SecurityFilterChain;
import org.springframework.web.servlet.mvc.method.annotation.RequestMappingHandlerMapping;

@Configuration(proxyBeanMethods = false)
@ConditionalOnExpression(
    "${auriga.services.ops.enabled:true} && ('${auriga.service.schema:}' == 'ops' || (${auriga.platform.combined:false} && '${auriga.platform.ops.jdbc-url:}' != ''))")
@Import(OpsController.class)
public class OpsRuntimeConfiguration {
  @Bean
  @ConditionalOnExpression("'${auriga.service.schema:}' == 'ops'")
  auriga.common.events.EventHandler opsInstanceEvents(OpsStore store) {
    return new OpsInstanceEvents(store);
  }

  @Bean
  OpsStore opsStore(ListableBeanFactory beans, Environment env) {
    DataSource ds;
    TenantTransactionManager tx;
    if (env.getProperty("auriga.platform.combined", Boolean.class, false)) {
      ds =
          new org.springframework.jdbc.datasource.DriverManagerDataSource(
              env.getRequiredProperty("auriga.platform.ops.jdbc-url"),
              env.getRequiredProperty("auriga.platform.ops.username"),
              env.getProperty("auriga.platform.ops.password", ""));
      auriga.common.persistence.CommonMigrations.migrate(ds, "ops");
      auriga.common.persistence.CommonMigrations.serviceFlyway(
              org.flywaydb.core.Flyway.configure()
                  .dataSource(ds)
                  .schemas("ops")
                  .locations("classpath:db/ops")
                  .load())
          .migrate();
      tx = new TenantTransactionManager(ds);
    } else {
      ds = beans.getBean(DataSource.class);
      tx = beans.getBean(TenantTransactionManager.class);
    }
    return new OpsStore(
        new JdbcTemplate(ds),
        tx,
        new OpsSecrets(env.getProperty("auriga.ops.notification-key", "")),
        new OpsWebhook());
  }

  @Bean
  @Order(1)
  @ConditionalOnWebApplication(type = ConditionalOnWebApplication.Type.SERVLET)
  SecurityFilterChain opsApiChain(
      HttpSecurity http,
      JwtDecoder decoder,
      ProblemSecurityHandlers handlers,
      auriga.common.autoconfigure.AurigaProperties properties,
      ObjectProvider<RequestMappingHandlerMapping> mappings)
      throws Exception {
    http.securityMatcher("/api/ops/v1/**");
    return AurigaWebSecurity.build(
        http,
        decoder,
        handlers,
        properties.getSecurity().getPublicPaths(),
        EndpointAuthorizationCheck.publicEndpoints(mappings));
  }
}
