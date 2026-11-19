package auriga.platform;

import static org.assertj.core.api.Assertions.assertThat;
import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.contracts.node.v1.SharedDshBinding;
import auriga.resource.api.SharedResourceNodes;
import auriga.workspace.api.SharedDshResourceNodes;
import java.util.UUID;
import org.junit.jupiter.api.Test;
import org.springframework.boot.test.context.runner.ApplicationContextRunner;

class PlatformSharedResourceDownloadConfigurationTest {
  @Test
  void combinedNodeAuthorityIsExplicitAndDelegatesOnlyThroughWorkspaceApi() {
    var workspace = mock(SharedDshResourceNodes.class);
    UUID tenant = UUID.randomUUID(), node = UUID.randomUUID();
    var binding = SharedDshBinding.getDefaultInstance();
    when(workspace.requireCurrent(tenant, node, binding)).thenReturn("sha256:" + "a".repeat(64));
    var context =
        new ApplicationContextRunner()
            .withUserConfiguration(PlatformSharedResourceDownloadConfiguration.class)
            .withPropertyValues(
                "auriga.platform.combined=true", "auriga.services.workspace.enabled=true")
            .withBean(SharedDshResourceNodes.class, () -> workspace);
    context.run(ctx -> assertThat(ctx).doesNotHaveBean(SharedResourceNodes.class));
    context
        .withPropertyValues("auriga.workspace.shared-dsh.enabled=true")
        .run(
            ctx -> {
              assertThat(ctx).hasNotFailed().hasSingleBean(SharedResourceNodes.class);
              assertEquals(
                  "sha256:" + "a".repeat(64),
                  ctx.getBean(SharedResourceNodes.class).requireCurrent(tenant, node, binding));
            });
    verify(workspace).requireCurrent(tenant, node, binding);
  }
}
