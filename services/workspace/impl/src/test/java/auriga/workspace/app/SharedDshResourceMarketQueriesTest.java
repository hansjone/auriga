package auriga.workspace.app;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.contracts.node.v1.*;
import auriga.workspace.api.SharedDshAuthority;
import auriga.workspace.persistence.WorkspaceStore;
import java.time.*;
import java.util.*;
import java.util.function.Supplier;
import org.junit.jupiter.api.Test;
import org.springframework.jdbc.core.JdbcTemplate;

class SharedDshResourceMarketQueriesTest {
  static final class Fixture {
    final UUID tenant = UUID.randomUUID(),
        member = UUID.randomUUID(),
        placement = UUID.randomUUID(),
        session = UUID.randomUUID();
    final JdbcTemplate jdbc = mock(JdbcTemplate.class);
    final WorkspaceStore store =
        spy(
            new WorkspaceStore(
                jdbc, mock(auriga.common.persistence.TenantTransactionManager.class)));
    final SharedDshAuthority authority = mock(SharedDshAuthority.class);
    final SharedDshResourceMarketQueries.ReadyEntryProbe entry =
        mock(SharedDshResourceMarketQueries.ReadyEntryProbe.class);
    final Clock clock = Clock.fixed(Instant.parse("2026-11-19T00:00:00Z"), ZoneOffset.UTC);
    final SharedDshBinding binding =
        SharedDshBinding.newBuilder()
            .setTenantId(tenant.toString())
            .setMemberId(member.toString())
            .setPlacementId(placement.toString())
            .setPhysicalInstanceId(UUID.randomUUID().toString())
            .setMode(DshInstanceMode.DSH_INSTANCE_MODE_DEDICATED)
            .setEpoch(2)
            .setRevision(3)
            .build();
    final SharedDshResourceView view =
        SharedDshResourceView.newBuilder()
            .setSchemaVersion(1)
            .setSource("platform")
            .setBinding(binding)
            .setViewId(UUID.randomUUID().toString())
            .setAuthorizationSetDigest("sha256:" + "a".repeat(64))
            .build();
    final SharedDshResourceReport report =
        SharedDshResourceReport.newBuilder()
            .setSchemaVersion(1)
            .setBinding(binding)
            .setViewId(view.getViewId())
            .setStatus("ready")
            .setReason("verified")
            .setObservedAt(clock.instant().getEpochSecond())
            .build();
    final Map<String, Object> row = new HashMap<>();
    final SharedDshResourceMarketQueries queries;

    Fixture() {
      doAnswer(c -> c.<Supplier<?>>getArgument(0).get()).when(store).tx(any());
      doNothing().when(store).lock();
      when(authority.resolve(member, placement, session))
          .thenReturn(new SharedDshAuthority.Placement(binding, "ready", 4));
      when(entry.observed(binding)).thenReturn(true);
      row.put("tenant_id", tenant);
      row.put("member_id", member);
      row.put("id", placement);
      row.put("pool_id", UUID.fromString(binding.getPhysicalInstanceId()));
      row.put("mode", "dedicated");
      row.put("epoch", 2L);
      row.put("revision", 3L);
      row.put("payload", view.toByteArray());
      row.put("report", report.toByteArray());
      when(jdbc.queryForList(
              anyString(), eq(tenant), eq(placement), eq(UUID.fromString(view.getViewId()))))
          .thenAnswer(
              c -> {
                String sql = c.getArgument(0);
                assertTrue(sql.contains("shared_dsh_pool_node"));
                assertTrue(sql.contains("NOT n.revoked"));
                assertTrue(sql.contains("p.state='ready'"));
                return List.of(row);
              });
      queries = new SharedDshResourceMarketQueries(store, authority, entry, clock);
    }

    <T> T run(Supplier<T> work) {
      return TenantContext.call(tenant, work::get);
    }
  }

  @Test
  void marketResolveUsesLiveMemberSessionAuthorityAndDoesNotCreateOrGrantPlacements() {
    var f = new Fixture();
    assertEquals(f.binding, f.run(() -> f.queries.resolve(f.member, f.placement, f.session)));
    verify(f.authority).resolve(f.member, f.placement, f.session);
    verifyNoMoreInteractions(f.authority);
    assertThrows(
        AurigaException.class,
        () -> f.run(() -> f.queries.resolve(UUID.randomUUID(), f.placement, f.session)));
  }

  @Test
  void appliedRequiresExactCurrentViewReceiptPreparedNodeAndActualEntryProbe() {
    var f = new Fixture();
    assertEquals(Optional.of(f.report), f.run(() -> f.queries.report(f.view)));
    f.row.put(
        "payload",
        f.view.toBuilder().setViewId(UUID.randomUUID().toString()).build().toByteArray());
    assertTrue(f.run(() -> f.queries.report(f.view)).isEmpty());
    f.row.put("payload", f.view.toByteArray());
    f.row.put(
        "report",
        f.report.toBuilder().setViewId(UUID.randomUUID().toString()).build().toByteArray());
    assertTrue(f.run(() -> f.queries.report(f.view)).isEmpty());
    f.row.put("report", f.report.toByteArray());
    when(f.entry.observed(f.binding)).thenReturn(false);
    assertTrue(f.run(() -> f.queries.report(f.view)).isEmpty());
  }

  @Test
  void changedBindingAndAReceiptChangedDuringProbeCannotReportApplied() {
    var f = new Fixture();
    f.row.put("revision", 4L);
    assertTrue(f.run(() -> f.queries.report(f.view)).isEmpty());
    f.row.put("revision", 3L);
    when(f.entry.observed(f.binding))
        .thenAnswer(
            c -> {
              f.row.put("report", null);
              return true;
            });
    assertTrue(f.run(() -> f.queries.report(f.view)).isEmpty());
    assertThrows(
        AurigaException.class,
        () -> TenantContext.call(UUID.randomUUID(), () -> f.queries.report(f.view)));
  }
}
