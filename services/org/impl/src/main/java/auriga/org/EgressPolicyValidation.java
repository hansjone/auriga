package auriga.org;

import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import java.util.HashSet;
import java.util.Set;
import tools.jackson.databind.JsonNode;

/** Configuration validation only; the exit must still validate every resolved IP at dial time. */
final class EgressPolicyValidation {
  private EgressPolicyValidation() {}

  static void validate(JsonNode value) {
    require(value != null && value.isObject());
    value
        .propertyNames()
        .forEach(
            name ->
                require(
                    Set.of("mode", "categories", "allowDomains", "denyDomains").contains(name)));
    if (value.has("mode"))
      require(
          value.path("mode").isString()
              && Set.of("open", "allowlist").contains(value.path("mode").asString()));
    if (value.path("mode").asString("").equals("open") && value.has("allowDomains"))
      require(value.path("allowDomains").isArray() && value.path("allowDomains").isEmpty());
    for (String field : Set.of("categories", "allowDomains", "denyDomains")) {
      JsonNode items = value.get(field);
      if (items == null) continue;
      require(items.isArray() && items.size() <= 256);
      Set<String> seen = new HashSet<>();
      for (JsonNode item : items) {
        require(item.isString());
        String host = item.asString();
        require(seen.add(host));
        // No category registry is published by the current exit. Do not accept ineffective grants.
        require(!field.equals("categories"));
        String name = host.startsWith("*.") ? host.substring(2) : host;
        require(name.length() <= 253 && name.matches("[a-z0-9-]+(?:\\.[a-z0-9-]+)+"));
        String[] labels = name.split("\\.");
        require(labels[labels.length - 1].matches("[a-z]{2,63}"));
        require(!host.startsWith("*.") || labels.length >= 2);
        for (String label : labels)
          require(label.length() <= 63 && !label.startsWith("-") && !label.endsWith("-"));
        require(
            !name.equals("localhost")
                && !name.endsWith(".localhost")
                && !name.endsWith(".local")
                && !name.endsWith(".internal")
                && !name.endsWith(".home.arpa"));
      }
    }
  }

  private static void require(boolean ok) {
    if (!ok) throw new AurigaException(CommonErrors.VALIDATION_FAILED);
  }
}
