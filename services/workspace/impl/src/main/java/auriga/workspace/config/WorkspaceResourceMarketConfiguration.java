package auriga.workspace.config;

import auriga.workspace.api.SharedDshResourceMarket;
import auriga.workspace.app.SharedDshPlacements;
import auriga.workspace.app.SharedDshResourceMarketQueries;
import auriga.workspace.persistence.WorkspaceStore;
import java.time.Clock;
import org.springframework.boot.autoconfigure.condition.ConditionalOnExpression;
import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;

@Configuration(proxyBeanMethods = false)
@ConditionalOnExpression(
    "${auriga.services.workspace.enabled:true} && ('${auriga.service.schema:}' == 'workspace' || ${auriga.platform.combined:false})")
@ConditionalOnProperty(name = "auriga.workspace.shared-dsh.enabled", havingValue = "true")
public class WorkspaceResourceMarketConfiguration {
  @Bean
  SharedDshResourceMarket sharedDshResourceMarket(
      WorkspaceStore store,
      SharedDshPlacements placements,
      SharedDshPlacements.EntryProbe entry,
      Clock clock) {
    return new SharedDshResourceMarketQueries(store, placements, entry, clock);
  }
}
