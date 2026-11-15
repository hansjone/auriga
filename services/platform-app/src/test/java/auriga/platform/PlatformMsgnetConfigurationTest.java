package auriga.platform;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.collab.app.NetworkMailboxService;
import auriga.collab.app.NetworkSigningKeys;
import auriga.collab.grpc.NetworkAuthorityClient;
import auriga.collab.grpc.NetworkSourceGrpcAuthority;
import auriga.common.persistence.TenantTransactionManager;
import javax.sql.DataSource;
import org.junit.jupiter.api.Test;
import org.springframework.boot.test.context.runner.ApplicationContextRunner;
import org.springframework.test.util.ReflectionTestUtils;

class PlatformMsgnetConfigurationTest {
  @Test
  void combinedMessageRuntimeUsesOnlyCollabDataSource() {
    var iam = mock(DataSource.class);
    var collab = mock(DataSource.class);
    var source = mock(NetworkSourceGrpcAuthority.class);
    var authority = mock(NetworkAuthorityClient.class);
    new ApplicationContextRunner()
        .withUserConfiguration(PlatformMsgnetConfiguration.class)
        .withPropertyValues(
            "auriga.platform.combined=true",
            "auriga.services.collab.enabled=true",
            "auriga.collab.network.auto-renew.enabled=true")
        .withBean(
            "iamDataSource", DataSource.class, () -> iam, definition -> definition.setPrimary(true))
        .withBean("collabDataSource", DataSource.class, () -> collab)
        .withBean(
            "collabTransactionManager",
            TenantTransactionManager.class,
            () -> new TenantTransactionManager(collab))
        .withBean(NetworkSourceGrpcAuthority.class, () -> source)
        .withBean(NetworkAuthorityClient.class, () -> authority)
        .withBean(
            NetworkSigningKeys.class, () -> new NetworkSigningKeys(java.time.Clock.systemUTC(), 1))
        .run(
            context -> {
              assertNull(context.getStartupFailure());
              var mailbox = context.getBean(NetworkMailboxService.class);
              assertSame(
                  collab,
                  ((org.springframework.jdbc.core.JdbcTemplate)
                          ReflectionTestUtils.getField(mailbox, "jdbc"))
                      .getDataSource());
              assertTrue(context.containsBean("networkMailboxGrpcService"));
              verifyNoInteractions(iam, collab, source, authority);
            });
  }
}
