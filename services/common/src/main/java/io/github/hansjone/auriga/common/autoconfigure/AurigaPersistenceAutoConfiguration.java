package io.github.hansjone.auriga.common.autoconfigure;

import com.baomidou.mybatisplus.extension.plugins.MybatisPlusInterceptor;
import com.baomidou.mybatisplus.extension.plugins.inner.TenantLineInnerInterceptor;
import io.github.hansjone.auriga.common.persistence.AurigaTenantLineHandler;
import io.github.hansjone.auriga.common.persistence.CommonMigrations;
import io.github.hansjone.auriga.common.persistence.TenantTransactionManager;
import java.util.Set;
import javax.sql.DataSource;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.beans.factory.SmartInitializingSingleton;
import org.springframework.boot.autoconfigure.AutoConfiguration;
import org.springframework.boot.autoconfigure.condition.ConditionalOnClass;
import org.springframework.boot.autoconfigure.condition.ConditionalOnMissingBean;
import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty;
import org.springframework.boot.autoconfigure.condition.ConditionalOnSingleCandidate;
import org.springframework.boot.flyway.autoconfigure.FlywayMigrationStrategy;
import org.springframework.boot.jdbc.autoconfigure.DataSourceAutoConfiguration;
import org.springframework.boot.jdbc.autoconfigure.DataSourceTransactionManagerAutoConfiguration;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.core.Ordered;
import org.springframework.core.annotation.Order;
import org.springframework.transaction.TransactionManager;

/**
 * Tenant isolation in the persistence layer: tenant-stamping transaction manager (RLS), the
 * MyBatis-Plus tenant line interceptor, and common migrations ahead of the service's Flyway.
 */
@AutoConfiguration(
    after = DataSourceAutoConfiguration.class,
    before = DataSourceTransactionManagerAutoConfiguration.class,
    beforeName = {
      "com.baomidou.mybatisplus.autoconfigure.MybatisPlusAutoConfiguration",
      "com.baomidou.mybatisplus.autoconfigure.MybatisPlusInnerInterceptorAutoConfiguration"
    })
@ConditionalOnSingleCandidate(DataSource.class)
public class AurigaPersistenceAutoConfiguration {
  @Bean
  @ConditionalOnMissingBean(TransactionManager.class)
  TenantTransactionManager transactionManager(DataSource dataSource) {
    return new TenantTransactionManager(dataSource);
  }

  @Configuration(proxyBeanMethods = false)
  @ConditionalOnClass(MybatisPlusInterceptor.class)
  static class MybatisPlus {
    /**
     * Collected by MyBatis-Plus into its interceptor chain; first so pagination sees the tenant.
     */
    @Bean
    @Order(Ordered.HIGHEST_PRECEDENCE)
    TenantLineInnerInterceptor aurigaTenantLineInterceptor(AurigaProperties properties) {
      return new TenantLineInnerInterceptor(
          new AurigaTenantLineHandler(
              Set.copyOf(properties.getPersistence().getNonTenantTables())));
    }

    /** UUID columns map to {@link java.util.UUID} everywhere. */
    @Bean
    com.baomidou.mybatisplus.autoconfigure.ConfigurationCustomizer aurigaUuidTypeHandler() {
      return configuration ->
          configuration
              .getTypeHandlerRegistry()
              .register(
                  java.util.UUID.class,
                  new io.github.hansjone.auriga.common.persistence.UuidTypeHandler());
    }

    /** Fails startup if a custom MybatisPlusInterceptor dropped the tenant line interceptor. */
    @Bean
    SmartInitializingSingleton aurigaTenantLineGuard(
        ObjectProvider<MybatisPlusInterceptor> interceptors) {
      return () ->
          interceptors.forEach(
              i -> {
                boolean present =
                    i.getInterceptors().stream()
                        .anyMatch(TenantLineInnerInterceptor.class::isInstance);
                if (!present) {
                  throw new IllegalStateException(
                      "MybatisPlusInterceptor without TenantLineInnerInterceptor: tenant isolation"
                          + " would be lost. Add the aurigaTenantLineInterceptor bean first.");
                }
              });
    }
  }

  @Configuration(proxyBeanMethods = false)
  @ConditionalOnClass(FlywayMigrationStrategy.class)
  @ConditionalOnProperty(prefix = "auriga.service", name = "schema")
  static class Migrations {
    @Bean
    @ConditionalOnMissingBean
    FlywayMigrationStrategy aurigaFlywayMigrationStrategy(
        DataSource dataSource, AurigaProperties properties) {
      String schema = properties.getService().getSchema();
      return flyway -> {
        DataSource target =
            flyway.getConfiguration().getDataSource() != null
                ? flyway.getConfiguration().getDataSource()
                : dataSource;
        CommonMigrations.migrate(target, schema);
        CommonMigrations.serviceFlyway(flyway).migrate();
      };
    }
  }
}
