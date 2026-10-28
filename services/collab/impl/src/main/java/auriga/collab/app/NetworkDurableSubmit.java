package auriga.collab.app;

import auriga.collab.api.NetworkSourceAuthority.Source;
import auriga.collab.app.NetworkMailboxWireService.SourceGate;
import auriga.collab.grpc.NetworkSourceGrpcAuthority;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.persistence.TenantTransactionManager;
import auriga.common.persistence.UuidV7;
import auriga.contracts.iam.v1.*;
import java.math.BigDecimal;
import java.sql.Timestamp;
import java.time.*;
import java.util.*;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.transaction.support.TransactionTemplate;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** Ordinary online session unicast: freeze, authorize without PG resources, then locked CAS. */
final class NetworkDurableSubmit {
  private final NetworkMailboxWireService wire;
  private final NetworkMailboxService mailbox;
  private final JdbcTemplate jdbc;
  private final TransactionTemplate tx;
  private final NetworkSourceGrpcAuthority authority;
  private final Clock clock;

  NetworkDurableSubmit(
      NetworkMailboxWireService wire,
      NetworkMailboxService mailbox,
      JdbcTemplate jdbc,
      TenantTransactionManager manager,
      NetworkSourceGrpcAuthority authority,
      Clock clock) {
    this.wire = wire;
    this.mailbox = mailbox;
    this.jdbc = jdbc;
    tx = new TransactionTemplate(manager);
    this.authority = authority;
    this.clock = clock;
  }

  static void validate(JsonNode request) {
    NetworkSchemas.validate("send", "SubmitRequest", request);
    for (String forbidden :
        List.of(
            "from",
            "tenant",
            "tenant_id",
            "realm",
            "node",
            "user",
            "sender_kind",
            "reply_to",
            "source_context",
            "server_auth",
            "authorization_ref",
            "origin",
            "correlation_id",
            "causation_id")) check(!request.has(forbidden));
    check(
        "message".equals(request.path("kind").asString())
            && "session".equals(request.path("to").path("type").asString())
            && "durable".equals(request.path("durability").asString())
            && request.path("source").hasNonNull("source_binding")
            && !request.path("source").has("human_intent_id")
            && !request.has("in_reply_to")
            && !request.has("reply_mode")
            && (!request.has("dispatch_mode")
                || "atomic".equals(request.path("dispatch_mode").asString())));
  }

  String submit(NetworkAuthorityConnection connection, byte[] bytes) {
    var request = NetworkJson.parse(bytes);
    validate(request);
    check(connection.getRealm().equals(request.path("to").path("realm").asString()));
    return authority.sourceRequest(
        () -> {
          // Request ID is an internal correlation only; the HTTPS body remains SubmitRequest.
          var gate =
              wire.sourceGate(
                  connection, NetworkJson.object().put("request_id", UuidV7.next().toString()));
          var pending = tx.execute(s -> freeze(gate, request));
          if (pending.get("result_json") != null) return pending.get("result_json").toString();
          var target =
              targetConnection(
                  NetworkJson.parse(pending.get("target_fence_json").toString()).path("node"));
          var targetGate =
              wire.sourceGate(
                  target, NetworkJson.object().put("request_id", UuidV7.next().toString()));
          Instant until =
              authority.withTarget(
                  target,
                  targetGate.target(),
                  () ->
                      authority.authorizeDelivery(
                          tenant(),
                          pending.get("message_json").toString(),
                          pending.get("source_context_json").toString(),
                          target.getNode()));
          // Refresh both authenticated connections outside the final transaction; local generations
          // and source permission/stop versions are still rechecked under locks below.
          Instant refreshed = wire.refreshSourceGate(gate);
          Instant targetUntil = wire.refreshSourceGate(targetGate);
          return tx.execute(
              s ->
                  commit(
                      gate,
                      targetGate,
                      request,
                      pending,
                      Collections.min(List.of(until, refreshed, targetUntil))));
        });
  }

