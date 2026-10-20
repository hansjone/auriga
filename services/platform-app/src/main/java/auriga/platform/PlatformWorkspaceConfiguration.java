package auriga.platform;

import auriga.common.context.TenantContext;
import auriga.common.events.*;
import auriga.common.observability.EventMetrics;
import auriga.common.persistence.*;
import auriga.contracts.org.v1.*;
import auriga.org.OrgService;
import auriga.org.web.model.WorkspacePolicy;
import auriga.workspace.api.WorkspaceInputs;
import com.zaxxer.hikari.HikariDataSource;
import java.time.Clock;
import java.time.Duration;
import java.util.UUID;
import javax.sql.DataSource;
import org.flywaydb.core.Flyway;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.beans.factory.annotation.Qualifier;
import org.springframework.boot.autoconfigure.condition.*;
import org.springframework.context.annotation.*;
import org.springframework.core.env.Environment;
import org.springframework.jdbc.core.JdbcTemplate;
import tools.jackson.databind.json.JsonMapper;

/** Workspace wiring only: independent migration/runtime roles, transactions and outbox. */
@Configuration(proxyBeanMethods = false)
@ConditionalOnExpression(
    "${auriga.platform.combined:false} && ${auriga.services.workspace.enabled:false}")
public class PlatformWorkspaceConfiguration {
  @Bean(destroyMethod = "close")
  DataSource workspaceDataSource(Environment env) {
    String url = env.getRequiredProperty("auriga.platform.workspace.jdbc-url");
    try (var migration = new HikariDataSource()) {
      migration.setJdbcUrl(url);
      migration.setUsername(
          env.getRequiredProperty("auriga.platform.workspace.migration-username"));
      migration.setPassword(
          env.getRequiredProperty("auriga.platform.workspace.migration-password"));
      // Flyway needs ≥2 connections (migrate + PG advisory lock); size 1 deadlocks for 30s.
      migration.setMaximumPoolSize(2);
      migration.setSchema("workspace");
      CommonMigrations.migrate(migration, "workspace");
      CommonMigrations.serviceFlyway(
              Flyway.configure()
                  .dataSource(migration)
                  .schemas("workspace")
                  .defaultSchema("workspace")
                  .locations("classpath:db/workspace")
                  .load())
          .migrate();
    }
    var ds = new HikariDataSource();
    ds.setJdbcUrl(url);
    ds.setUsername(env.getRequiredProperty("auriga.platform.workspace.username"));
    ds.setPassword(env.getRequiredProperty("auriga.platform.workspace.password"));
    ds.setSchema("workspace");
    ds.setMaximumPoolSize(20);
    ds.setPoolName("auriga-workspace");
    return ds;
  }

  @Bean
  TenantTransactionManager workspaceTransactionManager(
      @Qualifier("workspaceDataSource") DataSource ds) {
    return new TenantTransactionManager(ds);
  }

  @Bean
  EventPublisher workspaceEventPublisher(
      @Qualifier("workspaceDataSource") DataSource ds,
      Clock clock,
      Environment env,
      ObjectProvider<OutboxForwarder> forwarders) {
    return new OutboxEventPublisher(
        new JdbcTemplate(ds),
        "workspace",
        "workspace",
        env.getProperty("auriga.events.validate-schemas", Boolean.class, true)
            ? new EventSchemas()
            : null,
        JsonMapper.builder().build(),
        clock,
        () -> forwarders.forEach(OutboxForwarder::wakeUp));
  }

  @Bean
  @ConditionalOnExpression("'${auriga.events.nats-url:}' != ''")
  OutboxForwarder workspaceOutboxForwarder(
      @Qualifier("workspaceDataSource") DataSource ds,
      EventTransport transport,
      EventMetrics metrics,
      Clock clock,
      Environment env) {
    return new OutboxForwarder(
        ds,
        "workspace",
        transport,
        metrics,
        clock,
        new OutboxForwarder.Settings(
            env.getProperty("auriga.events.batch-size", Integer.class, 100),
            Duration.ofMillis(500),
            Duration.ofSeconds(1),
            Duration.ofMinutes(5),
            Duration.ofDays(7)));
  }

  @Bean
  WorkspaceInputs combinedWorkspaceInputs(OrgService org) {
    return new WorkspaceInputs() {
      @Override
      public Member member(UUID tenant, UUID owner) {
        return TenantContext.call(
            tenant,
            () -> {
              var json = org.memberJson(owner);
              return Member.newBuilder()
                  .setAccountId(owner.toString())
                  .setTenantId(tenant.toString())
                  .setDisplayName(json.path("displayName").asString())
                  .setStatus(
                      switch (json.path("status").asString()) {
                        case "active" -> MemberStatus.MEMBER_STATUS_ACTIVE;
                        case "disabled" -> MemberStatus.MEMBER_STATUS_DISABLED;
                        case "offboarded" -> MemberStatus.MEMBER_STATUS_OFFBOARDED;
                        default -> MemberStatus.MEMBER_STATUS_UNSPECIFIED;
                      })
                  .setMemberType(
                      "external".equals(json.path("memberType").asString())
                          ? MemberType.MEMBER_TYPE_EXTERNAL
                          : MemberType.MEMBER_TYPE_MEMBER)
                  .build();
            });
      }

      @Override
      public InstanceQuota quota(UUID tenant, UUID owner) {
        return TenantContext.call(
            tenant,
            () -> {
              var json = org.getEffectiveQuota(owner).path("instance");
              var quota = InstanceQuota.newBuilder();
              if (json.hasNonNull("cpuMillis")) quota.setCpuMillis(json.path("cpuMillis").asLong());
              if (json.hasNonNull("memoryMb")) quota.setMemoryMb(json.path("memoryMb").asLong());
              if (json.hasNonNull("diskGb")) quota.setDiskGb(json.path("diskGb").asLong());
              if (json.hasNonNull("maxInstances"))
                quota.setMaxInstances(json.path("maxInstances").asLong());
              quota.setAlwaysOnAllowed(json.path("alwaysOnAllowed").asBoolean());
              return quota.build();
            });
      }

      @Override
      public WorkspacePolicy policy(UUID tenant) {
        return TenantContext.call(
            tenant,
            () ->
                JsonMapper.builder()
                    .build()
                    .treeToValue(org.getPolicy("workspace").path("value"), WorkspacePolicy.class));
      }
    };
  }
}
