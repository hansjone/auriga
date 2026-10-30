package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.error.AurigaException;
import java.nio.charset.StandardCharsets;
import java.util.*;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.MethodSource;
import tools.jackson.databind.node.ObjectNode;

class NetworkDeliveryJsonTest {
  static ObjectNode message() {
    var from = address("session", "1", "1");
    return Json.object(
        "message_id",
        "message",
        "from",
        from,
        "reply_to",
        from,
        "to",
        address("system", "2", "0"),
        "thread_id",
        "thread",
        "thread_sequence",
        "1",
        "in_reply_to",
        null,
        "kind",
        "message",
        "durability",
        "durable",
        "flags",
        List.of(),
        "ttl",
        60,
        "ext",
        Map.of("e2e_signature", Map.of()),
        "body",
        Map.of("subtype", "text", "text", "你好"),
        "sender_kind",
        "agent",
        "message_class",
        "unicast",
        "intent",
        "information",
        "authorization_ref",
        Map.of(
            "source_binding",
            "binding",
            "source_revision",
            "1",
            "policy_revision",
            "1",
            "broadcast_grant_id",
            nullValue()),
        "created_at",
        "2026-10-25T00:00:00Z",
        "expires_at",
        "2026-10-25T00:01:00Z");
  }

  private static Object nullValue() {
    return Json.MAPPER.nullNode();
  }

  static ObjectNode address(String type, String node, String local) {
    return Json.object("version", 1, "realm", "2", "type", type, "node", node, "local", local);
  }

  static ObjectNode context(ObjectNode message) {
    return Json.object(
        "realm",
        "2",
        "node",
        "1",
        "user",
        "owner",
        "sender_kind",
        "agent",
        "display_label",
        "Alice",
        "observed_at",
        "2026-10-25T00:00:00Z",
        "message_digest",
        IamNetworkDeliveryJson.digest(message));
  }

  static void invalid(Runnable work) {
    assertEquals(
        "common.validation_failed",
        assertThrows(AurigaException.class, work::run).errorCode().code());
  }

  @Test
  void fullUnknownFieldsAreRetainedAndCoveredByJcs() {
    var m = message();
    m.set("future", Json.object("😀", "\n", "\ue000", 3, "a", "é"));
    ((ObjectNode) m.path("from")).put("future.address", "retained");
    var c = context(m);
    assertEquals(m, IamNetworkDeliveryJson.parse(m.toString().getBytes(StandardCharsets.UTF_8)));
    IamNetworkDeliveryJson.validate(m, c);
    assertEquals(
        "{\"a\":\"é\",\"😀\":\"\\n\",\"\ue000\":3}",
        IamNetworkDeliveryJson.canonical(m.path("future")));
    assertEquals(
        "sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a",
        IamNetworkDeliveryJson.digest(Json.object()));
    m.put("another", true);
    invalid(() -> IamNetworkDeliveryJson.validate(m, c));
  }

  static List<byte[]> badRaw() {
    var inputs = new ArrayList<byte[]>();
    for (String raw :
        List.of(
            "{\"x\":1,\"x\":2}",
            "{} trailing",
            "{}{}",
            "{\"x\":\"\\ud800\"}",
            "{\"x\":\"\\udfff\"}",
            "{\"x\":\"\n\"}",
            "{\"x\":1.0}",
            "{\"x\":1e0}",
            "{\"x\":NaN}",
            "{\"x\":9007199254740992}",
            "{\"x\":-9007199254740992}",
            "[".repeat(16) + "0" + "]".repeat(16),
            "[" + String.join(",", Collections.nCopies(257, "0")) + "]",
            "\"" + "a".repeat(16385) + "\"",
            "\"" + "界".repeat(5462) + "\"",
            "{"
                + String.join(
                    ",",
                    java.util.stream.IntStream.range(0, 129)
                        .mapToObj(i -> "\"k" + i + "\":0")
                        .toList())
                + "}",
            "["
                + String.join(
                    ",",
                    Collections.nCopies(
                        33, "[" + String.join(",", Collections.nCopies(256, "0")) + "]"))
                + "]",
            " ".repeat(65537))) inputs.add(raw.getBytes(StandardCharsets.UTF_8));
    inputs.add(new byte[] {'"', (byte) 0xc0, (byte) 0xaf, '"'});
    inputs.add(new byte[] {'"', (byte) 0xed, (byte) 0xa0, (byte) 0x80, '"'});
    return inputs;
  }