  private Map<String, Object> freeze(SourceGate gate, JsonNode request) {
    lockNodes(
        gate.connection().getRealm(),
        gate.connection().getNode(),
        request.path("to").path("node").asString());
    wire.lockSourceGate(gate, false);
    String binding = request.path("source").path("source_binding").asString();
    var source = source(binding, false);
    check(
        gate.connection().getNode().equals(source.get("node_id"))
            && gate.connection().getRealm().equals(source.get("realm")));
    var idempotency = request.path("idempotency");
    var found =
        jdbc.queryForList(
            "SELECT * FROM collab.network_submit WHERE tenant_id=? AND source_binding=? AND producer_epoch=? AND producer_key=? FOR UPDATE",
            tenant(),
            binding,
            idempotency.path("producer_epoch").asString(),
            idempotency.path("key").asString());
    if (!found.isEmpty()) {
      var original = found.getFirst();
      check(NetworkJson.canonical(request).equals(original.get("request_json")));
      // Lookup precedes fresh source/epoch checks: committed results survive source expiry,
      // stop and connection takeover, while current authenticated node ownership is required.
      return original;
    }
    var node = node(gate.connection().getRealm(), gate.connection().getNode());
    check(
        node.get("producer_epoch")
            .toString()
            .equals(idempotency.path("producer_epoch").asString()));
    var observation = observation(source, gate);
    var local =
        registration(
            gate.connection().getRealm(),
            gate.connection().getNode(),
            observation.getSource().getRegistration().getLocal());
    checkRegistration(local, observation.getSource().getRegistration());
    var target = target(request.path("to"));
    var from = observation.getSource();
    var verified =
        new Source(
            gate.connection().getRealm(),
            gate.connection().getNode(),
            from.getRegistration().getLocal(),
            "session",
            "agent",
            from.getOwnerUserId(),
            "协作来源",
            null,
            Instant.parse(observation.getObservedAt()),
            binding,
            Long.toUnsignedString(from.getRevision()),
            Long.toUnsignedString(observation.getPolicyRevision()),
            ((Timestamp) source.get("valid_until")).toInstant());
    var fields = ((ObjectNode) request).deepCopy();
    fields.remove(List.of("kind", "source", "idempotency", "thread_id"));
    UUID messageId = UuidV7.next();
    var store =
        new NetworkMailboxService.StoreRequest(
            tenant(),
            verified.realm(),
            verified.node(),
            verified.local(),
            "message",
            NetworkJson.canonical(request.path("body")),
            request.has("thread_id") ? uuid(request.path("thread_id").asString()) : null,
            List.of(
                new NetworkMailboxService.NodeTarget(
                    request.path("to").path("node").asString(),
                    List.of(request.path("to").path("local").asString()))),
            null,
            null,
            true);
    var message =
        NetworkSourceStamp.message(
            store,
            verified,
            messageId,
            clock.instant(),
            NetworkJson.canonical(fields),
            null,
            "session");
    UUID thread = uuid(message.path("thread_id").asString());
    lockThread(thread);
    // The pending row remains the exclusive sequence reservation after this short transaction.
    // Reject other keys before freezing them, so their original key can safely retry later.
    mailbox.requireThreadUnreserved(tenant(), thread);
    message.put("thread_sequence", nextSequence(thread));
    var context = NetworkSourceStamp.context(verified, message);
    mailbox.preflightSubmit(message, context);
    UUID id = UuidV7.next();
    jdbc.update(
        """
        INSERT INTO collab.network_submit(id,tenant_id,source_binding,producer_epoch,producer_key,request_json,
          message_id,delivery_id,recipient_id,message_json,source_context_json,source_fence_json,target_fence_json)
        VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
        """,
        id,
        tenant(),
        binding,
        idempotency.path("producer_epoch").asString(),
        idempotency.path("key").asString(),
        NetworkJson.canonical(request),
        messageId,
        UuidV7.next(),
        UuidV7.next(),
        NetworkJson.canonical(message),
        NetworkJson.canonical(context),
        fence(source, local),
        NetworkJson.canonical(target));
    return jdbc.queryForMap(
        "SELECT * FROM collab.network_submit WHERE tenant_id=? AND id=?", tenant(), id);
  }

