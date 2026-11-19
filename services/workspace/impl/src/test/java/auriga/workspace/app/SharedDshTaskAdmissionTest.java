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

/** Real admission policy, SQL effects isolated; transactional concurrency is in the PG suite. */
class SharedDshTaskAdmissionTest {
  @Test
  void admissionRequiresCurrentHolderAndFreshSamplesForEveryLivePool() {
    var f = new Fixture();
    f.run(
        () -> {
          f.admission.admit(f.node, f.holder, f.binding, f.task);
          return null;
        });
    assertEquals(f.task, f.active.get("task_id").toString());
    for (var key : List.of("lease_live", "holder", "valid_until", "observed_at")) {
      f.active = null;
      var original = f.pool.get(key);
      f.pool.put(
          key,
          switch (key) {
            case "lease_live" -> false;
            case "holder" -> UUID.randomUUID();
            case "valid_until" -> 1000L;
            default -> 1001L;
          });
      assertEquals(
          "common.unavailable",
          f.error(() -> f.admission.admit(f.node, f.holder, f.binding, f.task)));
      assertNull(f.active);
      f.pool.put(key, original);
    }
    var other = new HashMap<>(f.pool);
    other.put("id", UUID.randomUUID());
    other.put("holder", null);
    f.extraPools.add(other);
    assertEquals(
        "common.unavailable",
        f.error(() -> f.admission.admit(f.node, f.holder, f.binding, f.task)));
  }

  @Test
  void exactIntegerEightyPercentAndFourTasksRemainHardAdmissionLimits() {
    var f = new Fixture();
    f.pool.put("used_bytes", 80L);
    assertEquals(
        "common.conflict", f.error(() -> f.admission.admit(f.node, f.holder, f.binding, f.task)));
    f.pool.put("used_bytes", 9007199254740991L);
    f.pool.put("limit_bytes", 9007199254740991L);
    assertEquals(
        "common.conflict", f.error(() -> f.admission.admit(f.node, f.holder, f.binding, f.task)));
    f.pool.put("used_bytes", 79L);
    f.pool.put("limit_bytes", 100L);
    f.activeCount = 4;
    assertEquals(
        "common.conflict", f.error(() -> f.admission.admit(f.node, f.holder, f.binding, f.task)));
    f.activeCount = 0;
    f.registered = 21;
    assertEquals(
        "common.conflict", f.error(() -> f.admission.admit(f.node, f.holder, f.binding, f.task)));
    f.registered = 20;
    f.run(
        () -> {
          f.admission.admit(f.node, f.holder, f.binding, f.task);
          return null;
        });
    assertNotNull(f.active);
  }

  @Test
  void releaseBeforeAdmitIsDurableAndAnOldReleaseCannotDeleteANewTask() {
    var f = new Fixture();
    f.run(
        () -> {
          f.admission.release(f.node, f.binding, f.task);
          return null;
        });
    assertEquals(
        "common.conflict", f.error(() -> f.admission.admit(f.node, f.holder, f.binding, f.task)));
    String next = UUID.randomUUID().toString();
    f.run(
        () -> {
          f.admission.admit(f.node, f.holder, f.binding, next);
          return null;
        });
    f.run(
        () -> {
          f.admission.release(f.node, f.binding, f.task);
          return null;
        });
    assertEquals(next, f.active.get("task_id").toString());
    f.run(
        () -> {
          f.admission.release(f.node, f.binding, next);
          return null;
        });
    assertNull(f.active);
  }

  @Test
  void wrongBindingCannotReleaseOrAdoptAnotherReservationAndWrongNodeIsRejected() {
    var f = new Fixture();
    f.run(
        () -> {
          f.admission.admit(f.node, f.holder, f.binding, f.task);
          return null;
        });
    f.run(
        () -> {
          f.admission.admit(f.node, f.holder, f.binding, f.task);
          return null;
        });
    var other = f.binding.toBuilder().setPlatformSessionId(UUID.randomUUID().toString()).build();
    assertEquals("common.not_found", f.error(() -> f.admission.release(f.node, other, f.task)));
    assertEquals(
        "common.forbidden",
        f.error(() -> f.admission.release(UUID.randomUUID(), f.binding, f.task)));
    assertEquals(f.task, f.active.get("task_id").toString());
  }

