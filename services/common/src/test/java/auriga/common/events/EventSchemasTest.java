package auriga.common.events;

import static org.junit.jupiter.api.Assertions.*;

import java.util.List;
import java.util.Map;
import java.util.UUID;
import org.junit.jupiter.api.Test;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.json.JsonMapper;
import tools.jackson.databind.node.ObjectNode;

class EventSchemasTest {
  private static final JsonMapper JSON = JsonMapper.builder().build();
  private final EventSchemas schemas = new EventSchemas();

  static Map<String, Object> userCreated() {
    return Map.of(
        "member",
        Map.of(
            "userId", UUID.randomUUID().toString(),
            "displayName", "Alice",
            "email", "alice@example.com",
            "memberType", "member",
            "status", "active",
            "groupIds", List.of(),
            "roleKeys", List.of("member")),
        "via",
        "admin",
        "actorId",
        UUID.randomUUID().toString());
  }

  static ObjectNode envelope(String type, Object data) {
    ObjectNode e = JSON.createObjectNode();
    e.put("specversion", "1.0");
    e.put("id", UUID.randomUUID().toString());
    e.put("source", "auriga/org");
    e.put("type", type);
    e.put("time", "2026-10-19T08:00:00Z");
    e.put("subject", "s1");
    e.put("datacontenttype", "application/json");
    e.put("dataversion", "1");
    e.put("tenantid", UUID.randomUUID().toString());
    e.set("data", JSON.valueToTree(data));
    return e;
  }

  @Test
  void typeMapsToSchemaDefinition() {
    assertEquals(
        "classpath:auriga/contracts/events/user.schema.json#/$defs/UserPermissionsChanged",
        EventSchemas.dataSchemaLocation("user.permissions_changed"));
    assertEquals(
        "classpath:auriga/contracts/events/audit.schema.json#/$defs/AuditRecorded",
        EventSchemas.dataSchemaLocation("audit.recorded"));
  }

  @Test
  void acceptsContractConformingEvent() {
    assertDoesNotThrow(() -> schemas.validate(envelope("user.created", userCreated())));
  }

  @Test
  void producerSideIsStrict() {
    var data = new java.util.HashMap<>(userCreated());
    data.put("unexpected", true);
    assertThrows(
        InvalidEventException.class, () -> schemas.validate(envelope("user.created", data)));
    var missing = new java.util.HashMap<>(userCreated());
    missing.remove("via");
    assertThrows(
        InvalidEventException.class, () -> schemas.validate(envelope("user.created", missing)));
    ObjectNode badId = envelope("user.created", userCreated());
    badId.put("tenantid", "not-a-uuid");
    assertThrows(InvalidEventException.class, () -> schemas.validate(badId));
    ObjectNode extraAttribute = envelope("user.created", userCreated());
    extraAttribute.put("foo", "bar");
    assertThrows(InvalidEventException.class, () -> schemas.validate(extraAttribute));
  }

  @Test
  void unknownTypeIsRejected() {
    assertThrows(
        InvalidEventException.class, () -> schemas.validate(envelope("nosuch.thing", Map.of())));
  }

  @Test
  void consumerSideIsLenient() {
    ObjectNode e = envelope("user.created", userCreated());
    e.put("futureattribute", "x");
    ((ObjectNode) e.get("data")).put("futureField", 1);
    ReceivedEvent received = ReceivedEvent.parse(JSON.writeValueAsBytes(e));
    assertEquals("user.created", received.type());
    record Partial(String via) {}
    assertEquals("admin", received.dataAs(Partial.class).via());
    JsonNode node = received.data();
    assertEquals(1, node.get("futureField").asInt());
  }

  @Test
  void consumerRejectsMalformedEnvelope() {
    assertThrows(IllegalArgumentException.class, () -> ReceivedEvent.parse("{}".getBytes()));
    assertThrows(IllegalArgumentException.class, () -> ReceivedEvent.parse("nope".getBytes()));
  }
}
