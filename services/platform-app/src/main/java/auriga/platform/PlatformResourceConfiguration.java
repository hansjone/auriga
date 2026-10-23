package auriga.platform;

import auriga.common.context.TenantContext;
import auriga.common.persistence.CommonMigrations;
import auriga.common.persistence.TenantTransactionManager;
import auriga.contracts.org.v1.*;
import auriga.org.OrgService;
import auriga.resource.api.ResourceAccessInputs;
import com.zaxxer.hikari.HikariDataSource;
import java.util.UUID;
import javax.sql.DataSource;
import org.flywaydb.core.Flyway;
import org.springframework.beans.factory.annotation.Qualifier;
import org.springframework.boot.autoconfigure.condition.ConditionalOnExpression;
import org.springframework.context.annotation.*;
import org.springframework.core.env.Environment;
import org.springframework.jdbc.core.JdbcTemplate;

/** Resource-only combined wiring; org reads stay behind its service boundary and transaction. */
@Configuration(proxyBeanMethods = false)
@ConditionalOnExpression(
    "${auriga.platform.combined:false} && ${auriga.services.resource.enabled:false}")
public class PlatformResourceConfiguration {
  @Bean(destroyMethod = "close")
  DataSource resourceDataSource(Environment env) {
    String url = env.getRequiredProperty("auriga.platform.resource.jdbc-url");
    try (var migration = new HikariDataSource()) {
      migration.setJdbcUrl(url);
      migration.setUsername(env.getRequiredProperty("auriga.platform.resource.migration-username"));
      migration.setPassword(env.getRequiredProperty("auriga.platform.resource.migration-password"));
      migration.setMaximumPoolSize(2);
      migration.setSchema("resource");
      rejectPrivilegedRole(migration, false);
      CommonMigrations.migrate(migration, "resource");
      CommonMigrations.serviceFlyway(
              Flyway.configure()
                  .dataSource(migration)
                  .schemas("resource")
                  .defaultSchema("resource")
                  .locations("classpath:db/resource")
                  .load())
          .migrate();
    }
    var runtime = new HikariDataSource();
    runtime.setJdbcUrl(url);
    runtime.setUsername(env.getRequiredProperty("auriga.platform.resource.username"));
    runtime.setPassword(env.getRequiredProperty("auriga.platform.resource.password"));
    runtime.setSchema("resource");
    runtime.setPoolName("auriga-resource");
    runtime.setMaximumPoolSize(10);
    try {
      rejectPrivilegedRole(runtime, true);
      return runtime;
    } catch (RuntimeException e) {
      runtime.close();
      throw e;
    }
  }

  private static void rejectPrivilegedRole(DataSource source, boolean runtime) {
    String ownsTables =
        runtime
            ? " OR EXISTS (SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='resource' AND pg_has_role(r.oid,c.relowner,'MEMBER') AND c.relkind IN ('r','p'))"
            : "";
    boolean privileged =
        Boolean.TRUE.equals(
            new JdbcTemplate(source)
                .queryForObject(
                    "SELECT r.rolsuper OR r.rolbypassrls"
                        + ownsTables
                        + " FROM pg_roles r WHERE r.rolname=current_user",
                    Boolean.class));
    if (privileged)
      throw new IllegalStateException(
          "Resource database roles must not bypass tenant isolation or run as the table owner");
  }

  @Bean
  TenantTransactionManager resourceTransactionManager(
      @Qualifier("resourceDataSource") DataSource source) {
    return new TenantTransactionManager(source);
  }

  @Bean
  ResourceAccessInputs combinedResourceInputs(OrgService org) {
    return new ResourceAccessInputs() {
      @Override
      public Member member(UUID tenantId, UUID accountId) {
        return TenantContext.call(
            tenantId,
            () -> {
              var value = org.memberJson(accountId);
              var member =
                  Member.newBuilder()
                      .setTenantId(tenantId.toString())
                      .setAccountId(value.path("id").asString())
                      .setStatus(status(value.path("status").asString()))
                      .setMemberType(type(value.path("memberType").asString()));
              value.path("groupIds").forEach(id -> member.addGroupIds(id.asString()));
              return member.build();
            });
      }

      @Override
      public GetEffectivePermissionsResponse permissions(UUID tenantId, UUID accountId) {
        return TenantContext.call(
            tenantId,
            () -> {
              var value = org.getEffectivePermissions(accountId);
              return GetEffectivePermissionsResponse.newBuilder()
                  .setStatus(status(value.status()))
                  .setMemberType(type(value.memberType()))
                  .addAllPermissions(value.permissions())
                  .build();
            });
      }

      @Override
      public boolean allowPersonalPlugins(UUID tenantId) {
        return TenantContext.call(
            tenantId,
            () -> {
              var value = org.getPolicy("workspace").path("value").path("allowPersonalPlugins");
              if (!value.isBoolean())
                throw new IllegalStateException("Invalid workspace personal-plugin policy");
              return value.asBoolean();
            });
      }
    };
  }

  private static MemberStatus status(String value) {
    return switch (value) {
      case "active" -> MemberStatus.MEMBER_STATUS_ACTIVE;
      case "disabled" -> MemberStatus.MEMBER_STATUS_DISABLED;
      case "offboarded" -> MemberStatus.MEMBER_STATUS_OFFBOARDED;
      default -> MemberStatus.MEMBER_STATUS_UNSPECIFIED;
    };
  }

  private static MemberType type(String value) {
    return switch (value) {
      case "member" -> MemberType.MEMBER_TYPE_MEMBER;
      case "external" -> MemberType.MEMBER_TYPE_EXTERNAL;
      default -> MemberType.MEMBER_TYPE_UNSPECIFIED;
    };
  }
}
