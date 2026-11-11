package auriga.ops;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.context.TenantContext;
import auriga.common.events.ReceivedEvent;
import java.util.*;
import org.junit.jupiter.api.Test;
import tools.jackson.databind.json.JsonMapper;

class OpsInstanceEventsTest {
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
              calls.add(active);
            });
    var data =
        JsonMapper.builder().build().createObjectNode().put("instanceId", instance.toString());
    for (String type : List.of("instance.failed", "instance.started", "instance.deleted")) {
      var e =
          new ReceivedEvent(
              UUID.randomUUID(),
              type,
              "workspace",
              instance.toString(),
              java.time.Instant.now(),
              tenant,
              "1",
              null,
              data);
      TenantContext.run(tenant, () -> handler.handle(e));
    }
    assertEquals(List.of(true, false, false), calls);
    var e =
        new ReceivedEvent(
            UUID.randomUUID(),
            "instance.failed",
            "workspace",
            instance.toString(),
            java.time.Instant.now(),
            UUID.randomUUID(),
            "1",
            null,
            data);
    assertThrows(RuntimeException.class, () -> TenantContext.run(tenant, () -> handler.handle(e)));
  }
}
