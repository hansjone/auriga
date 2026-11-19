package auriga.workspace.config;

import auriga.workspace.api.SharedDshResourceNodes;
import auriga.workspace.app.SharedDshResourceNodeChecks;
import auriga.workspace.persistence.WorkspaceStore;
import org.springframework.boot.autoconfigure.condition.ConditionalOnExpression;
import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;

@Configuration(proxyBeanMethods = false)
@ConditionalOnExpression(
    "${auriga.services.workspace.enabled:true} && ('${auriga.service.schema:}' == 'workspace' || ${auriga.platform.combined:false})")
@ConditionalOnProperty(name = "auriga.workspace.shared-dsh.enabled", havingValue = "true")
public class WorkspaceSharedDshResourceConfiguration {
  @Bean
  SharedDshResourceNodes sharedDshResourceNodes(WorkspaceStore store) {
    return new SharedDshResourceNodeChecks(store);
  }
}