  @Test
  void memoryRejectsOldConnectionSamplesClockRegressionAndRuntimeGenerationRollback() {
    var f = new Fixture();
    var good = f.sample();
    f.run(
        () -> {
          f.admission.memory(f.node, f.holder, 990, good);
          return null;
        });
    assertEquals(1, f.memoryWrites);
    assertEquals(
        "common.validation_failed",
        f.error(() -> f.admission.memory(f.node, f.holder, 1001, good)));
    assertEquals(
        "common.conflict",
        f.error(
            () ->
                f.admission.memory(
                    f.node, f.holder, 990, good.toBuilder().setObservedGeneration(1).build())));
    assertEquals(
        "common.conflict",
        f.error(
            () ->
                f.admission.memory(
                    f.node,
                    f.holder,
                    990,
                    good.toBuilder().setObservedAt(999).setValidUntil(1029).build())));
    for (var bad :
        List.of(
            good.toBuilder().setValidUntil(1031).build(),
            good.toBuilder().setMemoryLimitBytes(0).build(),
            good.toBuilder().setMemoryUsedBytes(-1).build(),
            good.toBuilder().setObservedAt(1001).build(),
            good.toBuilder().setValidUntil(1000).build())) {
      assertEquals(
          "common.validation_failed",
          f.error(() -> f.admission.memory(f.node, f.holder, 990, bad)));
    }
    assertEquals(1, f.memoryWrites);
  }

  static final class Fixture {
    final UUID tenant = UUID.randomUUID(),
        node = UUID.randomUUID(),
        holder = UUID.randomUUID(),
        poolId = UUID.randomUUID();
    final String task = UUID.randomUUID().toString();
    final SharedDshSessionBinding binding =
        SharedDshSessionBinding.newBuilder()
            .setTenantId(tenant.toString())
            .setMemberId(UUID.randomUUID().toString())
            .setPlacementId(UUID.randomUUID().toString())
            .setPhysicalInstanceId(poolId.toString())
            .setMode(DshInstanceMode.DSH_INSTANCE_MODE_SHARED)
            .setEpoch(1)
            .setRevision(1)
            .setSessionId(UUID.randomUUID().toString())
            .setPlatformSessionId(UUID.randomUUID().toString())
            .setAuthGeneration(1)
            .build();
    final JdbcTemplate jdbc = mock(JdbcTemplate.class);
    final SharedDshPlacements placements = mock(SharedDshPlacements.class);
    final SharedDshTaskAdmission admission;
    final Map<String, Object> pool = new HashMap<>();
    final List<Map<String, Object>> extraPools = new ArrayList<>();
    final Set<String> ended = new HashSet<>();
    Map<String, Object> active;
    long activeCount, registered = 20;
    int memoryWrites;

