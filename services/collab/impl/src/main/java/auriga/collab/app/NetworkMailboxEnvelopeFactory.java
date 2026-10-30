package auriga.collab.app;

import auriga.collab.app.NetworkEnvelopeSigner.Context;
import auriga.collab.app.NetworkMailboxService.FetchItem;
import auriga.collab.app.NetworkMailboxService.NodeRoute;
import auriga.collab.app.NetworkMailboxService.RecipientView;
import auriga.common.error.AurigaException;
import java.time.Instant;
import java.util.List;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** Structured mailbox projections only; no arbitrary client JSON signing endpoint. */
final class NetworkMailboxEnvelopeFactory {
  private final NetworkEnvelopeSigner signer;

  NetworkMailboxEnvelopeFactory(NetworkEnvelopeSigner signer) {
    this.signer = signer;
  }

  ObjectNode gap(long sequence, String reason, Context context, Instant deadline) {
    var gap =
        NetworkJson.object()
            .put("app_version", "1.0")
            .put("type", "gap")
            .put("epoch", Long.toString(context.epoch()))
            .put("from_seq", Long.toString(sequence))
            .put("to_seq", Long.toString(sequence))
            .put("reason", reason);
    return finish(gap, "GapItem", "protocol_response", context, deadline);
  }

  ObjectNode terminal(
      FetchItem item, List<RecipientView> recipients, Context context, Instant deadline) {
    var envelope = base(item, context).put("type", "terminal");
    var entries = envelope.putArray("recipients");
    for (var r : recipients)
      entries
          .addObject()
          .put("recipient_id", r.recipientId().toString())
          .put("transport_status", r.transportStatus())
          .put("state_version", Long.toString(r.stateVersion()));
    return finish(envelope, "TerminalItem", "protocol_response", context, deadline);
  }

  ObjectNode delivery(
      FetchItem item,
      ObjectNode message,
      ObjectNode source,
      JsonNode dispatch,
      ObjectNode contact,
      Context context,
      Instant deadline) {
    if (!NetworkJson.digest(message).equals(source.path("message_digest").asString())
        || !source.path("realm").equals(message.path("from").path("realm"))
        || !source.path("node").equals(message.path("from").path("node"))
        || !source.path("sender_kind").equals(message.path("sender_kind"))
        || !message.path("message_id").asString().equals(item.messageId().toString())
        || !message.path("to").path("realm").asString().equals(context.realm())) invalid();
    if (dispatch == null
        && (!message.path("to").path("node").asString().equals(context.node())
            || item.recipients().size() != 1)) invalid();
    if (contact != null && "pending".equals(contact.path("status").asString())) {
      var prompt =
          NetworkJson.object()
              .put("app_version", "1.0")
              .put("type", "contact_prompt")
              .put("epoch", Long.toString(context.epoch()))
              .put("delivery_id", item.deliveryId().toString())
              .put("mail_seq", Long.toString(item.mailSeq()))
              .put("contact_id", item.messageId().toString())
              .put("contact_scope", message.path("contact_scope").asString())
              .put("request_version", contact.path("decision_version").asString())
              .put("state_version", contact.path("decision_version").asString())
              .put("expires_at", contact.path("expires_at").asString());
      prompt.set("from", message.path("from"));
      prompt.set("source_context", source);
      prompt.set(
          "recipient", recipient(item.recipients().getFirst(), message, dispatch, context.node()));
      String text = message.path("body").path("text").asString("");
      prompt.put(
          "preview",
          text.substring(
              0, text.offsetByCodePoints(0, Math.min(280, text.codePointCount(0, text.length())))));
      return finish(prompt, "ContactPromptItem", "business_delivery", context, deadline);
    }
    var envelope = base(item, context).put("type", "delivery").put("message_state_version", "1");
    envelope.set("message", message);
    envelope.set("source_context", source);
    var recipients = envelope.putArray("recipients");
    for (var r : item.recipients()) recipients.add(recipient(r, message, dispatch, context.node()));
    if (dispatch != null) envelope.put("snapshot_id", dispatch.path("snapshot_id").asString());
    if (contact != null) {
      if (!"allowed".equals(contact.path("status").asString())) invalid();
      envelope
          .putObject("contact")
          .put("contact_id", item.messageId().toString())
          .put("state", "contact_allowed")
          .put("request_version", contact.path("decision_version").asString())
          .putNull("assigned_address");
    }
    return finish(envelope, "Delivery", "business_delivery", context, deadline);
  }

  ObjectNode page(
      List<ObjectNode> items, NodeRoute route, long scanAfter, Context context, Instant deadline) {
    return page(items, route, scanAfter, context, deadline, route.headSeq(), null);
  }

  ObjectNode page(
      List<ObjectNode> items,
      NodeRoute route,
      long scanAfter,
      Context context,
      Instant deadline,
      long snapshotHead,
      String continuation) {
    var header =
        NetworkJson.object()
            .put("app_version", "1.0")
            .put("type", "fetch_page")
            .put("request_id", context.requestId())
            .put("epoch", Long.toString(context.epoch()))
            .put("head_seq", Long.toString(route.headSeq()))
            .put("committed_cursor", Long.toString(route.committedCursor()))
            .put("scan_after", Long.toString(scanAfter))
            .put("snapshot_head", Long.toString(snapshotHead));
    if (continuation == null) header.putNull("next_continuation");
    else header.put("next_continuation", continuation);
    var manifest = header.putArray("manifest");
    for (var item : items) {
      var entry =
          manifest
              .addObject()
              .put("item_type", item.path("type").asString())
              .put("digest", NetworkJson.digest(item));
      if (item.has("mail_seq")) entry.put("mail_seq", item.path("mail_seq").asString());
      else entry.putNull("mail_seq");
    }
    var frame = NetworkJson.object().put("app_version", "1.0").put("type", "fetch_result");
    frame.set("page", signer.sign(header, "protocol_response", context, deadline));
    var entries = frame.putArray("items");
    for (var item : items) entries.add(item);
    NetworkSchemas.validate("protocol", "FetchResult", frame);
    return frame;
  }

  private ObjectNode recipient(
      RecipientView recipient, ObjectNode message, JsonNode dispatch, String node) {
    var entry =
        NetworkJson.object()
            .put("recipient_id", recipient.recipientId().toString())
            .put("state_version", Long.toString(recipient.stateVersion()))
            .put("local", recipient.targetLocal());
    if (dispatch == null) {
      if (!recipient.targetLocal().equals(message.path("to").path("local").asString())) invalid();
      entry.put("type", message.path("to").path("type").asString());
    } else {
      var member = NetworkSourceStamp.memberContext(dispatch, node, recipient.targetLocal());
      entry
          .put("type", member.path("type").asString())
          .put("membership_id", member.path("membership_id").asString());
    }
    return entry;
  }

  private ObjectNode base(FetchItem item, Context context) {
    var envelope =
        NetworkJson.object()
            .put("app_version", "1.0")
            .put("epoch", Long.toString(context.epoch()))
            .put("delivery_id", item.deliveryId().toString())
            .put("mail_seq", Long.toString(item.mailSeq()));
    envelope.putObject("target_node").put("realm", context.realm()).put("node", context.node());
    return envelope;
  }

  private ObjectNode finish(
      ObjectNode envelope, String definition, String purpose, Context context, Instant deadline) {
    var signed = signer.sign(envelope, purpose, context, deadline);
    NetworkSchemas.validate(definition, signed);
    return signed;
  }

  private static void invalid() {
    throw new AurigaException(NetworkErrors.PROTOCOL_ERROR);
  }
}
