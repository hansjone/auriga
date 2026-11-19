package auriga.workspace.config;

import static org.assertj.core.api.Assertions.assertThat;
import static org.mockito.Mockito.mock;

import auriga.workspace.api.SharedDshResourceMarket;
import auriga.workspace.app.SharedDshPlacements;
import auriga.workspace.persistence.WorkspaceStore;
import java.time.Clock;
import org.junit.jupiter.api.Test;
import org.springframework.boot.test.context.runner.ApplicationContextRunner;

class WorkspaceResourceMarketConfigurationTest {
  @Test
  void optInMarketUsesTheExistingPlacementAuthorityAndEntryProbe() {
    var runner =
        new ApplicationContextRunner()
            .withUserConfiguration(WorkspaceResourceMarketConfiguration.class)
            .withPropertyValues("auriga.platform.combined=true");
    runner.run(
        ctx -> assertThat(ctx).hasNotFailed().doesNotHaveBean(SharedDshResourceMarket.class));
    runner
        .withPropertyValues("auriga.workspace.shared-dsh.enabled=true")
        .run(ctx -> assertThat(ctx).hasFailed());
    runner
        .withPropertyValues("auriga.workspace.shared-dsh.enabled=true")
        .withBean(WorkspaceStore.class, () -> mock(WorkspaceStore.class))
        .withBean(SharedDshPlacements.class, () -> mock(SharedDshPlacements.class))
        .withBean(
            SharedDshPlacements.EntryProbe.class, () -> mock(SharedDshPlacements.EntryProbe.class))
        .withBean(Clock.class, Clock::systemUTC)
        .run(ctx -> assertThat(ctx).hasNotFailed().hasSingleBean(SharedDshResourceMarket.class));
  }
}
