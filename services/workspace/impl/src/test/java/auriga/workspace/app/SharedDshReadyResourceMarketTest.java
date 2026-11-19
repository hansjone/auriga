package auriga.workspace.app;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import auriga.contracts.node.v1.*;
import auriga.workspace.api.SharedDshResourceMarket;
import auriga.workspace.config.WorkspaceResourceMarketConfiguration;
import java.util.*;
import org.junit.jupiter.api.Test;
import org.springframework.boot.test.context.runner.ApplicationContextRunner;

class SharedDshReadyResourceMarketTest {
  static final class Fixture {
    final SharedDshResourceMarketQueriesTest.Fixture state =
        new SharedDshResourceMarketQueriesTest.Fixture();
    final SharedDshBinding binding;
    final SharedDshResourceView view;
    final SharedDshResourceReport report;
    final SharedDshEntryTransportFixture entry;
    final SharedDshResourceMarketQueries queries;
    boolean current = true;
    String placementState = "ready";
    long operations;

    Fixture() {
      this(false);
    }

    Fixture(boolean dedicated) {
      binding =
          state.binding.toBuilder()
              .setMode(
                  dedicated
                      ? DshInstanceMode.DSH_INSTANCE_MODE_DEDICATED
                      : DshInstanceMode.DSH_INSTANCE_MODE_SHARED)
              .build();
      view = state.view.toBuilder().setBinding(binding).build();
      report = state.report.toBuilder().setBinding(binding).build();
      state.row.put("mode", dedicated ? "dedicated" : "shared");
      state.row.put("payload", view.toByteArray());
      state.row.put("report", report.toByteArray());
      when(state.jdbc.queryForList(anyString(), eq(state.tenant), eq(state.placement)))
          .thenAnswer(
              call -> {
                String sql = call.getArgument(0);
                if (sql.contains("state='pending'"))
                  return placementState.equals("pending") ? List.of(state.row) : List.of();
                assertTrue(sql.contains("shared_dsh_pool_node"));
                assertTrue(sql.contains("NOT n.revoked"));
                assertTrue(sql.contains("x.state='ready' AND p.state='ready'"));
                return current && placementState.equals("ready") ? List.of(state.row) : List.of();
              });
      when(state.jdbc.queryForObject(
              anyString(), eq(Long.class), eq(state.tenant), eq(state.placement)))
          .thenAnswer(call -> operations);
      entry = new SharedDshEntryTransportFixture(state.store, state.clock, state.tenant);
      queries =
          new SharedDshResourceMarketQueries(
              state.store, state.authority, entry.transport, state.clock);
    }
  }

  @Test
  void realTransportObservesReadyResourcesWhileTheInitialAdmissionProbeStillRejectsReady() {
    var f = new Fixture();
    assertFalse(f.state.run(() -> f.entry.transport.ready(f.binding)));
    assertEquals(0, f.entry.requests);
    assertEquals(Optional.of(f.report), f.state.run(() -> f.queries.report(f.view)));
    assertEquals(1, f.entry.requests);
    var binding = f.entry.claims.path("binding");
    assertEquals(7, binding.size());
    auriga.workspace.domain.SharedDshEvents.binding(f.binding)
        .forEach(
            (name, value) -> {
              if (value instanceof Number number)
                assertEquals(number.longValue(), binding.path(name).asLong());
              else assertEquals(value, binding.path(name).asString());
            });
  }

  @Test
  void initialAdmissionStillRequiresPendingAndZeroOperationsAndNeverReplacesReadyObservation() {
    var f = new Fixture();
    f.placementState = "pending";
    assertTrue(f.state.run(() -> f.entry.transport.ready(f.binding)));
    assertEquals(1, f.entry.requests);
    assertFalse(f.state.run(() -> f.entry.transport.observed(f.binding)));
    f.operations = 1;
    assertFalse(f.state.run(() -> f.entry.transport.ready(f.binding)));
    assertEquals(1, f.entry.requests);
  }

