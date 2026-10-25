package auriga.collab.config;

import auriga.collab.api.CollabAdmission;
import auriga.collab.api.NetworkSourceAuthority;
import auriga.collab.app.CollabService;
import auriga.collab.app.NetworkMailboxService;
import auriga.collab.app.NetworkSigningKeys;
import auriga.collab.grpc.CollabGrpcService;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.persistence.TenantTransactionManager;
import auriga.iam.api.NetworkAuthority;
import java.time.Clock;
import javax.sql.DataSource;
import org.springframework.beans.factory.ObjectProvider;
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
  @org.springframework.boot.autoconfigure.condition.ConditionalOnMissingBean(
      NetworkSigningKeys.class)
  @org.springframework.boot.autoconfigure.condition.ConditionalOnProperty(
      prefix = "auriga.collab.network",
      name = "key-state-file")
  NetworkSigningKeys networkSigningKeys(
      @org.springframework.beans.factory.annotation.Value(
              "${auriga.collab.network.key-state-file:}")
          String stateFile) {
    if (stateFile.isBlank())
      throw new IllegalArgumentException("Network key state storage is required");
    return new NetworkSigningKeys(Clock.systemUTC(), 1, java.nio.file.Path.of(stateFile));
  }

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
  NetworkMailboxService networkMailboxService(
      DataSource source,
      TenantTransactionManager manager,
      ObjectProvider<NetworkSourceAuthority> authority,
      ObjectProvider<NetworkSigningKeys> keys,
      ObjectProvider<NetworkAuthority> iam) {
    Clock clock = Clock.systemUTC();
    return new NetworkMailboxService(
        new JdbcTemplate(source),
        manager,
        authority.getIfAvailable(),
        keys.getIfAvailable(() -> new NetworkSigningKeys(clock, 1)),
        clock,
        iam.getIfAvailable());
  }

  @Bean
  CollabGrpcService collabGrpcService(CollabService service) {
    return new CollabGrpcService(service);
  }
}
