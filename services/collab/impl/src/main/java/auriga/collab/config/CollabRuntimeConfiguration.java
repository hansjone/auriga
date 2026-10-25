package auriga.collab.config;

import auriga.collab.api.CollabAdmission;
import auriga.collab.app.CollabService;
import auriga.collab.app.NetworkMailboxService;
import auriga.collab.grpc.CollabGrpcService;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.persistence.TenantTransactionManager;
import javax.sql.DataSource;
import org.springframework.boot.autoconfigure.condition.ConditionalOnExpression;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.jdbc.core.JdbcTemplate;

/** Standalone collab schema only; combined datasource wiring is deliberately not invented. */
@Configuration(proxyBeanMethods = false)
@ConditionalOnExpression(
    "${auriga.services.collab.enabled:true} && '${auriga.service.schema:}' == 'collab'")
public class CollabRuntimeConfiguration {
  @Bean
  @org.springframework.boot.autoconfigure.condition.ConditionalOnProperty(
      prefix = "auriga.collab.control-plane",
      name = "enabled",
      havingValue = "false",
      matchIfMissing = true)
  CollabAdmission collabAdmission() {
    return (actor, operation) -> {
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    };
  }

  @Bean
  CollabService collabService(
      DataSource source, TenantTransactionManager manager, CollabAdmission admission) {
    return new CollabService(new JdbcTemplate(source), manager, admission);
  }

  @Bean
  NetworkMailboxService networkMailboxService(DataSource source, TenantTransactionManager manager) {
    return new NetworkMailboxService(new JdbcTemplate(source), manager);
  }

  @Bean
  CollabGrpcService collabGrpcService(CollabService service) {
    return new CollabGrpcService(service);
  }
}
