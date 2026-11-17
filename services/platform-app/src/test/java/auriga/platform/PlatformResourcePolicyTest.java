package auriga.platform;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.org.OrgService;
import auriga.resource.web.ResourceBody;
import java.util.UUID;
import org.junit.jupiter.api.Test;
import tools.jackson.databind.node.ObjectNode;

class PlatformResourcePolicyTest {
  @Test
  void oldPoliciesDefaultToPersonalPluginsAllowedAndExplicitFalseStillApplies() {
    var org = mock(OrgService.class);
    var inputs = new PlatformResourceConfiguration().combinedResourceInputs(org);
    UUID tenant = UUID.randomUUID();
    when(org.getPolicy("workspace")).thenReturn(null);
    assertTrue(inputs.allowPersonalPlugins(tenant));
    for (String policy :
        new String[] {
          "{}",
          "{\"value\":{}}",
          "{\"value\":{\"allowPersonalPlugins\":null}}",
          "{\"value\":{\"allowPersonalPlugins\":true}}"
        }) {
      when(org.getPolicy("workspace")).thenReturn((ObjectNode) ResourceBody.JSON.readTree(policy));
      assertTrue(inputs.allowPersonalPlugins(tenant), policy);
    }
    when(org.getPolicy("workspace"))
        .thenReturn(
            (ObjectNode)
                ResourceBody.JSON.readTree("{\"value\":{\"allowPersonalPlugins\":false}}"));
    assertFalse(inputs.allowPersonalPlugins(tenant));
    when(org.getPolicy("workspace"))
        .thenReturn(
            (ObjectNode)
                ResourceBody.JSON.readTree("{\"value\":{\"allowPersonalPlugins\":\"false\"}}"));
    assertThrows(IllegalStateException.class, () -> inputs.allowPersonalPlugins(tenant));
  }
}