    Fixture() {
      // WorkspaceStore's public JDBC field is deliberately a real object boundary.
      var actual =
          spy(
              new WorkspaceStore(
                  jdbc, mock(auriga.common.persistence.TenantTransactionManager.class)));
      doAnswer(c -> c.<Supplier<?>>getArgument(0).get()).when(actual).tx(any());
      doReturn(Map.of("revoked", false, "schedulable", true, "draining", false))
          .when(actual)
          .node(any());
      when(placements.resolve(any(), any(), any()))
          .thenReturn(
              new SharedDshAuthority.Placement(
                  SharedDshBinding.newBuilder()
                      .setTenantId(binding.getTenantId())
                      .setMemberId(binding.getMemberId())
                      .setPlacementId(binding.getPlacementId())
                      .setPhysicalInstanceId(binding.getPhysicalInstanceId())
                      .setMode(binding.getMode())
                      .setEpoch(1)
                      .setRevision(1)
                      .build(),
                  "ready",
                  1));
      admission =
          new SharedDshTaskAdmission(
              actual, placements, Clock.fixed(Instant.ofEpochSecond(1000), ZoneOffset.UTC));
      pool.putAll(
          Map.of(
              "id",
              poolId,
              "holder",
              holder,
              "current_holder",
              holder,
              "lease_live",
              true,
              "observed_at",
              1000L,
              "valid_until",
              1030L,
              "used_bytes",
              79L,
              "limit_bytes",
              100L));
      doAnswer(
              c -> {
                String sql = c.getArgument(0);
                Object[] args = (Object[]) c.getRawArguments()[1];
                if (sql.startsWith("SELECT node_id")) return List.of(Map.of("node_id", node));
                if (sql.startsWith("SELECT binding FROM shared_dsh_session"))
                  return List.of(Map.of("binding", binding.toByteArray()));
                if (sql.startsWith("SELECT binding FROM shared_dsh_task_end"))
                  return ended.contains(args[1] + ":" + args[2])
                      ? List.of(Map.of("binding", binding.toByteArray()))
                      : List.of();
                if (sql.startsWith("SELECT task_id")) {
                  if (active == null) return List.of();
                  return sql.contains("member_id") || active.get("task_id").equals(args[1])
                      ? List.of(active)
                      : List.of();
                }
                if (sql.startsWith("SELECT p.id")) {
                  var rows = new ArrayList<>(extraPools);
                  rows.add(pool);
                  return rows;
                }
                if (sql.startsWith("SELECT generation"))
                  return memoryWrites == 0
                      ? List.of()
                      : List.of(Map.of("generation", 2L, "observed_at", 1000L));
                throw new AssertionError(sql);
              })
          .when(jdbc)
          .queryForList(anyString(), any(Object[].class));
      doAnswer(
              c ->
                  ((String) c.getArgument(0)).contains("shared_dsh_placement")
                      ? registered
                      : activeCount)
          .when(jdbc)
          .queryForObject(anyString(), eq(Long.class), any(Object[].class));
      doAnswer(
              c -> {
                String sql = c.getArgument(0);
                Object[] args = (Object[]) c.getRawArguments()[1];
                if (sql.startsWith("INSERT INTO shared_dsh_pool_memory")) memoryWrites++;
                else if (sql.startsWith("INSERT INTO shared_dsh_active"))
                  active =
                      new HashMap<>(
                          Map.of("task_id", args[3], "binding", args[4], "node_id", args[5]));
                else if (sql.startsWith("INSERT INTO shared_dsh_task_end"))
                  ended.add(args[2] + ":" + args[3]);
                else if (sql.startsWith("DELETE FROM shared_dsh_active")
                    && active != null
                    && active.get("task_id").equals(args[1])
                    && active.get("node_id").equals(args[2])
                    && Arrays.equals((byte[]) active.get("binding"), (byte[]) args[3]))
                  active = null;
                return 1;
              })
          .when(jdbc)
          .update(anyString(), any(Object[].class));
    }

    SharedDshPoolMemorySample sample() {
      return SharedDshPoolMemorySample.newBuilder()
          .setSchemaVersion(1)
          .setTenantId(tenant.toString())
          .setPhysicalInstanceId(poolId.toString())
          .setObservedGeneration(2)
          .setObservedAt(1000)
          .setValidUntil(1030)
          .setMemoryUsedBytes(79)
          .setMemoryLimitBytes(100)
          .build();
    }

    <T> T run(Supplier<T> work) {
      return TenantContext.call(tenant, work::get);
    }

    String error(Runnable work) {
      return run(() -> assertThrows(AurigaException.class, work::run).errorCode().code());
    }
  }
}
