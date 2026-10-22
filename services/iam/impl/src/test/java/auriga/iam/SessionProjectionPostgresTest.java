package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.persistence.CommonMigrations;
import auriga.common.persistence.TenantTransactionManager;
import auriga.testsupport.NatsExtension;
import auriga.testsupport.PostgresExtension;
import com.zaxxer.hikari.HikariDataSource;
import java.time.*;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.AtomicBoolean;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.transaction.support.TransactionTemplate;

@ExtendWith({PostgresExtension.class, NatsExtension.class})
class SessionProjectionPostgresTest {
  static PostgresExtension.ServiceRoles roles;
  HikariDataSource source;
  JdbcTemplate jdbc;
  IamStore store;
  SessionKv primary;
  io.nats.client.Connection connection;
  NatsSessionKv secondary;
  final UUID tenant = UUID.randomUUID();
  final String hash = Secrets.hash(Secrets.random());
  final AdminCsrfPostgresTest.Time time = new AdminCsrfPostgresTest.Time();

  @BeforeAll
  static void migrate() {
    roles =
        PostgresExtension.serviceRoles(
            SessionProjectionPostgresTest.class, "projection_owner", "projection_app", "iam");
    CommonMigrations.migrate(roles.owner().dataSource(), "iam");
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas("iam")
                .locations("classpath:db/iam")
                .load())
        .migrate();
  }

  void openStore() {
    source = new HikariDataSource();
    source.setJdbcUrl(roles.runtime().jdbcUrl());
    source.setMaximumPoolSize(5);
    assertEquals(30000, source.getConnectionTimeout());
    jdbc = new JdbcTemplate(source);
    store = new IamStore(jdbc, new TenantTransactionManager(source));
    primary = new PostgresSessionKv(store, new ContractSchemas(), time);
  }

  @BeforeEach
  void setup() throws Exception {
    time.now = Instant.now();
    openStore();
    connection = io.nats.client.Nats.connect(NatsExtension.url());
    secondary = new NatsSessionKv(connection, new ContractSchemas());
  }

  @AfterEach
  void close() throws Exception {
    connection.close();
    source.close();
  }

  CompositeSessionKv composite(SessionKv target) {
    return new CompositeSessionKv(store, primary, target, time);
  }

  @Test
  void normalBackgroundWorkerOwnsOneConnectionWhileRequestResourcesAreReleased() throws Exception {
    var entered = new CountDownLatch(1);
    var release = new CountDownLatch(1);
    SessionKv target =
        new SessionKv() {
          public tools.jackson.databind.node.ObjectNode get(String key) {
            return secondary.get(key);
          }

          public void put(String key, tools.jackson.databind.node.ObjectNode value) {
            entered.countDown();
            try {
              assertTrue(release.await(5, TimeUnit.SECONDS));
            } catch (InterruptedException e) {
              Thread.currentThread().interrupt();
              throw new IllegalStateException(e);
            }
            secondary.put(key, value);
          }

          public void delete(String key) {
            secondary.delete(key);
          }
        };
    var worker = composite(target);
    try {
      store.tenant(
          tenant,
          () -> {
            worker.put(hash, IndependentReviewProbeTest.value(tenant, "background"));
            return null;
          });
      worker.start();
      assertTrue(entered.await(5, TimeUnit.SECONDS));
      store.requireTransactionReleased();
      assertEquals(1, source.getHikariPoolMXBean().getActiveConnections());
      try (var threads = Executors.newVirtualThreadPerTaskExecutor()) {
        var calls = new ArrayList<Future<?>>();
        for (int i = 0; i < 5; i++)
          calls.add(threads.submit(() -> store.tenant(tenant, () -> store.all("session"))));
        for (var call : calls) call.get(2, TimeUnit.SECONDS);
      }
      System.out.println(
          "BACKGROUND_POOL_PROBE request_resource=false worker_active=1 five_requests=completed pool=5");
      release.countDown();
      org.awaitility.Awaitility.await()
          .atMost(Duration.ofSeconds(3))
          .untilAsserted(() -> assertNotNull(secondary.get(hash)));
    } finally {
      release.countDown();
      worker.stop();
    }
    assertEquals(0, source.getHikariPoolMXBean().getActiveConnections());
  }

  int pending(UUID id) {
    return store.tenant(
        id,
        () -> jdbc.queryForObject("SELECT count(*) FROM iam.session_projection", Integer.class));
  }

  @Test
  void responseConfirmsMixedUpdatesAndDeletesAndRejectsCrossTenantBeforeCommit() {
    var worker = composite(secondary);
    String removed = Secrets.hash(Secrets.random());
    worker.start();
    try {
      auriga.common.context.TenantContext.run(
          tenant,
          () ->
              store.responseTx(
                  worker,
                  () -> {
                    worker.put(hash, IndependentReviewProbeTest.value(tenant, "first"));
                    worker.put(removed, IndependentReviewProbeTest.value(tenant, "deleted"));
                    worker.delete(removed);
                    worker.put(hash, IndependentReviewProbeTest.value(tenant, "latest"));
                    return null;
                  }));
      assertEquals("latest", secondary.get(hash).path("accessToken").asString());
      assertNull(secondary.get(removed));
      assertEquals(0, pending(tenant));
      String forbidden = Secrets.hash(Secrets.random());
      assertThrows(
          auriga.common.persistence.TenantTransactionManager.TenantMismatchException.class,
          () ->
              auriga.common.context.TenantContext.run(
                  tenant,
                  () ->
                      store.responseTx(
                          worker,
                          () -> {
                            worker.put(
                                forbidden, IndependentReviewProbeTest.value(tenant, "rollback"));
                            store.tenant(
                                UUID.randomUUID(),
                                () -> {
                                  worker.delete(hash);
                                  return null;
                                });
                            return null;
                          })));
      assertNull(secondary.get(forbidden));
      assertEquals("latest", secondary.get(hash).path("accessToken").asString());
      assertEquals(0, pending(tenant));
    } finally {
      worker.stop();
    }
  }

  @Test
  void boundedConfirmationQueueFailsExplicitlyWithoutDroppingDurableRevocations() throws Exception {
    var entered = new CountDownLatch(1);
    var release = new CountDownLatch(1);
    var target = spy(secondary);
    doAnswer(
            invocation -> {
              entered.countDown();
              assertTrue(release.await(5, TimeUnit.SECONDS));
              return invocation.callRealMethod();
            })
        .when(target)
        .put(eq(hash), any());
    var worker = composite(target);
    var hashes = new ArrayList<String>();
    for (int i = 0; i < 129; i++) hashes.add(Secrets.hash(Secrets.random()));
    try {
      store.tenant(
          tenant,
          () -> {
            worker.put(hash, IndependentReviewProbeTest.value(tenant, "blocked"));
            return null;
          });
      worker.start();
      assertTrue(entered.await(5, TimeUnit.SECONDS));
      var failure =
          assertThrows(
              auriga.common.error.AurigaException.class,
              () ->
                  auriga.common.context.TenantContext.run(
                      tenant,
                      () ->
                          store.responseTx(
                              worker,
                              () -> {
                                hashes.forEach(worker::delete);
                                return null;
                              })));
      assertEquals("common.unavailable", failure.errorCode().code());
      store.requireTransactionReleased();
      assertTrue(pending(tenant) >= 129);
      release.countDown();
      org.awaitility.Awaitility.await()
          .atMost(Duration.ofSeconds(3))
          .untilAsserted(() -> assertNotNull(secondary.get(hash)));
    } finally {
      release.countDown();
      worker.stop();
    }
    time.now = time.now.plusSeconds(2);
    worker.start();
    try {
      auriga.common.context.TenantContext.run(
          tenant,
          () -> {
            worker.awaitProjected(List.of(hash));
            for (int i = 0; i < hashes.size(); i += 32)
              worker.awaitProjected(hashes.subList(i, Math.min(i + 32, hashes.size())));
          });
    } finally {
      worker.stop();
    }
    assertEquals(0, pending(tenant));
    hashes.forEach(key -> assertNull(secondary.get(key)));
  }

  void drain(CompositeSessionKv worker) {
    for (int i = 0; i < 100 && pending(tenant) > 0; i++) worker.forwardOnce();
    assertEquals(0, pending(tenant));
  }

  @org.springframework.context.annotation.Configuration(proxyBeanMethods = false)
  static class LifecycleAssembly {
    @org.springframework.context.annotation.Bean
    SessionKv sessionKv(
        IamStore store,
        Clock clock,
        org.springframework.beans.factory.ObjectProvider<io.nats.client.Connection> connections) {
      return new IamRuntimeConfiguration()
          .iamSessionKv(
              store,
              new ContractSchemas(),
              clock,
              connections,
              new org.springframework.mock.env.MockEnvironment());
    }
  }

  @Test
  void springInterfaceBeanActuallyStartsAndStopsWorker() {
    CompositeSessionKv worker;
    try (var context =
        new org.springframework.context.annotation.AnnotationConfigApplicationContext()) {
      context.registerBean(IamStore.class, () -> store);
      context.registerBean(Clock.class, () -> time);
      context.registerBean(io.nats.client.Connection.class, () -> connection);
      context.register(LifecycleAssembly.class);
      context.refresh();
      worker = assertInstanceOf(CompositeSessionKv.class, context.getBean(SessionKv.class));
      assertTrue(worker.isRunning());
      store.tenant(
          tenant,
          () -> {
            worker.put(hash, IndependentReviewProbeTest.value(tenant, "spring"));
            return null;
          });
      org.awaitility.Awaitility.await()
          .atMost(Duration.ofSeconds(5))
          .untilAsserted(
              () -> {
                assertNotNull(secondary.get(hash));
                assertEquals(0, pending(tenant));
              });
    }
    assertFalse(worker.isRunning());
  }

  @Test
  void rollbackAndNestedSavepointNeverPublishOrLeaveWork() {
    var kv = composite(secondary);
    assertThrows(
        IllegalStateException.class,
        () ->
            store.tenant(
                tenant,
                () -> {
                  kv.put(hash, IndependentReviewProbeTest.value(tenant, "rollback"));
                  throw new IllegalStateException("rollback");
                }));
    assertEquals(0, pending(tenant));
    assertEquals(
        0,
        jdbc.queryForObject(
            "SELECT count(*) FROM iam.session_projection_tenant WHERE tenant_id=?",
            Integer.class,
            tenant));
    store.tenant(
        tenant,
        () -> {
          assertThrows(
              IllegalStateException.class,
              () ->
                  store.atomic(
                      () -> {
                        kv.put(hash, IndependentReviewProbeTest.value(tenant, "nested-rollback"));
                        throw new IllegalStateException("savepoint rollback");
                      }));
          return null;
        });
    assertEquals(0, pending(tenant));
    assertEquals(0, kv.forwardOnce());
    assertNull(secondary.get(hash));
  }

  @Test
  void externalOuterTransactionAndLatestRevocationDoNotUseCommitCallbacks() throws Exception {
    var kv = composite(secondary);
    var outer = new TransactionTemplate(new TenantTransactionManager(source));
    auriga.common.context.TenantContext.run(
        tenant,
        () ->
            outer.execute(
                status -> {
                  store.tx(
                      () -> {
                        kv.put(hash, IndependentReviewProbeTest.value(tenant, "old"));
                        return null;
                      });
                  try (var thread = Executors.newVirtualThreadPerTaskExecutor()) {
                    assertEquals(0, thread.submit(kv::forwardOnce).get(5, TimeUnit.SECONDS));
                  } catch (Exception error) {
                    throw new IllegalStateException(error);
                  }
                  assertNull(secondary.get(hash));
                  return null;
                }));
    store.tenant(
        tenant,
        () -> {
          kv.put(hash, IndependentReviewProbeTest.value(tenant, "latest"));
          return null;
        });
    drain(kv);
    assertEquals("latest", secondary.get(hash).path("accessToken").asString());
    store.tenant(
        tenant,
        () -> {
          kv.delete(hash);
          return null;
        });
    // Reconstruct a worker with no old callbacks; only persistent work is needed.
    drain(composite(secondary));
    assertNull(secondary.get(hash));
    assertEquals(0, kv.forwardOnce());
    assertNull(secondary.get(hash));
  }

  @Test
  void failedRevocationSurvivesStoreAndWorkerReconstructionWithBackoff() {
    var live = composite(secondary);
    store.tenant(
        tenant,
        () -> {
          live.put(hash, IndependentReviewProbeTest.value(tenant, "live"));
          return null;
        });
    drain(live);
    assertNotNull(secondary.get(hash));
    var unavailable = mock(SessionKv.class);
    doThrow(new IllegalStateException("broker unavailable")).when(unavailable).delete(hash);
    var failing = composite(unavailable);
    store.tenant(
        tenant,
        () -> {
          failing.delete(hash);
          return null;
        });
    failing.forwardOnce();
    assertEquals(1, pending(tenant));
    failing.forwardOnce();
    verify(unavailable, times(1)).delete(hash);
    // Destroy all DB/client-side projection state and reconnect as a freshly started service.
    source.close();
    openStore();
    time.now = time.now.plusSeconds(2);
    drain(composite(secondary));
    assertNull(secondary.get(hash));
  }

  @Test
  void enqueueDuringPublishRetainsNewUpdateOrRevocationWithoutDeadlock() throws Exception {
    for (boolean revoke : List.of(false, true)) {
      var ready = new CountDownLatch(1);
      var release = new CountDownLatch(1);
      var delayed = spy(secondary);
      doAnswer(
              call -> {
                ready.countDown();
                assertTrue(release.await(5, TimeUnit.SECONDS));
                return call.callRealMethod();
              })
          .when(delayed)
          .put(eq(hash), any());
      var kv = composite(delayed);
      store.tenant(
          tenant,
          () -> {
            kv.put(hash, IndependentReviewProbeTest.value(tenant, "old"));
            return null;
          });
      try (var threads = Executors.newVirtualThreadPerTaskExecutor()) {
        var publish =
            threads.submit(
                () -> {
                  // Prior tests leave tenant-discovery metadata; advance across its bounded pages.
                  for (int page = 0; page < 10 && ready.getCount() > 0; page++) kv.forwardOnce();
                });
        try {
          assertTrue(ready.await(5, TimeUnit.SECONDS));
          var write =
              threads.submit(
                  () ->
                      store.tenant(
                          tenant,
                          () -> {
                            if (revoke) kv.delete(hash);
                            else kv.put(hash, IndependentReviewProbeTest.value(tenant, "new"));
                            return null;
                          }));
          assertThrows(TimeoutException.class, () -> write.get(100, TimeUnit.MILLISECONDS));
          release.countDown();
          publish.get(5, TimeUnit.SECONDS);
          write.get(5, TimeUnit.SECONDS);
          assertEquals(1, pending(tenant));
          drain(composite(secondary));
          if (revoke) assertNull(secondary.get(hash));
          else assertEquals("new", secondary.get(hash).path("accessToken").asString());
        } finally {
          release.countDown();
        }
      }
    }
  }

  @Test
  void tenantDirectoryDiscoveryIsFairAndPayloadClaimsRemainUnderForceRls() {
    var tenants = new ArrayList<UUID>();
    var keys = new HashMap<UUID, String>();
    var bad = new AtomicBoolean(true);
    var target = spy(secondary);
    String failingHash = Secrets.hash(Secrets.random());
    doAnswer(
            call -> {
              if (bad.get()) throw new IllegalStateException("one key unavailable");
              return call.callRealMethod();
            })
        .when(target)
        .put(eq(failingHash), any());
    var kv = composite(target);
    for (int i = 0; i < 40; i++) {
      UUID id = UUID.randomUUID();
      tenants.add(id);
      String key = i == 0 ? failingHash : Secrets.hash(Secrets.random());
      keys.put(id, key);
      store.tenant(
          id,
          () -> {
            kv.put(key, IndependentReviewProbeTest.value(id, "tenant"));
            return null;
          });
    }
    for (UUID id : tenants) {
      assertEquals(1, pending(id));
      store.tenant(
          id,
          () -> {
            assertEquals(keys.get(id), store.claimSessionProjection(time.instant()).hash());
            return null;
          });
    }
    for (int i = 0; i < 5; i++) kv.forwardOnce();
    for (UUID id : tenants) assertEquals(keys.get(id).equals(failingHash) ? 1 : 0, pending(id));
    bad.set(false);
    time.now = time.now.plusSeconds(2);
    for (int i = 0; i < 5; i++) kv.forwardOnce();
    for (UUID id : tenants) assertEquals(0, pending(id));
    var flags =
        new JdbcTemplate(roles.owner().dataSource())
            .queryForMap(
                "SELECT relrowsecurity,relforcerowsecurity FROM pg_class WHERE oid='iam.session_projection'::regclass");
    assertEquals(Boolean.TRUE, flags.get("relrowsecurity"));
    assertEquals(Boolean.TRUE, flags.get("relforcerowsecurity"));
  }
}
