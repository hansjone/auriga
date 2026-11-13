package auriga.platform;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.contracts.node.v1.InstanceSpec;
import auriga.contracts.resource.v1.*;
import auriga.workspace.app.WorkspaceUnifiedResources;
import java.util.*;
import org.junit.jupiter.api.Test;

class PlatformUnifiedSnapshotTest {
  @Test
  void carriesGeneratedExpectedResourcesIncludingTombstones() {
    var tenant = UUID.randomUUID();
    var owner = UUID.randomUUID();
    var instance = UUID.randomUUID();
    var node = UUID.randomUUID();
    var refs =
        List.of(
                UnifiedResourceDesiredState.UNIFIED_RESOURCE_DESIRED_STATE_ENABLED,
                UnifiedResourceDesiredState.UNIFIED_RESOURCE_DESIRED_STATE_DISABLED,
                UnifiedResourceDesiredState.UNIFIED_RESOURCE_DESIRED_STATE_REMOVED)
            .stream()
            .map(
                state ->
                    UnifiedResourceRef.newBuilder()
                        .setInstallationRevision(8)
                        .setDesiredState(state)
                        .setEnabled(
                            state
                                == UnifiedResourceDesiredState
                                    .UNIFIED_RESOURCE_DESIRED_STATE_ENABLED)
                        .build())
            .toList();
    var resources = mock(WorkspaceUnifiedResources.class);
    var spec =
        InstanceSpec.newBuilder()
            .setGeneration(5)
            .setResourceSelectionRevision(4)
            .addAllUnifiedResources(refs)
            .build();
    when(resources.snapshot(tenant, instance))
        .thenReturn(
            new WorkspaceUnifiedResources.View(instance, owner, node, spec, 5, true, List.of()));
    var snapshot = new PlatformResourceWorkspace.Unified(resources).snapshot(tenant, instance);
    assertEquals(refs, snapshot.resources());
    assertEquals(5, snapshot.generation());
    assertEquals(4, snapshot.selectionRevision());
  }
}
