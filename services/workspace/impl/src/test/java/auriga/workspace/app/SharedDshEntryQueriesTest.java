package auriga.workspace.app;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import auriga.common.error.AurigaException;
import auriga.contracts.node.v1.*;
import auriga.workspace.api.SharedDshAuthority;
import auriga.workspace.persistence.WorkspaceStore;
import java.util.*;
import java.util.function.Supplier;
import org.junit.jupiter.api.Test;
import org.springframework.jdbc.core.JdbcTemplate;

class SharedDshEntryQueriesTest {
  @Test
  void sessionLookupUsesDurableOwnershipAndRechecksEveryCurrentBindingField() {
    UUID tenant = UUID.randomUUID(), pool = UUID.randomUUID(), session = UUID.randomUUID();
    var stored =
        SharedDshSessionBinding.newBuilder()
            .setTenantId(tenant.toString())
            .setPhysicalInstanceId(pool.toString())
            .setSessionId(session.toString())
            .setMemberId(UUID.randomUUID().toString())
            .setPlacementId(UUID.randomUUID().toString())
            .setPlatformSessionId(UUID.randomUUID().toString())
            .setMode(DshInstanceMode.DSH_INSTANCE_MODE_SHARED)
            .setEpoch(1)
            .setRevision(1)
            .setAuthGeneration(1)
            .build();
    var binding =
        SharedDshBinding.newBuilder()
            .setTenantId(stored.getTenantId())
            .setMemberId(stored.getMemberId())
            .setPlacementId(stored.getPlacementId())
            .setPhysicalInstanceId(stored.getPhysicalInstanceId())
            .setMode(stored.getMode())
            .setEpoch(stored.getEpoch())
            .setRevision(stored.getRevision())
            .build();
    var jdbc = mock(JdbcTemplate.class);
    // WorkspaceStore's public JDBC reference belongs to the real production
    // object; the transaction manager remains synthetic in this pure test.
    var manager = mock(auriga.common.persistence.TenantTransactionManager.class);
    var store = spy(new WorkspaceStore(jdbc, manager));
    doReturn(tenant).when(store).tenant();
    doAnswer(call -> ((Supplier<?>) call.getArgument(0)).get()).when(store).tx(any());
    when(jdbc.queryForList(anyString(), eq(tenant), eq(session)))
        .thenReturn(List.of(Map.of("binding", stored.toByteArray())));
    var authority = mock(SharedDshAuthority.class);
    var queries = new SharedDshEntryQueries(store, authority);
    var member = UUID.fromString(stored.getMemberId());
    var placement = UUID.fromString(stored.getPlacementId());
    var portal = UUID.fromString(stored.getPlatformSessionId());
    when(authority.resolve(member, placement, portal))
        .thenReturn(new SharedDshAuthority.Placement(binding, "ready", 1));
    assertEquals(stored, queries.lookup(pool, session));
    verify(authority).resolve(member, placement, portal);
    assertThrows(AurigaException.class, () -> queries.lookup(UUID.randomUUID(), session));
    for (var changed :
        List.of(
            binding.toBuilder().setEpoch(2).build(),
            binding.toBuilder().setRevision(2).build(),
            binding.toBuilder().setMode(DshInstanceMode.DSH_INSTANCE_MODE_DEDICATED).build(),
            binding.toBuilder().setPhysicalInstanceId(UUID.randomUUID().toString()).build())) {
      when(authority.resolve(member, placement, portal))
          .thenReturn(new SharedDshAuthority.Placement(changed, "ready", 1));
      assertThrows(AurigaException.class, () -> queries.lookup(pool, session));
    }
    when(authority.resolve(member, placement, portal))
        .thenReturn(new SharedDshAuthority.Placement(binding, "ready", 2));
    assertThrows(AurigaException.class, () -> queries.lookup(pool, session));
  }
}
