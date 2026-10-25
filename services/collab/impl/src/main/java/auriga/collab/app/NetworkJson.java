package auriga.collab.app;

import auriga.common.error.AurigaException;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.util.ArrayList;
import java.util.HexFormat;
import tools.jackson.core.JsonToken;
import tools.jackson.core.StreamReadConstraints;
import tools.jackson.core.StreamReadFeature;
import tools.jackson.core.json.JsonFactory;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.json.JsonMapper;
import tools.jackson.databind.node.ObjectNode;

/** RFC 8785 JCS for the safe-integer-only I-JSON subset required by network/README.md §1. */
public final class NetworkJson {
  private static final int MAX_BYTES = 65536;
  static final JsonMapper JSON = JsonMapper.builder().build();
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

  private NetworkJson() {}

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
      throw new AurigaException(NetworkErrors.PROTOCOL_ERROR);
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
          if (++values > 16384) fail();
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
        validate(result, 0, new int[] {0});
        return result;
      }
    } catch (AurigaException e) {
      throw e;
    } catch (RuntimeException e) {
      throw new AurigaException(NetworkErrors.PROTOCOL_ERROR);
    }
  }

  public static String canonical(JsonNode value) {
    validate(value, 0, new int[] {0});
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

  static ObjectNode object() {
    return JSON.createObjectNode();
  }

  private static void validate(JsonNode n, int depth, int[] values) {
    if (n == null || depth > 16 || ++values[0] > 8192) fail();
    if (n.isObject()) {
      if (n.size() > 128) fail();
      for (var entry : n.properties()) {
        if (entry.getKey().length() > 16384) fail();
        unicode(entry.getKey());
        validate(entry.getValue(), depth + 1, values);
      }
    } else if (n.isArray()) {
      if (n.size() > 256) fail();
      for (var item : n) validate(item, depth + 1, values);
    } else if (n.isString()) {
      if (n.asString().length() > 16384) fail();
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
    throw new AurigaException(NetworkErrors.PROTOCOL_ERROR);
  }
}
