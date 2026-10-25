package auriga.collab.network.local;

import java.math.BigDecimal;
import java.math.BigInteger;
import java.util.Map;
import tools.jackson.databind.JsonNode;

/** Pure registration-evidence assessment, independent of storage and receipt recovery. */
final class LocalRecovery {
  enum Resolution {
    READY,
    RETURN_ORIGINAL,
    RECOVERY_CONFLICT,
    IDENTITY_ROLLBACK
  }

  static Resolution inspect(
      BigInteger high, BigInteger revision, JsonNode ready, Map<String, Object> original) {
    BigInteger clientHigh = new BigInteger(ready.path("local_high_water").asString());
    BigInteger clientRevision = new BigInteger(ready.path("registration_revision").asString());
    JsonNode pending = ready.path("pending_registration");
    if (!pending.isNull()) {
      String local = pending.path("candidate_local").asString();
      if (original != null) {
        boolean same =
            matches(
                original,
                local,
                pending.path("allocation_op_id").asString(),
                pending.path("allocation_fingerprint").asString());
        boolean last = same && integer(original, "registration_revision").equals(revision);
        boolean missingResponse = clientRevision.add(BigInteger.ONE).equals(revision);
        boolean alreadySaved = clientRevision.equals(revision);
        return same && last && (missingResponse || alreadySaved) && clientHigh.equals(high)
            ? Resolution.RETURN_ORIGINAL
            : Resolution.RECOVERY_CONFLICT;
      }
      // No committed original: retry the SAME head; do not register it as a side effect of READY.
      return clientRevision.equals(revision)
              && clientHigh.equals(new BigInteger(local))
              && clientHigh.compareTo(high) > 0
          ? Resolution.READY
          : Resolution.RECOVERY_CONFLICT;
    }
    if (clientHigh.compareTo(high) < 0 || clientRevision.compareTo(revision) < 0)
      return Resolution.IDENTITY_ROLLBACK;
    return clientHigh.equals(high) && clientRevision.equals(revision)
        ? Resolution.READY
        : Resolution.RECOVERY_CONFLICT;
  }

  static boolean matches(Map<String, Object> row, String local, String op, String fingerprint) {
    return row.containsKey("local_id")
        && integer(row, "local_id").equals(new BigInteger(local))
        && op.equals(row.get("allocation_op_id"))
        && fingerprint.equals(row.get("allocation_fingerprint"));
  }

  private static BigInteger integer(Map<String, Object> row, String key) {
    return new BigDecimal(row.get(key).toString()).toBigIntegerExact();
  }

  private LocalRecovery() {}
}
