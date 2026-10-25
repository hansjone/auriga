package auriga.collab.app;

import auriga.common.error.AurigaException;
import java.util.List;
import tools.jackson.databind.JsonNode;

/** Stored destination and acceptance phase, independently of transport epoch/request/operation. */
final class NetworkReceiptProofs {
  static JsonNode target(
      JsonNode message,
      JsonNode dispatch,
      String realm,
      String node,
      String local,
      String registeredType) {
    NetworkSchemas.validate("Message", message);
    require(realm.equals(message.path("to").path("realm").asString()));
    JsonNode target;
    String messageClass = message.path("message_class").asString();
    if ("unicast".equals(messageClass)) target = message.path("to");
    else {
      require(
          "group".equals(message.path("to").path("type").asString())
              && "0".equals(message.path("to").path("node").asString()));
      String type = "system";
      if ("broadcast".equals(messageClass))
        require("0".equals(local) && "1".equals(message.path("to").path("local").asString()));
      else {
        require(dispatch != null);
        type = NetworkSourceStamp.memberContext(dispatch, node, local).path("type").asString();
      }
      target =
          NetworkJson.object()
              .put("version", 1)
              .put("realm", realm)
              .put("node", node)
              .put("local", local)
              .put("type", type);
    }
    require(
        realm.equals(target.path("realm").asString())
            && node.equals(target.path("node").asString())
            && local.equals(target.path("local").asString())
            && registeredType.equals(target.path("type").asString()));
    return target;
  }

  static void stage(JsonNode entry, JsonNode message, JsonNode target, JsonNode contact) {
    require(target.equals(entry.path("target")));
    require(
        !entry.hasNonNull(
            "assigned_address")); // CONTACT_ASSIGN/session continuation is not implemented here.
    if (!"host_accepted".equals(entry.path("status").asString())) return;
    boolean system = "0".equals(target.path("local").asString());
    require((system ? "system" : "session").equals(entry.path("accepted_by").asString()));
    if (system || !"unicast".equals(message.path("message_class").asString()))
      require(
          "inbox_only".equals(entry.path("acceptance_mode").asString())
              && "not_applicable".equals(entry.path("execution_status").asString()));
    if ("first_contact".equals(message.path("kind").asString())) {
      require(
          system
              && contact != null
              && "allowed".equals(contact.path("status").asString())
              && message.path("contact_scope").asString().equals(contact.path("scope").asString()));
    }
  }

  static String stable(JsonNode entry) {
    var result = NetworkJson.object();
    for (String key :
        List.of(
            "recipient_id",
            "target",
            "message_digest",
            "host_request_id",
            "accepted_by",
            "acceptance_mode",
            "evidence_digest")) result.set(key, entry.path(key));
    if (entry.hasNonNull("assigned_address"))
      result.set("assigned_address", entry.path("assigned_address"));
    else result.putNull("assigned_address");
    return NetworkJson.canonical(result);
  }

  private static void require(boolean value) {
    if (!value) throw new AurigaException(NetworkErrors.UNAUTHORIZED);
  }

  private NetworkReceiptProofs() {}
}
