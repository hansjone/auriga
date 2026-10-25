package auriga.collab.app;

import static org.junit.jupiter.api.Assertions.*;

import auriga.collab.api.NetworkSourceAuthority.Dispatch;
import auriga.collab.api.NetworkSourceAuthority.Source;
import auriga.collab.app.NetworkMailboxService.StoreRequest;
import auriga.common.error.AurigaException;
import java.time.Instant;
import java.util.Map;
import java.util.UUID;
import org.junit.jupiter.api.Test;

class NetworkSourceStampTest {
  static final Instant NOW = Instant.parse("2026-10-25T00:00:00Z");
  static final UUID TENANT = UUID.randomUUID();
  static final UUID MESSAGE = UUID.randomUUID();

  static Source source(String label) {
    return new Source(
        "2",
        "10",
        "1",
        "session",
        "agent",
        "user-alice",
        label,
        "7",
        NOW,
        "binding-test",
        "3",
        "8",
        NOW.plusSeconds(600));
  }

  static StoreRequest request(String realm, String node, String local) {
    return StoreRequest.unicast(TENANT, realm, node, local, "hello", "20", "3");
  }

  @Test
  void stampHasTrustedIdentityCanonicalDigestAndStableMessage() {
    var message =
        NetworkSourceStamp.message(
            request("2", "10", "1"), source("协作会话"), MESSAGE, NOW, null, null);
    var stamp = NetworkSourceStamp.context(source("协作会话"), message);
    assertEquals("agent", message.path("sender_kind").asString());
    assertEquals("10", message.path("from").path("node").asString());
    assertEquals(message.path("from"), message.path("reply_to"));
    assertEquals("user-alice", stamp.path("user").asString());
    assertEquals("7", stamp.path("identity_revision").asString());
    assertEquals(NetworkJson.digest(message), stamp.path("message_digest").asString());
    assertEquals("2026-10-26T00:00:00Z", message.path("expires_at").asString());
  }

  @Test
  void forgedRealmNodeLocalAndEveryReservedIdentityFieldAreRejected() {
    for (var request :
        new StoreRequest[] {
          request("3", "10", "1"),
          request("2", "0", "1"),
          request("2", "99", "1"),
          request("2", "10", "99")
        }) {
      error(
          "network.sender_mismatch",
          () -> NetworkSourceStamp.message(request, source("协作会话"), MESSAGE, NOW, null, null));
    }
    for (String field :
        new String[] {
          "from",
          "realm",
          "node",
          "sender_kind",
          "reply_to",
          "source_context",
          "server_auth",
          "authorization_ref"
        }) {
      error(
          "network.sender_mismatch",
          () ->
              NetworkSourceStamp.message(
                  request("2", "10", "1"),
                  source("协作会话"),
                  MESSAGE,
                  NOW,
                  "{\"" + field + "\":\"forged\"}",
                  null));
    }
  }

  @Test
  void unknownOptionalValuesArePreservedAndCoveredByDigest() {
    var message =
        NetworkSourceStamp.message(
            request("2", "10", "1"),
            source("协作会话"),
            MESSAGE,
            NOW,
            "{\"future_optional\":{\"signature\":\"data\"},\"ext\":{\"com.example.trace\":{\"x\":42},\"e2e_signature\":null}}",
            null);
    String original = NetworkJson.digest(message);
    assertEquals("data", message.path("future_optional").path("signature").asString());
    assertEquals(42, message.path("ext").path("com.example.trace").path("x").asInt());
    ((tools.jackson.databind.node.ObjectNode) message.path("future_optional"))
        .put("signature", "changed");
    assertNotEquals(original, NetworkJson.digest(message));
  }

  @Test
  void reservedLabelsAreNeutralizedWithoutNormalizingSignedStrings() {
    for (String label : new String[] {"总部", "管\u200b理员", "ＳＹＳＴＥＭ", "Admin\u202eistrator"}) {
      var message =
          NetworkSourceStamp.message(
              request("2", "10", "1"), source(label), MESSAGE, NOW, null, null);
      assertEquals(
          "协作来源",
          NetworkSourceStamp.context(source(label), message).path("display_label").asString());
    }
  }

  @Test
  void groupSourceAndExpiredOrMissingProofAreRejected() {
    var group =
        new Source(
            "2",
            "10",
            "1",
            "group",
            "agent",
            "user-alice",
            "label",
            "1",
            NOW,
            "binding",
            "1",
            "1",
            NOW.plusSeconds(1));
    error(
        "network.sender_mismatch",
        () -> NetworkSourceStamp.message(request("2", "10", "1"), group, MESSAGE, NOW, null, null));
    error(
        "network.sender_mismatch",
        () ->
            NetworkSourceStamp.message(
                request("2", "10", "1"),
                source("label"),
                MESSAGE,
                NOW.plusSeconds(601),
                null,
                null));
    error(
        "network.sender_mismatch",
        () -> NetworkSourceStamp.message(request("2", "10", "1"), null, MESSAGE, NOW, null, null));
    error(
        "network.protocol_error",
        () ->
            NetworkSourceStamp.message(
                request("2", "10", "1"), source("label"), MESSAGE, NOW, "[]", null));
  }

  @Test
  void broadcastClassAndGrantComeFromVerifiedSnapshot() {
    var dispatch =
        new Dispatch(
            "{\"version\":1,\"realm\":\"2\",\"node\":\"0\",\"local\":\"1\",\"type\":\"group\"}",
            "snapshot",
            Map.of("20.0", "membership"),
            Map.of("20.0", "system"),
            "grant",
            "4",
            "agent");
    var request = StoreRequest.unicast(TENANT, "2", "10", "1", "hello", "20", "0");
    var message =
        NetworkSourceStamp.message(request, source("label"), MESSAGE, NOW, null, dispatch);
    assertEquals("broadcast", message.path("message_class").asString());
    assertEquals("grant", message.path("authorization_ref").path("broadcast_grant_id").asString());
  }

  static void error(String code, Runnable action) {
    assertEquals(code, assertThrows(AurigaException.class, action::run).errorCode().code());
  }
}
