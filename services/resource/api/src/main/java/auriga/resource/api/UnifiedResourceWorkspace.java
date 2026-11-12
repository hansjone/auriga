package auriga.resource.api;

import auriga.contracts.resource.v1.ResolveUnifiedResourcesResponse;
import auriga.contracts.resource.v1.ResourceRevocationList;
import auriga.contracts.resource.v1.UnifiedInstallationReport;
import auriga.contracts.resource.v1.UnifiedResourceRef;
import auriga.contracts.resource.v1.UnifiedSelection;
import java.util.List;
import java.util.UUID;

/** Unified resource port. Tenant, ownership and exact revision are checked on every operation. */
public interface UnifiedResourceWorkspace {
  record Snapshot(
      UUID instance,
      UUID owner,
      UUID node,
      long selectionRevision,
      long generation,
      long observedGeneration,
      boolean ready,
      List<UnifiedInstallationReport> reports,
      // Expected resources from resource resolution, including each installationRevision.
      // Reports cannot supply their own expected counters. Missing/zero counters deny applied.
      List<UnifiedResourceRef> resources) {}

  Snapshot snapshot(UUID tenant, UUID instance);

  Snapshot apply(
      UUID tenant,
      UUID instance,
      UUID owner,
      long selectionRevision,
      boolean administrator,
      List<UnifiedSelection> selections,
      ResourceRevocationList revocations);

  Snapshot refresh(
      UUID tenant,
      UUID instance,
      UUID owner,
      long selectionRevision,
      ResolveUnifiedResourcesResponse resolution);

  void revoke(UUID tenant, UUID instance, ResourceRevocationList revocations);
}
