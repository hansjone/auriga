package auriga.collab.app;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.persistence.UuidV7;
import auriga.contracts.iam.v1.*;
import java.nio.charset.StandardCharsets;
import java.sql.Timestamp;
import java.time.Instant;
import java.util.*;
import java.util.function.Consumer;
import org.junit.jupiter.api.Test;

/** Unit evidence for the local association/CAS only; no database or real IAM ownership claim. */
class NetworkLocalSourceHistoryTest {
  static final UUID TENANT = UUID.randomUUID();

  @Test
  void completeDigestBindsRawFailedFrameAndEveryFrozenColumn() {
    TenantContext.run(
        TENANT,
        () -> {
          var row = row();
          var snapshot = NetworkLocalSourceHistory.freeze(row);
          assertEquals("not_accepted", snapshot.result().path("result").asString());
          var fields = new ArrayList<>(row.keySet());
          for (String field : fields) {
            var bad = new LinkedHashMap<>(row);
            Object original = bad.get(field);
            bad.put(
                field,
                original instanceof byte[]
                    ? new byte[] {1}
                    : original instanceof Number
                        ? 2
                        : original instanceof Timestamp
                            ? Timestamp.from(Instant.EPOCH)
                            : original instanceof UUID ? UUID.randomUUID() : "tampered");
            unknown(() -> snapshot.requireUnchanged(bad));
          }
          var rawChanged = new LinkedHashMap<>(row);
          rawChanged.put(
              "original_frame",
              (" " + new String((byte[]) row.get("original_frame"), StandardCharsets.UTF_8))
                  .getBytes(StandardCharsets.UTF_8));
          assertNotEquals(
              row.get("local_history_digest"), NetworkLocalSourceHistory.digest(rawChanged));
          unknown(() -> NetworkLocalSourceHistory.freeze(rawChanged));
        });
  }

  @Test
  void oldIncompleteOrRecomputedWrongOriginalConnectionNeverBecomesProvenHistory()
      throws Exception {
    TenantContext.run(
        TENANT,
        () -> {
          for (String field :
              List.of(
                  "local_history_version",
                  "local_history_digest",
                  "original_connection_anchor",
                  "original_frame")) {
            var bad = row();
            bad.put(field, null);
            unknown(() -> NetworkLocalSourceHistory.freeze(bad));
          }
          for (String field : List.of("ingress", "server", "client", "account")) {
            var bad = row();
            var c = original(bad).toBuilder();
            switch (field) {
              case "ingress" -> c.setIngress("b".repeat(26));
              case "server" -> c.setServerId("N" + "B".repeat(55));
              case "client" -> c.setClientId("999");
              case "account" -> c.setClientAccount("another-account");
              default -> throw new AssertionError(field);
            }
            bad.put("original_connection", c.build().toByteArray());
            bad.put("local_history_digest", NetworkLocalSourceHistory.digest(bad));
            unknown(() -> NetworkLocalSourceHistory.freeze(bad));
          }
        });
  }

  @Test
  void snapshotsDefensivelyCopyBytesAndTimestampsAndNeverExposeMutableResults() {
    TenantContext.run(
        TENANT,
        () -> {
          var row = row();
          var matching = copy(row);
          var snapshot = NetworkLocalSourceHistory.freeze(row);
          ((byte[]) row.get("original_connection"))[0] ^= 1;
          ((Timestamp) row.get("created_at")).setTime(0);
          snapshot.result().put("result", "registered");
          snapshot.requireUnchanged(matching);
          assertEquals("not_accepted", snapshot.result().path("result").asString());
          unknown(() -> snapshot.requireUnchanged(row));
        });
  }

  @Test
  void invalidOriginalWitnessAndEpochRemainFrozenFailedInputWithoutNormalization() {
    TenantContext.run(
        TENANT,
        () -> {
          var row = row();
          var frame =
              (tools.jackson.databind.node.ObjectNode)
                  NetworkJson.parse((byte[]) row.get("original_frame"));
          frame.put("native_witness", "invalid-native-input");
          row.put("original_frame", NetworkJson.canonical(frame).getBytes(StandardCharsets.UTF_8));
          row.put("witness_json", NetworkJson.canonical(frame.path("native_witness")));
          row.put("local_history_digest", NetworkLocalSourceHistory.digest(row));
          var snapshot = NetworkLocalSourceHistory.freeze(row);
          snapshot.requireUnchanged(row);
          assertEquals(
              "invalid-old-epoch",
              NetworkJson.parse((byte[]) row.get("original_frame")).path("epoch").asString());
          assertEquals("\"invalid-native-input\"", row.get("witness_json"));
          assertEquals("not_accepted", snapshot.result().path("result").asString());
        });
  }

