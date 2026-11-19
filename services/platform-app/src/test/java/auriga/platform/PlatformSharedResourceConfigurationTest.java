package auriga.platform;

import static org.assertj.core.api.Assertions.assertThat;
import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.contracts.node.v1.SharedDshBinding;
import auriga.contracts.node.v1.SharedDshResourceView;
import auriga.contracts.resource.v1.ResourceRevocationList;
import auriga.resource.api.SharedResourceResolution;
import auriga.workspace.api.SharedDshAuthority.ResourceInputs;
import auriga.workspace.api.SharedDshRuntimeResources;
import java.util.List;
import java.util.UUID;
import org.junit.jupiter.api.Test;
import org.springframework.boot.test.context.runner.ApplicationContextRunner;

class PlatformSharedResourceConfigurationTest {
  private ApplicationContextRunner context() {
    return new ApplicationContextRunner()
        .withUserConfiguration(PlatformSharedResourceConfiguration.class)
        .withPropertyValues(
            "auriga.platform.combined=true", "auriga.services.workspace.enabled=true");
  }

  @Test
  void sharedResourceAdapterRemainsOptInAndRequiresTheResourceAuthority() {
    context().run(ctx -> assertThat(ctx).hasNotFailed().doesNotHaveBean(ResourceInputs.class));
    context()
        .withPropertyValues("auriga.workspace.shared-dsh.enabled=true")
        .run(ctx -> assertThat(ctx).hasFailed());
  }

  @Test
  void workspaceReceivesTheSameAuthoritativeViewAndItsSignedMaterial() {
    var resources = mock(SharedResourceResolution.class);
    UUID tenant = UUID.randomUUID(), member = UUID.randomUUID();
    var binding =
        SharedDshBinding.newBuilder()
            .setTenantId(tenant.toString())
            .setMemberId(member.toString())
            .build();
    var view =
        SharedDshResourceView.newBuilder()
            .setBinding(binding)
            .setViewId(UUID.randomUUID().toString())
            .build();
    var revocations = ResourceRevocationList.newBuilder().setRevision(7).build();
    var snapshot = new SharedResourceResolution.Snapshot(view, List.of(), revocations);
    when(resources.authorizationSetDigest(tenant, member)).thenReturn("sha256:" + "a".repeat(64));
    when(resources.snapshot(binding)).thenReturn(snapshot);
    context()
        .withPropertyValues("auriga.workspace.shared-dsh.enabled=true")
        .withBean(SharedResourceResolution.class, () -> resources)
        .run(
            ctx -> {
              assertThat(ctx)
                  .hasNotFailed()
                  .hasSingleBean(ResourceInputs.class)
                  .hasSingleBean(SharedDshRuntimeResources.class);
              assertEquals(
                  "sha256:" + "a".repeat(64),
                  ctx.getBean(ResourceInputs.class).authorizationSetDigest(tenant, member));
              assertEquals(view, ctx.getBean(ResourceInputs.class).resourceView(binding));
              var actual = ctx.getBean(SharedDshRuntimeResources.class).snapshot(binding);
              assertEquals(view, actual.getView());
              assertEquals(revocations, actual.getRevocations());
              assertEquals(snapshot.resources(), actual.getResourcesList());
            });
  }
}