  private String commit(
      SourceGate gate,
      SourceGate targetGate,
      JsonNode request,
      Map<String, Object> pending,
      Instant until) {
    lockNodes(
        gate.connection().getRealm(),
        gate.connection().getNode(),
        targetGate.connection().getNode());
    wire.lockSourceGate(gate, false);
    wire.lockSourceGate(targetGate, false);
    var current =
        jdbc.queryForMap(
            "SELECT * FROM collab.network_submit WHERE tenant_id=? AND id=? FOR UPDATE",
            tenant(),
            pending.get("id"));
    if (current.get("result_json") != null) return current.get("result_json").toString();
    check(until.isAfter(clock.instant()));
    var source = source(pending.get("source_binding").toString(), true);
    var observation = observation(source, gate);
    var local =
        registration(
            gate.connection().getRealm(),
            gate.connection().getNode(),
            observation.getSource().getRegistration().getLocal());
    checkRegistration(local, observation.getSource().getRegistration());
    check(pending.get("source_fence_json").equals(fence(source, local)));
    check(
        pending.get("target_fence_json").equals(NetworkJson.canonical(target(request.path("to")))));
    var message = (ObjectNode) NetworkJson.parse(pending.get("message_json").toString());
    check(Instant.parse(message.path("expires_at").asString()).isAfter(clock.instant()));
    UUID thread = uuid(message.path("thread_id").asString());
    lockThread(thread);
    check(nextSequence(thread).equals(message.path("thread_sequence").asString()));
    // Check again with the current signing keys before any durable body/mailbox writes.
    mailbox.preflightSubmit(
        message, (ObjectNode) NetworkJson.parse(pending.get("source_context_json").toString()));
    jdbc.update(
        """
        INSERT INTO collab.network_message(id,tenant_id,realm,kind,body,source_node,source_local,thread_id,message_json,source_context_json)
        VALUES (?,?,?,'message',?,?,?,?,?,?)
        """,
        pending.get("message_id"),
        tenant(),
        gate.connection().getRealm(),
        NetworkJson.canonical(request.path("body")),
        gate.connection().getNode(),
        observation.getSource().getRegistration().getLocal(),
        thread,
        pending.get("message_json"),
        pending.get("source_context_json"));
    long seq =
        jdbc.queryForObject(
            "UPDATE collab.network_node SET head_seq=head_seq+1,updated_at=now() WHERE tenant_id=? AND realm=? AND node_id=? RETURNING head_seq",
            Long.class,
            tenant(),
            targetGate.connection().getRealm(),
            targetGate.connection().getNode());
    jdbc.update(
        "INSERT INTO collab.network_mail_item(id,tenant_id,message_id,target_realm,target_node,mail_seq) VALUES (?,?,?,?,?,?)",
        pending.get("delivery_id"),
        tenant(),
        pending.get("message_id"),
        targetGate.connection().getRealm(),
        targetGate.connection().getNode(),
        seq);
    jdbc.update(
        "INSERT INTO collab.network_recipient(id,tenant_id,delivery_id,target_local) VALUES (?,?,?,?)",
        pending.get("recipient_id"),
        tenant(),
        pending.get("delivery_id"),
        request.path("to").path("local").asString());
    var result =
        NetworkJson.object()
            .put("status", "platform_stored")
            .put("message_id", pending.get("message_id").toString())
            .put("thread_id", thread.toString())
            .put("message_class", "unicast")
            .put("dispatch_status", "pending")
            .put("expires_at", message.path("expires_at").asString());
    result.set("from", message.path("from"));
    result.set("to", message.path("to"));
    NetworkSchemas.validate("send", "SendResult", result);
    String json = NetworkJson.canonical(result);
    check(
        jdbc.update(
                "UPDATE collab.network_submit SET result_json=? WHERE tenant_id=? AND id=? AND result_json IS NULL",
                json,
                tenant(),
                pending.get("id"))
            == 1);
    return json;
  }

  private Map<String, Object> source(String binding, boolean lock) {
    var rows =
        jdbc.queryForList(
            """
        SELECT b.*,l.realm,l.node_id,l.registration_id,l.stopped,l.head_binding,
          l.stop_version AS current_stop_version,l.permission_version AS current_permission_version
        FROM collab.network_source_binding b JOIN collab.network_source_lineage l ON l.tenant_id=b.tenant_id AND l.id=b.lineage_id
        WHERE b.tenant_id=? AND b.source_binding=?
        """
                + (lock ? " FOR UPDATE OF b,l" : ""),
            tenant(),
            binding);
    check(rows.size() == 1);
    return rows.getFirst();
  }

  private GetAgentSourceResponse observation(Map<String, Object> row, SourceGate gate) {
    try {
      check(
          !Boolean.TRUE.equals(row.get("stopped"))
              && row.get("source_binding").equals(row.get("head_binding"))
              && Objects.equals(row.get("stop_version"), row.get("current_stop_version"))
              && Objects.equals(
                  row.get("permission_version"), row.get("current_permission_version"))
              && row.get("valid_until") instanceof Timestamp time
              && time.toInstant().isAfter(clock.instant())
              && ((Number) row.get("epoch")).longValue() == gate.target().epoch()
              && Objects.equals(row.get("boot_id"), gate.before().get("local_boot_id"))
              && Arrays.equals(
                  (byte[]) row.get("observation_connection"), gate.connection().toByteArray()));
      var response = GetAgentSourceResponse.parseFrom((byte[]) row.get("observation"));
      check(
          response.hasSource()
              && response.hasLineageStopped()
              && !response.getLineageStopped()
              && response.getConnection().equals(gate.connection())
              && response.getSource().getSourceBinding().equals(row.get("source_binding"))
              && response.getSource().getState()
                  == NetworkAgentSourceState.NETWORK_AGENT_SOURCE_STATE_ACTIVE
              && response.getSource().getScope()
                  == NetworkAgentSourceScope.NETWORK_AGENT_SOURCE_SCOPE_DURABLE_UNICAST_SEND
              && response.getPolicyRevision() != 0);
      return response;
    } catch (com.google.protobuf.InvalidProtocolBufferException | NullPointerException e) {
      throw new AurigaException(NetworkErrors.UNAUTHORIZED);
    }
  }

