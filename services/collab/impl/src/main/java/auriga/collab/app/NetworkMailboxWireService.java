package auriga.collab.app;

import auriga.collab.api.NetworkSourceAuthority.Target;
import auriga.collab.grpc.NetworkAuthorityClient;
import auriga.collab.grpc.NetworkSourceGrpcAuthority;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.ErrorCode;
import auriga.common.persistence.TenantTransactionManager;
import auriga.contracts.iam.v1.NetworkAuthorityConnection;
import io.grpc.Status;
import java.time.*;
import java.util.*;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.transaction.support.TransactionTemplate;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/**
 * Transactional wire adaptation. Unsupported recovery modes stay fenced, never silently ignored.
 */
public class NetworkMailboxWireService {
  private static final ErrorCode RECOVERY =
      ErrorCode.of("network.recovery_conflict", 412, Status.Code.FAILED_PRECONDITION);
  private final NetworkMailboxService mailbox;
  private final JdbcTemplate jdbc;
  private final TransactionTemplate tx;
  private final NetworkSourceGrpcAuthority source;
  private final NetworkEnvelopeSigner signer;
  private final Clock clock;

  public NetworkMailboxWireService(
      NetworkMailboxService mailbox,
      JdbcTemplate jdbc,
      TenantTransactionManager manager,
      NetworkSourceGrpcAuthority source,
      NetworkSigningKeys keys,
      Clock clock) {
    this.mailbox = mailbox;
    this.jdbc = jdbc;
    tx = new TransactionTemplate(manager);
    this.source = source;
    signer = new NetworkEnvelopeSigner(keys, clock);
    this.clock = clock;
  }

  public String ready(NetworkAuthorityConnection c, byte[] frame) {
    JsonNode request = frame(frame, "Ready");
    NetworkAuthorityClient.binding(c);
    Instant deadline = source.verify(c);
    UUID tenant = TenantContext.require();
    return tx.execute(
        status -> {
          jdbc.update(
              "INSERT INTO collab.network_node(tenant_id,realm,node_id) VALUES (?,?,?) ON CONFLICT DO NOTHING",
              tenant,
              c.getRealm(),
              c.getNode());
          var row = locked(c);
          String binding = bindingJson(c);
          String requestJson = NetworkJson.canonical(request);
          if (c.getIngress().equals(row.get("ingress_id"))) {
            require(binding.equals(row.get("wire_binding_json")), NetworkErrors.UNAUTHORIZED);
            require(requestJson.equals(row.get("wire_ready_json")), NetworkErrors.CONFLICT);
          } else {
            require(
                jdbc.queryForObject(
                        "SELECT count(*) FROM collab.network_wire_ingress WHERE tenant_id=? AND ingress_id=?",
                        Integer.class,
                        tenant,
                        c.getIngress())
                    == 0,
                NetworkErrors.STALE_EPOCH);
            String incarnation =
                row.get("wire_incarnation") == null
                    ? UUID.randomUUID().toString()
                    : row.get("wire_incarnation").toString();
            Target initial =
                new Target(
                    c.getRealm(),
                    c.getNode(),
                    incarnation,
                    1,
                    request.path("channel_nonce").asString(),
                    request.path("request_id").asString(),
                    deadline);
            var route =
                source.withTarget(
                    c, initial, () -> mailbox.ready(tenant, c.getRealm(), c.getNode()));
            boolean healthy =
                request.path("local_high_water").asString().equals("0")
                    && request.path("registration_revision").asString().equals("0")
                    && request
                        .path("resume_cursor")
                        .asString()
                        .equals(Long.toString(route.committedCursor()))
                    && request
                        .path("receipt_checkpoint")
                        .path("checkpoint_seq")
                        .asString()
                        .equals("0")
                    && request
                        .path("receipt_checkpoint")
                        .path("checkpoint_revision")
                        .asString()
                        .equals("0")
                    && request.path("pending_registration").isNull()
                    && (!request.has("evidence") || request.path("evidence").isEmpty());
            jdbc.update(
                "UPDATE collab.network_node SET ingress_id=?,wire_binding_json=?,wire_ready_json=?,wire_incarnation=?,wire_nonce=?,wire_status=?,route_expires_at=? WHERE tenant_id=? AND realm=? AND node_id=?",
                c.getIngress(),
                binding,
                requestJson,
                incarnation,
                initial.channelNonce(),
                healthy ? "ready" : "recovery_conflict",
                java.sql.Timestamp.from(deadline),
                tenant,
                c.getRealm(),
                c.getNode());
            jdbc.update(
                "INSERT INTO collab.network_wire_ingress(tenant_id,realm,node_id,ingress_id) VALUES (?,?,?,?)",
                tenant,
                c.getRealm(),
                c.getNode(),
                c.getIngress());
            row = locked(c);
          }
          var target = target(c, row, request.path("request_id").asString(), deadline);
          var current = row;
          return source.withTarget(
              c,
              target,
              () -> {
                var route = mailbox.head(tenant, c.getRealm(), c.getNode());
                var result =
                    base("ready_result")
                        .put("request_id", target.requestId())
                        .put("status", current.get("wire_status").toString())
                        .put("epoch", Long.toString(target.epoch()))
                        .put("head_seq", Long.toString(route.headSeq()))
                        .put("committed_cursor", Long.toString(route.committedCursor()))
                        .put("retention_low_seq", "0")
                        .put("local_high_water", "0")
                        .put("registration_revision", "0");
                result
                    .putObject("node_identity")
                    .put("realm", c.getRealm())
                    .put("node", c.getNode())
                    .put("incarnation", target.incarnation());
                result.putNull("seen_checkpoint");
                result.putArray("required_features");
                return signed(result, "ReadyResult", target);
              });
        });
  }

