package auriga.collab.app;

import auriga.collab.api.NetworkSourceAuthority.Dispatch;
import auriga.collab.api.NetworkSourceAuthority.Source;
import auriga.common.error.AurigaException;
import auriga.common.persistence.UuidV7;
import java.time.Instant;
import java.time.temporal.ChronoUnit;
import java.util.Set;
import java.util.UUID;
import tools.jackson.databind.node.ObjectNode;

/** Server-owned identity and immutable message construction for the N1 mailbox. */
final class NetworkSourceStamp {
  private NetworkSourceStamp() {}

  static ObjectNode message(
      NetworkMailboxService.StoreRequest request,
      Source source,
      UUID messageId,
      Instant now,
      String fieldsJson,
      Dispatch dispatch,
      String targetType) {
    if (source == null
        || source.validUntil() == null
        || !source.validUntil().isAfter(now)
        || !request.realm().equals(source.realm())
        || !request.sourceNode().equals(source.node())
        || !request.sourceLocal().equals(source.local())
        || "0".equals(source.node())) mismatch();
    boolean human = "human".equals(source.senderKind());
    if (human) {
      if (!"system".equals(source.addressType()) || !"0".equals(source.local())) mismatch();
    } else if (!"agent".equals(source.senderKind())
        || !("session".equals(source.addressType()) || "subagent".equals(source.addressType()))
        || "0".equals(source.local())) mismatch();
    var parsed = fieldsJson == null ? NetworkJson.object() : NetworkJson.parse(fieldsJson);
    if (!(parsed instanceof ObjectNode)) throw new AurigaException(NetworkErrors.PROTOCOL_ERROR);
    ObjectNode fields = (ObjectNode) parsed;
    for (String field : Set.of("origin", "correlation_id", "causation_id")) {
      if (fields.has(field)) throw new AurigaException(NetworkErrors.UNAUTHORIZED);
    }
    if (fields.has("dispatch_mode") && !"atomic".equals(fields.path("dispatch_mode").asString()))
      throw new AurigaException(NetworkErrors.PROTOCOL_ERROR);
    if (fields.has("message_class")) throw new AurigaException(NetworkErrors.CLASS_MISMATCH);
    for (String name :
        Set.of(
            "from",
            "realm",
            "node",
            "sender_kind",
            "reply_to",
            "source_context",
            "server_auth",
            "authorization_ref")) {
      if (fields.has(name)) mismatch();
    }
    if ((request.targets().size() != 1 || request.targets().getFirst().locals().size() != 1)
        && dispatch == null) {
      throw new AurigaException(NetworkErrors.PROTOCOL_ERROR);
    }
    var target = request.targets().getFirst();
    String targetLocal = target.locals().getFirst();
    ObjectNode from = address(source.realm(), source.node(), source.local(), source.addressType());
    ObjectNode to =
        dispatch == null
            ? address(source.realm(), target.nodeId(), targetLocal, targetType)
            : (ObjectNode) NetworkJson.parse(dispatch.addressJson());
    if (dispatch != null
        && (!"group".equals(to.path("type").asString())
            || !"0".equals(to.path("node").asString())
            || !source.realm().equals(to.path("realm").asString()))) {
      throw new AurigaException(NetworkErrors.PROTOCOL_ERROR);
    }
    if (fields.has("to") && !to.equals(fields.get("to")))
      throw new AurigaException(NetworkErrors.PROTOCOL_ERROR);
    ObjectNode message = fields.deepCopy();
    message.put("message_id", messageId.toString());
    message.set("from", from);
    message.set("reply_to", from.deepCopy());
    message.set("to", to);
    String messageClass =
        dispatch == null
            ? "unicast"
            : "1".equals(to.path("local").asString()) ? "broadcast" : "multicast";
    message
        .put("kind", request.kind())
        .put("sender_kind", source.senderKind())
        .put("message_class", messageClass)
        .put("durability", "durable")
        .put(
            "thread_id",
            request.threadId() == null ? UuidV7.next().toString() : request.threadId().toString())
        .put("thread_sequence", "1");
    if (!message.has("in_reply_to")) message.putNull("in_reply_to");
    if (!message.has("intent")) message.put("intent", "request");
    if (!message.has("flags")) message.putArray("flags");
    if (!message.has("ext")) message.putObject("ext").putNull("e2e_signature");
    if (!message.has("body"))
      message.putObject("body").put("subtype", "text").put("text", request.body());
    if (NetworkJson.canonical(message.get("body"))
            .getBytes(java.nio.charset.StandardCharsets.UTF_8)
            .length
        > 16384) throw new AurigaException(NetworkErrors.PROTOCOL_ERROR);
    if (!message.has("ttl")) message.put("ttl", 86400);
    long ttl = message.path("ttl").asLong();
    if (ttl < 1 || ttl > 86400) throw new AurigaException(NetworkErrors.PROTOCOL_ERROR);
    Instant created = now.truncatedTo(ChronoUnit.SECONDS);
    message
        .put("created_at", created.toString())
        .put("expires_at", created.plusSeconds(ttl).toString());
    if ("first_contact".equals(request.kind()))
      message.put("contact_scope", request.contactScope());
    var auth = message.putObject("authorization_ref");
    auth.put("policy_revision", source.policyRevision()).putNull("broadcast_grant_id");
    if ("broadcast".equals(messageClass)) {
      if (dispatch.broadcastGrantId() == null)
        throw new AurigaException(NetworkErrors.UNAUTHORIZED);
      auth.put("broadcast_grant_id", dispatch.broadcastGrantId())
          .put("grant_revision", dispatch.grantRevision())
          .put("subject_type", dispatch.subjectType());
    }
    if (human) auth.put("human_intent_id", source.sourceBinding());
    else
      auth.put("source_binding", source.sourceBinding())
          .put("source_revision", source.sourceRevision());
    NetworkSchemas.validate("Message", message);
    if (message.has("response_deadline")) {
      Instant response = Instant.parse(message.path("response_deadline").asString());
      if (!"request".equals(message.path("intent").asString())
          || !response.isAfter(created)
          || response.isAfter(created.plusSeconds(86400))
          || response.isAfter(created.plusSeconds(ttl)))
        throw new AurigaException(NetworkErrors.PROTOCOL_ERROR);
    }
    return message;
  }

