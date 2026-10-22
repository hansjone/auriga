package auriga.contracts.node;

import static org.junit.jupiter.api.Assertions.*;

import auriga.contracts.node.v1.ArtifactStatement;
import auriga.contracts.node.v1.RuntimeReleaseEnvelope;
import com.google.protobuf.Timestamp;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.security.*;
import java.security.spec.X509EncodedKeySpec;
import java.time.Instant;
import java.time.ZoneOffset;
import java.time.format.DateTimeFormatter;
import java.util.*;
import org.junit.jupiter.api.Test;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.json.JsonMapper;
import tools.jackson.databind.node.ObjectNode;

/** Contract-only mapping and Java-signed interoperability fixtures; no production trust engine. */
class RuntimeTrustVectorsTest {
  private static final JsonMapper JSON = JsonMapper.builder().build();
  private static final Path FIXTURE = Path.of("../../contracts/runtime/signature-vectors.json");
  private static final Base64.Encoder B64 = Base64.getUrlEncoder().withoutPadding();
  private static final String TAR = "sha256:" + "a".repeat(64);
  private static final String IMAGE = "sha256:" + "b".repeat(64);

  @Test
  void sharedJavaSignaturesAndDeterministicProtobufMappings() throws Exception {
    var generated = generate();
    Files.createDirectories(Path.of("target"));
    var output = Path.of("target/java-runtime-signature-vectors.json");
    Files.writeString(output, JSON.writerWithDefaultPrettyPrinter().writeValueAsString(generated));
    if (Boolean.getBoolean("auriga.runtime.generateVectors")) {
      Files.writeString(
          FIXTURE, JSON.writerWithDefaultPrettyPrinter().writeValueAsString(generated) + "\n");
    }
    var fixture = JSON.readTree(Files.readString(FIXTURE));
    assertEquals("Java JCA Ed25519", fixture.get("producer").asString());
    assertEquals(14, fixture.get("cases").size());
    var resigned = fixture.get("resigned");
    assertEquals(fixture.get("cases").get(0).get("canonical"), resigned.get("canonical"));
    assertNotEquals(
        fixture.get("cases").get(0).get("publicKeySpki"), resigned.get("publicKeySpki"));
    verify(
        resigned.get("publicKeySpki").asString(),
        resigned.get("signature").asString(),
        resigned.get("canonical").asString().getBytes(StandardCharsets.UTF_8));
    for (var vector : fixture.get("cases")) {
      var body = (ObjectNode) vector.get("document").deepCopy();
      var signature =
          vector.get("schema").asString().equals("runtime")
              ? body.remove("signature").get("value").asString()
              : vector.get("signature").asString();
      var canonical = canonical(body);
      assertEquals(vector.get("canonical").asString(), canonical);
      assertEquals(
          vector.get("sha256").asString(), sha(canonical.getBytes(StandardCharsets.UTF_8)));
      verify(
          vector.get("publicKeySpki").asString(),
          signature,
          canonical.getBytes(StandardCharsets.UTF_8));
      var timestamp = vector.get("timestamp");
      var ts =
          Timestamp.newBuilder()
              .setSeconds(timestamp.get("seconds").asLong())
              .setNanos(timestamp.get("nanos").asInt())
              .build();
      assertEquals(body.get("issuedAt").asString(), time(ts));
      if (vector.get("schema").asString().equals("runtime")) {
        var model =
            RuntimeReleaseEnvelope.newBuilder()
                .setSchemaVersion(1)
                .setPurpose("dsh_runtime")
                .setName("dsh")
                .setArtifactId(body.get("artifactId").asString())
                .setDshVersion(body.get("dshVersion").asString())
                .setOs(body.get("os").asString())
                .setArch(body.get("arch").asString())
                .setArchiveDigest(body.get("archiveDigest").asString())
                .setImageDigest(body.get("imageDigest").asString())
                .setReleaseSequence(body.get("releaseSequence").asLong())
                .setIssuedAt(ts)
                .build();
        assertEquals(canonical, canonical(runtimeBody(model)));
      } else {
        var model =
            ArtifactStatement.newBuilder()
                .setPurpose(body.get("purpose").asString())
                .setName(body.get("name").asString())
                .setVersion(body.get("version").asString())
                .setOs(body.get("os").asString())
                .setArch(body.get("arch").asString())
                .setDigest(body.get("digest").asString())
                .setIssuedAt(ts)
                .build();
        assertEquals(canonical, canonical(statementBody(model)));
      }
    }
    for (var key : fixture.get("keys")) {
      var raw = Base64.getUrlDecoder().decode(key.get("publicKey").asString());
      assertEquals(32, raw.length);
      if (key.get("endorsedByKid").asString().isEmpty()) continue;
      var input = endorsementInput(key.get("kid").asString(), raw);
      assertEquals(key.get("inputHex").asString(), HexFormat.of().formatHex(input));
      assertEquals(key.get("inputLength").asInt(), input.length);
      verify(key.get("parentPublicKeySpki").asString(), key.get("endorsement").asString(), input);
    }
  }