  public String head(NetworkAuthorityConnection c) {
    return associated(
        c,
        null,
        (row, target, route) -> {
          return signed(
              base("head")
                  .put("epoch", Long.toString(target.epoch()))
                  .put("head_seq", Long.toString(route.headSeq())),
              "Head",
              target);
        });
  }

  public String fetch(NetworkAuthorityConnection c, byte[] bytes) {
    var request = frame(bytes, "Fetch");
    // Existing core has cursor pagination only. Never discard proof/hold/barrier instructions.
    require(request.path("continuation").isNull(), NetworkErrors.PROTOCOL_ERROR);
    for (String field : List.of("held_ids", "paused", "state_barrier"))
      require(!request.has(field) || request.path(field).isEmpty(), NetworkErrors.PROTOCOL_ERROR);
    return associated(
        c,
        request.path("request_id").asString(),
        (row, target, route) -> {
          require(decimal(request, "epoch") == target.epoch(), NetworkErrors.STALE_EPOCH);
          return mailbox.fetchPage(
              TenantContext.require(),
              c.getRealm(),
              c.getNode(),
              decimal(request, "resume_cursor"),
              request.path("max_items").asInt(),
              request.path("max_bytes").asInt());
        });
  }

  public String receipt(NetworkAuthorityConnection c, byte[] bytes) {
    var request = frame(bytes, "Receipt");
    return associated(
        c,
        request.path("request_id").asString(),
        (row, target, route) -> {
          UUID tenant = TenantContext.require();
          require(decimal(request, "epoch") == target.epoch(), NetworkErrors.STALE_EPOCH);
          String operation = request.path("operation_id").asString();
          String canonical = NetworkJson.canonical(request);
          var existing =
              jdbc.queryForList(
                  "SELECT request_json,result_json FROM collab.network_wire_receipt WHERE tenant_id=? AND realm=? AND node_id=? AND operation_id=?",
                  tenant,
                  c.getRealm(),
                  c.getNode(),
                  operation);
          if (!existing.isEmpty()) {
            require(
                canonical.equals(existing.getFirst().get("request_json")), NetworkErrors.CONFLICT);
            var result =
                (ObjectNode) NetworkJson.parse(existing.getFirst().get("result_json").toString());
            return signed(result, "ReceiptCommitted", target);
          }
          UUID delivery = uuid(request.path("delivery_id").asString());
          var items = new ArrayList<NetworkMailboxService.ReceiptItem>();
          var unique = new HashSet<UUID>();
          for (var entry : request.path("results")) {
            UUID recipient = uuid(entry.path("recipient_id").asString());
            require(unique.add(recipient), NetworkErrors.PROTOCOL_ERROR);
            var records =
                jdbc.queryForList(
                    """
            SELECT r.target_local,r.state_version,m.message_json FROM collab.network_recipient r
            JOIN collab.network_mail_item i ON i.tenant_id=r.tenant_id AND i.id=r.delivery_id
            JOIN collab.network_message m ON m.tenant_id=i.tenant_id AND m.id=i.message_id
            WHERE r.tenant_id=? AND r.id=? AND i.id=? AND i.target_realm=? AND i.target_node=? AND i.mail_seq=? FOR UPDATE OF r
            """,
                    tenant,
                    recipient,
                    delivery,
                    c.getRealm(),
                    c.getNode(),
                    decimal(request, "mail_seq"));
            require(records.size() == 1, NetworkErrors.UNAUTHORIZED);
            var record = records.getFirst();
            var address = entry.path("target");
            require(
                c.getRealm().equals(address.path("realm").asString())
                    && c.getNode().equals(address.path("node").asString())
                    && record.get("target_local").equals(address.path("local").asString()),
                NetworkErrors.UNAUTHORIZED);
            require(
                address
                    .path("type")
                    .asString()
                    .equals(
                        source.resolveTargetType(
                            tenant, c.getRealm(), c.getNode(), address.path("local").asString())),
                NetworkErrors.UNAUTHORIZED);
            require(
                decimal(entry, "expected_state_version") == number(record, "state_version"),
                NetworkErrors.CONFLICT);
            require(record.get("message_json") != null, NetworkErrors.PROTOCOL_ERROR);
            var message = NetworkJson.parse(record.get("message_json").toString());
            require(
                NetworkJson.digest(message).equals(entry.path("message_digest").asString()),
                NetworkErrors.CONFLICT);
            String outcome = entry.path("status").asString();
            require(
                Set.of("host_accepted", "quarantined", "rejected").contains(outcome),
                NetworkErrors.PROTOCOL_ERROR);
            if ("host_accepted".equals(outcome)
                && !"unicast".equals(message.path("message_class").asString("unicast")))
              require(
                  "inbox_only".equals(entry.path("acceptance_mode").asString())
                      && "not_applicable".equals(entry.path("execution_status").asString()),
                  NetworkErrors.PROTOCOL_ERROR);
            items.add(
                new NetworkMailboxService.ReceiptItem(
                    recipient, "rejected".equals(outcome) ? "revoked" : outcome));
          }
          var commit =
              mailbox.receipt(
                  new NetworkMailboxService.ReceiptRequest(
                      tenant,
                      c.getRealm(),
                      c.getNode(),
                      target.epoch(),
                      delivery,
                      decimal(request, "mail_seq"),
                      items));
          var result =
              base("receipt_committed")
                  .put("request_id", target.requestId())
                  .put("operation_id", operation)
                  .put("delivery_id", delivery.toString())
                  .put("mail_seq", Long.toString(commit.mailSeq()))
                  .put("mail_item_closed", commit.mailItemClosed())
                  .put("committed_cursor", Long.toString(commit.committedCursor()));
          var results = result.putArray("results");
          for (var entry : commit.results())
            results
                .addObject()
                .put("recipient_id", entry.recipientId().toString())
                .put("transport_status", entry.transportStatus())
                .put("state_version", Long.toString(entry.stateVersion()));
          String signed = signed(result, "ReceiptCommitted", target);
          jdbc.update(
              "INSERT INTO collab.network_wire_receipt(tenant_id,realm,node_id,operation_id,request_json,result_json) VALUES (?,?,?,?,?,?)",
              tenant,
              c.getRealm(),
              c.getNode(),
              operation,
              canonical,
              NetworkJson.canonical(result));
          return signed;
        });
  }

