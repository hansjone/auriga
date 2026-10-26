package auriga.iam;

import auriga.common.error.AurigaException;
import com.networknt.schema.*;
import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.time.Instant;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.HexFormat;
import java.util.Map;
import java.util.Set;
import tools.jackson.core.JsonToken;
import tools.jackson.core.StreamReadConstraints;
import tools.jackson.core.StreamReadFeature;
import tools.jackson.core.json.JsonFactory;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.json.JsonMapper;
import tools.jackson.databind.node.ObjectNode;

/** RFC 8785 JCS for the safe-integer-only I-JSON subset required by network/README.md §1. */
final class IamNetworkDeliveryJson {
  private static final int MAX_BYTES = 65536;
  private static final JsonMapper JSON = JsonMapper.builder().build();
  private static final JsonFactory FACTORY =
      JsonFactory.builder()
          .enable(StreamReadFeature.STRICT_DUPLICATE_DETECTION)
          .streamReadConstraints(
              StreamReadConstraints.builder()
                  .maxNestingDepth(16)
                  .maxStringLength(16384)
                  .maxNumberLength(17)
                  .build())
          .build();

  private IamNetworkDeliveryJson() {}

  private static final SchemaRegistry SCHEMAS =
      SchemaRegistry.withDefaultDialect(
          SpecificationVersion.DRAFT_2020_12,
          b ->
              b.schemaRegistryConfig(
                      SchemaRegistryConfig.builder().formatAssertionsEnabled(true).build())
                  .schemas(localSchemas()));

  private static Map<String, String> localSchemas() {
    var result = new HashMap<String, String>();
    for (String file : new String[] {"address.schema.json", "envelope.schema.json"}) {
      String path = "auriga/iam/contracts/network/" + file;
      try (var input = IamNetworkDeliveryJson.class.getClassLoader().getResourceAsStream(path)) {
        if (input == null) throw new IllegalStateException("Missing network schema resource");
        JsonNode schema = JSON.readTree(input);
        allowOptionalFields(schema);
        result.put("classpath:" + path, schema.toString());
        result.put(schema.path("$id").asString(), schema.toString());
      } catch (IOException e) {
        throw new IllegalStateException("Cannot read network schema resource");
      }
    }
    return result;
  }

  // Consumer compatibility: retain unknown fields, while known forbidden properties remain false.
  private static void allowOptionalFields(JsonNode schema) {
    if (schema instanceof ObjectNode object) {
      if (object.path("additionalProperties").isBoolean()
          && !object.path("additionalProperties").asBoolean())
        object.put("additionalProperties", true);
      for (var entry : object.properties()) allowOptionalFields(entry.getValue());
    } else if (schema.isArray()) for (var item : schema) allowOptionalFields(item);
  }

