package auriga.collab.app;

import auriga.common.error.AurigaException;
import java.util.*;
import tools.jackson.databind.JsonNode;

/** Protocol security features and adapter implementation capabilities are both required. */
final class NetworkWireCapabilities {
  static final List<String> BASE =
      List.of("server_auth_v1", "source_attestation_v1", "inbox_only_v1");

  static List<String> verify(JsonNode ready, boolean broadcast) {
    var required = new ArrayList<>(BASE);
    if (broadcast) required.add("broadcast_grants_v1");
    for (String side : List.of("features", "adapter_capabilities")) {
      var supplied = new HashSet<String>();
      for (var value : ready.path(side)) supplied.add(value.asString());
      if (!supplied.containsAll(required)) throw new AurigaException(NetworkErrors.PROTOCOL_ERROR);
    }
    return List.copyOf(required);
  }

  private NetworkWireCapabilities() {}
}
