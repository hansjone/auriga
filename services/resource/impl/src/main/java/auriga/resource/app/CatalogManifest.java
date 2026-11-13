package auriga.resource.app;

import static auriga.resource.web.ResourceBody.*;

import com.networknt.schema.*;
import java.net.URI;
import java.nio.ByteBuffer;
import java.nio.charset.*;
import java.security.MessageDigest;
import java.util.*;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** Exact bytes are retained separately from the display JSON. P1 deliberately denies all tools. */
public final class CatalogManifest {
  private static final SchemaRegistry REGISTRY =
      SchemaRegistry.withDefaultDialect(
          SpecificationVersion.DRAFT_2020_12,
          b ->
              b.schemaIdResolvers(
                      r ->
                          r.mapPrefix(
                              "https://auriga.example.com/contracts/",
                              "classpath:auriga/contracts/"))
                  .schemaRegistryConfig(
                      SchemaRegistryConfig.builder().formatAssertionsEnabled(true).build()));
  private static final Schema SKILL =
      REGISTRY.getSchema(
          SchemaLocation.of(
              "classpath:auriga/contracts/resource/package.schema.json#/$defs/SkillManifest"));

  private CatalogManifest() {}

  public static ObjectNode parse(byte[] raw, String name, String version, String expected) {
    var m = auriga.resource.web.ResourceBody.parse(raw, 262144, null, Set.of());
    if (!SKILL.validate(m).isEmpty()
        || !"skill".equals(m.path("kind").asString())
        || !name.equals(m.path("name").asString())
        || !version.equals(m.path("version").asString())
        || !ResourceSemver.valid(version)
        || !digest(raw).equals(expected)) throw invalid();
    for (var host : m.path("permissions").path("outboundHosts")) host(host.asString());
    for (var a : m.path("artifacts"))
      if (!a.path("role").asString().equals("payload")
          || !a.path("os").asString().equals("any")
          || !a.path("arch").asString().equals("any")
          || !a.path("mediaType").asString().equals("application/gzip")
          || a.path("sizeBytes").asLong() > 5242880) throw invalid();
    // Validate npm-style bounded comparisons; no unresolved or arbitrary expressions.
    range(text(m.path("skill"), "dshVersionRange", 128));
    return m;
  }

  public static void range(String value) {
    if (value.equals("*")) return;
    for (String branch : value.split("\\|\\|", -1)) {
      if (branch.isBlank()) throw invalid();
      for (String term : branch.trim().split("\\s+")) {
        String v = term.replaceFirst("^(?:>=|<=|>|<|=|\\^|~)", "");
        if (!ResourceSemver.valid(v)) throw invalid();
      }
    }
  }

  public static String digest(byte[] raw) {
    try {
      return "sha256:" + HexFormat.of().formatHex(MessageDigest.getInstance("SHA-256").digest(raw));
    } catch (java.security.NoSuchAlgorithmException e) {
      throw new IllegalStateException(e);
    }
  }

  public static String utf8(byte[] bytes) {
    try {
      return StandardCharsets.UTF_8
          .newDecoder()
          .onMalformedInput(CodingErrorAction.REPORT)
          .onUnmappableCharacter(CodingErrorAction.REPORT)
          .decode(ByteBuffer.wrap(bytes))
          .toString();
    } catch (CharacterCodingException e) {
      throw invalid();
    }
  }

  public static void path(String name) {
    if (name.isEmpty() || name.startsWith("/") || name.contains("\\") || name.contains(":"))
      throw invalid();
    for (String segment : name.split("/", -1)) {
      if (segment.isEmpty()
          || segment.equals(".")
          || segment.equals("..")
          || segment.endsWith(".")
          || segment.endsWith(" ")
          || segment.chars().anyMatch(c -> c < 32 || "<>\"|?*".indexOf(c) >= 0)) throw invalid();
      String base = segment.split("\\.", 2)[0].toUpperCase(Locale.ROOT);
      if (Set.of("CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$").contains(base)
          || base.matches("(?:COM|LPT)[1-9¹²³]")) throw invalid();
    }
  }

  public static void host(String host) {
    if (!host.equals(host.toLowerCase(Locale.ROOT)) || host.length() > 253 || host.isBlank())
      throw invalid();
    if (host.contains(":")) {
      // Numeric IPv6 only, never DNS resolution.
      if (!host.matches("[0-9a-f:]+")) throw invalid();
      try {
        if (!(java.net.InetAddress.getByName(host) instanceof java.net.Inet6Address))
          throw invalid();
      } catch (java.net.UnknownHostException e) {
        throw invalid();
      }
      return;
    }
    for (String part : host.split("\\.", -1))
      if (!part.matches("[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?")) throw invalid();
    if (host.matches("[0-9.]+")) {
      var parts = host.split("\\.", -1);
      if (parts.length != 4) throw invalid();
      for (var part : parts)
        if (part.length() > 3
            || (part.length() > 1 && part.startsWith("0"))
            || Integer.parseInt(part) > 255) throw invalid();
    }
  }

  public static void url(String value) {
    try {
      URI u = URI.create(value);
      if (!Set.of("http", "https").contains(u.getScheme())
          || u.getHost() == null
          || u.getRawUserInfo() != null
          || u.getRawQuery() != null
          || u.getRawFragment() != null) throw invalid();
    } catch (IllegalArgumentException e) {
      throw invalid();
    }
  }

  public static List<String> artifactDigests(JsonNode manifest) {
    var out = new TreeSet<String>();
    manifest.path("artifacts").forEach(a -> out.add(a.path("digest").asString()));
    return List.copyOf(out);
  }
}
