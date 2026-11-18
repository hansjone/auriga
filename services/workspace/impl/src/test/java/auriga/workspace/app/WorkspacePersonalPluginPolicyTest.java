package auriga.workspace.app;

import static org.junit.jupiter.api.Assertions.*;

import auriga.org.web.model.WorkspacePolicy;
import org.junit.jupiter.api.Test;

class WorkspacePersonalPluginPolicyTest {
  @Test
  void missingPolicyAndUnsetFieldsUseTheContractDefaultWhileExplicitDenialIsPreserved() {
    assertTrue(WorkspaceService.allowPersonalPlugins(null));
    var policy = new WorkspacePolicy();
    assertTrue(WorkspaceService.allowPersonalPlugins(policy));
    policy.setAllowPersonalPlugins(null);
    assertTrue(WorkspaceService.allowPersonalPlugins(policy));
    policy.setAllowPersonalPlugins(false);
    assertFalse(WorkspaceService.allowPersonalPlugins(policy));
    policy.setAllowPersonalPlugins(true);
    assertTrue(WorkspaceService.allowPersonalPlugins(policy));
  }
}
