package auriga.workspace.app;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.observability.JsonLogFormatter;
import auriga.contracts.node.v1.InstanceSpec;
import ch.qos.logback.classic.Logger;
import ch.qos.logback.classic.spi.ILoggingEvent;
import ch.qos.logback.core.read.ListAppender;
import java.util.Map;
import org.junit.jupiter.api.Test;
import org.slf4j.LoggerFactory;
import org.slf4j.MDC;
import tools.jackson.databind.json.JsonMapper;

class WorkspaceStartupTraceTest {
  private static final String PARENT = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01";

  @Test
  void realDispatchLogMatchesMessageChildAndLeavesBusinessBytesUnchanged() {
    var spec = InstanceSpec.newBuilder().setInstanceId("instance-a").setGeneration(7).build();
    byte[] before = spec.toByteArray();
    var logger = (Logger) LoggerFactory.getLogger(WorkspaceStartupTrace.class);
    var events =
        new ListAppender<ILoggingEvent>() {
          @Override
          protected void append(ILoggingEvent event) {
            event.prepareForDeferredProcessing();
            super.append(event);
          }
        };
    events.start();
    logger.addAppender(events);
    try {
      var message = WorkspaceStartupTrace.dispatch(Map.of("startup_traceparent", PARENT), spec);
      assertArrayEquals(before, message.getSpec().toByteArray());
      assertEquals(PARENT.substring(3, 35), message.getTraceparent().substring(3, 35));
      assertNotEquals(PARENT.substring(36, 52), message.getTraceparent().substring(36, 52));
      var json =
          JsonMapper.builder()
              .build()
              .readTree(new JsonLogFormatter().format(events.list.getFirst()));
      assertEquals(PARENT.substring(3, 35), json.get("trace_id").asString());
      assertEquals(message.getTraceparent().substring(36, 52), json.get("spanId").asString());
      assertEquals(
          PARENT.substring(36, 52), json.get("mdc_context").get("parent_span_id").asString());
      assertEquals("instance-a", json.get("mdc_context").get("instance_id").asString());
      assertNull(MDC.get("traceId"));
    } finally {
      logger.detachAppender(events);
      events.stop();
    }
  }

  @Test
  void malformedOrMissingObservationNeverRejectsBusinessSpec() {
    var spec = InstanceSpec.newBuilder().setInstanceId("instance-a").setGeneration(7).build();
    for (String invalid :
        new String[] {
          "",
          PARENT + "," + PARENT,
          "x".repeat(10000),
          "00-" + "0".repeat(32) + "-00f067aa0ba902b7-01"
        }) {
      var message = WorkspaceStartupTrace.dispatch(Map.of("startup_traceparent", invalid), spec);
      assertEquals(spec, message.getSpec());
      assertTrue(message.getTraceparent().isEmpty());
    }
    assertTrue(WorkspaceStartupTrace.dispatch(Map.of(), spec).getTraceparent().isEmpty());
  }
}