  static ObjectNode context(Source source, ObjectNode message) {
    var out = NetworkJson.object();
    out.put("realm", source.realm())
        .put("node", source.node())
        .put("user", source.user())
        .put("sender_kind", source.senderKind())
        .put("message_digest", NetworkJson.digest(message))
        .put("display_label", safeLabel(source.displayLabel()));
    if (source.identityRevision() != null) out.put("identity_revision", source.identityRevision());
    if (source.observedAt() != null)
      out.put("observed_at", source.observedAt().truncatedTo(ChronoUnit.SECONDS).toString());
    NetworkSchemas.validate("SourceContext", out);
    return out;
  }

  static ObjectNode dispatchContext(
      Dispatch dispatch, java.util.List<NetworkMailboxService.NodeTarget> targets) {
    var metadata = NetworkJson.object();
    metadata.put("snapshot_id", dispatch.snapshotId());
    var members = metadata.putArray("members");
    for (var target : targets)
      for (String local : target.locals()) {
        String key = target.nodeId() + "." + local;
        if (dispatch.membershipIds() == null
            || !dispatch.membershipIds().containsKey(key)
            || dispatch.recipientTypes() == null
            || !dispatch.recipientTypes().containsKey(key))
          throw new AurigaException(NetworkErrors.UNAUTHORIZED);
        members
            .addObject()
            .put("node", target.nodeId())
            .put("local", local)
            .put("membership_id", dispatch.membershipIds().get(key))
            .put("type", dispatch.recipientTypes().get(key));
      }
    NetworkJson.canonical(metadata);
    return metadata;
  }

  static tools.jackson.databind.JsonNode memberContext(
      tools.jackson.databind.JsonNode metadata, String node, String local) {
    for (var member : metadata.path("members")) {
      if (node.equals(member.path("node").asString())
          && local.equals(member.path("local").asString())) return member;
    }
    throw new AurigaException(NetworkErrors.PROTOCOL_ERROR);
  }

  static ObjectNode address(String realm, String node, String local, String type) {
    return NetworkJson.object()
        .put("version", 1)
        .put("realm", realm)
        .put("node", node)
        .put("local", local)
        .put("type", type);
  }

  private static String safeLabel(String label) {
    if (label == null || label.isBlank() || label.length() > 64) return "协作来源";
    String normalized =
        java.text.Normalizer.normalize(label, java.text.Normalizer.Form.NFKC)
            .replaceAll("[\\p{Cf}\\p{Cc}\\p{Z}]", "")
            .toLowerCase(java.util.Locale.ROOT);
    for (String reserved : Set.of("总部", "管理员", "系统", "headquarters", "administrator", "system")) {
      if (normalized.contains(reserved)) return "协作来源";
    }
    if (label
        .codePoints()
        .anyMatch(c -> Character.getType(c) == Character.FORMAT || Character.isISOControl(c)))
      return "协作来源";
    return label;
  }

  private static void mismatch() {
    throw new AurigaException(NetworkErrors.SENDER_MISMATCH);
  }
}
