package auriga.workspace.domain;

import auriga.common.error.*;
import auriga.contracts.node.v1.*;
import java.time.Instant;
import java.util.Set;

/** Initial budgets are admission limits, not measured capacity or isolation guarantees. */
public final class SharedDshPolicy {
  public static final int REGISTERED = 20, ACTIVE = 4, INODES = 100000;
  public static final long BYTES = 5368709120L, CANCEL_SECONDS = 30, ARCHIVE_SECONDS = 2592000;

  private SharedDshPolicy() {}

  public static void valid(boolean ok) {
    if (!ok) throw new AurigaException(CommonErrors.VALIDATION_FAILED);
  }

  public static void conflict(boolean ok) {
    if (!ok) throw new AurigaException(CommonErrors.CONFLICT);
  }

  public static String mode(DshInstanceMode mode) {
    return switch (mode) {
      case DSH_INSTANCE_MODE_SHARED -> "shared";
      case DSH_INSTANCE_MODE_DEDICATED -> "dedicated";
      default -> throw new AurigaException(CommonErrors.VALIDATION_FAILED);
    };
  }

  public static DshInstanceMode mode(String mode) {
    return switch (mode) {
      case "shared" -> DshInstanceMode.DSH_INSTANCE_MODE_SHARED;
      case "dedicated" -> DshInstanceMode.DSH_INSTANCE_MODE_DEDICATED;
      default -> throw new AurigaException(CommonErrors.VALIDATION_FAILED);
    };
  }

  public static boolean stepUp(Instant stepUp, Instant now) {
    return stepUp != null
        && !stepUp.isAfter(now)
        && now.getEpochSecond() - stepUp.getEpochSecond() < 600;
  }

  public static SharedDshRuntimeIntent intent(
      String operation, SharedDshBinding binding, String action) {
    valid(
        Set.of(
                "cooperative-cancel",
                "stop-pool",
                "discard-sessions",
                "archive-reset",
                "prepare-empty")
            .contains(action));
    return SharedDshRuntimeIntent.newBuilder()
        .setSchemaVersion(1)
        .setOperationId(operation)
        .setBinding(binding)
        .setAction(action)
        .setCancelWaitSeconds(CANCEL_SECONDS)
        .setArchiveRetentionSeconds(ARCHIVE_SECONDS)
        .setWorkspacePolicy("empty")
        .setSessionPolicy("discard")
        .build();
  }

  /** Exact immutable binding/action prevents late ACK from advancing a new step or generation. */
  public static boolean confirms(
      SharedDshRuntimeIntent intent, SharedDshRuntimeReceipt receipt, long now) {
    if (!receipt.getUnknownFields().asMap().isEmpty()
        || !receipt.getBinding().getUnknownFields().asMap().isEmpty()
        || receipt.getSchemaVersion() != 1
        || !receipt.getOperationId().equals(intent.getOperationId())
        || !receipt.getBinding().equals(intent.getBinding())
        || !receipt.getAction().equals(intent.getAction())
        || receipt.getObservedAt() > now
        || receipt.getObservedAt() < 0
        || receipt.getReceiptId().isBlank()
        || !receipt.getOutcome().equals("confirmed")) return false;
    return switch (intent.getAction()) {
      case "stop-pool" -> receipt.getExecutionTreeStopped();
      case "discard-sessions" -> receipt.getSessionsDiscarded();
      case "prepare-empty" -> receipt.getWorkspaceEmpty();
      case "archive-reset" ->
          receipt.getWorkspaceEmpty()
              && !receipt.getArchiveId().isBlank()
              && receipt.getArchiveExpiresAt() == receipt.getObservedAt() + ARCHIVE_SECONDS;
      case "cooperative-cancel" -> true;
      default -> false;
    };
  }
}
