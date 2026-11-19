package auriga.platform;

import auriga.contracts.node.v1.SharedDshBinding;
import auriga.contracts.node.v1.SharedDshResourceView;
import auriga.contracts.workspace.v1.GetSharedDshResourceSnapshotResponse;
import auriga.resource.api.SharedResourceResolution;
import auriga.workspace.api.SharedDshAuthority.ResourceInputs;
import auriga.workspace.api.SharedDshRuntimeResources;
import java.util.UUID;
import org.springframework.boot.autoconfigure.condition.ConditionalOnExpression;
import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;

@Configuration(proxyBeanMethods = false)
@ConditionalOnExpression(
    "${auriga.platform.combined:false} && ${auriga.services.workspace.enabled:false} && ${auriga.services.resource.enabled:true}")
@ConditionalOnProperty(name = "auriga.workspace.shared-dsh.enabled", havingValue = "true")
public class PlatformSharedResourceConfiguration {
  @Bean
  SharedDshRuntimeResources combinedSharedRuntimeResources(SharedResourceResolution resources) {
    return binding -> {
      var snapshot = resources.snapshot(binding);
      return GetSharedDshResourceSnapshotResponse.newBuilder()
          .setView(snapshot.view())
          .addAllResources(snapshot.resources())
          .setRevocations(snapshot.revocations())
          .build();
    };
  }

  @Bean
  ResourceInputs combinedSharedResourceInputs(SharedResourceResolution resources) {
    return new ResourceInputs() {
      @Override
      public String authorizationSetDigest(UUID tenant, UUID member) {
        return resources.authorizationSetDigest(tenant, member);
      }

      @Override
      public SharedDshResourceView resourceView(SharedDshBinding binding) {
        return resources.snapshot(binding).view();
      }
    };
  }
}
