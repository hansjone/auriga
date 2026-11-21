package auriga.workspace.app;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.persistence.TenantTransactionManager;
import auriga.contracts.node.v1.*;
import auriga.workspace.persistence.WorkspaceStore;
import java.util.*;
import org.junit.jupiter.api.Test;
import org.springframework.jdbc.core.JdbcTemplate;

class SharedDshNodeRuntimeWiringTest {
  @Test
  void replaysRealViewsOnFreshConnectionAndFencesReportsByPoolOwner() {
    UUID tenant = UUID.randomUUID(),
        node = UUID.randomUUID(),
        pool = UUID.randomUUID(),
        placement = UUID.randomUUID(),
        member = UUID.randomUUID();
    var jdbc = mock(JdbcTemplate.class);
    var store = new WorkspaceStore(jdbc, mock(TenantTransactionManager.class));
    var placements = mock(SharedDshPlacements.class);
    var row = new HashMap<String, Object>();
    row.putAll(
        Map.of(
            "id",
            placement,
            "tenant_id",
            tenant,
            "member_id",
            member,
            "pool_id",
            pool,
            "mode",
            "shared",
            "epoch",
            1L,
            "revision",
            1L));
    var binding = SharedDshPlacements.binding(row);
    var view =
        SharedDshResourceView.newBuilder()
            .setSchemaVersion(1)
            .setBinding(binding)
            .setViewId(UUID.randomUUID().toString())
            .setSource("platform")
            .setAuthorizationSetDigest("sha256:" + "a".repeat(64))
            .build();
    when(placements.resourceView(binding)).thenReturn(view);
    when(jdbc.queryForList(anyString(), any(Object[].class)))
        .thenAnswer(
            call -> {
              String sql = call.getArgument(0);
              return sql.startsWith("SELECT node_id")
                  ? List.of(Map.of("node_id", node))
                  : List.of(row);
            });
    TenantContext.run(
        tenant,
        () -> {
          var first = new SharedDshNodeRuntime(store, placements);
          assertEquals(List.of(view), first.views(node, null).views());
          var reconnected = new SharedDshNodeRuntime(store, placements);
          assertEquals(List.of(view), reconnected.views(node, null).views());
          var report =
              SharedDshResourceReport.newBuilder()
                  .setBinding(binding)
                  .setViewId(view.getViewId())
                  .build();
          reconnected.resourceReport(node, report);
          verify(placements).reportResource(report);
          assertThrows(
              AurigaException.class, () -> reconnected.resourceReport(UUID.randomUUID(), report));
          var impact =
              SharedDshGuardianImpact.newBuilder()
                  .setTenantId(tenant.toString())
                  .setPhysicalInstanceId(pool.toString())
                  .addAffected(binding)
                  .build();
          reconnected.guardian(node, impact);
          verify(placements).nodeGuardian(impact);
          assertThrows(
              AurigaException.class, () -> reconnected.guardian(UUID.randomUUID(), impact));
        });
  }
}
