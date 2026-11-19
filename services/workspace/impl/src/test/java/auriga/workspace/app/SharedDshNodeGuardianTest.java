package auriga.workspace.app;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.persistence.TenantTransactionManager;
import auriga.contracts.node.v1.*;
import auriga.workspace.api.SharedDshAuthority;
import auriga.workspace.persistence.WorkspaceStore;
import java.time.*;
import java.util.*;
import java.util.function.Supplier;
import org.junit.jupiter.api.Test;
import org.springframework.jdbc.core.JdbcTemplate;

class SharedDshNodeGuardianTest {
  @Test
  void completeOwningNodeImpactFencesOnceAndCannotRegressStopEvidence() {
    UUID tenant = UUID.randomUUID(), pool = UUID.randomUUID(), operation = UUID.randomUUID();
    var clock = Clock.fixed(Instant.parse("2026-11-19T00:00:00Z"), ZoneOffset.UTC);
    var jdbc = mock(JdbcTemplate.class);
    var store = spy(new WorkspaceStore(jdbc, mock(TenantTransactionManager.class)));
    doNothing().when(store).lock();
    doAnswer(c -> c.<Supplier<?>>getArgument(0).get()).when(store).tx(any());
    var rows = new LinkedHashMap<UUID, Map<String, Object>>();
    for (int i = 0; i < 2; i++) {
      UUID id = UUID.randomUUID();
      var row = new HashMap<String, Object>();
      row.putAll(
          Map.of(
              "id",
              id,
              "tenant_id",
              tenant,
              "pool_id",
              pool,
              "member_id",
              UUID.randomUUID(),
              "mode",
              "shared",
              "epoch",
              1L,
              "revision",
              1L,
              "auth_generation",
              1L,
              "state",
              "ready"));
      rows.put(id, row);
    }
    var receipts = new HashMap<UUID, byte[]>();
    var fences = new ArrayList<UUID>();
    var poolBlocked = new boolean[1];
    when(jdbc.queryForList(anyString(), any(Object[].class)))
        .thenAnswer(
            c -> {
              String sql = c.getArgument(0);
              Object[] args = (Object[]) c.getRawArguments()[1];
              if (sql.contains("FROM shared_dsh_node_guardian"))
                return receipts.containsKey(args[1])
                    ? List.of(Map.of("payload", receipts.get(args[1])))
                    : List.of();
              if (sql.contains("FROM shared_dsh_resource_view")) return List.of();
              if (sql.contains("FROM shared_dsh_placement")) {
                return rows.values().stream()
                    .filter(r -> !sql.contains("member_id=?") || r.get("member_id").equals(args[1]))
                    .map(HashMap::new)
                    .toList();
              }
              return List.of();
            });
    when(jdbc.update(anyString(), any(Object[].class)))
        .thenAnswer(
            c -> {
              String sql = c.getArgument(0);
              Object[] args = (Object[]) c.getRawArguments()[1];
              if (sql.startsWith("INSERT INTO shared_dsh_node_guardian"))
                receipts.put((UUID) args[1], (byte[]) args[3]);
              else if (sql.startsWith("UPDATE shared_dsh_node_guardian"))
                receipts.put((UUID) args[2], (byte[]) args[0]);
              else if (sql.startsWith("UPDATE shared_dsh_pool SET state='blocked'"))
                poolBlocked[0] = true;
              else if (sql.startsWith("UPDATE shared_dsh_placement SET epoch=epoch+1")) {
                UUID id = (UUID) args[1];
                fences.add(id);
                var r = rows.get(id);
                for (var key : List.of("epoch", "revision", "auth_generation"))
                  r.put(key, ((Number) r.get(key)).longValue() + 1);
                r.put("state", "unavailable");
              }
              return 1;
            });
    var impact =
        SharedDshGuardianImpact.newBuilder()
            .setSchemaVersion(1)
            .setTenantId(tenant.toString())
            .setPhysicalInstanceId(pool.toString())
            .setOperationId(operation.toString())
            .setScope("pool")
            .setReason("resource-expired")
            .setState("blocked")
            .setObservedAt(clock.instant().getEpochSecond());
    rows.values().forEach(r -> impact.addAffected(SharedDshPlacements.binding(r)));
    var authority =
        new SharedDshPlacements(
            store,
            mock(SharedDshAuthority.Inputs.class),
            mock(SharedDshPlacements.EntryProbe.class),
            clock);
    TenantContext.run(
        tenant,
        () -> {
          assertThrows(
              AurigaException.class,
              () -> authority.nodeGuardian(impact.clone().removeAffected(0).build()));
          assertFalse(poolBlocked[0]);
          assertTrue(receipts.isEmpty());
          authority.nodeGuardian(impact.build());
          assertTrue(poolBlocked[0]);
          assertEquals(2, fences.size());
          assertTrue(
              rows.values().stream()
                  .allMatch(
                      r -> r.get("state").equals("unavailable") && r.get("epoch").equals(2L)));
          authority.nodeGuardian(impact.build());
          assertEquals(2, fences.size());
          authority.nodeGuardian(impact.clone().setState("stopped").build());
          assertEquals(2, fences.size());
          assertThrows(AurigaException.class, () -> authority.nodeGuardian(impact.build()));
        });
  }
}
