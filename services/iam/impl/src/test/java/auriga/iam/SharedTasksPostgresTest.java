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
  void absentFinishFencesABeginAlreadyWaitingOnExternalOwnership() throws Exception {
    var f = new Fixture();
    var entered = new CountDownLatch(1);
    var resume = new CountDownLatch(1);
    var delayed =
        new SharedTasks(
            store,
            f.sessions,
            f.uac,
            (tenant, account, context) -> Set.of("ai.models.invoke"),
            (pool, session) -> {
              entered.countDown();
              try {
                if (!resume.await(15, TimeUnit.SECONDS))
                  throw new IllegalStateException("test timeout");
              } catch (InterruptedException error) {
                Thread.currentThread().interrupt();
                throw new IllegalStateException(error);
              }
              return f.binding;
            },
            f.tokens,
            CLOCK,
            () -> false);
    try (var workers = Executors.newVirtualThreadPerTaskExecutor()) {
      var pending =
          workers.submit(() -> TenantContext.call(f.tenant, () -> delayed.begin(f.request())));
      try {
        assertTrue(entered.await(15, TimeUnit.SECONDS));
        var finished =
            TenantContext.call(
                f.tenant,
                () ->
                    f.authority()
                        .finish(
                            FinishSharedTaskRequest.newBuilder()
                                .setBinding(f.binding)
                                .setTaskId(f.task)
                                .build()));
        assertTrue(finished.getFenced());
        assertEquals(f.binding, finished.getBinding());
        assertEquals(f.task, finished.getTaskId());
      } finally {
        resume.countDown();
      }
      var error = assertThrows(ExecutionException.class, () -> pending.get(15, TimeUnit.SECONDS));
      assertEquals(
          "common.conflict",
          ((auriga.common.error.AurigaException) error.getCause()).errorCode().code());
    }
    TenantContext.run(
        f.tenant,
        () ->
            store.tx(
                () -> {
                  assertNull(store.get("shared_task", f.task, false));
                  assertEquals(
                      1,
                      jdbc.queryForObject(
                          "SELECT count(*) FROM iam.shared_task_end WHERE tenant_id=?",
                          Integer.class,
                          f.tenant));
                  return null;
                }));
    assertThrows(auriga.common.error.AurigaException.class, f::begin);
  }

  @Test
  void concurrentFinishAndBeginCommitOnlyOneTerminalHistory() throws Exception {
    var f = new Fixture();
    var start = new CountDownLatch(1);
    try (var workers = Executors.newVirtualThreadPerTaskExecutor()) {
      var begin =
          workers.submit(
              () -> {
                assertTrue(start.await(15, TimeUnit.SECONDS));
                try {
                  f.begin();
                  return true;
                } catch (auriga.common.error.AurigaException error) {
                  assertEquals("common.conflict", error.errorCode().code());
                  return false;
                }
              });
      var finish =
          workers.submit(
              () -> {
                assertTrue(start.await(15, TimeUnit.SECONDS));
                return TenantContext.call(
                    f.tenant,
                    () ->
                        f.authority()
                            .finish(
                                FinishSharedTaskRequest.newBuilder()
                                    .setBinding(f.binding)
                                    .setTaskId(f.task)
                                    .build()));
              });
      start.countDown();
      begin.get(15, TimeUnit.SECONDS);
      assertTrue(finish.get(15, TimeUnit.SECONDS).getFenced());
    }
    assertThrows(auriga.common.error.AurigaException.class, f::begin);
    TenantContext.run(
        f.tenant,
        () ->
            store.tx(
                () -> {
                  var task = store.get("shared_task", f.task, false);
                  assertTrue(task == null || task.path("ended").asBoolean());
                  assertEquals(
                      1,
                      jdbc.queryForObject(
                          "SELECT count(*) FROM iam.shared_task_end WHERE tenant_id=?",
                          Integer.class,
                          f.tenant));
                  return null;
                }));
  }

  @Test
  void absentFinishIsBindingScopedAndSurvivesSessionDeletion() throws Exception {
    var f = new Fixture();
    var other = f.binding.toBuilder().setSessionId(UUID.randomUUID().toString()).build();
    TenantContext.run(
        f.tenant,
        () -> {
          assertTrue(
              f.authority()
                  .finish(
                      FinishSharedTaskRequest.newBuilder()
                          .setBinding(other)
                          .setTaskId(f.task)
                          .build())
                  .getFenced());
        });
    assertNotNull(f.begin());
    TenantContext.run(
        f.tenant,
        () -> {
          assertTrue(
              f.authority()
                  .finish(
                      FinishSharedTaskRequest.newBuilder()
                          .setBinding(f.binding)
                          .setTaskId(f.task)
                          .build())
                  .getFenced());
          store.tx(
              () -> {
                jdbc.update("DELETE FROM iam.shared_capability WHERE tenant_id=?", f.tenant);
                jdbc.update("DELETE FROM iam.shared_task WHERE tenant_id=?", f.tenant);
                store.delete("session", f.binding.getPlatformSessionId());
                return null;
              });
          assertTrue(
              f.authority()
                  .finish(
                      FinishSharedTaskRequest.newBuilder()
                          .setBinding(f.binding)
                          .setTaskId(f.task)
                          .build())
                  .getFenced());
          store.tx(
              () -> {
                assertEquals(
                    2,
                    jdbc.queryForObject(
                        "SELECT count(*) FROM iam.shared_task_end WHERE tenant_id=?",
                        Integer.class,
                        f.tenant));
                return null;
              });
        });
    TenantContext.run(
        UUID.randomUUID(),
        () ->
            store.tx(
                () -> {
                  assertEquals(
                      0,
                      jdbc.queryForObject(
                          "SELECT count(*) FROM iam.shared_task_end", Integer.class));
                  return null;
                }));
  }

  @Test
  void sharedMessageAllocationIsConcurrentDurableAndIndependentOfTheAuthorizingTask()
      throws Exception {
    var f = new Fixture();
    var authority =
        new SharedMessages(store, f.authority(), mock(IamNetworkAuthority.class), CLOCK);
    var cap =
        TenantContext.call(
            f.tenant,
            () ->
                f.authority()
                    .begin(
                        BeginSharedTaskRequest.newBuilder()
                            .setBinding(f.binding)
                            .setTaskId(f.task)
                            .setAudience(MESSAGE)
                            .addScopes("message.send")
                            .build())
                    .getCapability());
    String op = UUID.randomUUID().toString();
    var request =
        OpenSharedMessageContextRequest.newBuilder()
            .setCapability(cap)
            .setOperationId(op)
            .setAuthenticatedNodeId(UUID.randomUUID().toString())
            .setBootId(UUID.randomUUID().toString())
            .setNativeBindingId(UUID.randomUUID().toString())
            .setNativeBindingRevision("1")
            .build();
    SharedMessageContext a, b;
    try (var pool = Executors.newVirtualThreadPerTaskExecutor()) {
      var first = pool.submit(() -> TenantContext.call(f.tenant, () -> authority.open(request)));
      var second = pool.submit(() -> TenantContext.call(f.tenant, () -> authority.open(request)));
      a = first.get(15, TimeUnit.SECONDS);
      b = second.get(15, TimeUnit.SECONDS);
    }
    assertEquals(a, b);
    TenantContext.run(
        f.tenant,
        () -> {
          assertEquals(
              a,
              authority.read(
                  ReadSharedMessageContextRequest.newBuilder().setContextId(op).build()));
          f.authority()
              .finish(
                  FinishSharedTaskRequest.newBuilder()
                      .setBinding(f.binding)
                      .setTaskId(f.task)
                      .build());
          assertThrows(auriga.common.error.AurigaException.class, () -> authority.open(request));
          assertEquals(
              a,
              authority.read(
                  ReadSharedMessageContextRequest.newBuilder().setContextId(op).build()));
          var next =
              f.authority()
                  .begin(
                      BeginSharedTaskRequest.newBuilder()
                          .setBinding(f.binding)
                          .setTaskId(UUID.randomUUID().toString())
                          .setAudience(MESSAGE)
                          .addScopes("message.send")
                          .build());
          assertEquals(
              a, authority.open(request.toBuilder().setCapability(next.getCapability()).build()));
          assertThrows(
              auriga.common.error.AurigaException.class,
              () ->
                  authority.open(
                      request.toBuilder()
                          .setCapability(next.getCapability())
                          .setNativeBindingRevision("2")
                          .build()));
          store.tx(
              () -> {
                assertEquals(
                    1,
                    jdbc.queryForObject(
                        "SELECT count(*) FROM iam.shared_message_context WHERE tenant_id=? AND id=?",
                        Integer.class,
                        f.tenant,
                        UUID.fromString(op)));
                assertEquals(
                    0,
                    jdbc.queryForObject(
                        "SELECT count(*) FROM iam.network_node WHERE tenant_id=?",
                        Integer.class,
                        f.tenant));
                return null;
              });
          var newer =
              request.toBuilder()
                  .setCapability(next.getCapability())
                  .setOperationId(UUID.randomUUID().toString())
                  .setBootId(UUID.randomUUID().toString())
                  .setNativeBindingId(UUID.randomUUID().toString())
                  .build();
          var replacement = authority.open(newer);
          assertEquals(a.getRealm(), replacement.getRealm());
          assertNotEquals(a.getNode(), replacement.getNode());
          assertThrows(
              auriga.common.error.AurigaException.class,
              () ->
                  authority.read(
                      ReadSharedMessageContextRequest.newBuilder().setContextId(op).build()));
          assertThrows(
              auriga.common.error.AurigaException.class,
              () ->
                  authority.open(request.toBuilder().setCapability(next.getCapability()).build()));
          assertThrows(
              auriga.common.error.AurigaException.class,
              () ->
                  authority.open(
                      request.toBuilder()
                          .setCapability(next.getCapability())
                          .setOperationId(UUID.randomUUID().toString())
                          .build()));
          assertEquals(
              replacement,
              authority.read(
                  ReadSharedMessageContextRequest.newBuilder()
                      .setContextId(newer.getOperationId())
                      .build()));
        });
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
        () -> {
          var rebuilt = f.authority();
          assertTrue(
              rebuilt
                  .authorize(
                      AuthorizeSharedCapabilityRequest.newBuilder()
                          .setCapability(first.getCapability())
                          .build())
                  .getActive());
          assertTrue(
              rebuilt
                  .authorize(
                      AuthorizeSharedCapabilityRequest.newBuilder()
                          .setCapability(second.getCapability())
                          .build())
                  .getActive());
          var current =
              rebuilt.resolve(
                  ResolveSharedTaskRequest.newBuilder()
                      .setTenantId(f.tenant.toString())
                      .setSessionId(f.binding.getSessionId())
                      .setTaskId(f.task)
                      .setAudience(MODEL)
                      .setJti(first.getCapability().getJti())
                      .build());
          assertTrue(current.getActive());
          assertEquals(f.binding, current.getBinding());
          var renewed =
              rebuilt.issue(
                  IssueSharedCapabilityRequest.newBuilder()
                      .setBinding(f.binding)
                      .setTaskId(f.task)
                      .setAudience(MODEL)
                      .addScopes("model.invoke")
                      .build());
          assertTrue(
              f.authority()
                  .authorize(
                      AuthorizeSharedCapabilityRequest.newBuilder()
                          .setCapability(renewed.getCapability())
                          .build())
                  .getActive());
          var other = f.binding.toBuilder().setMemberId(UUID.randomUUID().toString()).build();
          assertEquals(
              "common.not_found",
              assertThrows(
                      auriga.common.error.AurigaException.class,
                      () ->
                          rebuilt.finish(
                              FinishSharedTaskRequest.newBuilder()
                                  .setBinding(other)
                                  .setTaskId(f.task)
                                  .build()))
                  .errorCode()
                  .code());
        });
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

  @Test
  void enterpriseReadAndCredentialRenewalSurvivePersistentClaimsAndReconstruction()
      throws Exception {
    var f = new Fixture();
    String credential = f.enableEnterprise();
    TenantContext.run(
        f.tenant,
        () -> {
          var first =
              f.authority()
                  .begin(
                      f.request().toBuilder()
                          .setAudience(ENTERPRISE)
                          .clearScopes()
                          .addAllScopes(List.of("credentials", "outbound"))
                          .build());
          assertTrue(
              f.authority()
                  .authorize(
                      AuthorizeSharedCapabilityRequest.newBuilder()
                          .setCapability(first.getCapability())
                          .build())
                  .getActive());
          var raw =
              f.authority()
                  .credentials(
                      ReadSharedCredentialsRequest.newBuilder()
                          .setCapability(first.getCapability())
                          .build());
          assertEquals("000042", raw.getEmpNo());
          assertEquals("synthetic-v1", raw.getToken());
          store.tx(
              () -> {
                var record = store.byId("uac_credential", credential, true);
                record.put("version", 2);
                store.save("uac_credential", record);
                return null;
              });
          assertFalse(
              f.authority()
                  .authorize(
                      AuthorizeSharedCapabilityRequest.newBuilder()
                          .setCapability(first.getCapability())
                          .build())
                  .getActive());
          var refreshed =
              f.authority()
                  .issue(
                      IssueSharedCapabilityRequest.newBuilder()
                          .setBinding(f.binding)
                          .setTaskId(f.task)
                          .setAudience(ENTERPRISE)
                          .addAllScopes(List.of("credentials", "outbound"))
                          .build());
          assertEquals(2, refreshed.getCapability().getCredentialVersion());
          assertTrue(
              f.authority()
                  .authorize(
                      AuthorizeSharedCapabilityRequest.newBuilder()
                          .setCapability(refreshed.getCapability())
                          .build())
                  .getActive());
          raw =
              f.authority()
                  .credentials(
                      ReadSharedCredentialsRequest.newBuilder()
                          .setCapability(refreshed.getCapability())
                          .build());
          assertEquals("000042", raw.getEmpNo());
          assertEquals("synthetic-v2", raw.getToken());
          f.authority()
              .finish(
                  FinishSharedTaskRequest.newBuilder()
                      .setBinding(f.binding)
                      .setTaskId(f.task)
                      .build());
          assertFalse(
              f.authority()
                  .authorize(
                      AuthorizeSharedCapabilityRequest.newBuilder()
                          .setCapability(refreshed.getCapability())
                          .build())
                  .getActive());
          assertThrows(
              auriga.common.error.AurigaException.class,
              () ->
                  f.authority()
                      .credentials(
                          ReadSharedCredentialsRequest.newBuilder()
                              .setCapability(refreshed.getCapability())
                              .build()));
        });
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

    String enableEnterprise() {
      String credential = UUID.randomUUID().toString();
      TenantContext.run(
          tenant,
          () ->
              store.tx(
                  () -> {
                    var session = store.byId("session", binding.getPlatformSessionId(), true);
                    String provider = session.path("providerId").asString();
                    store.insert(
                        "uac_credential",
                        credential,
                        Json.object(
                            "kind",
                            "credential",
                            "accountId",
                            binding.getMemberId(),
                            "providerId",
                            provider,
                            "version",
                            1,
                            "invalid",
                            false,
                            "updatedAt",
                            CLOCK.instant().toString()),
                        null);
                    session.put("uacCredentialId", credential);
                    session.set(
                        "uacSessionBinding",
                        Json.object(
                            "authGeneration",
                            1,
                            "sourceSessionId",
                            binding.getPlatformSessionId(),
                            "providerId",
                            provider));
                    store.save("session", session);
                    return null;
                  }));
      when(uac.sharedCredentials(binding.getMemberId(), binding.getPlatformSessionId()))
          .thenAnswer(
              call -> {
                long version =
                    store.byId("uac_credential", credential, false).path("version").asLong();
                return Json.object(
                    "empNo",
                    "000042",
                    "token",
                    "synthetic-v" + version,
                    "updatedAt",
                    CLOCK.instant().toString(),
                    "credentialVersion",
                    version);
              });
      return credential;
    }

    SharedTasks authority(SharedTaskTokens signing) {
      return new SharedTasks(
          store,
          sessions,
          uac,
          (tenant, account, context) -> Set.of("ai.models.invoke", "collab.messages.use"),
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
