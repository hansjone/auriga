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
    "${auriga.services.ops.enabled:true} && ('${auriga.service.schema:}' == 'ops' || (${auriga.platform.combined:false}))")
@Import(OpsController.class)
public class OpsRuntimeConfiguration {
  @Bean
  auriga.common.events.EventHandler opsInstanceEvents(OpsStore store) {
    return new OpsInstanceEvents(store);
  }

  @Bean
  OpsStore opsStore(ListableBeanFactory beans, Environment env) {
    DataSource ds;
    TenantTransactionManager tx;
    if (env.getProperty("auriga.platform.combined", Boolean.class, false)) {
      ds = beans.getBean("opsDataSource", DataSource.class);
      tx = beans.getBean("opsTransactionManager", TenantTransactionManager.class);
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
  OpsDeliveryWorker opsDeliveryWorker(OpsStore store) {
    return new OpsDeliveryWorker(store);
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
