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
@org.springframework.boot.autoconfigure.condition.ConditionalOnExpression(
    "${auriga.services.audit.enabled:true} && '${auriga.audit.jdbc-url:}' != ''")
public class AuditRuntimeConfiguration {
  @Bean(name = "auditDataSource", destroyMethod = "close")
  DataSource auditDataSource(Environment env) {
    String url = env.getRequiredProperty("auriga.audit.jdbc-url");
    try (var owner = new HikariDataSource()) {
      owner.setJdbcUrl(url);
      owner.setUsername(env.getRequiredProperty("auriga.audit.migration-username"));
      owner.setPassword(env.getRequiredProperty("auriga.audit.migration-password"));
      owner.setMaximumPoolSize(2);
      rejectPrivilegedRole(owner, false);
      new AuditStore(owner);
    }
    var ds = new HikariDataSource();
    ds.setJdbcUrl(url);
    ds.setUsername(env.getRequiredProperty("auriga.audit.username"));
    ds.setPassword(env.getRequiredProperty("auriga.audit.password"));
    ds.setMaximumPoolSize(3);
    ds.setPoolName("auriga-audit");
    try {
      rejectPrivilegedRole(ds, true);
      return ds;
    } catch (RuntimeException ex) {
      ds.close();
      throw ex;
    }
  }

  private static void rejectPrivilegedRole(DataSource ds, boolean runtime) {
    String owns =
        runtime
            ? " OR EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='audit' AND pg_has_role(r.oid,c.relowner,'MEMBER') AND c.relkind IN ('r','p'))"
            : "";
    if (Boolean.TRUE.equals(
        new JdbcTemplate(ds)
            .queryForObject(
                "SELECT r.rolsuper OR r.rolbypassrls"
                    + owns
                    + " FROM pg_roles r WHERE r.rolname=current_user",
                Boolean.class)))
      throw new IllegalStateException(
          "Audit database roles must not bypass isolation or run as the table owner");
  }

  @Bean
  AuditStore auditStore(@Qualifier("auditDataSource") DataSource ds) {
    return new AuditStore(ds, false);
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
      AuditStore store,
      ObjectProvider<RequestMappingHandlerMapping> mappings)
      throws Exception {
    http.securityMatcher("/api/audit/**");
    http.addFilterBefore(
        new AuditDeniedFilter(store),
        org.springframework.security.oauth2.server.resource.web.authentication
            .BearerTokenAuthenticationFilter.class);
    return AurigaWebSecurity.build(
        http,
        decoder,
        handlers,
        properties.getSecurity().getPublicPaths(),
        EndpointAuthorizationCheck.publicEndpoints(mappings));
  }
}
