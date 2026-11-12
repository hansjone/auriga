package auriga.org;

import java.util.LinkedHashSet;
import java.util.List;
import java.util.Locale;
import java.util.Set;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** Read-only upgrade projection. Invalid legacy items never block the remaining valid rules. */
final class EgressPolicyCompatibility {
  private EgressPolicyCompatibility() {}

  static ObjectNode project(ObjectNode policy) {
    JsonNode raw = policy.path("value");
    boolean seed =
        policy.path("version").asLong() == 1
            && !policy.has("updatedBy")
            && raw.isObject()
            && !raw.has("mode")
            && raw.propertyNames().stream()
                .allMatch(Set.of("categories", "allowDomains", "denyDomains")::contains)
            && List.of("categories", "allowDomains", "denyDomains").stream()
                .allMatch(
                    key -> !raw.has(key) || (raw.path(key).isArray() && raw.path(key).isEmpty()));
    var value = OrgJson.object("categories", List.of());
    for (String key : List.of("allowDomains", "denyDomains")) {
      Set<String> valid = new LinkedHashSet<>();
      JsonNode items = raw.path(key);
      if (items.isArray())
        for (JsonNode item : items) {
          if (!item.isString() || valid.size() == 256) continue;
          String domain = item.asString().trim().toLowerCase(Locale.ROOT);
          try {
            EgressPolicyValidation.validate(OrgJson.object(key, List.of(domain)));
            valid.add(domain);
          } catch (auriga.common.error.AurigaException ignored) {
            // Legacy categories, URLs, IPs and malformed domains are not executable grants.
          }
        }
      value.set(key, OrgJson.MAPPER.valueToTree(valid));
    }
    String mode = raw.path("mode").asString("");
    if (!Set.of("open", "allowlist").contains(mode))
      mode = value.path("allowDomains").isEmpty() ? "open" : "allowlist";
    value.put("mode", mode);
    if (mode.equals("open")) value.set("allowDomains", OrgJson.MAPPER.createArrayNode());
    policy.put("configured", !seed);
    policy.put("legacyAdjusted", !seed && !value.equals(raw));
    policy.set("value", value);
    return policy;
  }

  static ObjectNode canonical(JsonNode value) {
    EgressPolicyValidation.validate(value);
    return (ObjectNode) project(OrgJson.object("version", 2, "value", value)).path("value");
  }
}