  static void validate(JsonNode message, JsonNode context) {
    schema("Message", message);
    schema("SourceContext", context);
    var from = message.path("from");
    var to = message.path("to");
    var auth = message.path("authorization_ref");
    if (!"agent".equals(message.path("sender_kind").asString())
        || !"session".equals(from.path("type").asString())
        || !Set.of("message", "reply").contains(message.path("kind").asString())
        || !Set.of("information", "request").contains(message.path("intent").asString())
        || !"durable".equals(message.path("durability").asString())
        || !"unicast".equals(message.path("message_class").asString())
        || "group".equals(to.path("type").asString())
        || !sameAddress(from, message.path("reply_to"))
        || !from.path("realm").equals(to.path("realm"))
        || ("reply".equals(message.path("kind").asString())
            && !"private".equals(message.path("reply_mode").asString()))
        || !IamNetworkAuthority.validNumber(auth.path("source_revision").asString(), false)
        || !IamNetworkAuthority.validNumber(auth.path("policy_revision").asString(), false)
        || !auth.path("broadcast_grant_id").isNull()
        || auth.has("human_intent_id")
        || auth.has("grant_revision")
        || auth.has("subject_type")
        || context.has("identity_revision")
        || !context.has("observed_at")
        || !"agent".equals(context.path("sender_kind").asString())
        || !context.path("realm").equals(from.path("realm"))
        || !context.path("node").equals(from.path("node"))
        || !digest(message).equals(context.path("message_digest").asString())) fail();
    if (bytes(canonical(message.path("body"))) > 16384) fail();
    Instant created = Instant.parse(message.path("created_at").asString());
    Instant expires = Instant.parse(message.path("expires_at").asString());
    if (!expires.isAfter(created)
        || expires.isAfter(created.plusSeconds(message.path("ttl").asLong()))) fail();
    if (message.has("response_deadline")) {
      Instant response = Instant.parse(message.path("response_deadline").asString());
      if (!response.isAfter(created)
          || response.isAfter(created.plusSeconds(86400))
          || response.isAfter(expires)) fail();
    }
    String label = context.path("display_label").asString();
    String normalized =
        java.text.Normalizer.normalize(label, java.text.Normalizer.Form.NFKC)
            .replaceAll("[\\p{Cf}\\p{Cc}\\p{Z}]", "")
            .toLowerCase(java.util.Locale.ROOT);
    if (label.isBlank()
        || label.length() > 64
        || label
            .codePoints()
            .anyMatch(c -> Character.getType(c) == Character.FORMAT || Character.isISOControl(c)))
      fail();
    for (String reserved : Set.of("总部", "管理员", "系统", "headquarters", "administrator", "system"))
      if (normalized.contains(reserved)) fail();
  }

  private static void schema(String definition, JsonNode value) {
    var schema =
        SCHEMAS.getSchema(
            SchemaLocation.of(
                "https://auriga.example.com/contracts/network/envelope.schema.json#/$defs/"
                    + definition));
    if (!schema.validate(value).isEmpty()) fail();
  }

  private static boolean sameAddress(JsonNode a, JsonNode b) {
    for (String field : Set.of("version", "realm", "type", "node", "local"))
      if (!a.path(field).equals(b.path(field))) return false;
    return true;
  }

  static int bytes(String value) {
    return value.getBytes(StandardCharsets.UTF_8).length;
  }

  public static JsonNode parse(byte[] input) {
    if (input == null || input.length > MAX_BYTES) fail();
    try {
      return parse(
          StandardCharsets.UTF_8
              .newDecoder()
              .onMalformedInput(java.nio.charset.CodingErrorAction.REPORT)
              .onUnmappableCharacter(java.nio.charset.CodingErrorAction.REPORT)
              .decode(java.nio.ByteBuffer.wrap(input))
              .toString());
    } catch (java.nio.charset.CharacterCodingException e) {
      throw new AurigaException(auriga.common.error.CommonErrors.VALIDATION_FAILED);
    }
  }

  public static JsonNode parse(String input) {
    try {
      if (input == null || input.getBytes(StandardCharsets.UTF_8).length > MAX_BYTES) fail();
      unicode(input);
      int values = 0;
      try (var parser = FACTORY.createParser(input)) {
        JsonToken token;
        while ((token = parser.nextToken()) != null) {
          if (token != JsonToken.PROPERTY_NAME
              && token != JsonToken.END_ARRAY
              && token != JsonToken.END_OBJECT
              && ++values > 8192) fail();
          if (token == JsonToken.VALUE_NUMBER_FLOAT) fail();
          if (token == JsonToken.VALUE_NUMBER_INT) {
            long n = parser.getLongValue();
            if (n < -9007199254740991L || n > 9007199254740991L) fail();
          }
          if (token == JsonToken.VALUE_STRING || token == JsonToken.PROPERTY_NAME)
            unicode(parser.getString());
        }
      }
      try (var parser = FACTORY.createParser(input)) {
        JsonNode result = JSON.readTree(parser);
        if (result == null || parser.nextToken() != null) fail();
        validate(result, 1, new int[] {0});
        return result;
      }
    } catch (AurigaException e) {
      throw e;
    } catch (RuntimeException e) {
      throw new AurigaException(auriga.common.error.CommonErrors.VALIDATION_FAILED);
    }
  }

