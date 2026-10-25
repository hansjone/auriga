package auriga.collab.config;

import auriga.collab.api.CollabAdmission;
import auriga.collab.app.CollabService;
import auriga.collab.app.NetworkMailboxService;
import auriga.collab.app.NetworkMailboxWireService;
import auriga.collab.app.NetworkSigningKeys;
import auriga.collab.grpc.CollabGrpcService;
import auriga.collab.grpc.NetworkAuthorityClient;
import auriga.collab.grpc.NetworkMailboxGrpcService;
import auriga.collab.grpc.NetworkSourceGrpcAuthority;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.persistence.TenantTransactionManager;
import java.nio.file.Path;
import java.time.Clock;
import java.time.Instant;
import javax.sql.DataSource;
import org.springframework.boot.autoconfigure.condition.ConditionalOnExpression;
import org.springframework.boot.context.properties.bind.Bindable;
import org.springframework.boot.context.properties.bind.Binder;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.core.env.Environment;
import org.springframework.jdbc.core.JdbcTemplate;

/** Standalone collab schema only; combined datasource wiring is deliberately not invented. */
@Configuration(proxyBeanMethods = false)
@ConditionalOnExpression(
    "${auriga.services.collab.enabled:true} && '${auriga.service.schema:}' == 'collab'")
public class CollabRuntimeConfiguration {
  @Bean
  @org.springframework.boot.autoconfigure.condition.ConditionalOnMissingBean(
      NetworkSigningKeys.class)
  NetworkSigningKeys networkSigningKeys(Environment env) {
    String stateFile = env.getRequiredProperty("auriga.collab.network.key-state-file");
    if (stateFile.isBlank())
      throw new IllegalArgumentException("Network key state storage is required");
    var configured =
        Binder.get(env)
            .bind("auriga.collab.network.signing-keys", Bindable.listOf(SigningKey.class))
            .orElseThrow(() -> new IllegalArgumentException("Network signing keys are required"));
    if (configured.isEmpty() || configured.size() > 128)
      throw new IllegalArgumentException("Invalid signing key count");
    var keys = new NetworkSigningKeys(Clock.systemUTC(), 1, Path.of(stateFile));
    try {
      for (var key : configured)
        keys.addFromFiles(
            key.realm(),
            key.purpose(),
            key.kid(),
            key.publicFile(),
            key.privateFile(),
            Instant.parse(key.notBefore()),
            Instant.parse(key.notAfter()),
            key.state());
      return keys;
    } catch (RuntimeException e) {
      keys.close();
      throw e;
    }
  }

  public record SigningKey(
      String realm,
      String purpose,
      String kid,
      Path publicFile,
      Path privateFile,
      String notBefore,
      String notAfter,
      NetworkSigningKeys.State state) {}

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
      NetworkSourceGrpcAuthority authority,
      NetworkSigningKeys keys,
      NetworkAuthorityClient iam) {
    Clock clock = Clock.systemUTC();
    return new NetworkMailboxService(
        new JdbcTemplate(source), manager, authority, keys, clock, iam);
  }

  @Bean
  NetworkMailboxWireService networkMailboxWireService(
      NetworkMailboxService service,
      DataSource source,
      TenantTransactionManager manager,
      NetworkSourceGrpcAuthority authority,
      NetworkSigningKeys keys) {
    return new NetworkMailboxWireService(
        service, new JdbcTemplate(source), manager, authority, keys, Clock.systemUTC());
  }

  @Bean
  NetworkMailboxGrpcService networkMailboxGrpcService(NetworkMailboxWireService service) {
    return new NetworkMailboxGrpcService(service);
  }

  @Bean
  CollabGrpcService collabGrpcService(CollabService service) {
    return new CollabGrpcService(service);
  }
}
