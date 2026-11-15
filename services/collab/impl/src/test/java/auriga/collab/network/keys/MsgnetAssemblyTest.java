package auriga.collab.network.keys;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.collab.api.NetworkRenewal;
import auriga.collab.app.NetworkSigningKeys;
import auriga.common.error.AurigaException;
import auriga.common.persistence.TenantTransactionManager;
import javax.sql.DataSource;
import org.junit.jupiter.api.Test;
import org.springframework.boot.test.context.runner.ApplicationContextRunner;
import org.springframework.context.support.GenericApplicationContext;
import org.springframework.mock.env.MockEnvironment;

class MsgnetAssemblyTest {
  @Test
  void unavailableKeysDoNotStopIndependentCoreStartup() {
    new ApplicationContextRunner()
        .withUserConfiguration(MsgnetRenewalConfiguration.class)
        .withPropertyValues(
            "auriga.service.schema=collab", "auriga.collab.network.auto-renew.enabled=true")
        .run(
            context -> {
              assertNull(context.getStartupFailure());
              assertNotNull(context.getBean(NetworkSigningKeys.class));
              assertNotNull(context.getBean(MsgnetRenewalController.class));
              assertEquals(
                  "collab.msgnet_projection_pending",
                  assertThrows(
                          AurigaException.class,
                          () -> context.getBean(NetworkRenewal.class).status())
                      .errorCode()
                      .code());
            });
  }

  @Test
  void combinedDeploymentChoosesCollabRolesInsteadOfThePrimaryIamRole() {
    try (var context = new GenericApplicationContext()) {
      var iam = mock(DataSource.class);
      var collab = mock(DataSource.class);
      var iamManager = new TenantTransactionManager(iam);
      var collabManager = new TenantTransactionManager(collab);
      context.registerBean(
          "iamDataSource", DataSource.class, () -> iam, definition -> definition.setPrimary(true));
      context.registerBean("collabDataSource", DataSource.class, () -> collab);
      context.registerBean(
          "iamTransactionManager",
          TenantTransactionManager.class,
          () -> iamManager,
          definition -> definition.setPrimary(true));
      context.registerBean(
          "collabTransactionManager", TenantTransactionManager.class, () -> collabManager);
      context.refresh();
      var combined = new MockEnvironment().withProperty("auriga.platform.combined", "true");
      assertSame(collab, MsgnetDatabase.source(context, combined));
      assertSame(collabManager, MsgnetDatabase.manager(context, combined));
      assertSame(iam, MsgnetDatabase.source(context, new MockEnvironment()));
      assertSame(iamManager, MsgnetDatabase.manager(context, new MockEnvironment()));
      verifyNoInteractions(iam, collab);
    }
  }
}
