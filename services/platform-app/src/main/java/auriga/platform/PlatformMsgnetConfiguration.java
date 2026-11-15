package auriga.platform;

import auriga.collab.app.*;
import auriga.collab.grpc.*;
import auriga.collab.network.local.LocalRegistry;
import auriga.common.persistence.TenantTransactionManager;
import java.time.Clock;
import javax.sql.DataSource;
import org.springframework.beans.factory.annotation.Qualifier;
import org.springframework.boot.autoconfigure.condition.ConditionalOnExpression;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.core.env.Environment;
import org.springframework.jdbc.core.JdbcTemplate;

/** Runtime message network with explicit collab role selection in a combined deployment. */
@Configuration(proxyBeanMethods = false)
@ConditionalOnExpression(
    "${auriga.platform.combined:false} && ${auriga.services.collab.enabled:false} && ${auriga.collab.network.auto-renew.enabled:false}")
public class PlatformMsgnetConfiguration {
  @Bean
  @ConditionalOnExpression("${auriga.services.workspace.enabled:false}")
  auriga.workspace.api.MsgnetNodeControl msgnetNodeControl(
      auriga.collab.api.NetworkRenewal renewal) {
    return new PlatformMsgnetNodeControl(renewal);
  }

  @Bean
  NetworkMailboxService networkMailboxService(
      @Qualifier("collabDataSource") DataSource source,
      @Qualifier("collabTransactionManager") TenantTransactionManager manager,
      NetworkSourceGrpcAuthority authority,
      NetworkSigningKeys keys,
      NetworkAuthorityClient iam) {
    return new NetworkMailboxService(
        new JdbcTemplate(source), manager, authority, keys, Clock.systemUTC(), iam);
  }

  @Bean
  LocalRegistry localRegistry(
      @Qualifier("collabDataSource") DataSource source,
      @Qualifier("collabTransactionManager") TenantTransactionManager manager,
      NetworkSourceGrpcAuthority authority,
      NetworkAuthorityClient iam) {
    return new LocalRegistry(new JdbcTemplate(source), manager, authority, iam, Clock.systemUTC());
  }

  @Bean
  NetworkMailboxWireService networkMailboxWireService(
      NetworkMailboxService service,
      LocalRegistry locals,
      @Qualifier("collabDataSource") DataSource source,
      @Qualifier("collabTransactionManager") TenantTransactionManager manager,
      NetworkSourceGrpcAuthority authority,
      NetworkSigningKeys keys,
      Environment env) {
    return new NetworkMailboxWireService(
        service,
        locals,
        new JdbcTemplate(source),
        manager,
        authority,
        keys,
        Clock.systemUTC(),
        env.getProperty("auriga.collab.network.broadcast-enabled", Boolean.class, false));
  }

  @Bean
  NetworkMailboxGrpcService networkMailboxGrpcService(NetworkMailboxWireService service) {
    return new NetworkMailboxGrpcService(service);
  }

  @Bean
  NetworkSourceMaintenance networkSourceMaintenance(
      @Qualifier("collabDataSource") DataSource source,
      @Qualifier("collabTransactionManager") TenantTransactionManager manager,
      NetworkMailboxWireService wire) {
    return new NetworkSourceMaintenance(new JdbcTemplate(source), manager, wire);
  }

  @Bean
  @ConditionalOnExpression("'${auriga.events.nats-url:}' != ''")
  auriga.common.events.OutboxForwarder collabOutboxForwarder(
      @Qualifier("collabDataSource") DataSource source,
      auriga.common.events.EventTransport transport,
      auriga.common.observability.EventMetrics metrics,
      Clock clock,
      Environment env) {
    return new auriga.common.events.OutboxForwarder(
        source,
        "collab",
        transport,
        metrics,
        clock,
        new auriga.common.events.OutboxForwarder.Settings(
            env.getProperty("auriga.events.batch-size", Integer.class, 100),
            env.getProperty(
                "auriga.events.poll-interval",
                java.time.Duration.class,
                java.time.Duration.ofMillis(500)),
            env.getProperty(
                "auriga.events.base-backoff",
                java.time.Duration.class,
                java.time.Duration.ofSeconds(1)),
            env.getProperty(
                "auriga.events.max-backoff",
                java.time.Duration.class,
                java.time.Duration.ofMinutes(5)),
            env.getProperty(
                "auriga.events.retention",
                java.time.Duration.class,
                java.time.Duration.ofDays(7))));
  }
}