  @ParameterizedTest
  @MethodSource("badRaw")
  void boundedStrictRawParserRejectsEveryIllegalCategory(byte[] raw) {
    invalid(() -> IamNetworkDeliveryJson.parse(raw));
  }

  @Test
  void bothSchemasAndKnownSemanticRestrictionsAreEnforced() {
    for (String field : List.of("message_id", "body", "authorization_ref", "expires_at")) {
      var m = message();
      m.remove(field);
      invalid(() -> IamNetworkDeliveryJson.validate(m, context(m)));
    }
    for (String field :
        List.of("realm", "user", "message_digest", "display_label", "observed_at")) {
      var m = message();
      var c = context(m);
      c.remove(field);
      invalid(() -> IamNetworkDeliveryJson.validate(m, c));
    }
    for (String raw :
        List.of(
            "{\"kind\":\"unknown\"}",
            "{\"kind\":\"first_contact\"}",
            "{\"kind\":\"control\"}",
            "{\"intent\":\"control\"}",
            "{\"sender_kind\":\"human\"}",
            "{\"durability\":\"ephemeral\"}",
            "{\"reply_mode\":\"group\"}",
            "{\"authorization_ref\":{\"policy_revision\":\"0\",\"broadcast_grant_id\":null}}",
            "{\"reply_to\":{\"version\":1,\"realm\":\"2\",\"type\":\"session\",\"node\":\"1\",\"local\":\"2\"}}",
            "{\"expires_at\":\"2026-10-25T00:02:00Z\"}",
            "{\"expires_at\":\"2026-10-24T23:59:00Z\"}")) {
      var m = message();
      m.setAll((ObjectNode) Json.MAPPER.readTree(raw));
      invalid(() -> IamNetworkDeliveryJson.validate(m, context(m)));
    }
    for (String label : List.of("", "总部", "SYSTEM", "a\nb", "a".repeat(65))) {
      var m = message();
      var c = context(m);
      c.put("display_label", label);
      invalid(() -> IamNetworkDeliveryJson.validate(m, c));
    }
  }

  @Test
  void exactHardBoundariesAndLegalPrivateReplyAreAccepted() {
    assertEquals(
        Json.object(),
        IamNetworkDeliveryJson.parse(("{}" + " ".repeat(65534)).getBytes(StandardCharsets.UTF_8)));
    assertEquals(
        16384,
        IamNetworkDeliveryJson.parse(
                ("\"" + "a".repeat(16384) + "\"").getBytes(StandardCharsets.UTF_8))
            .asString()
            .length());
    IamNetworkDeliveryJson.parse(
        ("[".repeat(15) + "0" + "]".repeat(15)).getBytes(StandardCharsets.UTF_8));
    var m = message();
    m.put("kind", "reply")
        .put("reply_mode", "private")
        .put("in_reply_to", "original")
        .put("intent", "request");
    m.put("response_deadline", "2026-10-25T00:00:30Z");
    IamNetworkDeliveryJson.validate(m, context(m));
    m.put("response_deadline", "2026-10-25T00:01:01Z");
    invalid(() -> IamNetworkDeliveryJson.validate(m, context(m)));
    m.remove("response_deadline");
    m.set("body", Json.object("subtype", "text", "text", "a".repeat(16384)));
    invalid(() -> IamNetworkDeliveryJson.validate(m, context(m)));
  }
}
