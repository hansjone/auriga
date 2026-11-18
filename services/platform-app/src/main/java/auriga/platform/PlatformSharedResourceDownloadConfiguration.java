package auriga.platform;

import auriga.resource.api.SharedResourceNodes;
import auriga.workspace.api.SharedDshResourceNodes;
import org.springframework.boot.autoconfigure.condition.ConditionalOnExpression;
import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;

@Configuration(proxyBeanMethods = false)
@ConditionalOnExpression(
    "${auriga.platform.combined:false} && ${auriga.services.workspace.enabled:false} && ${auriga.services.resource.enabled:true}")
@ConditionalOnProperty(name = "auriga.workspace.shared-dsh.enabled", havingValue = "true")
public class PlatformSharedResourceDownloadConfiguration {
  @Bean
  SharedResourceNodes combinedSharedResourceNodes(SharedDshResourceNodes workspace) {
    return workspace::requireCurrent;
  }
}