  @Test
  void timestampValidityAndShortestFractionAreExplicit() {
    assertEquals(
        "0001-01-01T00:00:00Z", time(Timestamp.newBuilder().setSeconds(-62135596800L).build()));
    assertEquals(
        "9999-12-31T23:59:59.999999999Z",
        time(Timestamp.newBuilder().setSeconds(253402300799L).setNanos(999999999).build()));
    for (long seconds : new long[] {-62135596801L, 253402300800L})
      assertThrows(
          IllegalArgumentException.class,
          () -> time(Timestamp.newBuilder().setSeconds(seconds).build()));
    for (int nanos : new int[] {-1, 1000000000})
      assertThrows(
          IllegalArgumentException.class,
          () -> time(Timestamp.newBuilder().setNanos(nanos).build()));
    assertThrows(IllegalArgumentException.class, () -> endorsementInput("bad/kid", new byte[32]));
    assertThrows(IllegalArgumentException.class, () -> endorsementInput("kid", new byte[31]));
    var node = JSON.createObjectNode().put("\ue000", "e\u0301").put("😀", "é");
    assertEquals("{\"😀\":\"é\",\"\ue000\":\"é\"}", canonical(node));
    assertThrows(
        IllegalArgumentException.class,
        () -> canonical(JSON.createObjectNode().put("bad", "\ud800")));
  }

  private static ObjectNode generate() throws Exception {
    var result = JSON.createObjectNode().put("producer", "Java JCA Ed25519");
    var cases = result.putArray("cases");
    var pair = KeyPairGenerator.getInstance("Ed25519").generateKeyPair();
    int[] nanos = {0, 100000000, 120000000, 123000000, 123456000, 123456789, 1};
    for (int i = 0; i < nanos.length; i++) {
      var ts = Timestamp.newBuilder().setSeconds(1792713600L).setNanos(nanos[i]).build();
      var runtime =
          RuntimeReleaseEnvelope.newBuilder()
              .setSchemaVersion(1)
              .setPurpose("dsh_runtime")
              .setName("dsh")
              .setArtifactId("dsh-é-é-😀-" + i)
              .setDshVersion("0.2.0+node22.19")
              .setOs(i == 6 ? "windows" : "linux")
              .setArch(i == 5 ? "arm64" : "amd64")
              .setArchiveDigest(i == 0 ? "" : TAR)
              .setImageDigest(i == 6 ? "" : IMAGE)
              .setReleaseSequence(i == 5 ? 9007199254740991L : 7)
              .setIssuedAt(ts)
              .build();
      var body = runtimeBody(runtime);
      var vector = signVector("runtime-" + i, "runtime", body, ts, pair);
      body.set(
          "signature",
          JSON.createObjectNode()
              .put("alg", "Ed25519")
              .put("kid", "release-root")
              .put("value", vector.get("signature").asString()));
      vector.remove("signature");
      vector.set("document", body);
      cases.add(vector);
      var statement =
          ArtifactStatement.newBuilder()
              .setPurpose("dsh_runtime")
              .setName("dsh")
              .setVersion("0.2.0+node22.19")
              .setOs(runtime.getOs())
              .setArch(runtime.getArch())
              .setDigest(TAR)
              .setIssuedAt(ts)
              .build();
      var old = statementBody(statement);
      var standalone = signVector("standalone-" + i, "standalone", old, ts, pair);
      standalone.set("document", old);
      cases.add(standalone);
    }
    // A different key may re-sign the same complete body without changing its sequence/digest.
    var resigningKey = KeyPairGenerator.getInstance("Ed25519").generateKeyPair();
    var resignedCanonical = cases.get(0).get("canonical").asString();
    result.set(
        "resigned",
        JSON.createObjectNode()
            .put("canonical", resignedCanonical)
            .put("publicKeySpki", B64.encodeToString(resigningKey.getPublic().getEncoded()))
            .put(
                "signature",
                sign(resigningKey, resignedCanonical.getBytes(StandardCharsets.UTF_8))));
    var root = KeyPairGenerator.getInstance("Ed25519").generateKeyPair();
    var child = KeyPairGenerator.getInstance("Ed25519").generateKeyPair();
    var leaf = KeyPairGenerator.getInstance("Ed25519").generateKeyPair();
    var keys = result.putArray("keys");
    keys.add(key("root", root, "", null, "2027-01-01T00:00:00Z"));
    keys.add(key("child", child, "root", root, "2028-01-01T00:00:00Z"));
    keys.add(key("leaf", leaf, "child", child, "2026-12-01T00:00:00Z"));
    return result;
  }

