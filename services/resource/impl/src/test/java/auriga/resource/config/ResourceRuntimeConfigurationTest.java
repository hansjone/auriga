package auriga.resource.config;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.security.*;
import auriga.iam.api.DevicePermissionAuthority;
import auriga.resource.api.ResourceAccessInputs;
import auriga.resource.app.ResourceAccess;
import java.util.*;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.beans.factory.support.DefaultListableBeanFactory;
import org.springframework.boot.test.system.CapturedOutput;
import org.springframework.boot.test.system.OutputCaptureExtension;

@ExtendWith(OutputCaptureExtension.class)
class ResourceRuntimeConfigurationTest {
  final UUID tenant = UUID.randomUUID();
  final String account = UUID.randomUUID().toString(), device = UUID.randomUUID().toString();
  final ResourceAccessInputs inputs = mock(ResourceAccessInputs.class);

  AurigaPrincipal actor() {
    return new AurigaPrincipal(
        account,
        tenant,
        PrincipalType.DEVICE,
        TokenContext.DEVICE,
        null,
        device,
        "member",
        List.of(),
        Set.of(ResourceAccess.READ),
        null,
        null,
        "fixture");
  }

  @Test
  void missingDeviceAuthorityIsVisibleAndFailsClosed(CapturedOutput output) {
    var beans = new DefaultListableBeanFactory();
    var access =
        new ResourceRuntimeConfiguration()
            .resourceAccess(inputs, beans.getBeanProvider(DevicePermissionAuthority.class));
    var error =
        assertThrows(
            AurigaException.class, () -> TenantContext.call(tenant, () -> access.resolve(actor())));
    assertEquals("common.forbidden", error.errorCode().code());
    assertTrue(
        output.getAll().contains("DevicePermissionAuthority is unavailable"), output.getAll());
    verifyNoInteractions(inputs);
  }

  @Test
  void configuredDeviceAuthorityIsUsed(CapturedOutput output) {
    var beans = new DefaultListableBeanFactory();
    var authority = mock(DevicePermissionAuthority.class);
    beans.registerSingleton("devicePermissionAuthority", authority);
    var access =
        new ResourceRuntimeConfiguration()
            .resourceAccess(inputs, beans.getBeanProvider(DevicePermissionAuthority.class));
    assertThrows(
        AurigaException.class, () -> TenantContext.call(tenant, () -> access.resolve(actor())));
    verify(authority).allowed(tenant, account, device, ResourceAccess.READ);
    assertFalse(
        output.getAll().contains("DevicePermissionAuthority is unavailable"), output.getAll());
  }
}
