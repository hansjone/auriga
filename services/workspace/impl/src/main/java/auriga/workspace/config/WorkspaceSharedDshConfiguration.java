package auriga.workspace.config;

import auriga.workspace.api.SharedDshAuthority.Inputs;
import auriga.workspace.app.SharedDshAdmin;
import auriga.workspace.app.SharedDshMaintenance;
import auriga.workspace.app.SharedDshNodeRuntime;
import auriga.workspace.app.SharedDshPlacements;
import auriga.workspace.app.SharedDshTickets;
import auriga.workspace.grpc.SharedDshGrpc;
import auriga.workspace.persistence.WorkspaceStore;
import java.time.Clock;
import org.springframework.boot.autoconfigure.condition.ConditionalOnExpression;
import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;

/** Opt-in assembly. The entry probe, exchange, and signer are mandatory deployment adapters. */
@Configuration(proxyBeanMethods = false)
@ConditionalOnExpression(
    "${auriga.services.workspace.enabled:true} && ('${auriga.service.schema:}' == 'workspace' || ${auriga.platform.combined:false})")
@ConditionalOnProperty(name = "auriga.workspace.shared-dsh.enabled", havingValue = "true")
public class WorkspaceSharedDshConfiguration {
  @Bean
  SharedDshNodeRuntime sharedDshNodeRuntime(WorkspaceStore store, SharedDshPlacements placements) {
    return new SharedDshNodeRuntime(store, placements);
  }

  @Bean
  SharedDshPlacements sharedDshPlacements(
      WorkspaceStore store, Inputs inputs, SharedDshPlacements.EntryProbe entry, Clock clock) {
    return new SharedDshPlacements(store, inputs, entry, clock);
  }

  @Bean
  SharedDshTickets sharedDshTickets(
      WorkspaceStore store,
      SharedDshPlacements placements,
      SharedDshTickets.Entry entry,
      SharedDshTickets.SigningKey signer,
      Clock clock) {
    return new SharedDshTickets(store, placements, entry, clock, signer.kid(), signer.key());
  }

  @Bean
  SharedDshAdmin sharedDshAdmin(
      WorkspaceStore store, SharedDshPlacements placements, Inputs inputs, Clock clock) {
    return new SharedDshAdmin(
        store,
        placements,
        new SharedDshMaintenance(store, placements, inputs, clock),
        inputs,
        clock);
  }

  /** The common internal gRPC server applies service-token verification before this adapter. */
  @Bean
  SharedDshGrpc sharedDshGrpc(SharedDshPlacements placements) {
    return new SharedDshGrpc(placements);
  }
}