  public static String canonical(JsonNode value) {
    validate(value, 1, new int[] {0});
    StringBuilder out = new StringBuilder();
    append(value, out);
    if (out.toString().getBytes(StandardCharsets.UTF_8).length > MAX_BYTES) fail();
    return out.toString();
  }

  public static String digest(JsonNode value) {
    try {
      return "sha256:"
          + HexFormat.of()
              .formatHex(
                  MessageDigest.getInstance("SHA-256")
                      .digest(canonical(value).getBytes(StandardCharsets.UTF_8)));
    } catch (NoSuchAlgorithmException e) {
      throw new IllegalStateException("SHA-256 unavailable", e);
    }
  }

  private static void validate(JsonNode n, int depth, int[] values) {
    if (n == null || depth > 16 || ++values[0] > 8192) fail();
    if (n.isObject()) {
      if (n.size() > 128) fail();
      for (var entry : n.properties()) {
        if (entry.getKey().getBytes(StandardCharsets.UTF_8).length > 16384) fail();
        unicode(entry.getKey());
        validate(entry.getValue(), depth + 1, values);
      }
    } else if (n.isArray()) {
      if (n.size() > 256) fail();
      for (var item : n) validate(item, depth + 1, values);
    } else if (n.isString()) {
      if (n.asString().getBytes(StandardCharsets.UTF_8).length > 16384) fail();
      unicode(n.asString());
    } else if (n.isNumber()) {
      if (!n.isIntegralNumber()
          || !n.canConvertToLong()
          || n.asLong() < -9007199254740991L
          || n.asLong() > 9007199254740991L) fail();
    } else if (!n.isNull() && !n.isBoolean()) fail();
  }

  private static void append(JsonNode n, StringBuilder out) {
    if (n.isObject()) {
      var names = new ArrayList<String>();
      n.propertyNames().forEach(names::add);
      names.sort(String::compareTo); // JCS sorts UTF-16 code units; no Unicode normalization.
      out.append('{');
      boolean comma = false;
      for (String key : names) {
        if (comma) out.append(',');
        quote(key, out);
        out.append(':');
        append(n.get(key), out);
        comma = true;
      }
      out.append('}');
    } else if (n.isArray()) {
      out.append('[');
      boolean comma = false;
      for (var item : n) {
        if (comma) out.append(',');
        append(item, out);
        comma = true;
      }
      out.append(']');
    } else if (n.isString()) quote(n.asString(), out);
    else if (n.isIntegralNumber()) out.append(n.asLong());
    else out.append(n.toString());
  }

  private static void quote(String s, StringBuilder out) {
    out.append('"');
    for (int i = 0; i < s.length(); i++) {
      char c = s.charAt(i);
      switch (c) {
        case '"' -> out.append("\\\"");
        case '\\' -> out.append("\\\\");
        case '\b' -> out.append("\\b");
        case '\t' -> out.append("\\t");
        case '\n' -> out.append("\\n");
        case '\f' -> out.append("\\f");
        case '\r' -> out.append("\\r");
        default -> {
          if (c < 0x20) {
            out.append("\\u00")
                .append(Character.forDigit(c >> 4, 16))
                .append(Character.forDigit(c & 15, 16));
          } else out.append(c);
        }
      }
    }
    out.append('"');
  }

  private static void unicode(String s) {
    for (int i = 0; i < s.length(); i++) {
      char c = s.charAt(i);
      if (Character.isHighSurrogate(c)) {
        if (++i >= s.length() || !Character.isLowSurrogate(s.charAt(i))) fail();
      } else if (Character.isLowSurrogate(c)) fail();
    }
  }

  private static void fail() {
    throw new AurigaException(auriga.common.error.CommonErrors.VALIDATION_FAILED);
  }
}
