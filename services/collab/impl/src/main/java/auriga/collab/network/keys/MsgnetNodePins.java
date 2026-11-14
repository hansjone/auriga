package auriga.collab.network.keys;

import static auriga.collab.network.keys.MsgnetV2Verifier.*;

import java.util.HashMap;
import java.util.HashSet;
import java.util.Map;
import java.util.UUID;
import tools.jackson.databind.JsonNode;

/** Public confirmation pins from the independent protected installer, not projection identities. */
final class MsgnetNodePins {
  private MsgnetNodePins() {}

  static Map<String, JsonNode> read(JsonNode source, UUID tenant, String realm) {
    schema("NodeControlPins", source, "invalid_chain");
    require(
        tenant.toString().equals(text(source, "tenant_id")) && realm.equals(text(source, "realm")),
        "scope_denied");
    var result = new HashMap<String, JsonNode>();
    var identities = new HashSet<String>();
    var nodes = new HashSet<String>();
    for (var pin : source.path("pins")) {
      integer(text(pin, "identity_revision"));
      MsgnetControlLedger.canonicalSpki(text(pin, "signing_spki"), "Ed25519");
      require(
          result.putIfAbsent(text(pin, "target_id"), pin.deepCopy()) == null
              && identities.add(text(pin, "identity_id"))
              && nodes.add(text(pin, "node_id")),
          "scope_denied");
    }
    return Map.copyOf(result);
  }
}
