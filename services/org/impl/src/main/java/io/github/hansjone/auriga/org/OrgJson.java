package io.github.hansjone.auriga.org;

import tools.jackson.databind.JsonNode;
import tools.jackson.databind.json.JsonMapper;
import tools.jackson.databind.node.ObjectNode;

final class OrgJson {
  static final JsonMapper MAPPER = JsonMapper.builder().build();

  static ObjectNode object(Object... pairs) {
    ObjectNode n = MAPPER.createObjectNode();
    for (int i = 0; i < pairs.length; i += 2)
      n.set(pairs[i].toString(), MAPPER.valueToTree(pairs[i + 1]));
    return n;
  }

  static ObjectNode read(String json) {
    return (ObjectNode) MAPPER.readTree(json);
  }

  static String string(JsonNode n, String key) {
    return n.path(key).asString("");
  }

  private OrgJson() {}
}
