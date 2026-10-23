package auriga.resource.web;

import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import jakarta.servlet.http.HttpServletRequest;
import java.io.IOException;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.util.*;
import tools.jackson.core.StreamReadFeature;
import tools.jackson.databind.DeserializationFeature;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.json.JsonMapper;

/** Bounded, strict contract parsing; no global Jackson changes or ignored protected fields. */
public record PackageBody(String name, String visibility, Set<UUID> groups) {
  private static final int MAX_BYTES = 32768;
  private static final JsonMapper JSON =
      JsonMapper.builder()
          .enable(StreamReadFeature.STRICT_DUPLICATE_DETECTION)
          .enable(DeserializationFeature.FAIL_ON_TRAILING_TOKENS)
          .build();

  public static PackageBody read(HttpServletRequest request, boolean create) {
    try {
      if (request.getContentLengthLong() > MAX_BYTES) throw invalid();
      byte[] bytes = request.getInputStream().readNBytes(MAX_BYTES + 1);
      if (bytes.length == 0 || bytes.length > MAX_BYTES) throw invalid();
      JsonNode body = JSON.readTree(bytes);
      if (body == null || !body.isObject() || body.isEmpty()) throw invalid();
      Set<String> allowed =
          create
              ? Set.of("name", "kind", "visibility", "groupIds")
              : Set.of("name", "visibility", "groupIds");
      for (String field : body.propertyNames()) if (!allowed.contains(field)) throw invalid();
      if (create
          && (!body.has("kind")
              || !body.get("kind").isString()
              || !body.get("kind").asString().equals("plugin"))) throw invalid();
      String name = null;
      if (body.has("name")) {
        var node = body.get("name");
        if (!node.isString()) throw invalid();
        name = node.asString();
        int length = name.codePointCount(0, name.length());
        if (length < 1 || length > 214 || name.indexOf('\0') >= 0) throw invalid();
        for (int index = 0; index < name.length(); index++) {
          char character = name.charAt(index);
          if (Character.isHighSurrogate(character)) {
            if (++index >= name.length() || !Character.isLowSurrogate(name.charAt(index)))
              throw invalid();
          } else if (Character.isLowSurrogate(character)) throw invalid();
        }
      } else if (create) throw invalid();
      String visibility = null;
      if (body.has("visibility")) {
        var node = body.get("visibility");
        if (!node.isString()
            || !Set.of("tenant", "members", "groups", "private").contains(node.asString()))
          throw invalid();
        visibility = node.asString();
      } else if (create) throw invalid();
      Set<UUID> groups = null;
      if (body.has("groupIds")) {
        var node = body.get("groupIds");
        if (!node.isArray() || node.size() > 200) throw invalid();
        groups = new TreeSet<>();
        for (JsonNode item : node) {
          if (!item.isString()
              || !item.asString()
                  .matches(
                      "[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}"))
            throw invalid();
          if (!groups.add(UUID.fromString(item.asString()))) throw invalid();
        }
        groups = Collections.unmodifiableSet(groups);
      }
      if (create && groups == null) groups = Set.of();
      return new PackageBody(name, visibility, groups);
    } catch (IOException | tools.jackson.core.JacksonException | IllegalArgumentException e) {
      throw invalid();
    }
  }

  public String fingerprint() {
    // Field order, UUID spelling and group set order are not meaningful in PackageInput.
    var canonical = new TreeMap<String, Object>();
    canonical.put("kind", "plugin");
    canonical.put("name", name);
    canonical.put("visibility", visibility);
    canonical.put("groupIds", groups.stream().map(UUID::toString).sorted().toList());
    try {
      return HexFormat.of()
          .formatHex(
              MessageDigest.getInstance("SHA-256").digest(JSON.writeValueAsBytes(canonical)));
    } catch (NoSuchAlgorithmException e) {
      throw new IllegalStateException(e);
    }
  }

  private static AurigaException invalid() {
    return new AurigaException(CommonErrors.VALIDATION_FAILED);
  }
}
