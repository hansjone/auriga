package auriga.resource.web;

import auriga.common.error.*;
import jakarta.servlet.http.HttpServletRequest;
import java.nio.ByteBuffer;
import java.nio.charset.*;
import java.util.*;
import tools.jackson.core.StreamReadFeature;
import tools.jackson.databind.*;
import tools.jackson.databind.json.JsonMapper;
import tools.jackson.databind.node.ObjectNode;

/** Local bounded/strict parser; never changes global Jackson settings. */
public final class ResourceBody {
  public static final JsonMapper JSON =
      JsonMapper.builder()
          .enable(StreamReadFeature.STRICT_DUPLICATE_DETECTION)
          .enable(DeserializationFeature.FAIL_ON_TRAILING_TOKENS)
          .build();

  private ResourceBody() {}

  public static ObjectNode read(
      HttpServletRequest request, Set<String> allowed, Set<String> required) {
    try {
      return parse(request.getInputStream().readNBytes(32769), 32768, allowed, required);
    } catch (java.io.IOException e) {
      throw invalid();
    }
  }

  public static ObjectNode parse(
      byte[] bytes, int maximum, Set<String> allowed, Set<String> required) {
    try {
      if (bytes.length == 0 || bytes.length > maximum) throw invalid();
      String utf8 =
          StandardCharsets.UTF_8
              .newDecoder()
              .onMalformedInput(CodingErrorAction.REPORT)
              .onUnmappableCharacter(CodingErrorAction.REPORT)
              .decode(ByteBuffer.wrap(bytes))
              .toString();
      var body = JSON.readTree(utf8);
      if (!(body instanceof ObjectNode object)) throw invalid();
      for (String name : object.propertyNames())
        if ((allowed != null && !allowed.contains(name)) || object.get(name).isNull())
          throw invalid();
      for (String name : required) if (!object.has(name)) throw invalid();
      return object;
    } catch (AurigaException e) {
      throw e;
    } catch (Exception e) {
      throw invalid();
    }
  }

  public static String text(JsonNode body, String name, int max) {
    var node = body.get(name);
    if (node == null || !node.isString()) throw invalid();
    String value = node.asString();
    if (value.isBlank()
        || !value.equals(value.trim())
        || value.codePointCount(0, value.length()) > max) throw invalid();
    for (int i = 0; i < value.length(); i++) {
      char c = value.charAt(i);
      if (c == 0) throw invalid();
      if (Character.isHighSurrogate(c)) {
        if (++i >= value.length() || !Character.isLowSurrogate(value.charAt(i))) throw invalid();
      } else if (Character.isLowSurrogate(c)) throw invalid();
    }
    return value;
  }

  public static UUID uuid(JsonNode body, String name) {
    try {
      String value = text(body, name, 36);
      UUID id = UUID.fromString(value);
      if (!id.toString().equalsIgnoreCase(value)) throw invalid();
      return id;
    } catch (IllegalArgumentException e) {
      throw invalid();
    }
  }

  public static long counter(JsonNode body, String name) {
    var node = body.get(name);
    if (node == null
        || !node.isIntegralNumber()
        || !node.canConvertToLong()
        || node.asLong() < 1
        || node.asLong() > 9007199254740991L) throw invalid();
    return node.asLong();
  }

  public static String header(HttpServletRequest request, String name) {
    var values = request.getHeaders(name);
    if (!values.hasMoreElements()) return null;
    String value = values.nextElement();
    if (values.hasMoreElements())
      throw name.equals("If-Match")
          ? new AurigaException(CommonErrors.PRECONDITION_FAILED)
          : invalid();
    return value;
  }

  public static AurigaException invalid() {
    return new AurigaException(CommonErrors.VALIDATION_FAILED);
  }
}
