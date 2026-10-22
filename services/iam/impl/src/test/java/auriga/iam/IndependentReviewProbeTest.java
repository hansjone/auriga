package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.persistence.CommonMigrations;
import auriga.common.persistence.TenantTransactionManager;
import auriga.testsupport.NatsExtension;
import auriga.testsupport.PostgresExtension;
import com.zaxxer.hikari.HikariDataSource;
import java.time.*;
import java.util.*;
import java.util.concurrent.*;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.transaction.support.TransactionSynchronization;
import org.springframework.transaction.support.TransactionSynchronizationManager;

@ExtendWith({PostgresExtension.class, NatsExtension.class})
class IndependentReviewProbeTest {
  @Test
  void productionSizedPoolReturnsConnectionsBeforeDurableProjection() throws Exception {
    var roles =
        PostgresExtension.serviceRoles(
            IndependentReviewProbeTest.class, "pool_owner", "pool_app", "iam");
    CommonMigrations.migrate(roles.owner().dataSource(), "iam");
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas("iam")
                .locations("classpath:db/iam")
                .load())
        .migrate();
    try (var ds = new HikariDataSource()) {
      ds.setJdbcUrl(roles.runtime().jdbcUrl());
      ds.setMaximumPoolSize(5);
      ds.setPoolName("independent-review-pool");
      // Keep Hikari's production default 30000ms connection timeout.
      assertEquals(30000, ds.getConnectionTimeout());
      var store = new IamStore(new JdbcTemplate(ds), new TenantTransactionManager(ds));
      var primary = new PostgresSessionKv(store, new ContractSchemas(), Clock.systemUTC());
      try (var nats = io.nats.client.Nats.connect(NatsExtension.url())) {
        var secondary = new NatsSessionKv(nats, new ContractSchemas());
        var composite = new CompositeSessionKv(store, primary, secondary);
        var allCommitted = new CyclicBarrier(5);
        var tenant = UUID.randomUUID();
        long started = System.nanoTime();
        composite.start();
        try {
          try (var threads = Executors.newVirtualThreadPerTaskExecutor()) {
            var results = new ArrayList<Future<?>>();
            for (int i = 0; i < 5; i++) {
              String key = Secrets.hash("independent-" + i);
              results.add(
                  threads.submit(
                      () ->
                          store.tenant(
                              tenant,
                              () -> {
                                TransactionSynchronizationManager.registerSynchronization(
                                    new TransactionSynchronization() {
                                      @Override
                                      public void afterCommit() {
                                        try {
                                          allCommitted.await(10, TimeUnit.SECONDS);
                                        } catch (Exception e) {
                                          throw new IllegalStateException(e);
                                        }
                                      }
                                    });
                                composite.put(key, value(tenant, "latest"));
                                return null;
                              })));
            }
            for (var result : results) result.get(10, TimeUnit.SECONDS);
          }
          long elapsed = TimeUnit.NANOSECONDS.toMillis(System.nanoTime() - started);
          assertTrue(elapsed < 10000, "Commit must not wait for the production connection timeout");
          org.awaitility.Awaitility.await()
              .atMost(Duration.ofSeconds(10))
              .untilAsserted(
                  () -> {
                    for (int i = 0; i < 5; i++) {
                      var projected = secondary.get(Secrets.hash("independent-" + i));
                      assertNotNull(projected);
                      assertEquals("latest", projected.path("accessToken").asString());
                    }
                  });
          long mutationStarted = System.nanoTime();
          try (var threads = Executors.newVirtualThreadPerTaskExecutor()) {
            var mutations = new ArrayList<Future<?>>();
            for (int i = 0; i < 5; i++) {
              final int index = i;
              mutations.add(
                  threads.submit(
                      () ->
                          store.tenant(
                              tenant,
                              () -> {
                                String key = Secrets.hash("independent-" + index);
                                if (index % 2 == 0) composite.put(key, value(tenant, "newest"));
                                else composite.delete(key);
                                return null;
                              })));
            }
            for (var mutation : mutations) mutation.get(10, TimeUnit.SECONDS);
          }
          long mutationElapsed = TimeUnit.NANOSECONDS.toMillis(System.nanoTime() - mutationStarted);
          assertTrue(mutationElapsed < 10000);
          org.awaitility.Awaitility.await()
              .atMost(Duration.ofSeconds(10))
              .untilAsserted(
                  () -> {
                    for (int i = 0; i < 5; i++) {
                      var projected = secondary.get(Secrets.hash("independent-" + i));
                      if (i % 2 == 0) {
                        assertNotNull(projected);
                        assertEquals("newest", projected.path("accessToken").asString());
                      } else assertNull(projected);
                    }
                  });
          System.out.printf(
              "REPAIRED_POOL_PROBE elapsed_ms=%d mutation_ms=%d projected=5/5 updates=3/3 revoked=2/2 pool=%d timeout_ms=%d%n",
              elapsed, mutationElapsed, ds.getMaximumPoolSize(), ds.getConnectionTimeout());
        } finally {
          composite.stop();
        }
        assertEquals(0, ds.getHikariPoolMXBean().getActiveConnections());
      }
    }
  }

  static tools.jackson.databind.node.ObjectNode value(UUID tenant, String access) {
    Instant now = Instant.now();
    return Json.object(
        "sessionId",
        UUID.randomUUID(),
        "accountId",
        UUID.randomUUID(),
        "tenantId",
        tenant,
        "audience",
        "admin",
        "accessToken",
        access,
        "accessTokenExpiresAt",
        now.plusSeconds(300).toString(),
        "expiresAt",
        now.plusSeconds(1200).toString(),
        "idleExpiresAt",
        now.plusSeconds(600).toString(),
        "csrfHash",
        Secrets.hash(Secrets.random()),
        "mfa",
        true,
        "authTime",
        now.toString());
  }
}
