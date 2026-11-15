package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;

import auriga.contracts.msgnet.model.*;
import java.nio.file.*;
import java.time.Instant;
import java.util.*;
import org.junit.jupiter.api.*;
import tools.jackson.databind.JsonNode;

class MessageNetworkK4VectorsTest {
  @Test
  void publicRelayPreservesAbsentSelectorsRootDigestAndExplicitLedgerNull() throws Exception {
    var fixture =
        Json.MAPPER.readTree(
            Files.readString(
                Path.of("../../../contracts/network/fixtures/auto-renewal-v2-vectors.json")));
    JsonNode evidence = null, root = null, mapping = null;
    for (var v : fixture.path("boundary_cases")) {
      if (v.path("id").asText().equals("k4-isolated-no-selector")) {
        evidence = v.path("input").path("evidence");
        root = v.path("input").path("control");
      }
      if (v.path("id").asText().equals("k4-ledger-complete"))
        mapping = v.path("input").path("response");
    }
    assertNotNull(evidence);
    assertNotNull(root);
    assertNotNull(mapping);
    var tls = javax.net.ssl.SSLContext.getInstance("TLS");
    tls.init(null, null, null);
    try (var ledger =
        new RemoteMessageNetworkLedger(
            java.net.URI.create("https://ledger.example.com"), tls, () -> "unused")) {
      var signed = Json.MAPPER.treeToValue(evidence, SignedTransportConfirmation.class);
      var raw = InstallationJson.parse(ledger.canonicalPublic(signed));
      assertEquals(evidence, raw);
      assertFalse(raw.path("payload").path("readers").get(0).has("selector"));
      var invalid = raw.deepCopy();
      ((tools.jackson.databind.node.ObjectNode) invalid.path("payload").path("readers").get(0))
          .putNull("selector");
      assertThrows(
          RuntimeException.class,
          () ->
              new ContractSchemas()
                  .validate(
                      "network/auto-renewal-v2.schema.json#/$defs/SignedTransportConfirmation",
                      invalid));
      var control = Json.MAPPER.treeToValue(root, SignedRootControl.class);
      var packet =
          new ControlPackage()
              .packageId("public-control")
              .tenantId(control.getPayload().getTenantId())
              .realm(control.getPayload().getRealm())
              .targetId("node-target")
              .control(control)
              .controlDigest(MessageNetworkSelectors.digest(root))
              .challenge("A".repeat(43));
      assertEquals(root, InstallationJson.parse(ledger.canonicalControl(packet)).path("control"));
      assertEquals(MessageNetworkSelectors.digest(root), MessageNetworkSelectors.digest(control));
      var snapshot = Json.MAPPER.treeToValue(mapping, ExpectedSelectors.class);
      snapshot.getExpectedSelectors().replaceAll((key, value) -> null);
      var serialized =
          InstallationJson.parse(ledger.canonicalPublic(snapshot)).path("expected_selectors");
      for (var key : snapshot.getExpectedSelectors().keySet()) {
        assertTrue(serialized.has(key));
        assertTrue(serialized.path(key).isNull());
      }
    }
  }

  @TestFactory
  List<DynamicTest> independentLedgerAndIsolatedReadersUseFrozenK4Vectors() throws Exception {
    var fixture =
        Json.MAPPER.readTree(
            Files.readString(
                Path.of("../../../contracts/network/fixtures/auto-renewal-v2-vectors.json")));
    var tests = new ArrayList<DynamicTest>();
    for (var v : fixture.path("boundary_cases")) {
      if (!v.path("id").asText().startsWith("k4-")) continue;
      tests.add(
          DynamicTest.dynamicTest(
              v.path("id").asText(),
              () -> {
                String verdict = "accept";
                try {
                  evaluate(v);
                } catch (IllegalStateException failure) {
                  verdict = failure.getMessage();
                }
                assertEquals(v.path("expected").asText(), verdict);
              }));
    }
    assertEquals(49, tests.size());
    return tests;
  }

  private static void evaluate(JsonNode vector) {
    var i = vector.path("input");
    var bundle = Json.MAPPER.treeToValue(i.path("bundle"), SignedTransportTrustBundle.class);
    if (vector.path("operation").asText().equals("expected_selectors")) {
      if (!i.path("authenticated_c_ledger").asBoolean())
        throw new IllegalStateException("scope_denied");
      var control = Json.MAPPER.treeToValue(i.path("control"), SignedRootControl.class);
      if (!Objects.equals(i.path("tenant_id").asText(), control.getPayload().getTenantId())
          || !Objects.equals(i.path("realm").asText(), control.getPayload().getRealm()))
        throw new IllegalStateException("scope_denied");
      MessageNetworkSelectors.verify(
          Json.MAPPER.treeToValue(i.path("response"), ExpectedSelectors.class),
          Json.MAPPER.treeToValue(i.path("query"), ExpectedSelectorsQuery.class),
          bundle,
          i.path("tenant_id").asText(),
          i.path("realm").asText(),
          control.getPayload().getControlRevision(),
          MessageNetworkSelectors.digest(i.path("control")),
          Instant.parse(i.path("now").asText()));
    } else {
      // The fixture's independent inputs exercise IAM's production READY check.
      var input = Json.MAPPER.createObjectNode();
      input.set("expected_selectors", i.path("expected_selectors"));
      if (i.has("expected_identities"))
        input.set("reader_identities", i.path("expected_identities"));
      var snapshot = Json.MAPPER.treeToValue(input, ExpectedSelectors.class);
      var evidence = Json.MAPPER.treeToValue(i.path("evidence"), SignedTransportConfirmation.class);
      MessageNetworkSelectors.readers(evidence.getPayload().getReaders(), snapshot, bundle);
    }
  }
}
