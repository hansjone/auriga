package auriga.workspace;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import auriga.common.error.*;
import auriga.common.persistence.TenantTransactionManager;
import auriga.workspace.api.SharedDshAuthority.Inputs;
import auriga.workspace.api.WorkspaceInputs;
import auriga.workspace.app.SharedDshAdmin;
import auriga.workspace.app.SharedDshMaintenance;
import auriga.workspace.persistence.WorkspaceStore;
import java.time.*;
import java.util.*;
import java.util.function.Supplier;
import org.junit.jupiter.api.*;
import org.springframework.jdbc.core.JdbcTemplate;

class SharedDshDiscoveryTest {
  final UUID tenant = UUID.randomUUID(), member = UUID.randomUUID(), instance = UUID.randomUUID();
  final Inputs authority = mock(Inputs.class);
  final WorkspaceInputs directory = mock(WorkspaceInputs.class);
  final JdbcTemplate jdbc = mock(JdbcTemplate.class);
  final WorkspaceStore store = spy(new WorkspaceStore(jdbc, mock(TenantTransactionManager.class)));
  final SharedDshAdmin admin = new SharedDshAdmin(store, null, null, authority, Clock.systemUTC());

  @BeforeEach
  void setup() {
    doReturn(tenant).when(store).tenant();
    doAnswer(call -> call.<Supplier<?>>getArgument(0).get()).when(store).tx(any());
    admin.memberDirectory(directory);
  }

  Map<String, Object> legacy(UUID id, boolean hold) {
    return Map.of(
        "id",
        id,
        "owner_id",
        member,
        "name",
        "研发",
        "phase",
        "running",
        "ready",
        true,
        "shared_rebuild_hold",
        hold,
        "maintenance",
        false,
        "dedicated",
        false);
  }

  Map<String, Object> check(String target) {
    var request =
        new SharedDshAdmin.PreflightRequest(
            List.of(new SharedDshAdmin.Member(member, instance, target)));
    var body = admin.preflight(request).body();
    assertEquals("unknown", body.get("capacityStatus"));
    return ((List<Map<String, Object>>) body.get("items")).getFirst();
  }

  @Test
  void rejectsAmbiguousOwnershipHoldAndMissingDedicatedEntitlement() {
    when(jdbc.queryForList(anyString(), eq(tenant), eq(member)))
        .thenReturn(
            List.of(legacy(instance, false), legacy(UUID.randomUUID(), false)),
            List.of(legacy(UUID.randomUUID(), false)),
            List.of(legacy(instance, true)),
            List.of(legacy(instance, false)),
            List.of(legacy(instance, false)));
    assertEquals("存在多个旧实例，请先核对归属", check("shared").get("reason"));
    assertEquals("旧实例已删除或归属已变更", check("shared").get("reason"));
    assertEquals("旧实例已在维护中", check("shared").get("reason"));
    assertEquals(false, check("dedicated").get("eligible"));
    assertEquals(true, check("shared").get("eligible"));
    doThrow(new AurigaException(CommonErrors.FORBIDDEN))
        .when(authority)
        .requireMember(tenant, member);
    assertEquals(false, check("shared").get("eligible"));
  }

  @Test
  void expiredSavedWindowStartsOnlyWithItsStoredDeadlineAndOriginalTag() {
    var clock = Clock.fixed(Instant.ofEpochSecond(1000), ZoneOffset.UTC);
    var maintenance = new SharedDshMaintenance(store, null, authority, clock);
    var browser = new SharedDshAdmin(store, null, maintenance, authority, clock);
    var plan = UUID.randomUUID();
    var draft = new HashMap<String, Object>();
    draft.put("id", plan);
    draft.put("revision", 2L);
    draft.put("started", false);
    String members =
        "\"members\":[{\"memberId\":\""
            + member
            + "\",\"legacyInstanceId\":\""
            + instance
            + "\",\"targetMode\":\"shared\"}],\"reason\":\"maintenance\"";
    draft.put("request", "{" + members + ",\"exportUntil\":1001}");
    when(jdbc.queryForList(contains("FROM shared_dsh_rebuild_plan"), eq(tenant), eq(plan)))
        .thenReturn(List.of(draft));
    when(jdbc.queryForList(contains("FROM instance i"), eq(tenant), eq(member)))
        .thenReturn(List.of(legacy(instance, false)));
    when(jdbc.queryForList(contains("FROM instance WHERE"), eq(tenant), eq(instance)))
        .thenReturn(List.of(Map.of("owner_id", member)));
    var future = browser.plan(plan);
    assertEquals(
        "common.conflict",
        assertThrows(
                AurigaException.class,
                () -> browser.start(member, member, plan, "future", future.etag()))
            .errorCode()
            .code());
    verify(jdbc, never())
        .update(startsWith("INSERT INTO shared_dsh_maintenance"), any(Object[].class));
    draft.put("request", "{" + members + ",\"exportUntil\":950}");
    var elapsed = browser.plan(plan);
    assertNotEquals(future.etag(), elapsed.etag());
    assertEquals(
        "common.precondition_failed",
        assertThrows(
                AurigaException.class,
                () -> browser.start(member, member, plan, "stale", future.etag()))
            .errorCode()
            .code());
    when(jdbc.queryForObject(
            contains("FROM shared_dsh_placement"), eq(Long.class), eq(tenant), eq(member)))
        .thenReturn(0L);
    when(jdbc.queryForObject(
            contains("FROM instance WHERE"), eq(Long.class), eq(tenant), eq(member)))
        .thenReturn(1L);
    // The JDBC double does not persist the insert. start() therefore finds no batch.
    assertEquals(
        "common.not_found",
        assertThrows(
                AurigaException.class,
                () -> browser.start(member, member, plan, "elapsed", elapsed.etag()))
            .errorCode()
            .code());
    verify(jdbc)
        .update(
            startsWith("INSERT INTO shared_dsh_maintenance"),
            eq(tenant),
            eq(member),
            eq(false),
            eq(plan),
            eq(instance),
            eq(950L),
            eq(member),
            eq(1000L));
    draft.put("request", "{" + members + "}");
    assertFalse(browser.plan(plan).body().containsKey("exportUntil"));
  }

  @Test
  void doesNotBindForeignIdentityOrInventDirectoryMetadata() {
    when(directory.member(tenant, member))
        .thenReturn(
            auriga.contracts.org.v1.Member.newBuilder()
                .setAccountId(member.toString())
                .setTenantId(UUID.randomUUID().toString())
                .setDisplayName("Foreign")
                .build());
    when(jdbc.queryForList(anyString(), eq(tenant), eq(member)))
        .thenReturn(List.of(legacy(instance, false)));
    var result = admin.candidates("研发", 50, 0, member).body();
    var item = ((List<Map<String, Object>>) result.get("items")).getFirst();
    assertEquals(member, item.get("memberId"));
    assertFalse(item.containsKey("displayName"));
    assertEquals(0, admin.candidates("Foreign", 50, 0, member).body().get("total"));
    assertEquals(1, admin.candidates("", 50, 1, member).body().get("total"));
    assertThrows(AurigaException.class, () -> admin.candidates("", 201, 0, null));
  }
}
