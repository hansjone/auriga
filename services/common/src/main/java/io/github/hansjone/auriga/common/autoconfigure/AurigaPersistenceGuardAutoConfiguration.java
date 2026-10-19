package io.github.hansjone.auriga.common.autoconfigure;

import com.baomidou.mybatisplus.extension.plugins.MybatisPlusInterceptor;
import com.baomidou.mybatisplus.extension.plugins.inner.TenantLineInnerInterceptor;
import io.github.hansjone.auriga.common.persistence.TenantTransactionManager;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import javax.sql.DataSource;
import org.apache.ibatis.session.SqlSessionFactory;
import org.springframework.beans.factory.ListableBeanFactory;
import org.springframework.beans.factory.SmartInitializingSingleton;
import org.springframework.boot.autoconfigure.AutoConfiguration;
import org.springframework.boot.autoconfigure.condition.ConditionalOnClass;
import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty;
import org.springframework.context.annotation.Bean;
import org.springframework.jdbc.datasource.DataSourceTransactionManager;
import org.springframework.transaction.PlatformTransactionManager;

/**
 * Fails startup instead of silently losing tenant isolation, also when the persistence
 * auto-configuration backed off (several DataSources): every MyBatis {@link SqlSessionFactory} must
 * carry the tenant line interceptor and every JDBC transaction manager must be a {@link
 * TenantTransactionManager}. Opt out only with {@code auriga.persistence.allow-unguarded=true}.
 */
@AutoConfiguration
@ConditionalOnClass(SqlSessionFactory.class)
@ConditionalOnProperty(
    prefix = "auriga.persistence",
    name = "allow-unguarded",
    havingValue = "false",
    matchIfMissing = true)
public class AurigaPersistenceGuardAutoConfiguration {
  @Bean
  static SmartInitializingSingleton aurigaPersistenceGuard(ListableBeanFactory beans) {
    return () -> {
      List<String> problems = new ArrayList<>();
      for (Map.Entry<String, SqlSessionFactory> e :
          beans.getBeansOfType(SqlSessionFactory.class).entrySet()) {
        boolean guarded =
            e.getValue().getConfiguration().getInterceptors().stream()
                .anyMatch(
                    i ->
                        i instanceof MybatisPlusInterceptor mp
                            && mp.getInterceptors().stream()
                                .anyMatch(TenantLineInnerInterceptor.class::isInstance));
        if (!guarded) problems.add("SqlSessionFactory '" + e.getKey() + "' has no tenant line");
      }
      if (!beans.getBeansOfType(DataSource.class).isEmpty()) {
        for (Map.Entry<String, PlatformTransactionManager> e :
            beans.getBeansOfType(PlatformTransactionManager.class).entrySet()) {
          if (e.getValue() instanceof DataSourceTransactionManager
              && !(e.getValue() instanceof TenantTransactionManager)) {
            problems.add("Transaction manager '" + e.getKey() + "' does not set the RLS tenant");
          }
        }
      }
      if (!problems.isEmpty()) {
        throw new IllegalStateException("Tenant isolation is not wired: " + problems);
      }
    };
  }
}