  private interface Work {
    String apply(Map<String, Object> row, Target target, NetworkMailboxService.NodeRoute route);
  }

  private String associated(NetworkAuthorityConnection c, String requestId, Work work) {
    NetworkAuthorityClient.binding(c);
    Instant deadline = source.verify(c);
    return tx.execute(
        status -> {
          var row = locked(c);
          require(bindingJson(c).equals(row.get("wire_binding_json")), NetworkErrors.STALE_EPOCH);
          require("ready".equals(row.get("wire_status")), RECOVERY);
          var target = target(c, row, requestId, deadline);
          return source.withTarget(
              c,
              target,
              () -> {
                // Replays must recheck current RECEIVE authority too, before looking up evidence.
                var route = mailbox.head(TenantContext.require(), c.getRealm(), c.getNode());
                return work.apply(row, target, route);
              });
        });
  }

  private Map<String, Object> locked(NetworkAuthorityConnection c) {
    var rows =
        jdbc.queryForList(
            "SELECT * FROM collab.network_node WHERE tenant_id=? AND realm=? AND node_id=? FOR UPDATE",
            TenantContext.require(),
            c.getRealm(),
            c.getNode());
    require(rows.size() == 1, NetworkErrors.STALE_EPOCH);
    return rows.getFirst();
  }

