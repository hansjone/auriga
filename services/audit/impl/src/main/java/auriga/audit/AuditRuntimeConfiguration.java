package auriga.audit;

import auriga.common.autoconfigure.AurigaProperties;
import auriga.common.events.*;
import auriga.common.observability.EventMetrics;
import auriga.common.persistence.TenantTransactionManager;
import auriga.common.security.*;
import com.zaxxer.hikari.HikariDataSource;
import io.micrometer.core.instrument.simple.SimpleMeterRegistry;
import io.nats.client.Connection;
import java.time.Clock;
import java.util.Set;
import javax.sql.DataSource;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.beans.factory.annotation.Qualifier;
import org.springframework.boot.autoconfigure.condition.*;
import org.springframework.context.annotation.*;
import org.springframework.core.annotation.Order;
import org.springframework.core.env.Environment;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.security.config.annotation.web.builders.HttpSecurity;
import org.springframework.security.oauth2.jwt.JwtDecoder;
import org.springframework.security.web.SecurityFilterChain;
import org.springframework.web.servlet.mvc.method.annotation.RequestMappingHandlerMapping;

/** Explicit dedicated audit credentials; never reuse another service's database role. */
@Configuration(proxyBeanMethods = false)
@ConditionalOnProperty(name = "auriga.audit.jdbc-url")
public class AuditRuntimeConfiguration {
  @Bean(name = "auditDataSource", destroyMethod = "close")
  DataSource auditDataSource(Environment env) {
    var ds = new HikariDataSource();
    ds.setJdbcUrl(env.getRequiredProperty("auriga.audit.jdbc-url"));
    ds.setUsername(env.getRequiredProperty("auriga.audit.username"));
    ds.setPassword(env.getProperty("auriga.audit.password", ""));
    ds.setMaximumPoolSize(3);
    return ds;
  }

  @Bean
  AuditStore auditStore(@Qualifier("auditDataSource") DataSource ds) {
    return new AuditStore(ds);
  }

  @Bean
  AuditController auditController(AuditStore store) {
    return new AuditController(store);
  }

  @Bean
  @ConditionalOnProperty(prefix = "auriga.events", name = "nats-url")
  NatsEventConsumer auditEventConsumer(
      Connection connection,
      AuditStore store,
      @Qualifier("auditDataSource") DataSource ds,
      AurigaProperties properties) {
    var handler =
        new EventHandler() {
          public String name() {
            return "records";
          }

          public Set<String> eventTypes() {
            return Set.of(EventEnvelope.AUDIT_TYPE);
          }

          public void handle(ReceivedEvent event) {
            store.accept(event);
          }
        };
    var processor =
        new InboxProcessor(
            new TenantTransactionManager(ds),
            new JdbcTemplate(ds),
            "audit",
            new EventMetrics(new SimpleMeterRegistry(), "audit"),
            Clock.systemUTC(),
            properties.getEvents().getMaxDeliveries(),
            EventProducers.of(null));
    return new NatsEventConsumer(
        connection,
        "audit",
        handler,
        processor,
        properties.getEvents().getAckWait(),
        properties.getEvents().getBaseBackoff());
  }

  @Bean
  @Order(2)
  @ConditionalOnWebApplication(type = ConditionalOnWebApplication.Type.SERVLET)
  SecurityFilterChain auditApiChain(
      HttpSecurity http,
      JwtDecoder decoder,
      ProblemSecurityHandlers handlers,
      AurigaProperties properties,
      ObjectProvider<RequestMappingHandlerMapping> mappings)
      throws Exception {
    http.securityMatcher("/api/audit/**");
    return AurigaWebSecurity.build(
        http,
        decoder,
        handlers,
        properties.getSecurity().getPublicPaths(),
        EndpointAuthorizationCheck.publicEndpoints(mappings));
  }
}
