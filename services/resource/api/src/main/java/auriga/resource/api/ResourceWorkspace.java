package auriga.resource.api;

import auriga.contracts.node.v1.PluginRef;
import auriga.contracts.resource.v1.ResourceRevocationList;
import java.util.*;

/** Workspace owns scheduling, specification generations and authenticated node observations. */
public interface ResourceWorkspace {
  record Snapshot(
      UUID instance,
      UUID owner,
      UUID node,
      long selectionRevision,
      long generation,
      long observedGeneration,
      boolean ready,
      String errorCode,
      List<PluginRef> plugins) {}

  record Selection(PluginRef plugin, String action) {}

  List<UUID> tenants();

  Snapshot snapshot(UUID tenant, UUID instance);

  Snapshot apply(
      UUID tenant,
      UUID instance,
      UUID owner,
      long revision,
      boolean administrator,
      List<Selection> changes,
      ResourceRevocationList revocations);

  Snapshot refresh(
      UUID tenant,
      UUID instance,
      UUID owner,
      long revision,
      List<Selection> changes,
      ResourceRevocationList revocations);

  void revoke(UUID tenant, UUID instance, ResourceRevocationList revocations);

  UUID authenticate(java.security.cert.X509Certificate[] chain);

  UUID nodeTenant(UUID node);
}