  private Target target(
      NetworkAuthorityConnection c, Map<String, Object> row, String requestId, Instant deadline) {
    return new Target(
        c.getRealm(),
        c.getNode(),
        row.get("wire_incarnation").toString(),
        number(row, "connection_epoch"),
        row.get("wire_nonce").toString(),
        requestId,
        deadline);
  }

  private String signed(ObjectNode result, String definition, Target target) {
    require(target.validUntil().isAfter(clock.instant()), NetworkErrors.UNAUTHORIZED);
    var signed =
        signer.sign(
            result,
            "protocol_response",
            new NetworkEnvelopeSigner.Context(
                target.realm(),
                target.node(),
                target.incarnation(),
                target.epoch(),
                target.channelNonce(),
                target.requestId()),
            target.validUntil());
    NetworkSchemas.validate("protocol", definition, signed);
    return NetworkJson.canonical(signed);
  }

  private static JsonNode frame(byte[] bytes, String definition) {
    var frame = NetworkJson.parse(bytes);
    require(
        definition.toLowerCase(Locale.ROOT).equals(frame.path("type").asString()),
        NetworkErrors.PROTOCOL_ERROR);
    NetworkSchemas.validate("protocol", definition, frame);
    return frame;
  }

  private static ObjectNode base(String type) {
    return NetworkJson.object().put("app_version", "1.0").put("type", type);
  }

  private static String bindingJson(NetworkAuthorityConnection c) {
    return NetworkJson.canonical(
        NetworkJson.object()
            .put("tenant", c.getTenantId())
            .put("realm", c.getRealm())
            .put("node", c.getNode())
            .put("ingress", c.getIngress())
            .put("server", c.getServerId())
            .put("client", c.getClientId())
            .put("account", c.getClientAccount()));
  }

  private static long number(Map<String, Object> row, String key) {
    return ((Number) row.get(key)).longValue();
  }

  private static long decimal(JsonNode frame, String key) {
    try {
      return Long.parseLong(frame.path(key).asString());
    } catch (NumberFormatException e) {
      throw new AurigaException(NetworkErrors.PROTOCOL_ERROR);
    }
  }

  private static UUID uuid(String value) {
    try {
      return UUID.fromString(value);
    } catch (IllegalArgumentException e) {
      throw new AurigaException(NetworkErrors.PROTOCOL_ERROR);
    }
  }

  private static void require(boolean valid, ErrorCode error) {
    if (!valid) throw new AurigaException(error);
  }
}
