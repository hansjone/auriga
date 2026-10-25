package auriga.collab.app;

import static org.junit.jupiter.api.Assertions.*;

import java.security.KeyPairGenerator;
import java.security.Signature;
import java.util.Base64;
import java.util.List;
import java.util.UUID;
import org.junit.jupiter.api.Test;
import tools.jackson.databind.node.ObjectNode;

class NetworkMailboxEnvelopeFactoryTest {
  record Fixture(
      NetworkSigningKeys keys,
      NetworkMailboxEnvelopeFactory factory,
      NetworkEnvelopeSigner.Context context,
      NetworkMailboxService.FetchItem item,
      ObjectNode message,
      ObjectNode source,
      ObjectNode contact)
      implements AutoCloseable {
    public void close() {
      keys.close();
    }
  }

  static Fixture fixture() throws Exception {
    var clock = NetworkEnvelopeSigningTest.CLOCK;
    var now = clock.instant();
    var keys = new NetworkSigningKeys(clock, 1);
    for (String purpose : List.of("business_delivery", "protocol_response")) {
      var pair = KeyPairGenerator.getInstance("Ed25519").generateKeyPair();
      keys.add(
          "2",
          purpose,
          purpose,
          pair.getPublic(),
          pair.getPrivate(),
          now.minusSeconds(1),
          now.plusSeconds(86400),
          NetworkSigningKeys.State.ACTIVE);
    }
    var vector =
        NetworkEnvelopeSigningTest.fixture("signature-vectors.json").path("vectors").get(0);
    var envelope = NetworkJson.parse(vector.path("canonical").asString());
    ObjectNode message = (ObjectNode) envelope.path("message").deepCopy();
    UUID messageId = UUID.randomUUID();
    message.put("message_id", messageId.toString());
    ObjectNode source = (ObjectNode) envelope.path("source_context").deepCopy();
    source.put("message_digest", NetworkJson.digest(message));
    var recipient = new NetworkMailboxService.RecipientView(UUID.randomUUID(), "0", "pending", 1);
    var item =
        new NetworkMailboxService.FetchItem(
            UUID.randomUUID(),
            1,
            messageId,
            "hello",
            "first_contact",
            "63",
            "418",
            null,
            now,
            List.of(recipient));
    var context =
        new NetworkEnvelopeSigner.Context(
            "2", "64", "inc-64-1", 42, "nonce-ready-0001", "req-fetch-0001");
    var contact =
        NetworkJson.object()
            .put("status", "pending")
            .put("decision_version", "1")
            .put("expires_at", now.plusSeconds(3600).toString());
    return new Fixture(
        keys,
        new NetworkMailboxEnvelopeFactory(new NetworkEnvelopeSigner(keys, clock)),
        context,
        item,
        message,
        source,
        contact);
  }

  @Test
  void pendingContactOnlyExposesBoundedApprovalPreview() throws Exception {
    try (var f = fixture()) {
      ((ObjectNode) f.message().path("body")).put("text", "汉".repeat(500));
      f.source().put("message_digest", NetworkJson.digest(f.message()));
      var prompt =
          f.factory()
              .delivery(
                  f.item(),
                  f.message(),
                  f.source(),
                  null,
                  f.contact(),
                  f.context(),
                  NetworkEnvelopeSigningTest.NOW.plusSeconds(300));
      assertEquals("contact_prompt", prompt.path("type").asString());
      assertEquals(
          280,
          prompt
              .path("preview")
              .asString()
              .codePointCount(0, prompt.path("preview").asString().length()));
      assertFalse(prompt.has("message"));
      assertEquals("business_delivery", prompt.path("server_auth").path("purpose").asString());
    }
  }

  @Test
  void pageSignatureBindsManifestAndGapDoesNotCarryBody() throws Exception {
    try (var f = fixture()) {
      var gap =
          f.factory()
              .gap(
                  1,
                  "awaiting_authorization",
                  f.context(),
                  NetworkEnvelopeSigningTest.NOW.plusSeconds(300));
      var page =
          f.factory()
              .page(
                  List.of(gap),
                  new NetworkMailboxService.NodeRoute("2", "64", 42, 10, 0),
                  1,
                  f.context(),
                  NetworkEnvelopeSigningTest.NOW.plusSeconds(300));
      var header = (ObjectNode) page.path("page");
      assertEquals("fetch_page", header.path("type").asString());
      assertEquals(
          NetworkJson.digest(gap), header.path("manifest").get(0).path("digest").asString());
      assertTrue(header.path("manifest").get(0).path("mail_seq").isNull());
      assertFalse(gap.has("message"));
      var verifier = Signature.getInstance("Ed25519");
      verifier.initVerify(f.keys().publicKey("protocol_response"));
      verifier.update(NetworkEnvelopeSigner.signingInput(header));
      byte[] signature =
          Base64.getUrlDecoder().decode(header.path("server_auth").path("signature").asString());
      assertTrue(verifier.verify(signature));
      header.put("scan_after", "2");
      verifier.initVerify(f.keys().publicKey("protocol_response"));
      verifier.update(NetworkEnvelopeSigner.signingInput(header));
      assertFalse(verifier.verify(signature));
    }
  }

  @Test
  void fullFrameBudgetIncludesPageAndItemSignatures() throws Exception {
    try (var f = fixture()) {
      f.contact().put("status", "allowed");
      ((ObjectNode) f.message().path("ext")).put("com.example.padding", "x".repeat(3000));
      f.source().put("message_digest", NetworkJson.digest(f.message()));
      var item =
          f.factory()
              .delivery(
                  f.item(),
                  f.message(),
                  f.source(),
                  null,
                  f.contact(),
                  f.context(),
                  NetworkEnvelopeSigningTest.NOW.plusSeconds(300));
      var route = new NetworkMailboxService.NodeRoute("2", "64", 42, 1, 0);
      var full =
          f.factory()
              .page(
                  List.of(item),
                  route,
                  1,
                  f.context(),
                  NetworkEnvelopeSigningTest.NOW.plusSeconds(300));
      assertTrue(
          NetworkJson.canonical(full).getBytes(java.nio.charset.StandardCharsets.UTF_8).length
              > 4096);
      var gap =
          f.factory()
              .gap(1, "quarantined", f.context(), NetworkEnvelopeSigningTest.NOW.plusSeconds(300));
      var bounded =
          f.factory()
              .page(
                  List.of(gap),
                  route,
                  1,
                  f.context(),
                  NetworkEnvelopeSigningTest.NOW.plusSeconds(300));
      assertTrue(
          NetworkJson.canonical(bounded).getBytes(java.nio.charset.StandardCharsets.UTF_8).length
              <= 4096);
    }
  }
}
