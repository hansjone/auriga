package io.github.hansjone.auriga.common.context;

import static org.junit.jupiter.api.Assertions.*;

import java.util.Optional;
import java.util.UUID;
import java.util.concurrent.Executors;
import java.util.concurrent.atomic.AtomicReference;
import org.junit.jupiter.api.Test;

class ContextTest {
  private static final UUID A = UUID.randomUUID();
  private static final UUID B = UUID.randomUUID();

  @Test
  void requireWithoutTenantFails() {
    assertTrue(TenantContext.current().isEmpty());
    assertThrows(MissingTenantException.class, TenantContext::require);
  }

  @Test
  void bindingIsScopedAndNests() {
    TenantContext.run(
        A,
        () -> {
          assertEquals(A, TenantContext.require());
          TenantContext.run(B, () -> assertEquals(B, TenantContext.require()));
          assertEquals(A, TenantContext.require());
        });
    assertTrue(TenantContext.current().isEmpty());
  }

  @Test
  void systemScopeHasNoTenant() {
    TenantContext.run(
        A,
        () ->
            TenantContext.runAsSystem(
                () -> {
                  assertTrue(TenantContext.isSystem());
                  assertTrue(TenantContext.current().isEmpty());
                  assertThrows(MissingTenantException.class, TenantContext::require);
                }));
  }

  @Test
  void otherThreadsDoNotInheritTheTenant() throws Exception {
    AtomicReference<Optional<UUID>> seen = new AtomicReference<>();
    TenantContext.call(
        A,
        () -> {
          try (var executor = Executors.newVirtualThreadPerTaskExecutor()) {
            executor.submit(() -> seen.set(TenantContext.current())).get();
          }
          Thread t = new Thread(() -> seen.set(TenantContext.current()));
          t.start();
          t.join();
          return null;
        });
    assertEquals(Optional.empty(), seen.get());
  }

  @Test
  void traceparentParsingAndPropagation() {
    String header = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01";
    TraceContext parsed = TraceContext.parse(header).orElseThrow();
    assertEquals("4bf92f3577b34da6a3ce929d0e0e4736", parsed.traceId());
    assertEquals(header, parsed.toTraceparent());
    TraceContext child = TraceContext.continueOrStart(header);
    assertEquals(parsed.traceId(), child.traceId());
    assertNotEquals(parsed.spanId(), child.spanId());
    for (String bad :
        new String[] {
          null,
          "",
          "00-00000000000000000000000000000000-00f067aa0ba902b7-01",
          "00-4bf92f3577b34da6a3ce929d0e0e4736-0000000000000000-01",
          "ff-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
          "00-4BF92F3577B34DA6A3CE929D0E0E4736-00f067aa0ba902b7-01",
          "garbage"
        }) {
      assertTrue(TraceContext.parse(bad).isEmpty(), String.valueOf(bad));
    }
    assertTrue(
        TraceContext.continueOrStart("garbage")
            .toTraceparent()
            .matches("00-[0-9a-f]{32}-[0-9a-f]{16}-01"));
  }

  @Test
  void requestIdIsSanitized() {
    assertEquals("req-1", RequestContext.sanitizeRequestId("req-1"));
    String generated = RequestContext.sanitizeRequestId("bad\nid");
    assertDoesNotThrow(() -> UUID.fromString(generated));
    assertDoesNotThrow(() -> UUID.fromString(RequestContext.sanitizeRequestId("x".repeat(200))));
    assertDoesNotThrow(() -> UUID.fromString(RequestContext.sanitizeRequestId(null)));
  }
}
