package auriga.collab.network.local;

import static auriga.collab.network.local.LocalRecovery.Resolution.*;
import static org.junit.jupiter.api.Assertions.*;

import auriga.collab.app.NetworkJson;
import java.math.BigDecimal;
import java.math.BigInteger;
import java.util.Map;
import org.junit.jupiter.api.Test;
import tools.jackson.databind.node.ObjectNode;

class LocalRecoveryTest {
  static final String FP = "sha256:" + "a".repeat(64);

  @Test
  void freshIdentityAndMatchingCountersAreReady() {
    assertEquals(READY, inspect("0", "0", ready("0", "0", null), null));
    assertEquals(READY, inspect("7", "1", ready("7", "1", null), null));
  }

  @Test
  void matchingPendingHeadRepairsOnlyOriginalCommittedResponse() {
    assertEquals(RETURN_ORIGINAL, inspect("7", "1", ready("7", "0", "7"), registration("7", "1")));
    assertEquals(RETURN_ORIGINAL, inspect("7", "1", ready("7", "1", "7"), registration("7", "1")));
  }

  @Test
  void uncommittedHeadMayRetryWithoutMovingPlatformWitness() {
    assertEquals(READY, inspect("7", "1", ready("8", "1", "8"), null));
  }

  @Test
  void oldSnapshotCannotBeRepairedByDownloadingCounters() {
    assertEquals(IDENTITY_ROLLBACK, inspect("7", "1", ready("0", "0", null), null));
    assertEquals(IDENTITY_ROLLBACK, inspect("7", "1", ready("7", "0", null), null));
  }

  @Test
  void higherClientWitnessIndicatesPossiblePlatformBackupRegression() {
    assertEquals(RECOVERY_CONFLICT, inspect("0", "0", ready("7", "1", null), null));
    assertEquals(RECOVERY_CONFLICT, inspect("7", "1", ready("8", "1", null), null));
  }

  @Test
  void fingerprintMismatchAndTwoDifferentMatchingRowsNeverRepair() {
    var changed = ready("7", "0", "7");
    ((ObjectNode) changed.path("pending_registration"))
        .put("allocation_fingerprint", "sha256:" + "b".repeat(64));
    assertEquals(RECOVERY_CONFLICT, inspect("7", "1", changed, registration("7", "1")));
    assertEquals(RECOVERY_CONFLICT, inspect("7", "1", ready("7", "0", "7"), Map.of()));
  }

  @Test
  void pendingHeadCannotExplainMoreThanOneMissingRevisionOrSkipAnotherCommittedHead() {
    assertEquals(
        RECOVERY_CONFLICT, inspect("8", "2", ready("8", "0", "8"), registration("8", "2")));
    assertEquals(
        RECOVERY_CONFLICT, inspect("8", "2", ready("8", "1", "7"), registration("7", "1")));
  }

  @Test
  void uncommittedHeadMustMatchLocalReservationAndCurrentRevision() {
    assertEquals(RECOVERY_CONFLICT, inspect("7", "1", ready("9", "1", "8"), null));
    assertEquals(RECOVERY_CONFLICT, inspect("7", "1", ready("8", "0", "8"), null));
    assertEquals(RECOVERY_CONFLICT, inspect("7", "1", ready("7", "1", "7"), null));
  }

  @Test
  void unsignedMaximumComparisonRemainsExact() {
    String max = "18446744073709551615";
    assertEquals(RETURN_ORIGINAL, inspect(max, "2", ready(max, "1", max), registration(max, "2")));
  }

  LocalRecovery.Resolution inspect(
      String high, String revision, ObjectNode ready, Map<String, Object> original) {
    return LocalRecovery.inspect(new BigInteger(high), new BigInteger(revision), ready, original);
  }

  ObjectNode ready(String high, String revision, String pending) {
    var ready =
        (ObjectNode)
            NetworkJson.parse(
                """
        {"local_high_water":"%s","registration_revision":"%s","pending_registration":null}
        """
                    .formatted(high, revision));
    if (pending != null)
      ready
          .putObject("pending_registration")
          .put("candidate_local", pending)
          .put("allocation_op_id", "op")
          .put("allocation_fingerprint", FP);
    return ready;
  }

  Map<String, Object> registration(String local, String revision) {
    return Map.of(
        "local_id",
        new BigDecimal(local),
        "registration_revision",
        new BigDecimal(revision),
        "allocation_op_id",
        "op",
        "allocation_fingerprint",
        FP);
  }
}
