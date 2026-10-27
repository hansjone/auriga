package auriga.collab.app;

import auriga.collab.grpc.NetworkAuthorityClient;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.ErrorCode;
import auriga.contracts.iam.v1.NetworkAuthorityConnection;
import auriga.contracts.iam.v1.NetworkConnectionAnchor;
import io.grpc.Status;
import java.security.MessageDigest;
import java.sql.Timestamp;
import java.util.*;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** Local association integrity only. Historical ownership is established by the pure IAM RPC. */
final class NetworkLocalSourceHistory {
  private static final ErrorCode UNKNOWN =
      ErrorCode.of("network.outcome_unknown", 503, Status.Code.FAILED_PRECONDITION);

  private NetworkLocalSourceHistory() {}

  static final class Snapshot {
    private final Map<String, Object> frozen;
    private final NetworkConnectionAnchor anchor;

    private Snapshot(Map<String, Object> row, NetworkConnectionAnchor anchor) {
      var copy = new LinkedHashMap<String, Object>();
      row.forEach(
          (key, value) ->
              copy.put(
                  key,
                  value instanceof byte[] bytes
                      ? bytes.clone()
                      : value instanceof Timestamp time
                          ? Timestamp.from(time.toInstant())
                          : value));
      frozen = Collections.unmodifiableMap(copy);
      this.anchor = anchor;
    }

    NetworkConnectionAnchor anchor() {
      return anchor;
    }

    ObjectNode result() {
      return (ObjectNode) NetworkJson.parse(frozen.get("result_json").toString());
    }

    void requireUnchanged(Map<String, Object> current) {
      check(current != null && frozen.keySet().equals(current.keySet()));
      frozen.forEach(
          (key, value) ->
              check(
                  value instanceof byte[] bytes
                      ? current.get(key) instanceof byte[] next && Arrays.equals(bytes, next)
                      : Objects.equals(value, current.get(key))));
      freeze(current);
    }
  }

  static Snapshot freeze(Map<String, Object> row) {
    try {
      check(row != null && Integer.valueOf(1).equals(row.get("local_history_version")));
      var anchor =
          NetworkConnectionAnchor.parseFrom((byte[]) row.get("original_connection_anchor"));
      NetworkAuthorityClient.connectionAnchor(anchor);
      var original = NetworkAuthorityConnection.parseFrom((byte[]) row.get("original_connection"));
      var frame = NetworkJson.parse((byte[]) row.get("original_frame"));
      var body = business(frame);
      String requestDigest = NetworkJson.digest(body);
      var expected =
          NetworkJson.object()
              .put("app_version", "1.0")
              .put("type", "source_register_result")
              .put("operation_id", row.get("operation_id").toString())
              .put("result", "not_accepted")
              .putNull("source");
      expected
          .putObject("failure_evidence")
          .put("authority", "collab")
          .put("reason", "rejected_before_pending")
          .put("request_digest", requestDigest);
      check(
          original.equals(anchor.getConnection())
              && TenantContext.require().equals(row.get("tenant_id"))
              && original.getRealm().equals(row.get("realm"))
              && original.getNode().equals(row.get("node_id"))
              && "Register".equals(row.get("method"))
              && "not_accepted".equals(row.get("status"))
              && "source_register".equals(frame.path("type").asString())
              && frame.path("operation_id").asString().equals(row.get("operation_id"))
              && frame.path("boot_id").asString().equals(row.get("boot_id"))
              // The original frame may itself have failed its epoch/witness fence before pending.
              && ((Number) row.get("epoch")).longValue() > 0
              && NetworkJson.canonical(body).equals(row.get("business_json"))
              && requestDigest.equals(row.get("request_digest"))
              && expected.equals(NetworkJson.parse(row.get("result_json").toString()))
              && row.get("iam_request") == null
              && row.get("iam_request_json") == null
              && row.get("iam_response") == null
              && row.get("lineage_id") == null
              && row.get("stop_version") == null
              && row.get("permission_version") == null
              && Objects.equals(
                  frame.hasNonNull("native_witness")
                      ? NetworkJson.canonical(frame.path("native_witness"))
                      : null,
                  row.get("witness_json"))
              && digest(row).equals(row.get("local_history_digest")));
      return new Snapshot(row, anchor);
    } catch (Exception e) {
      throw new AurigaException(UNKNOWN);
    }
  }

  static String digest(Map<String, Object> row) {
    try {
      var anchor =
          NetworkConnectionAnchor.parseFrom((byte[]) row.get("original_connection_anchor"));
      var original = NetworkAuthorityConnection.parseFrom((byte[]) row.get("original_connection"));
      var bound =
          NetworkJson.object()
              .put("version", Objects.toString(row.get("local_history_version"), ""))
              .put("id", row.get("id").toString())
              .put("tenant_id", row.get("tenant_id").toString())
              .put("method", row.get("method").toString())
              .put("operation_id", row.get("operation_id").toString())
              .put("realm", row.get("realm").toString())
              .put("node", row.get("node_id").toString())
              .put(
                  "original_frame_sha256",
                  "sha256:"
                      + HexFormat.of()
                          .formatHex(
                              MessageDigest.getInstance("SHA-256")
                                  .digest((byte[]) row.get("original_frame"))))
              .put("stored_epoch", row.get("epoch").toString())
              .put("stored_boot_id", row.get("boot_id").toString())
              .put("request_digest", row.get("request_digest").toString())
              .put("status", row.get("status").toString())
              .put("created_at", ((Timestamp) row.get("created_at")).toInstant().toString());
      bound
          .putObject("anchor")
          .put("anchor_id", anchor.getAnchorId())
          .set("connection", connection(anchor.getConnection()));
      bound.set("original_connection", connection(original));
      bound.set("business", NetworkJson.parse(row.get("business_json").toString()));
      bound.set("result", NetworkJson.parse(row.get("result_json").toString()));
      // The original bytes and stored witness text retain even malformed failed input verbatim.
      var witness =
          row.get("witness_json") == null
              ? null
              : NetworkJson.parse(row.get("witness_json").toString());
      bound.set(
          "witness", witness != null && witness.isObject() ? witness : NetworkJson.JSON.nullNode());
      nullable(bound, "stored_witness_json", row.get("witness_json"));
      for (String field :
          List.of(
              "iam_request",
              "iam_request_json",
              "iam_response",
              "lineage_id",
              "stop_version",
              "permission_version")) nullable(bound, field, row.get(field));
      return NetworkJson.digest(bound);
    } catch (Exception e) {
      throw new AurigaException(UNKNOWN);
    }
  }

  private static void nullable(ObjectNode target, String field, Object value) {
    if (value == null) target.putNull(field);
    else if (value instanceof byte[] bytes)
      target.put(field, Base64.getEncoder().encodeToString(bytes));
    else target.put(field, value.toString());
  }

  private static ObjectNode connection(NetworkAuthorityConnection c) {
    return NetworkJson.object()
        .put("tenant_id", c.getTenantId())
        .put("realm", c.getRealm())
        .put("node", c.getNode())
        .put("ingress", c.getIngress())
        .put("server_id", c.getServerId())
        .put("client_id", c.getClientId())
        .put("client_account", c.getClientAccount());
  }

  private static ObjectNode business(JsonNode frame) {
    var result = NetworkJson.object();
    for (String field :
        List.of("registration", "scope", "expected_revision", "predecessor_binding"))
      result.set(field, frame.path(field));
    return result;
  }

  private static void check(boolean value) {
    if (!value) throw new AurigaException(UNKNOWN);
  }
}