  @Test
  void productionMarketConfigurationUsesTheTransportReadyPort() {
    var f = new Fixture();
    new ApplicationContextRunner()
        .withUserConfiguration(WorkspaceResourceMarketConfiguration.class)
        .withPropertyValues(
            "auriga.platform.combined=true", "auriga.workspace.shared-dsh.enabled=true")
        .withBean(auriga.workspace.persistence.WorkspaceStore.class, () -> f.state.store)
        .withBean(
            SharedDshPlacements.class,
            () ->
                new SharedDshPlacements(
                    f.state.store,
                    mock(auriga.workspace.api.SharedDshAuthority.Inputs.class),
                    f.entry.transport,
                    f.state.clock))
        .withBean(SharedDshEntryTransport.class, () -> f.entry.transport)
        .withBean(java.time.Clock.class, () -> f.state.clock)
        .run(
            context ->
                assertEquals(
                    Optional.of(f.report),
                    f.state.run(
                        () -> context.getBean(SharedDshResourceMarket.class).report(f.view))));
  }

  @Test
  void unsafeProfileRedirectOrChangedReceiptCannotBecomeApplied() {
    var f = new Fixture();
    f.entry.profile =
        body -> {
          ((tools.jackson.databind.node.ObjectNode) body.path("capabilities")).put("shell", true);
          return body;
        };
    assertTrue(f.state.run(() -> f.queries.report(f.view)).isEmpty());
    f.entry.profile = body -> body;
    for (var header : List.of("Location", "Set-Cookie")) {
      f.entry.headers =
          Map.of("Content-Type", List.of("application/json"), header, List.of("unexpected"));
      assertTrue(f.state.run(() -> f.queries.report(f.view)).isEmpty());
    }
    f.entry.headers = Map.of("Content-Type", List.of("application/json"));
    f.entry.status = 503;
    assertTrue(f.state.run(() -> f.queries.report(f.view)).isEmpty());
    f.entry.status = 200;
    f.entry.duringProbe = () -> f.state.row.put("report", null);
    assertTrue(f.state.run(() -> f.queries.report(f.view)).isEmpty());
    f.state.row.put("report", f.report.toByteArray());
    f.entry.duringProbe = () -> f.current = false;
    assertTrue(f.state.run(() -> f.queries.report(f.view)).isEmpty());
  }

  @Test
  void eachObservationRechecksLiveExternalProfileAvailability() {
    var f = new Fixture();
    var available = new java.util.concurrent.atomic.AtomicBoolean(true);
    f.entry.responseStatus = () -> available.get() ? 200 : 503;
    assertEquals(Optional.of(f.report), f.state.run(() -> f.queries.report(f.view)));
    available.set(false);
    assertTrue(f.state.run(() -> f.queries.report(f.view)).isEmpty());
    available.set(true);
    assertEquals(Optional.of(f.report), f.state.run(() -> f.queries.report(f.view)));
  }

  @Test
  void dedicatedRequiresItsOwnExactProfileAndCannotAddUnobservedControllers() {
    var f = new Fixture(true);
    assertEquals(Optional.of(f.report), f.state.run(() -> f.queries.report(f.view)));
    f.entry.profile = body -> body.put("profile", "auriga.shared.v1");
    assertTrue(f.state.run(() -> f.queries.report(f.view)).isEmpty());
    f.entry.profile =
        body -> {
          ((tools.jackson.databind.node.ObjectNode) body.path("capabilities")).put("shell", true);
          return body;
        };
    assertTrue(f.state.run(() -> f.queries.report(f.view)).isEmpty());
    f.entry.profile =
        body -> {
          ((tools.jackson.databind.node.ObjectNode) body.path("capabilities"))
              .put("pluginInstall", false);
          return body;
        };
    assertTrue(f.state.run(() -> f.queries.report(f.view)).isEmpty());
    f.entry.profile = body -> body.put("schemaVersion", 4294967297L);
    assertTrue(f.state.run(() -> f.queries.report(f.view)).isEmpty());
  }
}
