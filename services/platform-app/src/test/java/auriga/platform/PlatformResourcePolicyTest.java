package auriga.platform;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.security.AurigaPrincipal;
import auriga.common.security.PrincipalType;
import auriga.common.security.TokenContext;
import auriga.iam.api.VerifiedAuthorDepartment;
import auriga.org.OrgService;
import auriga.resource.web.ResourceBody;
import java.util.Optional;
import java.util.UUID;
import org.junit.jupiter.api.Test;
import tools.jackson.databind.node.ObjectNode;

class PlatformResourcePolicyTest {
  @Test
  void combinesOrgDisplayNameWithTheCurrentActorsVerifiedDepartment() {
    var org = mock(OrgService.class);
    var iam = mock(VerifiedAuthorDepartment.class);
    UUID tenant = UUID.randomUUID();
    UUID account = UUID.randomUUID();
    var actor =
        new AurigaPrincipal(
            account.toString(),
            tenant,
            PrincipalType.USER,
            TokenContext.PORTAL,
            UUID.randomUUID().toString(),
            null,
            null,
            null,
            null,
            null,
            null,
            null);
    when(org.memberJson(account))
        .thenReturn(
            ResourceBody.JSON
                .createObjectNode()
                .put("id", account.toString())
                .put("displayName", "作者甲"));
    when(iam.authorDepartment(actor)).thenReturn("部门甲");
    var configuration = new PlatformResourceConfiguration();
    var inputs = configuration.combinedResourceInputs(org, Optional.of(iam));
    assertEquals("作者甲", inputs.member(tenant, account).getDisplayName());
    assertEquals("部门甲", inputs.authorDepartment(actor));
    assertEquals(
        "", configuration.combinedResourceInputs(org, Optional.empty()).authorDepartment(actor));
  }

  @Test
  void oldPoliciesDefaultToPersonalPluginsAllowedAndExplicitFalseStillApplies() {
    var org = mock(OrgService.class);
    var inputs = new PlatformResourceConfiguration().combinedResourceInputs(org, Optional.empty());
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
