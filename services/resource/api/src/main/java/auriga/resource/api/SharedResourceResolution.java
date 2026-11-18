package auriga.resource.api;

import auriga.contracts.node.v1.SharedDshBinding;
import auriga.contracts.node.v1.SharedDshResourceView;
import auriga.contracts.resource.v1.ResourceRevocationList;
import auriga.contracts.resource.v1.UnifiedResourceRef;
import java.io.InputStream;
import java.util.List;
import java.util.UUID;

/** Trusted workspace/node adapter. Its caller verifies the current placement before invoking. */
public interface SharedResourceResolution {
  record Snapshot(
      SharedDshResourceView view,
      List<UnifiedResourceRef> resources,
      ResourceRevocationList revocations) {
    public Snapshot {
      resources = List.copyOf(resources);
    }
  }

  String authorizationSetDigest(UUID tenant, UUID member);

  Snapshot snapshot(SharedDshBinding binding);

  /** Manifest when artifactId is null; the lease is reauthorized before opening object bytes. */
  InputStream artifact(SharedDshBinding binding, UUID leaseId, String artifactId);
}
