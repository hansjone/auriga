package auriga.iam;

import java.nio.charset.StandardCharsets;
import java.util.Collection;
import java.util.Set;
import tools.jackson.core.StreamReadConstraints;
import tools.jackson.core.StreamReadFeature;
import tools.jackson.core.json.JsonFactory;
import tools.jackson.databind.DeserializationFeature;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.json.JsonMapper;
import tools.jackson.databind.node.ObjectNode;

/** Allowlist serializer. Neither unknown keys nor unknown scalar values reach the sink. */
final class UacDiagnosticSanitizer {
  static final String VERSION = "1";
  private static final String REDACTED = "[REDACTED]";
  private static final int MAX_EVENT = 64 * 1024;
  private static final int MAX_NODES = 2048;
  private static final Set<String> CONTAINERS =
      Set.of("code", "bo", "data", "list", "rows", "result");
  private static final Set<String> MESSAGES =
      Set.of("success", "waiting", "rejected", "unavailable");
  private static final JsonMapper MAPPER =
      JsonMapper.builder(
              JsonFactory.builder()
                  .streamReadConstraints(
                      StreamReadConstraints.builder().maxNestingDepth(32).build())
                  .build())
          .enable(StreamReadFeature.STRICT_DUPLICATE_DETECTION)
          .enable(DeserializationFeature.FAIL_ON_TRAILING_TOKENS)
          .build();

  static String tokenHashPrefix(String token) {
    return token == null || token.isEmpty() ? null : Secrets.hash(token).substring(0, 12);
  }

  static ObjectNode sanitize(byte[] body, Collection<String> sensitiveValues) {
    int size = body == null ? 0 : body.length;
    if (size == 0 || size > UacTokenVerification.MAX_BODY) return omitted(size, "size_limit");
    try {
      JsonNode root = MAPPER.readTree(body);
      if (root == null || !root.isObject() && !root.isArray()) return omitted(size, "invalid_json");
      Budget budget = new Budget(sensitiveValues);
      JsonNode sanitized = walk(root, "", budget);
      ObjectNode result =
          Json.object(
              "udsRawSanitized",
              sanitized,
              "unknownFields",
              budget.unknown,
              "truncated",
              budget.truncated,
              "sanitizerVersion",
              VERSION);
      if (result.toString().getBytes(StandardCharsets.UTF_8).length > MAX_EVENT)
        return omitted(size, "event_limit");
      return result;
    } catch (RuntimeException e) {
      // Parser exception messages and locations may contain the original input.
      return omitted(size, "invalid_json");
    }
  }

  private static JsonNode walk(JsonNode node, String field, Budget budget) {
    if (++budget.nodes > MAX_NODES) {
      budget.truncated = true;
      return MAPPER.valueToTree(REDACTED);
    }
    if (node.isObject()) {
      ObjectNode result = Json.object();
      for (var entry : node.properties()) {
        if (budget.nodes >= MAX_NODES) {
          budget.truncated = true;
          break;
        }
        String key = entry.getKey();
        // A known container is traversed only as a container, never as an arbitrary string.
        if (CONTAINERS.contains(key) && !budget.containsSensitive(key)) {
          result.set(key, walk(entry.getValue(), key, budget));
        } else if ((key.equals("message") || key.equals("msg")) && !budget.containsSensitive(key)) {
          String value = entry.getValue().isString() ? entry.getValue().asString() : "";
          boolean safe = MESSAGES.contains(value) && !budget.containsSensitive(value);
          result.put(key, safe ? value : REDACTED);
          result.put("messageRedacted", !safe);
        } else {
          // Key names themselves can contain credentials, employee names, or other PII.
          result.put("_unknown_" + ++budget.unknown, REDACTED);
        }
      }
      return result;
    }
    if (node.isArray()) {
      var result = MAPPER.createArrayNode();
      for (JsonNode item : node) {
        if (budget.nodes >= MAX_NODES) {
          budget.truncated = true;
          break;
        }
        result.add(walk(item, field, budget));
      }
      return result;
    }
    String value = node.isString() ? node.asString() : "";
    if (field.equals("code")
        && value.matches("[A-Za-z0-9_-]{1,64}")
        && !budget.containsSensitive(value)) return MAPPER.valueToTree(value);
    return MAPPER.valueToTree(REDACTED);
  }

  private static ObjectNode omitted(int size, String reason) {
    return Json.object(
        "bytes", size, "reason", reason, "truncated", true, "sanitizerVersion", VERSION);
  }

  private static final class Budget {
    final Collection<String> sensitive;
    int nodes;
    int unknown;
    boolean truncated;

    Budget(Collection<String> values) {
      sensitive = values == null ? java.util.List.of() : values;
    }

    boolean containsSensitive(String value) {
      return sensitive.stream()
          .anyMatch(secret -> secret != null && !secret.isEmpty() && value.contains(secret));
    }
  }

  private UacDiagnosticSanitizer() {}
}
