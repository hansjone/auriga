package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.context.TenantContext;
import auriga.common.persistence.CommonMigrations;
import auriga.common.persistence.TenantTransactionManager;
import auriga.contracts.iam.v1.*;
import auriga.testsupport.PostgresExtension;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.AtomicInteger;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.ValueSource;
import org.springframework.jdbc.core.JdbcTemplate;

/** Real restricted PG/OAuth/redeem/Resolve/source RPCs. The org response is a counted fixture. */
@ExtendWith(PostgresExtension.class)
class NetworkSourceFencePostgresTest {
  final NetworkSourceAuthorityPostgresTest f = new NetworkSourceAuthorityPostgresTest();
  final AtomicInteger orgCalls = new AtomicInteger();

  @BeforeAll
  static void migrate() {
    var roles =
        PostgresExtension.serviceRoles(
            NetworkSourceFencePostgresTest.class, "fence_owner", "fence_app", "iam");
    CommonMigrations.migrate(roles.owner().dataSource(), "iam");
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas("iam")
                .locations("classpath:db/iam")
                .load())
        .migrate();
    NetworkSourceAuthorityPostgresTest.store =
        new IamStore(
            new JdbcTemplate(roles.runtime().dataSource()),
            new TenantTransactionManager(roles.runtime().dataSource()));
  }

  @BeforeEach
  void realIdentityAndRedemption() throws Exception {
    f.issuedDeviceAndRealRedemption();
    f.authority = spy(f.authority);
    f.sources = spy(f.authority(new IamNetworkSourceAuthority.Policy(30, 5)));
    f.organization =
        request -> {
          NetworkSourceAuthorityPostgresTest.store.requireTransactionReleased();
          orgCalls.incrementAndGet();
          return NetworkSourceAuthorityPostgresTest.allowed();
        };
    f.withRpc(
        new IamGrpcNetworkSources(f.sources),
        (source, network) ->
            TenantContext.run(
                f.tenant,
                () -> {
                  var resolved =
                      network
                          .withDeadlineAfter(5, TimeUnit.SECONDS)
                          .resolveNetworkTarget(
                              ResolveNetworkTargetRequest.newBuilder()
                                  .setConnection(f.connection)
                                  .setLocal("0")
                                  .build());
                  assertTrue(resolved.getAllowed());
                  assertEquals(f.connection, resolved.getConnection());
                  assertTrue(resolved.hasConnectionAnchor());
                  assertEquals(f.connection, resolved.getConnectionAnchor().getConnection());
                }));
    clearInvocations(f.authority, f.sources);
  }

  @AfterEach
  void clear() {
    f.clear();
  }

  static FenceAgentSourceReceiptRequest fence(RegisterAgentSourceRequest r) {
    return FenceAgentSourceReceiptRequest.newBuilder().setOriginalRequest(r).build();
  }

  RegisterAgentSourceResponse fenceRpc(RegisterAgentSourceRequest r) {
    var result = new java.util.concurrent.atomic.AtomicReference<RegisterAgentSourceResponse>();
    f.withRpc(
        stub ->
            result.set(
                TenantContext.call(
                    f.tenant,
                    () ->
                        stub.withDeadlineAfter(5, TimeUnit.SECONDS)
                            .fenceAgentSourceReceipt(fence(r)))));
    return result.get();
  }

  Map<String, List<String>> database() {
    return f.receiptDatabase();
  }

  int count(String table) {
    return f.tx(
        () ->
            NetworkSourceAuthorityPostgresTest.store
                .networkJdbc()
                .queryForObject(
                    "SELECT count(*) FROM iam." + table + " WHERE tenant_id=?",
                    Integer.class,
                    f.tenant));
  }

  void onlyReceiptWrites(Map<String, List<String>> before) {
    var after = database();
    for (var table : before.keySet()) {
      if (Set.of("network_source_anchor", "network_source_operation").contains(table)) {
        assertEquals(before.get(table).size() + 1, after.get(table).size(), table);
        assertTrue(after.get(table).containsAll(before.get(table)), table + " old rows immutable");
      } else
        assertEquals(
            before.get(table), after.get(table), table + " must not change, including xmin");
    }
  }

  void completeFailure(RegisterAgentSourceRequest r, RegisterAgentSourceResponse response) {
    assertEquals(
        RegisterAgentSourceResponse.newBuilder()
            .setDefinitivelyUncommitted(true)
            .setOperationId(r.getOperationId())
            .setRequestDigest(IamNetworkSources.registerDigest(r))
            .setRejectionReason("original_connection_invalid_uncommitted")
            .build(),
        response);
    var stored =
        f.tx(
            () ->
                new IamNetworkSources(NetworkSourceAuthorityPostgresTest.store, f.clock)
                    .registerReceipt(r));
    assertEquals(response, stored, "complete durable original Register receipt");
    assertEquals(1, count("network_source_operation"));
    assertEquals(1, count("network_source_anchor"));
    assertEquals(0, count("network_source"));
    assertEquals(0, count("network_source_chain"));
  }

  void noOnlineWork() {
    assertEquals(0, orgCalls.get());
    verify(f.sources, never()).get(any());
    verify(f.sources, never()).register(any());
    verify(f.authority, never()).sourceSnapshot(any(), anyBoolean());
    verify(f.authority, never()).redeem(any(), any(), any(), any(), any());
  }

  @ParameterizedTest
  @ValueSource(booleans = {false, true})
  void expiredOrActuallySweptMissIsPermanentAndReadable(boolean sweep) {
    var r = f.request("expired-miss");
    f.clock.advance(3601);
    if (sweep) f.cleanExpiredOriginalIngress();
    f.sources = spy(f.authority(new IamNetworkSourceAuthority.Policy(0, 0)));
    var before = database();
    var response = fenceRpc(r);
    completeFailure(r, response);
    onlyReceiptWrites(before);
    var committed = database();
    assertEquals(response, fenceRpc(r));
    f.withRpc(
        stub ->
            TenantContext.run(
                f.tenant,
                () -> {
                  assertEquals(
                      response,
                      stub.withDeadlineAfter(5, TimeUnit.SECONDS)
                          .readAgentSourceReceipt(
                              ReadAgentSourceReceiptRequest.newBuilder()
                                  .setOriginalRequest(r)
                                  .build()));
                  assertEquals(
                      response, stub.withDeadlineAfter(5, TimeUnit.SECONDS).registerAgentSource(r));
                  IamGrpcNetworkAuthoritySpringTest.grpc(
                      io.grpc.Status.Code.ABORTED,
                      () ->
                          stub.withDeadlineAfter(5, TimeUnit.SECONDS)
                              .fenceAgentSourceReceipt(
                                  fence(
                                      r.toBuilder()
                                          .setRegistration(
                                              r.getRegistration().toBuilder().setLocal("2"))
                                          .build())));
                }));
    assertEquals(committed, database());
    // Register replay above is permitted, but cannot perform any issuance checks.
    assertEquals(0, orgCalls.get());
    verify(f.sources, never()).get(any());
    verify(f.authority, never()).sourceSnapshot(any(), anyBoolean());
    f.error(
        "common.forbidden",
        () ->
            f.callers.as(
                "svc:ai-gateway",
                f.tenant,
                () ->
                    f.authority.redeem(
                        f.ticket.ticket(), f.node.node(), f.device, "server-a", f.binding)));
    assertEquals(committed, database(), "consumed ticket cannot recreate original ingress");
  }

  @Test
  void liveMissAndTemporaryDisablingNeverCreateAFence() {
    var r = f.request("still-live");
    f.sources = spy(f.authority(new IamNetworkSourceAuthority.Policy(0, 0)));
    var before = database();
    f.withRpc(
        stub ->
            TenantContext.run(
                f.tenant,
                () ->
                    IamGrpcNetworkAuthoritySpringTest.grpc(
                        io.grpc.Status.Code.PERMISSION_DENIED,
                        () ->
                            stub.withDeadlineAfter(5, TimeUnit.SECONDS)
                                .fenceAgentSourceReceipt(fence(r)))));
    assertEquals(before, database());
    noOnlineWork();
  }

  @Test
  void missingProvenanceFailsClosed() {
    // A pre-V12 connection is valid but has no committed source operation.
    var c = f.connection.toBuilder().setIngress("b".repeat(26)).build();
    var r = f.request("no-evidence").toBuilder().setConnection(c).build();
    f.clock.advance(3601);
    var before = database();
    f.error("common.forbidden", () -> f.call(() -> f.sources.fenceReceipt(fence(r))));
    assertEquals(before, database());
    noOnlineWork();
  }

  @Test
  void everyNonemptyConnectionSubstitutionFailsWithoutWrites() {
    f.clock.advance(3601);
    var r = f.request("tuple");
    var before = database();
    for (var field : f.connection.getDescriptorForType().getFields()) {
      String bad =
          switch (field.getName()) {
            case "tenant_id" -> UUID.randomUUID().toString();
            case "realm", "node", "client_id" -> "18446744073709551615";
            case "ingress" -> "b".repeat(26);
            case "server_id" -> "N" + "B".repeat(55);
            default -> "OTHER";
          };
      var c = f.connection.toBuilder().setField(field, bad).build();
      var altered =
          r.toBuilder()
              .setConnection(c)
              .setRegistration(
                  r.getRegistration().toBuilder().setRealm(c.getRealm()).setNode(c.getNode()))
              .build();
      f.withRpc(
          stub ->
              TenantContext.run(
                  f.tenant,
                  () ->
                      IamGrpcNetworkAuthoritySpringTest.grpc(
                          io.grpc.Status.Code.PERMISSION_DENIED,
                          () ->
                              stub.withDeadlineAfter(5, TimeUnit.SECONDS)
                                  .fenceAgentSourceReceipt(fence(altered)))));
      assertEquals(before, database(), field.getName());
    }
    noOnlineWork();
  }

  @ParameterizedTest
  @ValueSource(
      strings = {
        "device-owner",
        "session-owner",
        "device-session",
        "node-owner",
        "ingress-session",
        "json-expiry",
        "sql-expiry",
        "ingress-pk"
      })
  void changedRealAssociationOrInconsistentIngressCannotProveFailure(String change) {
    var r = f.request("bad-evidence");
    String other =
        f.anotherActiveAccount(); // Real same-tenant FK object, committed before mutation.
    f.clock.advance(3601);
    switch (change) {
      case "device-owner" -> f.mutate("device", f.device, "accountId", other);
      case "session-owner" -> f.mutate("session", f.sessionId, "accountId", other);
      case "device-session" -> {
        var replacement =
            f.tx(
                () -> {
                  var original =
                      NetworkSourceAuthorityPostgresTest.store.byId("session", f.sessionId, false);
                  return f.sessions.create(
                      f.account,
                      UUID.fromString(original.path("providerId").asString()),
                      "portal",
                      true,
                      java.time.Duration.ofHours(2),
                      java.time.Duration.ofHours(1));
                });
        f.mutate(
            "device", f.device, "sessionId", replacement.session().path("sessionId").asString());
      }
      case "node-owner" ->
          f.tx(
              () ->
                  NetworkSourceAuthorityPostgresTest.store
                      .networkJdbc()
                      .update(
                          "UPDATE iam.network_node SET account_id=? WHERE tenant_id=? AND node=?::numeric",
                          UUID.fromString(other),
                          f.tenant,
                          f.node.node()));
      case "ingress-session" ->
          f.mutate(
              "network_ingress",
              f.tx(
                      () ->
                          NetworkSourceAuthorityPostgresTest.store.get(
                              "network_ingress", f.connection.getIngress(), false))
                  .path("id")
                  .asString(),
              "sessionId",
              UUID.randomUUID().toString());
      case "json-expiry" ->
          f.mutate(
              "network_ingress",
              f.tx(
                      () ->
                          NetworkSourceAuthorityPostgresTest.store.get(
                              "network_ingress", f.connection.getIngress(), false))
                  .path("id")
                  .asString(),
              "expiresAt",
              f.clock.instant().plusSeconds(1).toString());
      case "sql-expiry" ->
          f.tx(
              () ->
                  NetworkSourceAuthorityPostgresTest.store
                      .networkJdbc()
                      .update(
                          "UPDATE iam.network_ingress SET expires_at=? WHERE tenant_id=?",
                          java.sql.Timestamp.from(f.clock.instant().plusSeconds(1)),
                          f.tenant));
      default ->
          f.tx(
              () ->
                  NetworkSourceAuthorityPostgresTest.store
                      .networkJdbc()
                      .update(
                          "UPDATE iam.network_ingress SET id=? WHERE tenant_id=?",
                          auriga.common.persistence.UuidV7.next(),
                          f.tenant));
    }
    var before = database();
    f.error("common.forbidden", () -> f.call(() -> f.sources.fenceReceipt(fence(r))));
    assertEquals(before, database());
    noOnlineWork();
  }

  @Test
  void preV12CommittedReceiptStillWinsAfterCleanupAndPolicyDisable() {
    // Explicit pre-V12 fixture: real identity and a separate broker client/ingress,
    // copied from the real redemption without its V12 provenance. Not a new redeem.
    var old = f.connection;
    f.connection =
        old.toBuilder()
            .setIngress("b".repeat(26))
            .setClientId(Long.toString(++f.transportClient))
            .build();
    assertNotEquals(old.getClientId(), f.connection.getClientId());
    f.tx(
        () -> {
          var data =
              NetworkSourceAuthorityPostgresTest.store
                  .get("network_ingress", old.getIngress(), false)
                  .deepCopy();
          data.put("clientId", f.connection.getClientId());
          NetworkSourceAuthorityPostgresTest.store.insert(
              "network_ingress",
              f.connection.getIngress(),
              data,
              java.time.Instant.parse(data.path("expiresAt").asString()));
          return null;
        });
    assertEquals(
        f.connection.getClientId(),
        f.tx(
                () ->
                    NetworkSourceAuthorityPostgresTest.store.get(
                        "network_ingress", f.connection.getIngress(), false))
            .path("clientId")
            .asString());
    assertEquals(
        old.getClientId(),
        f.tx(
                () ->
                    NetworkSourceAuthorityPostgresTest.store.get(
                        "network_ingress", old.getIngress(), false))
            .path("clientId")
            .asString());
    java.util.function.IntSupplier legacyProvenanceCount =
        () ->
            f.tx(
                () ->
                    NetworkSourceAuthorityPostgresTest.store
                        .networkJdbc()
                        .queryForObject(
                            "SELECT count(*) FROM iam.network_connection_anchor WHERE tenant_id=? AND ingress_key=?",
                            Integer.class,
                            f.tenant,
                            f.connection.getIngress()));
    assertEquals(0, legacyProvenanceCount.getAsInt());
    var r = f.request("committed");
    var response = f.call(() -> f.sources.register(r));
    assertTrue(response.hasSource());
    assertFalse(response.getDefinitivelyUncommitted());
    assertEquals(r.getConnection(), response.getSource().getOriginalConnection());
    f.clock.advance(3601);
    f.cleanExpiredOriginalIngress();
    f.sources = spy(f.authority(new IamNetworkSourceAuthority.Policy(0, 0)));
    orgCalls.set(0);
    clearInvocations(f.authority);
    assertEquals(0, legacyProvenanceCount.getAsInt());
    var before = database();
    assertEquals(response, fenceRpc(r));
    assertEquals(before, database());
    noOnlineWork();
  }

  @Test
  void registerFirstFenceReturnsOriginalSuccess() {
    var r = f.request("register-first");
    var response = f.call(() -> f.sources.register(r));
    var before = database();
    f.clock.advance(3601);
    assertEquals(response, fenceRpc(r));
    assertEquals(before, database());
    assertEquals(1, count("network_source"));
    assertEquals(1, count("network_source_operation"));
    assertEquals(1, orgCalls.get());
  }

  @Test
  void registerHoldsRealCommitLocksWhileFenceWaitsThenRecoversSuccess() throws Exception {
    var inserted = new CountDownLatch(1);
    var releaseCommit = new CountDownLatch(1);
    var writerPid = new AtomicInteger();
    var dataSource = NetworkSourceAuthorityPostgresTest.store.networkJdbc().getDataSource();
    var observed =
        new JdbcTemplate(dataSource) {
          @Override
          public int update(String sql, Object... args) {
            int changed = super.update(sql, args);
            if (sql.startsWith("INSERT INTO iam.network_source_operation")) {
              writerPid.set(queryForObject("SELECT pg_backend_pid()", Integer.class));
              inserted.countDown();
              await(releaseCommit); // Source/op exist only in this still-uncommitted real tx.
            }
            return changed;
          }
        };
    var originalStore = NetworkSourceAuthorityPostgresTest.store;
    NetworkSourceAuthorityPostgresTest.store =
        new IamStore(observed, new TenantTransactionManager(dataSource));
    f.sources = f.authority(new IamNetworkSourceAuthority.Policy(30, 5));
    var r = f.request("commit-first");
    try (var executor = Executors.newVirtualThreadPerTaskExecutor()) {
      var register = executor.submit(() -> f.call(() -> f.sources.register(r)));
      try {
        assertTrue(inserted.await(10, TimeUnit.SECONDS));
        var fence = executor.submit(() -> f.call(() -> f.sources.fenceReceipt(fence(r))));
        long until = System.nanoTime() + TimeUnit.SECONDS.toNanos(3);
        boolean blocked = false;
        while (!blocked && System.nanoTime() < until) {
          blocked =
              Boolean.TRUE.equals(
                  new JdbcTemplate(dataSource)
                      .queryForObject(
                          "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE ?=ANY(pg_blocking_pids(pid)))",
                          Boolean.class,
                          writerPid.get()));
          Thread.onSpinWait();
        }
        assertTrue(blocked, "Fence must actually wait on Register's uncommitted locks");
        releaseCommit.countDown();
        var committed = register.get(10, TimeUnit.SECONDS);
        assertTrue(committed.hasSource());
        assertEquals(committed, fence.get(10, TimeUnit.SECONDS));
        assertEquals(1, count("network_source"));
        assertEquals(1, count("network_source_chain"));
        assertEquals(1, count("network_source_operation"));
        assertEquals(1, orgCalls.get());
      } finally {
        releaseCommit.countDown();
      }
    } finally {
      NetworkSourceAuthorityPostgresTest.store = originalStore;
    }
  }

  @Test
  void fenceCommitsWhileOriginalRegisterIsPausedOutsideTransactions() throws Exception {
    var r = f.request("late-register");
    var enteredOrg = new CountDownLatch(1);
    var releaseOrg = new CountDownLatch(1);
    f.organization =
        request -> {
          NetworkSourceAuthorityPostgresTest.store.requireTransactionReleased();
          orgCalls.incrementAndGet();
          enteredOrg.countDown();
          await(releaseOrg);
          return NetworkSourceAuthorityPostgresTest.allowed();
        };
    try (var executor = Executors.newVirtualThreadPerTaskExecutor()) {
      var late = executor.submit(() -> f.call(() -> f.sources.register(r)));
      try {
        assertTrue(enteredOrg.await(10, TimeUnit.SECONDS), "real initial live checks completed");
        f.clock.advance(3601);
        f.cleanExpiredOriginalIngress();
        var before = database();
        var rejected = fenceRpc(r); // Commit completes before old remote check is released.
        completeFailure(r, rejected);
        onlyReceiptWrites(before);
        var committed = database();
        releaseOrg.countDown();
        assertEquals(rejected, late.get(10, TimeUnit.SECONDS));
        assertEquals(committed, database());
        assertEquals(1, orgCalls.get(), "only paused Register called org");
      } finally {
        releaseOrg.countDown();
      }
    }
  }

  @Test
  void sameKeyConcurrentFencesHaveOneCompleteReceipt() throws Exception {
    var r = f.request("competing-fences");
    f.clock.advance(3601);
    var ready = new CountDownLatch(2);
    var start = new CountDownLatch(1);
    var before = database();
    try (var executor = Executors.newVirtualThreadPerTaskExecutor()) {
      Callable<RegisterAgentSourceResponse> work =
          () -> {
            ready.countDown();
            await(start);
            return f.call(() -> f.sources.fenceReceipt(fence(r)));
          };
      var a = executor.submit(work);
      var b = executor.submit(work);
      try {
        assertTrue(ready.await(10, TimeUnit.SECONDS));
        start.countDown();
        var response = a.get(10, TimeUnit.SECONDS);
        assertEquals(response, b.get(10, TimeUnit.SECONDS));
        completeFailure(r, response);
        onlyReceiptWrites(before);
      } finally {
        start.countDown();
      }
    }
    noOnlineWork();
  }

  @Test
  void failureBeforeCommitRollsBackBothReceiptAndAnchor() {
    var r = f.request("rollback");
    f.clock.advance(3601);
    var before = database();
    assertThrows(
        IllegalStateException.class,
        () ->
            f.tx(
                () -> {
                  NetworkSourceAuthorityPostgresTest.store.lock("network-authority");
                  var response =
                      new IamNetworkSources(NetworkSourceAuthorityPostgresTest.store, f.clock)
                          .fence(r);
                  assertTrue(response.getDefinitivelyUncommitted());
                  throw new IllegalStateException("test rollback before COMMIT");
                }));
    assertEquals(before, database());
    completeFailure(r, fenceRpc(r));
  }

  @Test
  void originalOperationLockTimeoutIsUnavailableAndWritesNothing() throws Exception {
    var r = f.request("lock-timeout");
    f.clock.advance(3601);
    var locked = new CountDownLatch(1);
    var release = new CountDownLatch(1);
    var before = database();
    try (var executor = Executors.newVirtualThreadPerTaskExecutor()) {
      var holder =
          executor.submit(
              () ->
                  f.tx(
                      () -> {
                        NetworkSourceAuthorityPostgresTest.store.lock(
                            "network-source-operation:Register:" + r.getOperationId());
                        locked.countDown();
                        await(release);
                        return null;
                      }));
      try {
        assertTrue(locked.await(10, TimeUnit.SECONDS));
        var output = new IamGrpcDevicesSpringTest.Capture<RegisterAgentSourceResponse>();
        f.error(
            "common.unavailable",
            () ->
                f.call(
                    () -> {
                      new IamGrpcNetworkSources(f.sources)
                          .fenceAgentSourceReceipt(fence(r), output);
                      return null;
                    }));
        assertTrue(output.values.isEmpty());
        assertFalse(output.completed);
        assertEquals(before, database());
      } finally {
        release.countDown();
        holder.get(10, TimeUnit.SECONDS);
      }
    }
    noOnlineWork();
  }

  @Test
  void failedOperationAndBothProvenanceKindsAreImmutable() {
    var r = f.request("immutable");
    f.clock.advance(3601);
    completeFailure(r, fenceRpc(r));
    var before = database();
    for (String table :
        List.of("network_source_operation", "network_source_anchor", "network_connection_anchor")) {
      for (String sql :
          List.of(
              "DELETE FROM iam." + table + " WHERE tenant_id=?",
              "UPDATE iam." + table + " SET tenant_id=tenant_id WHERE tenant_id=?")) {
        var error =
            assertThrows(
                org.springframework.dao.DataAccessException.class,
                () ->
                    f.tx(
                        () ->
                            NetworkSourceAuthorityPostgresTest.store
                                .networkJdbc()
                                .update(sql, f.tenant)));
        assertInstanceOf(java.sql.SQLException.class, error.getMostSpecificCause());
        assertEquals("23514", ((java.sql.SQLException) error.getMostSpecificCause()).getSQLState());
        assertEquals(before, database());
      }
    }
    noOnlineWork();
  }

  static void await(CountDownLatch latch) {
    try {
      assertTrue(latch.await(10, TimeUnit.SECONDS), "bounded synchronization");
    } catch (InterruptedException e) {
      Thread.currentThread().interrupt();
      throw new AssertionError(e);
    }
  }
}
