package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.error.AurigaException;
import java.time.Clock;
import org.junit.jupiter.api.Test;

class AdminDevicesAuthorizationTest {
  @Test
  void controllerRequiresAdminPermissionAndRecentVerification() throws Exception {
    var clock = Clock.systemUTC();
    var read =
        auriga.common.security.AuthorizationRules.of(
            IamAdminDevicesController.class.getMethod("get", String.class),
            IamAdminDevicesController.class);
    var write =
        auriga.common.security.AuthorizationRules.of(
            IamAdminDevicesController.class.getMethod("revoke", String.class),
            IamAdminDevicesController.class);
    var principal = mock(auriga.common.security.AurigaPrincipal.class);
    assertEquals(
        "common.unauthenticated",
        assertThrows(AurigaException.class, () -> read.check(null, clock)).errorCode().code());
    assertEquals(
        "common.forbidden",
        assertThrows(AurigaException.class, () -> read.check(principal, clock)).errorCode().code());
    when(principal.isAdminContext()).thenReturn(true);
    assertEquals(
        "common.forbidden",
        assertThrows(AurigaException.class, () -> read.check(principal, clock)).errorCode().code());
    when(principal.hasPermission("iam.network.manage")).thenReturn(true);
    assertDoesNotThrow(() -> read.check(principal, clock));
    assertEquals(
        "iam.step_up_required",
        assertThrows(AurigaException.class, () -> write.check(principal, clock))
            .errorCode()
            .code());
    when(principal.steppedUpWithin(any(), any())).thenReturn(true);
    assertDoesNotThrow(() -> write.check(principal, clock));
  }
}
