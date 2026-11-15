package auriga.iam;

import auriga.contracts.msgnet.model.RootControl;
import auriga.contracts.msgnet.model.TransportReaderEvidence;
import com.fasterxml.jackson.annotation.JsonInclude;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.json.JsonMapper;

/** Recover absent optional fields when writing validated generated DTOs. */
final class MessageNetworkWire {
  @JsonInclude(JsonInclude.Include.NON_NULL)
  private abstract static class AbsentOptionalFields {}

  private static final JsonMapper JSON =
      JsonMapper.builder()
          .addMixIn(RootControl.class, AbsentOptionalFields.class)
          .addMixIn(TransportReaderEvidence.class, AbsentOptionalFields.class)
          .build();

  static JsonNode tree(Object value) {
    return JSON.valueToTree(value);
  }

  private MessageNetworkWire() {}
}
