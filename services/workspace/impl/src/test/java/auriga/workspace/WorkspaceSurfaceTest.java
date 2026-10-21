package auriga.workspace;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.error.ErrorMessages;
import auriga.workspace.config.WorkspaceRuntimeConfiguration;
import auriga.workspace.domain.WorkspaceErrors;
import auriga.workspace.web.*;
import java.util.*;
import org.junit.jupiter.api.Test;
import org.springframework.context.annotation.AnnotationConfigApplicationContext;
import org.springframework.core.env.MapPropertySource;

class WorkspaceSurfaceTest {
  @Test
  void nodeProtocolCannotBeCollectedByInternalJwtListener() {
    assertFalse(
        io.grpc.BindableService.class.isAssignableFrom(auriga.workspace.grpc.NodeSessions.class));
  }

  @Test
  void disabledModuleDoesNotRegisterHttpEndpointsOrRequireCa() {
    try (var context = new AnnotationConfigApplicationContext()) {
      context
          .getEnvironment()
          .getPropertySources()
          .addFirst(
              new MapPropertySource(
                  "test",
                  Map.of(
                      "auriga.services.workspace.enabled",
                      "false",
                      "auriga.platform.combined",
                      "true")));
      context.register(
          WorkspaceModuleConfiguration.class,
          WorkspaceRuntimeConfiguration.class,
          WorkspaceController.class,
          WorkspacePluginController.class,
          WorkspaceHeartbeatRequestAdvice.class,
          WorkspaceSseController.class,
          WorkspaceResponseAdvice.class);
      context.refresh();
      assertEquals(0, context.getBeansOfType(WorkspaceController.class).size());
      assertEquals(0, context.getBeansOfType(WorkspacePluginController.class).size());
      assertEquals(0, context.getBeansOfType(WorkspaceHeartbeatRequestAdvice.class).size());
      assertEquals(0, context.getBeansOfType(WorkspaceSseController.class).size());
    }
  }

  @Test
  void workspaceErrorsHaveBothLanguages() {
    var messages = new ErrorMessages(List.of("messages/workspace-errors"), Locale.ENGLISH);
    for (var code :
        List.of(
            WorkspaceErrors.ADMISSION_FULL,
            WorkspaceErrors.NOT_ALLOWED,
            WorkspaceErrors.NODE_NOT_EMPTY,
            WorkspaceErrors.PROTOCOL_UNSUPPORTED)) {
      String english =
          messages.message(code, Map.of("current", 1, "minSupported", 1), Locale.ENGLISH);
      String chinese =
          messages.message(
              code, Map.of("current", 1, "minSupported", 1), Locale.SIMPLIFIED_CHINESE);
      assertNotEquals(code.code(), english);
      assertNotEquals(code.code(), chinese);
      assertNotEquals(english, chinese);
    }
  }
}
