package auriga.iam;

import static auriga.iam.SharedTaskTokens.*;
import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import auriga.common.context.TenantContext;
import auriga.common.persistence.*;
import auriga.contracts.iam.v1.*;
import auriga.contracts.node.v1.*;
import auriga.testsupport.PostgresExtension;
import java.security.KeyPairGenerator;
import java.time.*;
import java.util.*;
import java.util.concurrent.*;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;

/** Real migrations, RLS, rollback and concurrent writers; external W/session policy is a double. */
@ExtendWith(PostgresExtension.class)
class SharedTasksPostgresTest {
  private static IamStore store;
  private static JdbcTemplate jdbc;
  private static final Clock CLOCK =
      Clock.fixed(Instant.parse("2026-11-19T00:00:00Z"), ZoneOffset.UTC);

  @BeforeAll
  static void migrate() {
    var roles =
        PostgresExtension.serviceRoles(
            SharedTasksPostgresTest.class, "shared_tasks_owner", "shared_tasks_app", "iam");
    CommonMigrations.migrate(roles.owner().dataSource(), "iam");
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas("iam")
                .locations("classpath:db/iam")
                .load())
        .migrate();
    jdbc = new JdbcTemplate(roles.runtime().dataSource());
    store = new IamStore(jdbc, new TenantTransactionManager(roles.runtime().dataSource()));
  }

  @Test
  void concurrentBeginCreatesOneDurableTaskAndFinishSurvivesReconstruction() throws Exception {
    var f = new Fixture();
    BeginSharedTaskResponse first;
    BeginSharedTaskResponse second;
    try (var pool = Executors.newVirtualThreadPerTaskExecutor()) {
      var a = pool.submit(() -> f.begin());
      var b = pool.submit(() -> f.begin());
      first = a.get(15, TimeUnit.SECONDS);
      second = b.get(15, TimeUnit.SECONDS);
    }
    TenantContext.run(
        f.tenant,
        () ->
            store.tx(
                () -> {
                  assertEquals(
                      1,
                      jdbc.queryForObject(
                          "SELECT count(*) FROM iam.shared_task WHERE tenant_id=? AND id=?",
                          Integer.class,
                          f.tenant,
                          UUID.fromString(f.task)));
                  assertEquals(
                      2,
                      jdbc.queryForObject(
                          "SELECT count(*) FROM iam.shared_capability WHERE tenant_id=? AND task_id=?",
                          Integer.class,
                          f.tenant,
                          UUID.fromString(f.task)));
                  return null;
                }));
    TenantContext.run(
        f.tenant,
        () ->
            f.authority()
                .finish(
                    FinishSharedTaskRequest.newBuilder()
                        .setBinding(f.binding)
                        .setTaskId(f.task)
                        .build()));
    TenantContext.run(
        f.tenant,
        () -> {
          assertFalse(
              f.authority()
                  .authorize(
                      AuthorizeSharedCapabilityRequest.newBuilder()
                          .setCapability(first.getCapability())
                          .build())
                  .getActive());
          assertFalse(
              f.authority()
                  .authorize(
                      AuthorizeSharedCapabilityRequest.newBuilder()
                          .setCapability(second.getCapability())
                          .build())
                  .getActive());
          assertThrows(RuntimeException.class, f::begin);
        });
  }

  @Test
  void rollbackLeavesNoTaskAndRuntimeRlsHidesOtherTenants() throws Exception {
    var f = new Fixture();
    var failingTokens = spy(f.tokens);
    doThrow(new IllegalStateException("synthetic signing failure")).when(failingTokens).sign(any());
    TenantContext.run(
        f.tenant,
        () -> {
          assertThrows(
              IllegalStateException.class, () -> f.authority(failingTokens).begin(f.request()));
          store.tx(
              () -> {
                assertNull(store.get("shared_task", f.task, false));
                return null;
              });
        });
    var issued = f.begin();
    TenantContext.run(
        UUID.randomUUID(),
        () ->
            store.tx(
                () -> {
                  assertEquals(
                      0,
                      jdbc.queryForObject(
                          "SELECT count(*) FROM iam.shared_task WHERE id=?",
                          Integer.class,
                          UUID.fromString(f.task)));
                  assertEquals(
                      0,
                      jdbc.queryForObject(
                          "SELECT count(*) FROM iam.shared_capability WHERE id=?",
                          Integer.class,
                          UUID.fromString(issued.getCapability().getJti())));
                  return null;
                }));
    TenantContext.run(
        f.tenant,
        () ->
            store.tx(
                () -> {
                  store.cleanSharedTasks(CLOCK.instant().plusSeconds(751));
                  assertNull(
                      store.get("shared_capability", issued.getCapability().getJti(), false));
                  assertNotNull(store.get("shared_task", f.task, false));
                  store.cleanSharedTasks(CLOCK.instant().plusSeconds(3601));
                  assertNull(store.get("shared_task", f.task, false));
                  return null;
                }));
  }

  private static final class Fixture {
    final UUID tenant = UUID.randomUUID();
    final String task = UUID.randomUUID().toString();
    final SharedDshSessionBinding binding;
    final SessionManager sessions = mock(SessionManager.class);
    final UacSessions uac = mock(UacSessions.class);
    final SharedTaskTokens tokens;

    Fixture() throws Exception {
      String account = UUID.randomUUID().toString();
      String provider = UUID.randomUUID().toString();
      String session = UUID.randomUUID().toString();
      binding =
          SharedDshSessionBinding.newBuilder()
              .setTenantId(tenant.toString())
              .setMemberId(account)
              .setPlacementId(UUID.randomUUID().toString())
              .setPhysicalInstanceId(UUID.randomUUID().toString())
              .setMode(DshInstanceMode.DSH_INSTANCE_MODE_SHARED)
              .setEpoch(1)
              .setRevision(1)
              .setAuthGeneration(1)
              .setSessionId(UUID.randomUUID().toString())
              .setPlatformSessionId(session)
              .build();
      TenantContext.run(
          tenant,
          () ->
              store.tx(
                  () -> {
                    store.insert("account", account, Json.object("status", "active"), null);
                    store.insert("idp_provider", provider, Json.object("enabled", true), null);
                    store.insert(
                        "session",
                        session,
                        Json.object(
                            "tenantId",
                            tenant.toString(),
                            "accountId",
                            account,
                            "providerId",
                            provider,
                            "sessionId",
                            session,
                            "audience",
                            "portal",
                            "authGeneration",
                            1,
                            "expiresAt",
                            CLOCK.instant().plusSeconds(3600).toString()),
                        CLOCK.instant().plusSeconds(3600));
                    return null;
                  }));
      when(sessions.activeById(session)).thenAnswer(call -> store.byId("session", session, true));
      var keys = new HashMap<String, SharedTaskTokens.Key>();
      for (String purpose : List.of(ENTERPRISE, MODEL, MESSAGE))
        keys.put(
            purpose,
            new SharedTaskTokens.Key(
                "pg-test", KeyPairGenerator.getInstance("Ed25519").generateKeyPair().getPrivate()));
      tokens = new SharedTaskTokens(keys);
    }

    SharedTasks authority() {
      return authority(tokens);
    }

    SharedTasks authority(SharedTaskTokens signing) {
      return new SharedTasks(
          store,
          sessions,
          uac,
          (tenant, account, context) -> Set.of("ai.models.invoke"),
          (pool, session) -> binding,
          signing,
          CLOCK,
          () -> true);
    }

    BeginSharedTaskRequest request() {
      return BeginSharedTaskRequest.newBuilder()
          .setBinding(binding)
          .setTaskId(task)
          .setAudience(MODEL)
          .addScopes("model.invoke")
          .build();
    }

    BeginSharedTaskResponse begin() {
      return TenantContext.call(tenant, () -> authority().begin(request()));
    }
  }
}
