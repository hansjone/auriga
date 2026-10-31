package auriga.workspace;

import static org.junit.jupiter.api.Assertions.*;

import auriga.contracts.iam.v1.IssueManagedDeviceResponse;
import auriga.workspace.app.WorkspaceManagedModels;
import auriga.workspace.app.WorkspaceService;
import java.util.*;
import org.junit.jupiter.api.Test;

class ManagedModelInstallationTest {
  @Test
  void invalidInstallationDisablesModelCapabilityOnceBeforeReconciliation() {
    var logger =
        (ch.qos.logback.classic.Logger)
            org.slf4j.LoggerFactory.getLogger(WorkspaceManagedModels.class);
    var appender =
        new ch.qos.logback.core.read.ListAppender<ch.qos.logback.classic.spi.ILoggingEvent>();
    appender.start();
    logger.addAppender(appender);
    try {
      var models = new WorkspaceManagedModels(null, null, WorkspaceService.JSON.createObjectNode());
      assertFalse(models.enabled());
      assertEquals("disabled_invalid_installation", models.state());
      for (int i = 0; i < 3; i++)
        models.reconcile(
            Map.of(),
            IssueManagedDeviceResponse.newBuilder().setModelCredential("unusable").build());
      assertEquals(1, appender.list.size());
      assertTrue(
          appender.list.getFirst().getFormattedMessage().contains("disabled_invalid_installation"));
    } finally {
      logger.detachAppender(appender);
      appender.stop();
    }
  }

  @Test
  void installationRequiresCompleteCanonicalPlatformModelEntry() {
    for (String url :
        List.of(
            "",
            "/ai/v1",
            "file:///ai/v1",
            "https://example.com/ai/v1?",
            "https://example.com/ai/%76%31",
            "https://user:password@example.com/ai/v1")) {
      var models =
          new WorkspaceManagedModels(
              null, null, WorkspaceService.JSON.createObjectNode().put("modelBaseUrl", url));
      assertFalse(models.enabled(), "invalid installation accepted");
    }
    assertTrue(
        new WorkspaceManagedModels(
                null,
                null,
                WorkspaceService.JSON
                    .createObjectNode()
                    .put("platformOrigin", "https://portal.example.com"))
            .enabled());
    assertTrue(
        new WorkspaceManagedModels(
                null,
                null,
                WorkspaceService.JSON
                    .createObjectNode()
                    .put("modelBaseUrl", "http://127.0.0.1:12345/ai/v1"))
            .enabled());
  }
}