  private static ObjectNode key(
      String kid, KeyPair pair, String parentKid, KeyPair parent, String notAfter)
      throws Exception {
    var spki = pair.getPublic().getEncoded();
    var raw = Arrays.copyOfRange(spki, spki.length - 32, spki.length);
    var key =
        JSON.createObjectNode()
            .put("kid", kid)
            .put("publicKey", B64.encodeToString(raw))
            .put("publicKeySpki", B64.encodeToString(spki))
            .put("notAfter", notAfter)
            .put("endorsedByKid", parentKid);
    if (parent == null) return key.put("endorsement", "");
    var input = endorsementInput(kid, raw);
    return key.put("endorsement", sign(parent, input))
        .put("parentPublicKeySpki", B64.encodeToString(parent.getPublic().getEncoded()))
        .put("inputHex", HexFormat.of().formatHex(input))
        .put("inputLength", input.length);
  }

  private static ObjectNode signVector(
      String name, String schema, ObjectNode body, Timestamp ts, KeyPair pair) throws Exception {
    var canonical = canonical(body);
    var bytes = canonical.getBytes(StandardCharsets.UTF_8);
    return JSON.createObjectNode()
        .put("name", name)
        .put("schema", schema)
        .put("canonical", canonical)
        .put("sha256", sha(bytes))
        .put("signature", sign(pair, bytes))
        .put("publicKeySpki", B64.encodeToString(pair.getPublic().getEncoded()))
        .set(
            "timestamp",
            JSON.createObjectNode().put("seconds", ts.getSeconds()).put("nanos", ts.getNanos()));
  }

  private static String sign(KeyPair pair, byte[] bytes) throws Exception {
    var signer = Signature.getInstance("Ed25519");
    signer.initSign(pair.getPrivate());
    signer.update(bytes);
    return B64.encodeToString(signer.sign());
  }

  private static void verify(String spki, String signature, byte[] bytes) throws Exception {
    var key =
        KeyFactory.getInstance("Ed25519")
            .generatePublic(new X509EncodedKeySpec(Base64.getUrlDecoder().decode(spki)));
    var verifier = Signature.getInstance("Ed25519");
    verifier.initVerify(key);
    verifier.update(bytes);
    var sig = Base64.getUrlDecoder().decode(signature);
    assertEquals(64, sig.length);
    assertEquals(signature, B64.encodeToString(sig));
    assertTrue(verifier.verify(sig));
    verifier.initVerify(key);
    verifier.update(Arrays.copyOf(bytes, bytes.length + 1));
    assertFalse(verifier.verify(sig));
  }

  private static byte[] endorsementInput(String kid, byte[] key) {
    if (!kid.matches("[A-Za-z0-9._-]{1,128}") || key.length != 32)
      throw new IllegalArgumentException("endorsement input");
    var name = kid.getBytes(StandardCharsets.UTF_8);
    var result = Arrays.copyOf(name, name.length + 32);
    System.arraycopy(key, 0, result, name.length, 32);
    return result;
  }

