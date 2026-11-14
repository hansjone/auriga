package auriga.workspace.domain;

import auriga.contracts.node.v1.*;
import com.google.protobuf.MessageLite;
import java.util.*;

/** Explicit JSON mapping: protobuf int64/default serialization is not the public contract. */
public final class SharedDshEvents {
  private SharedDshEvents() {}

  public static Map<String, Object> binding(SharedDshBinding b) {
    return Map.of(
        "tenantId",
        b.getTenantId(),
        "memberId",
        b.getMemberId(),
        "placementId",
        b.getPlacementId(),
        "physicalInstanceId",
        b.getPhysicalInstanceId(),
        "mode",
        SharedDshPolicy.mode(b.getMode()),
        "epoch",
        b.getEpoch(),
        "revision",
        b.getRevision());
  }

  public static Map<String, Object> data(MessageLite value) {
    if (value instanceof SharedDshRevocation r)
      return Map.of(
          "schemaVersion",
          r.getSchemaVersion(),
          "tenantId",
          r.getTenantId(),
          "memberId",
          r.getMemberId(),
          "placementId",
          r.getPlacementId(),
          "epoch",
          r.getEpoch(),
          "authGeneration",
          r.getAuthGeneration(),
          "revision",
          r.getRevision(),
          "reason",
          r.getReason(),
          "revokedAt",
          r.getRevokedAt());
    if (value instanceof SharedDshResourceReport r)
      return Map.of(
          "schemaVersion",
          r.getSchemaVersion(),
          "binding",
          binding(r.getBinding()),
          "viewId",
          r.getViewId(),
          "status",
          r.getStatus(),
          "reason",
          r.getReason(),
          "observedAt",
          r.getObservedAt());
    if (value instanceof SharedDshGuardianImpact r)
      return Map.of(
          "schemaVersion",
          r.getSchemaVersion(),
          "tenantId",
          r.getTenantId(),
          "physicalInstanceId",
          r.getPhysicalInstanceId(),
          "operationId",
          r.getOperationId(),
          "reason",
          r.getReason(),
          "scope",
          r.getScope(),
          "affected",
          r.getAffectedList().stream().map(SharedDshEvents::binding).toList(),
          "state",
          r.getState(),
          "observedAt",
          r.getObservedAt());
    throw new IllegalArgumentException("Unknown shared event type");
  }
}