  private ObjectNode target(JsonNode address) {
    var node = node(address.path("realm").asString(), address.path("node").asString());
    var registration =
        registration(
            address.path("realm").asString(),
            address.path("node").asString(),
            address.path("local").asString());
    check(Boolean.TRUE.equals(registration.get("receive_enabled")));
    var result = NetworkJson.object();
    result.set("node", snapshot(node));
    result.set("registration", snapshot(registration));
    return result;
  }

  private Map<String, Object> registration(String realm, String node, String local) {
    var rows =
        jdbc.queryForList(
            "SELECT * FROM collab.network_local_registration WHERE tenant_id=? AND realm=? AND node_id=? AND local_id=? FOR UPDATE",
            tenant(),
            realm,
            node,
            new BigDecimal(local));
    check(rows.size() == 1);
    var r = rows.getFirst();
    check("registered".equals(r.get("status")) && "session".equals(r.get("address_type")));
    return r;
  }

  private static void checkRegistration(Map<String, Object> row, NetworkAgentSourceRegistration r) {
    check(
        row.get("id").toString().equals(r.getRegistrationId())
            && row.get("allocation_op_id").equals(r.getAllocationOpId())
            && row.get("allocation_fingerprint").equals(r.getAllocationFingerprint())
            && row.get("registration_revision")
                .toString()
                .equals(Long.toUnsignedString(r.getRegistrationRevision()))
            && row.get("realm").equals(r.getRealm())
            && row.get("node_id").equals(r.getNode())
            && row.get("local_id").toString().equals(r.getLocal())
            && "session".equals(r.getAddressType()));
  }

  private Map<String, Object> node(String realm, String node) {
    var row =
        jdbc.queryForMap(
            "SELECT realm,node_id,wire_binding_json,connection_epoch,wire_status,local_identity_status,local_boot_id,producer_epoch FROM collab.network_node WHERE tenant_id=? AND realm=? AND node_id=?",
            tenant(),
            realm,
            node);
    check(
        "ready".equals(row.get("wire_status"))
            && "healthy".equals(row.get("local_identity_status"))
            && row.get("wire_binding_json") != null);
    return row;
  }

  private void lockNodes(String realm, String source, String target) {
    var rows =
        jdbc.queryForList(
            "SELECT node_id FROM collab.network_node WHERE tenant_id=? AND realm=? AND node_id IN (?,?) ORDER BY node_id FOR UPDATE",
            tenant(),
            realm,
            source,
            target);
    check(rows.size() == (source.equals(target) ? 1 : 2));
  }

  private static NetworkAuthorityConnection targetConnection(JsonNode node) {
    var c = NetworkJson.parse(node.path("wire_binding_json").asString());
    return NetworkAuthorityConnection.newBuilder()
        .setTenantId(c.path("tenant").asString())
        .setRealm(c.path("realm").asString())
        .setNode(c.path("node").asString())
        .setIngress(c.path("ingress").asString())
        .setServerId(c.path("server").asString())
        .setClientId(c.path("client").asString())
        .setClientAccount(c.path("account").asString())
        .build();
  }

  private static String fence(Map<String, Object> source, Map<String, Object> registration) {
    var result = NetworkJson.object();
    result.set("source", snapshot(source));
    result.set("registration", snapshot(registration));
    return NetworkJson.canonical(result);
  }

  static ObjectNode snapshot(Map<String, Object> row) {
    var result = NetworkJson.object();
    row.forEach(
        (key, value) -> {
          if (value == null) result.putNull(key);
          else
            result.put(
                key,
                value instanceof byte[] bytes
                    ? Base64.getEncoder().encodeToString(bytes)
                    : value instanceof Timestamp time
                        ? time.toInstant().toString()
                        : value.toString());
        });
    return result;
  }

  private void lockThread(UUID thread) {
    jdbc.queryForList(
        "SELECT pg_advisory_xact_lock(hashtextextended(?,0))", tenant() + "/" + thread);
  }

  private String nextSequence(UUID thread) {
    return jdbc.queryForObject(
        "SELECT coalesce(max((message_json::jsonb->>'thread_sequence')::numeric),0)+1 FROM collab.network_message WHERE tenant_id=? AND thread_id=?",
        String.class,
        tenant(),
        thread);
  }

  private static UUID tenant() {
    return TenantContext.require();
  }

  private static UUID uuid(String value) {
    try {
      return UUID.fromString(value);
    } catch (IllegalArgumentException e) {
      throw new AurigaException(NetworkErrors.PROTOCOL_ERROR);
    }
  }

  private static void check(boolean valid) {
    if (!valid) throw new AurigaException(NetworkErrors.CONFLICT);
  }
}
