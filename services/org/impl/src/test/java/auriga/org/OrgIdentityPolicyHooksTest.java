package auriga.org;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import java.util.UUID;
import org.junit.jupiter.api.Test;

class OrgIdentityPolicyHooksTest {
  @Test
  void preflightDelegatesToOrgAndFailsClosed() {
    var org = mock(OrgService.class);
    var hooks = new OrgIdentityPolicyHooks(org);
    UUID provider = UUID.randomUUID();
    when(org.validateInvitation("valid-invite", provider)).thenReturn(true);
    assertTrue(hooks.validateInvitation("valid-invite", provider));
    assertFalse(hooks.validateInvitation("invalid-invite", provider));
    assertFalse(hooks.validateInvitation("valid-invite", UUID.randomUUID()));
    verify(org).validateInvitation("valid-invite", provider);
    verify(org, never()).redeemInvitation(any(), any(), any(), any(), any());
  }
}
