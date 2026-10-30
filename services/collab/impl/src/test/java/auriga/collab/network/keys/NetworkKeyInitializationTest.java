package auriga.collab.network.keys;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.collab.app.NetworkSigningKeys;
import auriga.common.autoconfigure.AurigaCoreAutoConfiguration;
import auriga.common.autoconfigure.AurigaPersistenceAutoConfiguration;
import auriga.common.persistence.CommonMigrations;
import auriga.common.persistence.TenantTransactionManager;
import java.util.ArrayList;
import java.util.List;
import java.util.UUID;
import javax.sql.DataSource;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.Test;
import org.mockito.MockedConstruction;
import org.springframework.beans.factory.support.AbstractBeanDefinition;
import org.springframework.boot.autoconfigure.AutoConfigurations;
import org.springframework.boot.flyway.autoconfigure.FlywayAutoConfiguration;
import org.springframework.boot.flyway.autoconfigure.FlywayDataSource;
import org.springframework.boot.test.context.runner.ApplicationContextRunner;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.context.annotation.Primary;
import org.springframework.core.NestedExceptionUtils;
import org.springframework.jdbc.core.JdbcTemplate;

/** Real Boot initialization and common migration strategy; no JDBC connection or PG process. */
class NetworkKeyInitializationTest {
  @Test
  void standalonePublishesOnlyAfterCommonAndServiceMigrations() {
    run(false);
  }

  @Test
  void failedServiceMigrationPreventsPublicationAndStartup() {
    run(true);
  }

  private void run(boolean failMigration) {
    var state = new Initialization();
    var service = mock(Flyway.class);
    when(service.migrate())
        .thenAnswer(
            invocation -> {
              state.events.add("service");
              if (failMigration) throw new IllegalStateException("service migration failed");
              return null;
            });
    var settings = mock(NetworkKeySettings.class);
    var release = new RootKeyPublication.Release("test signed keyset", "test announcement");
    when(settings.tenant()).thenReturn(UUID.randomUUID());
    when(settings.mountedRelease()).thenReturn(release);
    try (var migrations = mockStatic(CommonMigrations.class);
        MockedConstruction<NetworkKeyOperations> publications =
            mockConstruction(
                NetworkKeyOperations.class,
                (operations, construction) -> {
                  assertSame(
                      state.runtime,
                      ((JdbcTemplate) construction.arguments().get(0)).getDataSource());
                  assertSame(
                      state.runtime,
                      ((TenantTransactionManager) construction.arguments().get(1)).getDataSource());
                  doAnswer(
                          invocation -> {
                            if (!state.events.equals(List.of("common", "service")))
                              throw new IllegalStateException(
                                  "publication before Flyway initialization");
                            state.events.add("publish");
                            return null;
                          })
                      .when(operations)
                      .publish(release);
                })) {
      migrations
          .when(() -> CommonMigrations.migrate(state.owner, "collab"))
          .thenAnswer(invocation -> state.events.add("common"));
      migrations
          .when(() -> CommonMigrations.serviceFlyway(any(Flyway.class)))
          .thenAnswer(
              invocation -> {
                var configuration = ((Flyway) invocation.getArgument(0)).getConfiguration();
                assertSame(state.owner, configuration.getDataSource());
                assertArrayEquals(new String[] {"collab"}, configuration.getSchemas());
                assertEquals(
                    "classpath:db/collab", configuration.getLocations()[0].getDescriptor());
                return service;
              });
      new ApplicationContextRunner()
          .withConfiguration(
              AutoConfigurations.of(
                  AurigaCoreAutoConfiguration.class,
                  AurigaPersistenceAutoConfiguration.class,
                  FlywayAutoConfiguration.class))
          .withUserConfiguration(NetworkKeyOperationsConfiguration.class, Sources.class)
          .withBean(Initialization.class, () -> state)
          .withPropertyValues(
              "auriga.platform.combined=false",
              "auriga.service.schema=collab",
              "auriga.collab.network.key-operations.enabled=true",
              "spring.flyway.schemas=collab",
              "spring.flyway.locations=classpath:db/collab")
          .withInitializer(
              context ->
                  context.addBeanFactoryPostProcessor(
                      factory -> {
                        // Only local file/signing ports are replaced. The production publication
                        // bean factory, Boot dependency detector and migration strategy stay real.
                        ((AbstractBeanDefinition) factory.getBeanDefinition("networkKeySettings"))
                            .setInstanceSupplier(() -> settings);
                        ((AbstractBeanDefinition) factory.getBeanDefinition("networkRootTrust"))
                            .setInstanceSupplier(() -> mock(RootKeyPublication.class));
                        ((AbstractBeanDefinition)
                                factory.getBeanDefinition("approvedNetworkSigningKeys"))
                            .setInstanceSupplier(() -> mock(NetworkSigningKeys.class));
                      }))
          .run(
              context -> {
                if (failMigration) {
                  assertNotNull(context.getStartupFailure());
                  assertEquals(
                      "service migration failed",
                      NestedExceptionUtils.getMostSpecificCause(context.getStartupFailure())
                          .getMessage());
                  assertEquals(List.of("common", "service"), state.events);
                  assertTrue(publications.constructed().isEmpty());
                } else {
                  assertNull(context.getStartupFailure());
                  assertEquals(List.of("common", "service", "publish"), state.events);
                  assertEquals(1, publications.constructed().size());
                  verify(publications.constructed().getFirst()).publish(release);
                }
              });
      verifyNoInteractions(state.owner, state.runtime);
    }
  }

  private static class Initialization {
    final DataSource runtime = mock(DataSource.class);
    final DataSource owner = mock(DataSource.class);
    final List<String> events = new ArrayList<>();
  }

  @Configuration(proxyBeanMethods = false)
  static class Sources {
    @Bean
    @Primary
    DataSource dataSource(Initialization state) {
      return state.runtime;
    }

    @Bean
    @FlywayDataSource
    DataSource migrationDataSource(Initialization state) {
      return state.owner;
    }
  }
}
