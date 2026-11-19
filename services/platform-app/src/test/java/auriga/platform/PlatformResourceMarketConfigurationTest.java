package auriga.platform;

import static org.assertj.core.api.Assertions.assertThat;
import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.contracts.node.v1.*;
import auriga.resource.api.SharedResourcePlacements;
import auriga.workspace.api.SharedDshResourceMarket;
import java.util.*;
import org.junit.jupiter.api.Test;
import org.springframework.boot.test.context.runner.ApplicationContextRunner;

class PlatformResourceMarketConfigurationTest {
  ApplicationContextRunner context() {
    return new ApplicationContextRunner()
        .withUserConfiguration(PlatformResourceMarketConfiguration.class)
        .withPropertyValues(
            "auriga.platform.combined=true", "auriga.services.workspace.enabled=true");
  }

  @Test
  void marketAdapterIsOptInAndFailsClosedWithoutWorkspaceAuthority() {
    context()
        .run(ctx -> assertThat(ctx).hasNotFailed().doesNotHaveBean(SharedResourcePlacements.class));
    context()
        .withPropertyValues("auriga.workspace.shared-dsh.enabled=true")
        .run(ctx -> assertThat(ctx).hasFailed());
  }

  @Test
  void combinedAdapterUsesTheCurrentTenantAndRealWorkspaceReportPort() {
    var workspace = mock(SharedDshResourceMarket.class);
    UUID tenant = UUID.randomUUID(),
        member = UUID.randomUUID(),
        placement = UUID.randomUUID(),
        session = UUID.randomUUID();
    var binding = SharedDshBinding.newBuilder().setTenantId(tenant.toString()).build();
    var view = SharedDshResourceView.newBuilder().setBinding(binding).build();
    when(workspace.resolve(member, placement, session)).thenReturn(binding);
    when(workspace.report(view)).thenReturn(Optional.empty());
    context()
        .withPropertyValues("auriga.workspace.shared-dsh.enabled=true")
        .withBean(SharedDshResourceMarket.class, () -> workspace)
        .run(
            ctx -> {
              assertThat(ctx).hasNotFailed().hasSingleBean(SharedResourcePlacements.class);
              var port = ctx.getBean(SharedResourcePlacements.class);
              TenantContext.call(
                  tenant,
                  () -> {
                    assertEquals(binding, port.resolve(tenant, member, placement, session));
                    assertTrue(port.report(view).isEmpty());
                    assertThrows(
                        AurigaException.class,
                        () -> port.resolve(UUID.randomUUID(), member, placement, session));
                    return null;
                  });
            });
  }
}
