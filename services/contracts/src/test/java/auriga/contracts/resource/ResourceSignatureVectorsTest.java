package auriga.contracts.resource;

import static org.junit.jupiter.api.Assertions.assertArrayEquals;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

import auriga.ai.web.model.Usage;
import auriga.resource.web.model.ResourceEnvelope;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.security.KeyFactory;
import java.security.KeyPairGenerator;
import java.security.MessageDigest;
import java.security.Signature;
import java.security.spec.X509EncodedKeySpec;
import java.util.ArrayList;
import java.util.Base64;
import java.util.HexFormat;
import java.util.List;
import org.junit.jupiter.api.Test;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.json.JsonMapper;
import tools.jackson.databind.node.ObjectNode;

/** Tests the resource schema's integer-only JCS domain, not a production signature verifier. */
class ResourceSignatureVectorsTest {
  private static final JsonMapper JSON = JsonMapper.builder().build();

  @Test
  void canonicalBytesAndSignaturesMatchSharedGoldenVectors() throws Exception {
    var input = getClass().getResourceAsStream("/signature-vectors.json");
    assertTrue(input != null);
    try (input) {
      var vectors = JSON.readTree(input).get("cases");
      assertEquals(4, vectors.size());
      var javaVectors = JSON.createArrayNode();
      for (var vector : vectors) {
        var document = (ObjectNode) vector.get("document").deepCopy();
        if (vector.get("schema").asString().equals("ResourceEnvelope")) {
          var model = JSON.treeToValue(document, ResourceEnvelope.class);
          assertEquals(document.get("notAfter").asString(), model.getNotAfter());
          assertEquals(
              document.get("statement").get("issuedAt").asString(),
              model.getStatement().getIssuedAt());
        }
        var signatureNode = document.remove("signature");
        var canonical = canonical(document);
        assertEquals(vector.get("canonical").asString(), canonical);
        var bytes = canonical.getBytes(StandardCharsets.UTF_8);
        assertEquals(
            vector.get("sha256").asString(),
            HexFormat.of().formatHex(MessageDigest.getInstance("SHA-256").digest(bytes)));
        var key =
            KeyFactory.getInstance("Ed25519")
                .generatePublic(
                    new X509EncodedKeySpec(
                        Base64.getUrlDecoder().decode(vector.get("publicKeySpki").asString())));
        var verifier = Signature.getInstance("Ed25519");
        verifier.initVerify(key);
        verifier.update(bytes);
        var signature = Base64.getUrlDecoder().decode(signatureNode.get("value").asString());
        assertTrue(verifier.verify(signature));
        bytes[0] ^= 1;
        verifier.initVerify(key);
        verifier.update(bytes);
        assertFalse(verifier.verify(signature));

        // Independently exercise Java signing with a new ephemeral key, never a committed private
        // key.
        var pair = KeyPairGenerator.getInstance("Ed25519").generateKeyPair();
        var signer = Signature.getInstance("Ed25519");
        signer.initSign(pair.getPrivate());
        signer.update(canonical.getBytes(StandardCharsets.UTF_8));
        var javaSignature = signer.sign();
        verifier.initVerify(pair.getPublic());
        verifier.update(canonical.getBytes(StandardCharsets.UTF_8));
        assertTrue(verifier.verify(javaSignature));
        var javaVector = JSON.createObjectNode();
        javaVector.put("name", vector.get("name").asString());
        javaVector.put("canonical", canonical);
        javaVector.put(
            "signature", Base64.getUrlEncoder().withoutPadding().encodeToString(javaSignature));
        javaVector.put(
            "publicKeySpki",
            Base64.getUrlEncoder().withoutPadding().encodeToString(pair.getPublic().getEncoded()));
        javaVectors.add(javaVector);
        assertArrayEquals(
            canonical.getBytes(StandardCharsets.UTF_8),
            vector.get("canonical").asString().getBytes(StandardCharsets.UTF_8));
      }
      // Public, ephemeral interoperability evidence. No private key is written.
      Files.writeString(
          Path.of("target/java-signature-vectors.json"), JSON.writeValueAsString(javaVectors));
    }
  }

  @Test
  void nullableLongPreservesNullAndZeroButRawPresenceNeedsSeparateValidation() {
    // This generated Java DTO cannot distinguish missing from explicit null. The source contract
    // remains required+nullable; service inputs must validate their raw JSON before mapping.
    var explicitNull = JSON.readValue("{\"inputTokens\":null}", Usage.class);
    var missing = JSON.readValue("{}", Usage.class);
    var zero = JSON.readValue("{\"inputTokens\":0}", Usage.class);
    assertNull(explicitNull.getInputTokens());
    assertNull(missing.getInputTokens());
    assertEquals(0L, zero.getInputTokens());
    assertTrue(JSON.readTree("{\"inputTokens\":null}").has("inputTokens"));
    assertFalse(JSON.readTree("{}").has("inputTokens"));
  }

  @Test
  void utf16OrderingAndEscapesFollowJcsWithoutNormalizingStrings() {
    var node = JSON.createObjectNode();
    node.put("\ue000", "line\n\t\u0001/\\\"");
    node.put("\ud83d\ude00", "中文");
    assertEquals("{\"😀\":\"中文\",\"\ue000\":\"line\\n\\t\\u0001/\\\\\\\"\"}", canonical(node));
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
    throw new IllegalArgumentException("outside the resource signature schema");
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