  @Test
  void compiledBadCasOmissionFailsTheSameFrozenHistoryInvariant() {
    TenantContext.run(
        TENANT,
        () -> {
          var original = row();
          var snapshot = NetworkLocalSourceHistory.freeze(original);
          var changed = copy(original);
          changed.put("original_connection", new byte[] {7});
          Consumer<Map<String, Object>> safe = snapshot::requireUnchanged;
          Consumer<Map<String, Object>> unsafe = new UnsafeHistoryCas()::requireUnchanged;
          assertRejectsChangedHistory(safe, changed);
          assertThrows(AssertionError.class, () -> assertRejectsChangedHistory(unsafe, changed));
        });
  }

  static final class UnsafeHistoryCas {
    void requireUnchanged(Map<String, Object> row) {
      // Deliberately bad compiled local implementation: an ID/status check is not a frozen-row CAS.
      if (row.get("id") == null || !"not_accepted".equals(row.get("status")))
        throw new AssertionError("not a tombstone");
    }
  }

  private static void assertRejectsChangedHistory(
      Consumer<Map<String, Object>> check, Map<String, Object> changed) {
    unknown(() -> check.accept(changed));
  }

  static Map<String, Object> row() {
    var row = new LinkedHashMap<String, Object>();
    var c =
        NetworkAuthorityConnection.newBuilder()
            .setTenantId(TENANT.toString())
            .setRealm("2")
            .setNode("20")
            .setIngress("a".repeat(26))
            .setServerId("N" + "A".repeat(55))
            .setClientId("12")
            .setClientAccount("clients-test")
            .build();
    var frame =
        NetworkJson.object()
            .put("app_version", "1.0")
            .put("type", "source_register")
            .put("operation_id", "local-op")
            .put("request_id", "original-request")
            .put("epoch", "invalid-old-epoch")
            .put("boot_id", "old-boot")
            .put("mode", "issue")
            .put("scope", "invalid-scope")
            .put("expected_revision", "0")
            .put("predecessor_binding", "")
            .putNull("native_witness");
    frame.putObject("registration").put("local", "7");
    var business = NetworkJson.object();
    for (String field :
        List.of("registration", "scope", "expected_revision", "predecessor_binding"))
      business.set(field, frame.path(field));
    String requestDigest = NetworkJson.digest(business);
    var result =
        NetworkJson.object()
            .put("app_version", "1.0")
            .put("type", "source_register_result")
            .put("operation_id", "local-op")
            .put("result", "not_accepted")
            .putNull("source");
    result
        .putObject("failure_evidence")
        .put("authority", "collab")
        .put("reason", "rejected_before_pending")
        .put("request_digest", requestDigest);
    row.put("id", UuidV7.next());
    row.put("tenant_id", TENANT);
    row.put("method", "Register");
    row.put("operation_id", "local-op");
    row.put("realm", "2");
    row.put("node_id", "20");
    row.put("business_json", NetworkJson.canonical(business));
    row.put("request_digest", requestDigest);
    row.put("original_frame", NetworkJson.canonical(frame).getBytes(StandardCharsets.UTF_8));
    row.put("original_connection", c.toByteArray());
    for (String field :
        List.of(
            "iam_request",
            "iam_request_json",
            "iam_response",
            "lineage_id",
            "witness_json",
            "stop_version",
            "permission_version")) row.put(field, null);
    row.put("epoch", 1L);
    row.put("boot_id", "old-boot");
    row.put("status", "not_accepted");
    row.put("result_json", NetworkJson.canonical(result));
    row.put("created_at", Timestamp.from(Instant.parse("2026-10-27T00:00:00Z")));
    row.put("local_history_version", 1);
    row.put(
        "original_connection_anchor",
        NetworkConnectionAnchor.newBuilder()
            .setAnchorId(UuidV7.next().toString())
            .setConnection(c)
            .build()
            .toByteArray());
    row.put("local_history_digest", NetworkLocalSourceHistory.digest(row));
    return row;
  }

  private static NetworkAuthorityConnection original(Map<String, Object> row) {
    try {
      return NetworkAuthorityConnection.parseFrom((byte[]) row.get("original_connection"));
    } catch (Exception e) {
      throw new AssertionError(e);
    }
  }

  private static Map<String, Object> copy(Map<String, Object> row) {
    var copy = new LinkedHashMap<String, Object>();
    row.forEach(
        (k, v) ->
            copy.put(
                k,
                v instanceof byte[] b
                    ? b.clone()
                    : v instanceof Timestamp t ? Timestamp.from(t.toInstant()) : v));
    return copy;
  }

  private static void unknown(Runnable work) {
    assertEquals(
        "network.outcome_unknown",
        assertThrows(AurigaException.class, work::run).errorCode().code());
  }
}