  private static ObjectNode runtimeBody(RuntimeReleaseEnvelope m) {
    if (m.getReleaseSequence() < 1
        || m.getReleaseSequence() > 9007199254740991L
        || !m.hasIssuedAt()
        || !m.getUnknownFields().asMap().isEmpty())
      throw new IllegalArgumentException("runtime mapping");
    return JSON.createObjectNode()
        .put("schemaVersion", m.getSchemaVersion())
        .put("purpose", m.getPurpose())
        .put("name", m.getName())
        .put("artifactId", m.getArtifactId())
        .put("dshVersion", m.getDshVersion())
        .put("os", m.getOs())
        .put("arch", m.getArch())
        .put("archiveDigest", m.getArchiveDigest())
        .put("imageDigest", m.getImageDigest())
        .put("releaseSequence", m.getReleaseSequence())
        .put("issuedAt", time(m.getIssuedAt()));
  }

  private static ObjectNode statementBody(ArtifactStatement m) {
    if (!m.hasIssuedAt() || !m.getUnknownFields().asMap().isEmpty())
      throw new IllegalArgumentException("statement mapping");
    return JSON.createObjectNode()
        .put("purpose", m.getPurpose())
        .put("name", m.getName())
        .put("version", m.getVersion())
        .put("os", m.getOs())
        .put("arch", m.getArch())
        .put("digest", m.getDigest())
        .put("issuedAt", time(m.getIssuedAt()));
  }

  private static String time(Timestamp ts) {
    if (ts.getSeconds() < -62135596800L
        || ts.getSeconds() > 253402300799L
        || ts.getNanos() < 0
        || ts.getNanos() > 999999999) throw new IllegalArgumentException("invalid Timestamp");
    var base =
        DateTimeFormatter.ofPattern("uuuu-MM-dd'T'HH:mm:ss")
            .withZone(ZoneOffset.UTC)
            .format(Instant.ofEpochSecond(ts.getSeconds()));
    return base
        + (ts.getNanos() == 0
            ? ""
            : "." + String.format(Locale.ROOT, "%09d", ts.getNanos()).replaceFirst("0+$", ""))
        + "Z";
  }

  private static String sha(byte[] bytes) throws Exception {
    return HexFormat.of().formatHex(MessageDigest.getInstance("SHA-256").digest(bytes));
  }

  private static String canonical(JsonNode value) {
    if (value.isObject()) {
      List<String> keys = new ArrayList<>(value.propertyNames());
      keys.sort(String::compareTo); // Java String order is UTF-16 code-unit order.
      List<String> items = new ArrayList<>();
      for (var key : keys) items.add(quote(key) + ":" + canonical(value.get(key)));
      return "{" + String.join(",", items) + "}";
    }
    if (value.isArray()) {
      List<String> items = new ArrayList<>();
      value.forEach(item -> items.add(canonical(item)));
      return "[" + String.join(",", items) + "]";
    }
    if (value.isString()) return quote(value.asString());
    if (value.isIntegralNumber()) {
      long n = value.asLong();
      if (n < 0 || n > 9007199254740991L) throw new IllegalArgumentException("unsafe integer");
      return Long.toString(n);
    }
    if (value.isBoolean() || value.isNull()) return value.toString();
    throw new IllegalArgumentException("outside the runtime signature schema");
  }

  private static String quote(String value) {
    var out = new StringBuilder("\"");
    for (int i = 0; i < value.length(); i++) {
      char c = value.charAt(i);
      switch (c) {
        case '"' -> out.append("\\\"");
        case '\\' -> out.append("\\\\");
        case '\b' -> out.append("\\b");
        case '\f' -> out.append("\\f");
        case '\n' -> out.append("\\n");
        case '\r' -> out.append("\\r");
        case '\t' -> out.append("\\t");
        default -> {
          if (c < 0x20) out.append(String.format("\\u%04x", (int) c));
          else if (Character.isHighSurrogate(c)) {
            if (i + 1 >= value.length() || !Character.isLowSurrogate(value.charAt(i + 1))) {
              throw new IllegalArgumentException("unpaired surrogate");
            }
            out.append(c).append(value.charAt(++i));
          } else if (Character.isLowSurrogate(c)) {
            throw new IllegalArgumentException("unpaired surrogate");
          } else out.append(c);
        }
      }
    }
    return out.append('"').toString();
  }
}
