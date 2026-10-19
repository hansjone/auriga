package auriga.platform;

import auriga.common.audit.AuditLogger;
import auriga.common.audit.OutboxAuditLogger;
import auriga.common.events.EventPublisher;
import auriga.common.events.EventSchemas;
import auriga.common.events.NatsEvents;
import auriga.common.events.OutboxEventPublisher;
import auriga.common.persistence.CommonMigrations;
import auriga.common.persistence.TenantTransactionManager;
import com.zaxxer.hikari.HikariDataSource;
import io.nats.client.Connection;
import java.time.Clock;
import java.time.Duration;
import javax.sql.DataSource;
import org.flywaydb.core.Flyway;
import org.springframework.beans.factory.annotation.Qualifier;
import org.springframework.boot.autoconfigure.condition.ConditionalOnExpression;
import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty;
import org.springframework.boot.autoconfigure.condition.ConditionalOnWebApplication;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.context.annotation.Primary;
import org.springframework.core.annotation.Order;
import org.springframework.core.env.Environment;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.security.config.annotation.web.builders.HttpSecurity;
import org.springframework.security.config.annotation.web.configurers.AbstractHttpConfigurer;
import org.springframework.security.config.http.SessionCreationPolicy;
import org.springframework.security.web.SecurityFilterChain;
import tools.jackson.databind.json.JsonMapper;

/**
 * Multi-schema assembly for M1 single-process deploy: separate DataSources / outboxes for iam and
 * org. Enable with {@code auriga.platform.combined=true}. Stores are created by iam/org runtime
 * configs via {@code @Qualifier("iamDataSource")} / {@code orgDataSource}.
 */
@Configuration(proxyBeanMethods = false)
@ConditionalOnProperty(prefix = "auriga.platform", name = "combined", havingValue = "true")
public class PlatformCombinedConfiguration {

  @Bean
  @Primary
  MutableClock platformClock() {
    return new MutableClock();
  }

  /** Expose the same instance as {@link Clock} for iam/org runtime beans. */
  @Bean
  Clock clock(MutableClock platformClock) {
    return platformClock;
  }

  @Bean(name = "iamDataSource", destroyMethod = "close")
  DataSource iamDataSource(Environment env) {
    DataSource ds =
        dataSource(
            env.getRequiredProperty("auriga.platform.iam.jdbc-url"),
            env.getRequiredProperty("auriga.platform.iam.username"),
            env.getProperty("auriga.platform.iam.password", ""));
    migrate("iam", ds, "classpath:db/iam");
    return ds;
  }

  @Bean(name = "orgDataSource", destroyMethod = "close")
  DataSource orgDataSource(Environment env) {
    DataSource ds =
        dataSource(
            env.getRequiredProperty("auriga.platform.org.jdbc-url"),
            env.getRequiredProperty("auriga.platform.org.username"),
            env.getProperty("auriga.platform.org.password", ""));
    migrate("org", ds, "classpath:db/org");
    return ds;
  }

  private static HikariDataSource dataSource(String url, String user, String password) {
    HikariDataSource ds = new HikariDataSource();
    ds.setJdbcUrl(url);
    ds.setUsername(user);
    ds.setPassword(password);
    ds.setMaximumPoolSize(5);
    ds.setPoolName("auriga-" + user);
    return ds;
  }

  private static void migrate(String schema, DataSource ds, String locations) {
    CommonMigrations.migrate(ds, schema);
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(ds)
                .schemas(schema)
                .defaultSchema(schema)
                .locations(locations)
                .load())
        .migrate();
  }

  @Bean
  @Primary
  TenantTransactionManager iamTransactionManager(@Qualifier("iamDataSource") DataSource ds) {
    return new TenantTransactionManager(ds);
  }

  @Bean(name = "orgTransactionManager")
  TenantTransactionManager orgTransactionManager(@Qualifier("orgDataSource") DataSource ds) {
    return new TenantTransactionManager(ds);
  }

  @Bean(name = "iamEventPublisher")
  @Primary
  EventPublisher iamEventPublisher(
      @Qualifier("iamDataSource") DataSource ds, Clock clock, Environment env) {
    boolean validate = env.getProperty("auriga.events.validate-schemas", Boolean.class, true);
    return new OutboxEventPublisher(
        new JdbcTemplate(ds),
        "iam",
        "iam",
        validate ? new EventSchemas() : null,
        JsonMapper.builder().build(),
        clock,
        () -> {});
  }

  @Bean(name = "orgEventPublisher")
  EventPublisher orgEventPublisher(
      @Qualifier("orgDataSource") DataSource ds, Clock clock, Environment env) {
    boolean validate = env.getProperty("auriga.events.validate-schemas", Boolean.class, true);
    return new OutboxEventPublisher(
        new JdbcTemplate(ds),
        "org",
        "org",
        validate ? new EventSchemas() : null,
        JsonMapper.builder().build(),
        clock,
        () -> {});
  }

  @Bean
  @Primary
  AuditLogger auditLogger(@Qualifier("iamEventPublisher") EventPublisher iamEvents) {
    return new OutboxAuditLogger(iamEvents);
  }

  @Bean(destroyMethod = "close")
  @ConditionalOnExpression("'${auriga.events.nats-url:}' != ''")
  Connection aurigaNatsConnection(Environment env) throws Exception {
    String url = env.getRequiredProperty("auriga.events.nats-url");
    Connection connection =
        io.nats.client.Nats.connect(NatsEvents.options(url, "auriga-platform", null, null, false));
    if (env.getProperty("auriga.events.ensure-streams", Boolean.class, true)) {
      NatsEvents.ensureStreams(
          connection,
          env.getProperty("auriga.events.duplicate-window", Duration.class, Duration.ofHours(1)));
    }
    return connection;
  }

  /** Health/error after service-scoped chains (iam/org matchers leave these unmatched). */
  @Bean
  @Order(50)
  @ConditionalOnWebApplication(type = ConditionalOnWebApplication.Type.SERVLET)
  SecurityFilterChain platformOpsChain(HttpSecurity http) throws Exception {
    http.securityMatcher("/actuator/**", "/error")
        .authorizeHttpRequests(a -> a.anyRequest().permitAll())
        .csrf(AbstractHttpConfigurer::disable)
        .sessionManagement(s -> s.sessionCreationPolicy(SessionCreationPolicy.STATELESS));
    return http.build();
  }
}
