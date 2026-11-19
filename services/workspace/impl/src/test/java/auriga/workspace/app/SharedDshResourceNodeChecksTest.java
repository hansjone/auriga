package auriga.workspace.app;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import auriga.common.error.AurigaException;
import auriga.contracts.node.v1.*;
import auriga.workspace.persistence.WorkspaceStore;
import java.util.*;
import java.util.function.Supplier;
import org.junit.jupiter.api.Test;
import org.springframework.jdbc.core.JdbcTemplate;

class SharedDshResourceNodeChecksTest {
  @Test
  void preparedNodeAuthorityIsTenantScopedAndMatchesEveryCurrentBindingField() {
    UUID tenant = UUID.randomUUID(), node = UUID.randomUUID(), placement = UUID.randomUUID();
    var binding =
        SharedDshBinding.newBuilder()
            .setTenantId(tenant.toString())
            .setMemberId(UUID.randomUUID().toString())
            .setPlacementId(placement.toString())
            .setPhysicalInstanceId(UUID.randomUUID().toString())
            .setMode(DshInstanceMode.DSH_INSTANCE_MODE_SHARED)
            .setEpoch(1)
            .setRevision(1)
            .build();
    var jdbc = mock(JdbcTemplate.class);
    var store =
        spy(
            new WorkspaceStore(
                jdbc, mock(auriga.common.persistence.TenantTransactionManager.class)));
    doAnswer(call -> ((Supplier<?>) call.getArgument(1)).get()).when(store).in(eq(tenant), any());
    doNothing().when(store).lock();
    var row =
        Map.<String, Object>of(
            "tenant_id",
            tenant,
            "id",
            placement,
            "member_id",
            UUID.fromString(binding.getMemberId()),
            "pool_id",
            UUID.fromString(binding.getPhysicalInstanceId()),
            "mode",
            "shared",
            "epoch",
            1L,
            "revision",
            1L,
            "authorization_digest",
            "sha256:" + "a".repeat(64));
    when(jdbc.queryForList(anyString(), eq(tenant), eq(placement), eq(node)))
        .thenAnswer(
            call -> {
              String sql = call.getArgument(0);
              assertTrue(sql.contains("JOIN shared_dsh_pool_node"));
              assertTrue(sql.contains("pn.node_id=? AND NOT n.revoked"));
              assertTrue(sql.contains("x.state IN ('pending','ready')"));
              assertTrue(sql.contains("p.state IN ('pending','ready')"));
              return List.of(row);
            });
    var checks = new SharedDshResourceNodeChecks(store);
    assertEquals("sha256:" + "a".repeat(64), checks.requireCurrent(tenant, node, binding));
    for (var changed :
        List.of(
            binding.toBuilder().setMemberId(UUID.randomUUID().toString()).build(),
            binding.toBuilder().setPhysicalInstanceId(UUID.randomUUID().toString()).build(),
            binding.toBuilder().setEpoch(2).build(),
            binding.toBuilder().setRevision(2).build(),
            binding.toBuilder().setMode(DshInstanceMode.DSH_INSTANCE_MODE_DEDICATED).build()))
      assertThrows(AurigaException.class, () -> checks.requireCurrent(tenant, node, changed));
    assertThrows(
        AurigaException.class, () -> checks.requireCurrent(tenant, UUID.randomUUID(), binding));
    assertThrows(
        AurigaException.class, () -> checks.requireCurrent(UUID.randomUUID(), node, binding));
    when(jdbc.queryForList(anyString(), eq(tenant), eq(placement), eq(node))).thenReturn(List.of());
    assertThrows(AurigaException.class, () -> checks.requireCurrent(tenant, node, binding));
  }
}
