package auriga.contracts.collab;

import static org.junit.jupiter.api.Assertions.*;

import java.io.ByteArrayInputStream;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.security.KeyFactory;
import java.security.Signature;
import java.security.cert.CertificateFactory;
import java.security.spec.X509EncodedKeySpec;
import java.util.Base64;
import java.util.HashMap;
import org.junit.jupiter.api.Test;
import tools.jackson.databind.json.JsonMapper;

/** 跨语言公有合成签名向量；生产转换验证由实现线消费同一 cases。 */
class MsgnetV2VectorsTest {
  @Test
  void consumesEverySemanticCaseThroughContractReferenceAdapter() throws Exception {
    // 契约线共用参考adapter；C/N/P必须把此协议接到各自生产验证器，不能据此声称生产互操作通过。
    var json = JsonMapper.builder().build();
    var bytes =
        Files.readAllBytes(
            Path.of("../../contracts/network/fixtures/auto-renewal-v2-vectors.json"));
    var fixture = json.readTree(bytes);
    var expected = new HashMap<String, String>();
    for (var group : new String[] {"cases", "issuer_cases", "boundary_cases"}) {
      for (var vector : fixture.get(group)) {
        assertTrue(vector.get("input").isObject());
        assertNull(
            expected.put(
                group + "/" + vector.get("id").asString(), vector.get("expected").asString()));
      }
    }
    var executable =
        Path.of(
            "target",
            "node",
            System.getProperty("os.name").startsWith("Windows") ? "node.exe" : "node");
    var process =
        new ProcessBuilder(
                executable.toString(),
                "../../contracts/network/fixtures/run-msgnet-v2-cases.mjs",
                "--stdin")
            .redirectError(ProcessBuilder.Redirect.INHERIT)
            .start();
    try (var stdin = process.getOutputStream()) {
      stdin.write(bytes);
    }
    var results = json.readTree(process.getInputStream().readAllBytes());
    assertEquals(0, process.waitFor());
    assertEquals(expected.size(), results.size());
    for (var result : results) {
      var key = result.get("group").asString() + "/" + result.get("id").asString();
      assertEquals(expected.remove(key), result.get("result").asString(), key);
    }
    assertTrue(expected.isEmpty());
  }

  @Test
  void verifiesSharedV2SignaturesAndRejectsDomainConfusion() throws Exception {
    var fixture =
        JsonMapper.builder()
            .build()
            .readTree(
                Files.readString(
                    Path.of("../../contracts/network/fixtures/auto-renewal-v2-vectors.json")));
    assertTrue(fixture.get("synthetic").asBoolean());
    assertTrue(fixture.get("cases").size() >= 30);
    var decoder = Base64.getUrlDecoder();
    for (var chain : fixture.get("certificates")) {
      var factory = CertificateFactory.getInstance("X.509");
      var ca =
          factory.generateCertificate(
              new ByteArrayInputStream(decoder.decode(chain.get("ca_der").asString())));
      var leaf =
          factory.generateCertificate(
              new ByteArrayInputStream(decoder.decode(chain.get("leaf_der").asString())));
      leaf.verify(ca.getPublicKey());
      ca.verify(ca.getPublicKey());
    }
    for (var vector : fixture.get("signatures")) {
      var key =
          KeyFactory.getInstance("Ed25519")
              .generatePublic(
                  new X509EncodedKeySpec(decoder.decode(vector.get("public_key_spki").asString())));
      var verifier = Signature.getInstance("Ed25519");
      verifier.initVerify(key);
      verifier.update(
          (vector.get("domain").asString() + "\0" + vector.get("canonical").asString())
              .getBytes(StandardCharsets.UTF_8));
      assertEquals(
          vector.get("valid").asBoolean(),
          verifier.verify(decoder.decode(vector.get("signature").asString())),
          vector.get("id").asString());
      verifier.initVerify(key);
      verifier.update(
          ("Auriga.ServerEnvelope.v1\0" + vector.get("canonical").asString())
              .getBytes(StandardCharsets.UTF_8));
      assertFalse(verifier.verify(decoder.decode(vector.get("signature").asString())));
    }
  }
}
