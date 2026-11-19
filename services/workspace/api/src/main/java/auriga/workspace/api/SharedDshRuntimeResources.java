package auriga.workspace.api;

import auriga.contracts.node.v1.SharedDshBinding;
import auriga.contracts.workspace.v1.GetSharedDshResourceSnapshotResponse;

/** Combined-platform adapter to the resource service's live, signed runtime material. */
public interface SharedDshRuntimeResources {
  GetSharedDshResourceSnapshotResponse snapshot(SharedDshBinding binding);
}
