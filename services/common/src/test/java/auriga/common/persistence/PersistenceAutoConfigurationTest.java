package auriga.common.persistence;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.autoconfigure.AurigaCoreAutoConfiguration;
import auriga.common.autoconfigure.AurigaPersistenceAutoConfiguration;
import auriga.common.context.MissingTenantException;
import auriga.common.context.TenantContext;
import com.baomidou.mybatisplus.extension.plugins.MybatisPlusInterceptor;
import com.baomidou.mybatisplus.extension.plugins.inner.TenantLineInnerInterceptor;
import java.util.Set;
import java.util.UUID;
import javax.sql.DataSource;
import org.junit.jupiter.api.Test;
import org.postgresql.ds.PGSimpleDataSource;
import org.springframework.boot.autoconfigure.AutoConfigurations;
import org.springframework.boot.test.context.runner.ApplicationContextRunner;
import org.springframework.transaction.TransactionManager;

class PersistenceAutoConfigurationTest {
  private final ApplicationContextRunner runner =
      new ApplicationContextRunner()
          .withConfiguration(
              AutoConfigurations.of(
                  AurigaCoreAutoConfiguration.class, AurigaPersistenceAutoConfiguration.class))
          .withBean(DataSource.class, PGSimpleDataSource::new);

  @Test
  void registersTenantAwareInfrastructure() {
    runner.run(
        ctx -> {
          assertNull(ctx.getStartupFailure());
          assertInstanceOf(TenantTransactionManager.class, ctx.getBean(TransactionManager.class));
          assertNotNull(ctx.getBean(TenantLineInnerInterceptor.class));
        });
  }

  @Test
  void customInterceptorWithoutTenantLineFailsStartup() {
    runner
        .withBean(MybatisPlusInterceptor.class, MybatisPlusInterceptor::new)
        .run(ctx -> assertNotNull(ctx.getStartupFailure()));
  }

  @Test
  void tenantLineHandlerRules() {
    var handler = new AurigaTenantLineHandler(Set.of("Country"));
    assertTrue(handler.ignoreTable("outbox_event"));
    assertTrue(handler.ignoreTable("\"org\".\"country\""));
    assertFalse(handler.ignoreTable("org.member"));
    assertThrows(MissingTenantException.class, handler::getTenantId);
    UUID tenant = UUID.randomUUID();
    assertEquals(
        "'" + tenant + "'", TenantContext.call(tenant, () -> handler.getTenantId().toString()));
    TenantContext.runAsSystem(() -> assertTrue(handler.ignoreTable("org.member")));
  }

  @Test
  void schemaNamesAreValidated() {
    assertThrows(
        IllegalArgumentException.class, () -> CommonMigrations.requireSchemaName("x; drop"));
    assertThrows(
        IllegalArgumentException.class, () -> CommonMigrations.enableTenantRls("org", "a'b"));
    assertEquals(
        "SELECT org.auriga_enable_tenant_rls('org.member')",
        CommonMigrations.enableTenantRls("org", "member"));
  }

  @Test
  void multipleDataSourcesWithoutTenantWiringFailStartup() {
    ApplicationContextRunner multi =
        new ApplicationContextRunner()
            .withConfiguration(
                AutoConfigurations.of(
                    AurigaCoreAutoConfiguration.class,
                    AurigaPersistenceAutoConfiguration.class,
                    auriga.common.autoconfigure.AurigaPersistenceGuardAutoConfiguration.class))
            .withBean("primary", DataSource.class, PGSimpleDataSource::new)
            .withBean("reporting", DataSource.class, PGSimpleDataSource::new);
    // the persistence auto-configuration backs off; plain transaction manager is caught
    multi
        .withBean(
            org.springframework.jdbc.datasource.DataSourceTransactionManager.class,
            () ->
                new org.springframework.jdbc.datasource.DataSourceTransactionManager(
                    new PGSimpleDataSource()))
        .run(
            ctx -> {
              assertNotNull(ctx.getStartupFailure());
              assertTrue(
                  String.valueOf(
                          org.springframework.core.NestedExceptionUtils.getMostSpecificCause(
                                  ctx.getStartupFailure())
                              .getMessage())
                      .contains("does not set the RLS tenant"));
            });
    // a MyBatis session factory without the tenant line interceptor is caught
    multi
        .withBean(
            org.apache.ibatis.session.SqlSessionFactory.class,
            () ->
                new org.apache.ibatis.session.SqlSessionFactoryBuilder()
                    .build(new org.apache.ibatis.session.Configuration()))
        .run(ctx -> assertNotNull(ctx.getStartupFailure()));
    // tenant-aware wiring passes; explicit opt-out is possible
    multi
        .withBean(
            TenantTransactionManager.class,
            () -> new TenantTransactionManager(new PGSimpleDataSource()))
        .run(ctx -> assertNull(ctx.getStartupFailure()));
    multi
        .withPropertyValues("auriga.persistence.allow-unguarded=true")
        .withBean(
            org.springframework.jdbc.datasource.DataSourceTransactionManager.class,
            () ->
                new org.springframework.jdbc.datasource.DataSourceTransactionManager(
                    new PGSimpleDataSource()))
        .run(ctx -> assertNull(ctx.getStartupFailure()));
  }
}
