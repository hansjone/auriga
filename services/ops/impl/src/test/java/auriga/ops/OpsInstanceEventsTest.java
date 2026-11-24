package auriga.ops;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.context.TenantContext;
import auriga.common.events.ReceivedEvent;
import java.util.*;
import org.junit.jupiter.api.Test;
import tools.jackson.databind.json.JsonMapper;

class OpsInstanceEventsTest {
  private static final JsonMapper JSON = JsonMapper.builder().build();

  private static tools.jackson.databind.node.ObjectNode workspaceEventData(
      UUID instance, String type) {
    var data = JSON.createObjectNode();
    var ref = data.putObject("instance");
    ref.put("instanceId", instance.toString());
    ref.put("ownerId", UUID.randomUUID().toString());
    ref.putNull("nodeId");
    ref.put("generation", 1L);
    switch (type) {
      case "instance.failed" -> data.put("code", "START_FAILED");
      case "instance.started" -> data.put("reason", "user");
      case "instance.deleted" -> {
        data.put("dataPurged", false);
        data.putNull("actorId");
      }
      default -> {}
    }
    return data;
  }

  @Test
  void mapsRealInstanceEventsAndRejectsTenantMismatch() {
    var calls = new ArrayList<Boolean>();
    UUID tenant = UUID.randomUUID();
    UUID instance = UUID.randomUUID();
    var handler =
        new OpsInstanceEvents(
            (t, s, id, level, active) -> {
              assertEquals(tenant, t);
              assertEquals("instance_failed", s);
              assertEquals(instance.toString(), id);
              assertEquals("critical", level);
              calls.add(active);
            });
    for (String type : List.of("instance.failed", "instance.started", "instance.deleted")) {
      var e =
          new ReceivedEvent(
              UUID.randomUUID(),
              type,
              "auriga/workspace",
              instance.toString(),
              java.time.Instant.now(),
              tenant,
              "1",
              null,
              workspaceEventData(instance, type));
      TenantContext.run(tenant, () -> handler.handle(e));
    }
    assertEquals(List.of(true, false, false), calls);
    var e =
        new ReceivedEvent(
            UUID.randomUUID(),
            "instance.failed",
            "auriga/workspace",
            instance.toString(),
            java.time.Instant.now(),
            UUID.randomUUID(),
            "1",
            null,
            workspaceEventData(instance, "instance.failed"));
    assertThrows(RuntimeException.class, () -> TenantContext.run(tenant, () -> handler.handle(e)));
  }

  @Test
  void fallsBackToFlatInstanceIdAndEnvelopeSubject() {
    var subjects = new ArrayList<String>();
    UUID tenant = UUID.randomUUID();
    UUID flatInstance = UUID.randomUUID();
    UUID subjectInstance = UUID.randomUUID();
    var handler = new OpsInstanceEvents((t, s, id, level, active) -> subjects.add(id));

    var flatEvent =
        new ReceivedEvent(
            UUID.randomUUID(),
            "instance.failed",
            "auriga/workspace",
            null,
            java.time.Instant.now(),
            tenant,
            "1",
            null,
            JSON.createObjectNode().put("instanceId", flatInstance.toString()));
    var subjectOnlyEvent =
        new ReceivedEvent(
            UUID.randomUUID(),
            "instance.failed",
            "auriga/workspace",
            subjectInstance.toString(),
            java.time.Instant.now(),
            tenant,
            "1",
            null,
            JSON.createObjectNode().put("code", "START_FAILED"));

    TenantContext.run(
        tenant,
        () -> {
          handler.handle(flatEvent);
          handler.handle(subjectOnlyEvent);
        });
    assertEquals(List.of(flatInstance.toString(), subjectInstance.toString()), subjects);
  }

  @Test
  void ignoresMissingOrInvalidInstanceIdWithoutThrowing() {
    var calls = new ArrayList<String>();
    UUID tenant = UUID.randomUUID();
    var handler = new OpsInstanceEvents((t, s, id, level, active) -> calls.add(id));

    var invalidNested = JSON.createObjectNode();
    invalidNested.putObject("instance").put("instanceId", "not-a-uuid");
    var nonStringNested = JSON.createObjectNode();
    nonStringNested.putObject("instance").put("instanceId", 12345);
    var invalidFlat = JSON.createObjectNode().put("instanceId", "bad-flat-uuid");
    var empty = JSON.createObjectNode().put("code", "START_FAILED");

    var events =
        List.of(
            new ReceivedEvent(
                UUID.randomUUID(),
                "instance.failed",
                "auriga/workspace",
                null,
                java.time.Instant.now(),
                tenant,
                "1",
                null,
                empty),
            new ReceivedEvent(
                UUID.randomUUID(),
                "instance.failed",
                "auriga/workspace",
                "   ",
                java.time.Instant.now(),
                tenant,
                "1",
                null,
                empty),
            new ReceivedEvent(
                UUID.randomUUID(),
                "instance.failed",
                "auriga/workspace",
                "invalid-subject",
                java.time.Instant.now(),
                tenant,
                "1",
                null,
                empty),
            new ReceivedEvent(
                UUID.randomUUID(),
                "instance.failed",
                "auriga/workspace",
                UUID.randomUUID().toString(),
                java.time.Instant.now(),
                tenant,
                "1",
                null,
                invalidNested),
            new ReceivedEvent(
                UUID.randomUUID(),
                "instance.failed",
                "auriga/workspace",
                UUID.randomUUID().toString(),
                java.time.Instant.now(),
                tenant,
                "1",
                null,
                nonStringNested),
            new ReceivedEvent(
                UUID.randomUUID(),
                "instance.failed",
                "auriga/workspace",
                UUID.randomUUID().toString(),
                java.time.Instant.now(),
                tenant,
                "1",
                null,
                invalidFlat));

    for (var event : events) {
      assertDoesNotThrow(() -> TenantContext.run(tenant, () -> handler.handle(event)));
    }
    assertTrue(calls.isEmpty());
  }

  @Test
  void ignoresEventsBeforePersistedActivation() {
    var since = java.time.Instant.now();
    var calls = new ArrayList<Boolean>();
    var handler = new OpsInstanceEvents((t, s, id, level, active) -> calls.add(active), since);
    var tenant = UUID.randomUUID();
    var instance = UUID.randomUUID();
    var data = workspaceEventData(instance, "instance.failed");
    for (var time : List.of(since.minusSeconds(1), since.plusSeconds(1))) {
      var e =
          new ReceivedEvent(
              UUID.randomUUID(),
              "instance.failed",
              "auriga/workspace",
              instance.toString(),
              time,
              tenant,
              "1",
              null,
              data);
      TenantContext.run(tenant, () -> handler.handle(e));
    }
    assertEquals(List.of(true), calls);
  }
}
