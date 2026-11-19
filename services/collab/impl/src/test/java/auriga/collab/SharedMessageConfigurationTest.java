package auriga.collab;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.collab.app.NetworkSigningKeys;
import auriga.collab.config.SharedMessageConfiguration;
import auriga.collab.grpc.SharedMessageGrpcService;
import auriga.collab.network.keys.MsgnetRenewal;
import auriga.common.persistence.TenantTransactionManager;
import io.grpc.ManagedChannel;
import java.time.Clock;
import javax.sql.DataSource;
import org.junit.jupiter.api.Test;
import org.springframework.boot.test.context.runner.ApplicationContextRunner;

class SharedMessageConfigurationTest {
  private ApplicationContextRunner enabled() {
    return new ApplicationContextRunner()
        .withUserConfiguration(SharedMessageConfiguration.class)
        .withPropertyValues(
            "auriga.platform.combined=false",
            "auriga.service.schema=collab",
            "auriga.services.collab.enabled=true",
            "auriga.collab.shared-messages.enabled=true");
  }

  @Test
  void standaloneReusesRealDurableEnginesAndExistingAuthoritiesWithoutStartingThem() {
    var data = mock(DataSource.class);
    var channel = mock(ManagedChannel.class);
    var renewal = mock(MsgnetRenewal.class);
    var keys = new NetworkSigningKeys(Clock.systemUTC(), 1);
    enabled()
        .withBean(DataSource.class, () -> data)
        .withBean(TenantTransactionManager.class, () -> new TenantTransactionManager(data))
        .withBean("collabNetworkIamChannel", ManagedChannel.class, () -> channel)
        .withBean(NetworkSigningKeys.class, () -> keys)
        .withBean(MsgnetRenewal.class, () -> renewal)
        .run(
            context -> {
              assertNull(context.getStartupFailure());
              assertEquals(1, context.getBeansOfType(SharedMessageGrpcService.class).size());
              assertSame(keys, context.getBean(NetworkSigningKeys.class));
              assertSame(renewal, context.getBean(MsgnetRenewal.class));
              verifyNoInteractions(data, channel, renewal);
            });
  }

  @Test
  void combinedDisabledOrWrongSchemaDoesNotRegisterStandaloneAdapter() {
    for (String property :
        new String[] {
          "auriga.platform.combined=true",
          "auriga.service.schema=iam",
          "auriga.services.collab.enabled=false",
          "auriga.collab.shared-messages.enabled=false"
        })
      enabled()
          .withPropertyValues(property)
          .run(
              context -> {
                assertNull(context.getStartupFailure());
                assertTrue(context.getBeansOfType(SharedMessageGrpcService.class).isEmpty());
              });
    new ApplicationContextRunner()
        .withUserConfiguration(SharedMessageConfiguration.class)
        .run(
            context -> {
              assertNull(context.getStartupFailure());
              assertTrue(context.getBeansOfType(SharedMessageGrpcService.class).isEmpty());
            });
  }

  @Test
  void explicitEnableCannotInventMissingSigningAuthorityOrIamChannel() {
    var data = mock(DataSource.class);
    var minimal =
        enabled()
            .withBean(DataSource.class, () -> data)
            .withBean(TenantTransactionManager.class, () -> new TenantTransactionManager(data));
    minimal.run(context -> assertNotNull(context.getStartupFailure()));
    minimal
        .withBean("collabNetworkIamChannel", ManagedChannel.class, () -> mock(ManagedChannel.class))
        .run(context -> assertNotNull(context.getStartupFailure()));
    verifyNoInteractions(data);
  }
}
