package auriga.platform;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.contracts.node.v1.SharedDshBinding;
import auriga.contracts.node.v1.SharedDshResourceReport;
import auriga.contracts.node.v1.SharedDshResourceView;
import auriga.resource.api.SharedResourcePlacements;
import auriga.workspace.api.SharedDshResourceMarket;
import java.util.Optional;
import java.util.UUID;
import org.springframework.boot.autoconfigure.condition.ConditionalOnExpression;
import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;

@Configuration(proxyBeanMethods = false)
@ConditionalOnExpression(
    "${auriga.platform.combined:false} && ${auriga.services.workspace.enabled:false} && ${auriga.services.resource.enabled:true}")
@ConditionalOnProperty(name = "auriga.workspace.shared-dsh.enabled", havingValue = "true")
public class PlatformResourceMarketConfiguration {
  @Bean
  SharedResourcePlacements combinedResourcePlacements(SharedDshResourceMarket workspace) {
    return new SharedResourcePlacements() {
      @Override
      public SharedDshBinding resolve(UUID tenant, UUID member, UUID placement, UUID session) {
        if (!tenant.equals(TenantContext.require()))
          throw new AurigaException(CommonErrors.NOT_FOUND);
        return workspace.resolve(member, placement, session);
      }

      @Override
      public Optional<SharedDshResourceReport> report(SharedDshResourceView expected) {
        return workspace.report(expected);
      }
    };
  }
}
