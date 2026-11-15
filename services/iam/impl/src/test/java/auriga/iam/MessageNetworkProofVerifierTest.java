package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;

import java.security.KeyPairGenerator;
import java.util.*;
import org.junit.jupiter.api.*;

class MessageNetworkProofVerifierTest {
  @Test
  void authenticatesTheExactRootAuthorityBundleAndConfirmation() throws Exception {
    var f = new MessageNetworkProofFixture();
    assertEquals(f.tenant.toString(), f.verify().getPayload().getTenantId());
    var other = KeyPairGenerator.getInstance("Ed25519").generateKeyPair();
    var wrongRoot =
        new MessageNetworkProofVerifier(
            MessageNetworkProofFixture.spki(other), f.rootKid(), f.clock);
    assertThrows(
        RuntimeException.class,
        () ->
            wrongRoot.verify(
                f.transport(),
                f.history(),
                MessageNetworkProofFixture.bytes(f.control),
                MessageNetworkProofFixture.bytes(f.evidence),
                f.record(),
                f.tenant.toString(),
                "1"));
  }

  interface Mutation {
    void apply(MessageNetworkProofFixture fixture) throws Exception;
  }

  @TestFactory
  List<DynamicTest> rejectsForgedPurposesRevocationAndUnauthorizedRuntimeKeys() {
    Map<String, Mutation> cases = new LinkedHashMap<>();
    cases.put(
        "root signature",
        f -> MessageNetworkProofFixture.payload(f.control).put("minimum_generation", "2"));
    cases.put(
        "root kid",
        f ->
            ((tools.jackson.databind.node.ObjectNode) f.control.path("root_auth"))
                .put("kid", "uninstalled-root"));
    cases.put(
        "authority signature",
        f -> MessageNetworkProofFixture.payload(f.authority).put("review_period_days", 366));
    cases.put(
        "bundle signature",
        f -> MessageNetworkProofFixture.payload(f.bundle).put("issued_at", "2026-11-13T00:00:00Z"));
    cases.put("confirmation signature", f -> f.row().put("identity_id", "forged-reader"));
    cases.put(
        "confirmation purpose",
        f ->
            f.sign(
                f.evidence,
                "runtime_auth",
                "Auriga.NetworkTransportTrustBundle.v2",
                f.runtimeKid(),
                f.runtime));
    cases.put(
        "bundle purpose",
        f ->
            f.sign(
                f.bundle,
                "runtime_auth",
                "Auriga.NetworkTransportConfirmation.v2",
                f.runtimeKid(),
                f.runtime));
    cases.put(
        "runtime kid",
        f ->
            ((tools.jackson.databind.node.ObjectNode) f.evidence.path("runtime_auth"))
                .put("kid", "other-runtime"));
    cases.put(
        "revoked authority",
        f -> {
          ((tools.jackson.databind.node.ArrayNode)
                  MessageNetworkProofFixture.payload(f.control).path("revoked_authority_ids"))
              .add(MessageNetworkProofFixture.payload(f.authority).path("authority_id").asText());
          f.refresh();
        });
    cases.put(
        "missing capability",
        f -> {
          MessageNetworkProofFixture.payload(f.authority)
              .set("capabilities", Json.MAPPER.createArrayNode().add("keyset"));
          f.refresh();
        });
    cases.put(
        "transport scope",
        f -> {
          ((tools.jackson.databind.node.ObjectNode)
                  MessageNetworkProofFixture.payload(f.authority).path("transport_scope").get(0))
              .put("identity_id", "other-identity");
          f.refresh();
        });
    cases.put(
        "not allowed",
        f -> {
          MessageNetworkProofFixture.payload(f.control)
              .set("allowed_authorities", Json.MAPPER.createArrayNode());
          f.signRoot();
        });
    cases.put(
        "revoked reader",
        f -> {
          ((tools.jackson.databind.node.ArrayNode)
                  MessageNetworkProofFixture.payload(f.control).path("revoked_installation_ids"))
              .add(f.identityId);
          f.refresh();
        });
    cases.put(
        "future issuer",
        f -> {
          MessageNetworkProofFixture.payload(f.authority)
              .put("not_before", f.clock.instant().plusSeconds(60).toString());
          f.refresh();
        });
    return cases.entrySet().stream()
        .map(
            entry ->
                DynamicTest.dynamicTest(
                    entry.getKey(),
                    () -> {
                      var fixture = new MessageNetworkProofFixture();
                      entry.getValue().apply(fixture);
                      assertThrows(RuntimeException.class, fixture::verify);
                    }))
        .toList();
  }
}
