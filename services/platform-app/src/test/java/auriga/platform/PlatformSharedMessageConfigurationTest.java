package auriga.platform;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.collab.app.NetworkSigningKeys;
import auriga.collab.grpc.SharedMessageGrpcService;
import auriga.collab.network.keys.MsgnetRenewal;
import auriga.common.persistence.TenantTransactionManager;
import io.grpc.ManagedChannel;
import java.time.Clock;
import javax.sql.DataSource;
import org.junit.jupiter.api.Test;
import org.springframework.boot.test.context.runner.ApplicationContextRunner;

class PlatformSharedMessageConfigurationTest {
  @Test
  void installsRealSharedOnlyMailboxWithoutReadingKeysOrTouchingAnyDatabaseAtStartup() {
    var collab = mock(DataSource.class);
    var iam = mock(DataSource.class);
    var channel = mock(ManagedChannel.class);
    var renewal = mock(MsgnetRenewal.class);
    new ApplicationContextRunner()
        .withUserConfiguration(PlatformSharedMessageConfiguration.class)
        .withPropertyValues(
            "auriga.platform.combined=true",
            "auriga.services.collab.enabled=true",
            "auriga.iam.shared-tasks.enabled=true",
            "auriga.collab.shared-messages.enabled=true")
        .withBean(
            "iamDataSource", DataSource.class, () -> iam, definition -> definition.setPrimary(true))
        .withBean("collabDataSource", DataSource.class, () -> collab)
        .withBean(
            "collabTransactionManager",
            TenantTransactionManager.class,
            () -> new TenantTransactionManager(collab))
        .withBean("collabNetworkIamChannel", ManagedChannel.class, () -> channel)
        .withBean(NetworkSigningKeys.class, () -> new NetworkSigningKeys(Clock.systemUTC(), 1))
        .withBean(MsgnetRenewal.class, () -> renewal)
        .run(
            context -> {
              assertNull(context.getStartupFailure());
              assertNotNull(context.getBean(SharedMessageGrpcService.class));
              verifyNoInteractions(collab, iam, renewal);
            });
  }

  @Test
  void disabledSharedBranchDoesNotRequireTheNewDependencies() {
    new ApplicationContextRunner()
        .withUserConfiguration(PlatformSharedMessageConfiguration.class)
        .run(
            context -> {
              assertNull(context.getStartupFailure());
              assertFalse(context.containsBean("sharedMessageGrpcService"));
            });
  }
}
