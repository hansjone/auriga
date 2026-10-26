package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.error.AurigaException;
import auriga.common.persistence.*;
import auriga.contracts.iam.v1.*;
import auriga.testsupport.PostgresExtension;
import java.time.*;
import java.util.*;
import java.util.concurrent.*;
import java.util.function.Supplier;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;

/** Real database kernel tests. verified() inputs are fixtures, NOT identity/service-auth proof. */
@ExtendWith(PostgresExtension.class)
class NetworkSourceStorePostgresTest {
  static IamStore store;
  static JdbcTemplate jdbc;
  static JdbcTemplate ownerJdbc;
  final UUID tenant = UuidV7.next(),
      owner = UuidV7.next(),
      device = UuidV7.next(),
      session = UuidV7.next();
  final TestClock clock = new TestClock();
  IamNetworkSources sources;
  NetworkAuthorityConnection connection;
  NetworkAgentSourceRegistration registration;

  @BeforeAll
  static void migrate() {
    var roles =
        PostgresExtension.serviceRoles(
            NetworkSourceStorePostgresTest.class, "source_owner", "source_app", "iam");
    CommonMigrations.migrate(roles.owner().dataSource(), "iam");
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas("iam")
                .locations("classpath:db/iam")
                .load())
        .migrate();
    jdbc = new JdbcTemplate(roles.runtime().dataSource());
    ownerJdbc = new JdbcTemplate(roles.owner().dataSource());
    store = new IamStore(jdbc, new TenantTransactionManager(roles.runtime().dataSource()));
  }

  @BeforeEach
  void setup() {
    sources = new IamNetworkSources(store, clock);
    String realm =
        tx(
            () ->
                jdbc.queryForObject(
                    "SELECT (nextval('iam.network_realm_number')::numeric+9223372036854775809)::text",
                    String.class));
    connection =
        NetworkAuthorityConnection.newBuilder()
            .setTenantId(tenant.toString())
            .setRealm(realm)
            .setNode("1")
            .setIngress("a".repeat(26))
            .setServerId("N" + "A".repeat(55))
            .setClientId("1")
            .setClientAccount("TEST")
            .build();
    registration =
        NetworkAgentSourceRegistration.newBuilder()
            .setRegistrationId("registration")
            .setAllocationOpId("allocation")
            .setAllocationFingerprint("sha256:" + "a".repeat(64))
            .setRegistrationRevision(1)
            .setRealm(realm)
            .setNode("1")
            .setLocal("1")
            .setAddressType("session")
            .build();
    tx(
        () -> {
          UUID provider = UuidV7.next();
          store.insert("account", owner.toString(), Json.object("status", "active"), null);
          store.insert("idp_provider", provider.toString(), Json.object(), null);
          store.insert(
              "session",
              session.toString(),
              Json.object("accountId", owner, "providerId", provider, "sessionId", session),
              clock.instant().plusSeconds(3600));
          store.insert(
              "device",
              device.toString(),
              Json.object("accountId", owner, "sessionId", session),
              null);
          jdbc.update(
              "INSERT INTO iam.network_realm(tenant_id,realm) VALUES (?,?)",
              tenant,
              new java.math.BigDecimal(connection.getRealm()));
          jdbc.update(
              "INSERT INTO iam.network_node(tenant_id,node,account_id,device_id) VALUES (?,1,?,?)",
              tenant,
              owner,
              device);
          ingress(connection);
          return null;
        });
  }

  void ingress(NetworkAuthorityConnection c) {
    store.insert(
        "network_ingress",
        c.getIngress(),
        Json.object(
            "realm",
            c.getRealm(),
            "node",
            c.getNode(),
            "device",
            device,
            "serverId",
            c.getServerId(),
            "clientId",
            c.getClientId(),
            "account",
            c.getClientAccount(),
            "userNkey",
            "U" + "A".repeat(55),
            "audience",
            "test",
            "expiresAt",
            clock.instant().plusSeconds(3600).toString()),
        clock.instant().plusSeconds(3600));
    jdbc.update(
        "INSERT INTO iam.network_ingress_tenant(tenant_id) VALUES (?) ON CONFLICT DO NOTHING",
        tenant);
  }

  <T> T tx(Supplier<T> work) {
    return store.tenant(tenant, work);
  }

  IamNetworkSources.VerifiedSnapshot fixture(
      NetworkAuthorityConnection c, NetworkAgentSourceRegistration r) {
    // Future authority must supply full fresh checks and hold session/device/policy locks here.
    return sources.verified(c, r, owner, device, session, clock.instant().plusSeconds(30));
  }

  RegisterAgentSourceRequest request(String op) {
    return RegisterAgentSourceRequest.newBuilder()
        .setConnection(connection)
        .setRegistration(registration)
        .setOperationId(op)
        .setScopeValue(1)
        .build();
  }

  RegisterAgentSourceResponse register(RegisterAgentSourceRequest r) {
    return tx(() -> sources.register(r, fixture(r.getConnection(), r.getRegistration())));
  }

  IamNetworkSources.View get(NetworkAgentSource s) {
    return tx(
        () ->
            sources.get(
                GetAgentSourceRequest.newBuilder()
                    .setSourceBinding(s.getSourceBinding())
                    .setConnection(connection)
                    .setRegistration(registration)
                    .build(),
                fixture(connection, registration)));
  }

  RevokeAgentSourceRequest revokeRequest(NetworkAgentSource s, String op) {
    return RevokeAgentSourceRequest.newBuilder()
        .setSourceBinding(s.getSourceBinding())
        .setConnection(connection)
        .setExpectedRevision(s.getRevision())
        .setOperationId(op)
        .build();
  }

  RegisterAgentSourceRequest successor(NetworkAgentSource s, String op) {
    return request(op).toBuilder()
        .setPredecessorBinding(s.getSourceBinding())
        .setExpectedRevision(s.getRevision())
        .build();
  }

  NetworkAgentSource expired(NetworkAgentSource s) {
    clock.advance(31);
    var result = get(s).source();
    assertEquals(3, result.getStateValue());
    assertEquals(2, result.getRevision());
    return result;
  }

  static void code(String expected, Runnable action) {
    assertEquals(expected, assertThrows(AurigaException.class, action::run).errorCode().code());
  }

  @Test
  void healthyConnectionGetsNewGenerationAndOldReceiptsNeverChange() {
    var r1 = request("first");
    var b1 = register(r1).getSource();
    code("common.conflict", () -> register(request("duplicate-first")));
    code("common.conflict", () -> register(successor(b1, "active-predecessor")));
    var terminal = expired(b1);
    var r2 = successor(terminal, "second");
    var b2 = register(r2).getSource();
    assertNotEquals(b1.getSourceBinding(), b2.getSourceBinding());
    assertEquals(b1.getSourceBinding(), b2.getPredecessorBinding());
    assertEquals(connection, b2.getOriginalConnection());
    assertEquals(registration, b2.getRegistration());
    assertEquals(1, b2.getRevision());
    assertTrue(Instant.parse(b2.getExpiresAt()).isAfter(Instant.parse(b1.getExpiresAt())));
    assertEquals(b1, tx(() -> sources.registerReceipt(r1)).getSource());
    assertEquals(terminal, get(b1).source());
    code("common.conflict", () -> register(successor(terminal, "fork")));
    code(
        "common.conflict",
        () -> register(r2.toBuilder().setExpectedRevision(1).setOperationId("stale").build()));
  }

  @Test
  void lostResponseSurvivesRealV7IngressCleanupButUnknownOperationDoesNot() {
    var r = request("lost-response");
    var original = register(r);
    clock.advance(3601);
    assertTrue(new NetworkIngressCleanup(store, clock).sweepOnce() > 0);
    assertNull(tx(() -> store.get("network_ingress", connection.getIngress(), false)));
    assertEquals(original, tx(() -> sources.register(r, null)));
    assertNull(tx(() -> sources.registerReceipt(request("unknown"))));
    code("common.unavailable", () -> tx(() -> sources.register(request("unknown"), null)));
    code("common.forbidden", () -> register(request("unknown-fresh-fixture")));
    code(
        "common.conflict",
        () ->
            tx(
                () ->
                    sources.registerReceipt(
                        r.toBuilder()
                            .setExpectedRevision(1)
                            .setPredecessorBinding("different")
                            .build())));
    var stop = revokeRequest(original.getSource(), "stop-after-cleanup");
    var response = tx(() -> sources.revoke(stop));
    assertEquals(3, response.getSource().getStateValue());
    assertEquals(response, tx(() -> sources.revoke(stop)));
    assertEquals(original, tx(() -> sources.registerReceipt(r)));
  }

  @Test
  void revokingExpiredPredecessorStopsAndRevokesActiveSuccessorAtomically() {
    var r = request("first");
    var b1 = register(r).getSource();
    var e1 = expired(b1);
    var b2 = register(successor(e1, "second")).getSource();
    var stop = revokeRequest(e1, "stop");
    var response = tx(() -> sources.revoke(stop));
    assertEquals(e1, response.getSource());
    var head = get(b2);
    assertTrue(head.stopped());
    assertEquals(2, head.source().getStateValue());
    assertEquals(2, head.source().getRevision());
    assertEquals(response, tx(() -> sources.revoke(stop)));
    code("common.conflict", () -> register(successor(head.source(), "after-stop")));
    assertEquals(b1, tx(() -> sources.registerReceipt(r)).getSource());
    assertEquals(
        response.getSource(),
        tx(() -> sources.revoke(revokeRequest(e1, "stop-again"))).getSource());
  }

  @Test
  void concurrentSameOperationSameBodyHasOneReceiptAndChangedBodyConflicts() throws Exception {
    var r = request("same");
    var results = race(() -> register(r), () -> register(r));
    assertEquals(results.getFirst(), results.getLast());
    assertEquals(1, count("network_source"));
    assertEquals(1, count("network_source_operation"));
    var changed = r.toBuilder().setRegistration(registration.toBuilder().setLocal("2")).build();
    code("common.conflict", () -> register(changed));
    var other = request("different-op");
    var outcomes =
        race(
            () -> outcome(() -> register(other)),
            () ->
                outcome(
                    () ->
                        register(
                            other.toBuilder()
                                .setRegistration(registration.toBuilder().setLocal("2"))
                                .build())));
    assertEquals(List.of("common.conflict", "common.conflict"), outcomes);
  }

  @Test
  void concurrentDifferentBodiesForUncommittedOperationCommitExactlyOne() throws Exception {
    var r = request("new-operation");
    var r2 =
        r.toBuilder()
            .setRegistration(registration.toBuilder().setRegistrationId("another"))
            .build();
    var results = race(() -> outcome(() -> register(r)), () -> outcome(() -> register(r2)));
    assertEquals(1, results.stream().filter("ok"::equals).count());
    assertEquals(1, results.stream().filter("common.conflict"::equals).count());
    assertEquals(1, count("network_source"));
    assertEquals(1, count("network_source_chain"));
  }

  @Test
  void concurrentSuccessorsHaveOneWinnerAndStopRaceLeavesNoActiveHead() throws Exception {
    var e1 = expired(register(request("first")).getSource());
    var results =
        race(
            () -> outcome(() -> register(successor(e1, "next-a"))),
            () -> outcome(() -> register(successor(e1, "next-b"))));
    assertEquals(1, results.stream().filter("ok"::equals).count());
    assertEquals(1, results.stream().filter("common.conflict"::equals).count());
    assertEquals(2, count("network_source"));
    tx(() -> sources.revoke(revokeRequest(e1, "stop")));
    assertEquals(
        0,
        tx(
            () ->
                jdbc.queryForObject(
                    "SELECT count(*) FROM iam.network_source WHERE tenant_id=? AND state=1",
                    Integer.class,
                    tenant)));
  }

  @Test
  void stopRacingNewGenerationAlwaysStopsLineage() throws Exception {
    var e1 = expired(register(request("first")).getSource());
    var stop = revokeRequest(e1, "stop");
    var results =
        race(
            () -> outcome(() -> register(successor(e1, "next"))),
            () -> outcome(() -> tx(() -> sources.revoke(stop))));
    assertEquals("ok", results.getLast());
    assertTrue(Set.of("ok", "common.conflict").contains(results.getFirst()));
    assertEquals(
        0,
        tx(
            () ->
                jdbc.queryForObject(
                    "SELECT count(*) FROM iam.network_source WHERE tenant_id=? AND state=1",
                    Integer.class,
                    tenant)));
    assertTrue(get(e1).stopped());
    code("common.conflict", () -> register(successor(e1, "late")));
  }

  @Test
  void forceRlsAndExplicitTenantChecksHideAllFourTables() {
    var r = request("first");
    var source = register(r).getSource();
    assertEquals(
        Boolean.FALSE,
        jdbc.queryForObject(
            "SELECT rolsuper OR rolbypassrls FROM pg_roles WHERE rolname=current_user",
            Boolean.class));
    assertEquals(
        4,
        jdbc.queryForObject(
            "SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='iam' AND c.relname IN ('network_source','network_source_chain','network_source_anchor','network_source_operation') AND c.relrowsecurity AND c.relforcerowsecurity",
            Integer.class));
    for (String table : tables())
      assertEquals(0, jdbc.queryForObject("SELECT count(*) FROM iam." + table, Integer.class));
    store.tenant(
        UuidV7.next(),
        () -> {
          for (String table : tables())
            assertEquals(
                0, jdbc.queryForObject("SELECT count(*) FROM iam." + table, Integer.class));
          code("common.forbidden", () -> sources.registerReceipt(r));
          code("common.forbidden", () -> sources.revoke(revokeRequest(source, "foreign")));
          return null;
        });
  }

  @Test
  void changedOwnerDeviceSessionAndMissingOwnershipAnchorFailClosed() {
    var r = request("first");
    var b = register(r).getSource();
    UUID other = UuidV7.next();
    tx(
        () -> {
          store.insert("account", other.toString(), Json.object("status", "active"), null);
          return null;
        });
    var original = tx(() -> store.get("device", device.toString(), false));
    for (String field : List.of("accountId", "sessionId")) {
      tx(
          () -> {
            var d = original.deepCopy();
            d.put(field, other.toString());
            store.save("device", d);
            return null;
          });
      code("common.forbidden", () -> tx(() -> sources.registerReceipt(r)));
      code("common.forbidden", () -> tx(() -> sources.revoke(revokeRequest(b, "bad-owner"))));
      tx(
          () -> {
            store.save("device", original);
            return null;
          });
    }
    tx(
        () -> {
          jdbc.update("UPDATE iam.network_node SET account_id=? WHERE tenant_id=?", other, tenant);
          return null;
        });
    code("common.forbidden", () -> tx(() -> sources.registerReceipt(r)));
    tx(
        () -> {
          jdbc.update("UPDATE iam.network_node SET account_id=? WHERE tenant_id=?", owner, tenant);
          store.delete("session", session.toString());
          return null;
        });
    code("common.forbidden", () -> tx(() -> sources.registerReceipt(r)));
    assertThrows(
        org.springframework.dao.DataAccessException.class,
        () ->
            tx(
                () ->
                    jdbc.update(
                        "DELETE FROM iam.network_source_anchor WHERE tenant_id=?", tenant)));
  }

  @Test
  void revocationAndIdentityExpiryDoNotBlockOriginalReceiptButFactsRemainTerminal() {
    var r = request("first");
    var b = register(r).getSource();
    tx(
        () -> {
          for (String table : List.of("session", "device", "account")) {
            var id = table.equals("session") ? session : table.equals("device") ? device : owner;
            var row = store.get(table, id.toString(), true);
            row.put("revoked", true);
            row.put("status", "disabled");
            store.save(table, row);
          }
          return null;
        });
    assertEquals(b, tx(() -> sources.registerReceipt(r)).getSource());
    var revoked = tx(() -> sources.revoke(revokeRequest(b, "revoke"))).getSource();
    assertEquals(2, revoked.getStateValue());
    clock.advance(90);
    assertEquals(
        revoked, get(b).source()); // Fixture still attests identity; only kernel state is tested.
    clock.advance(-180);
    assertEquals(revoked, get(b).source());
  }

  @Test
  void expirationIsPersistedOnceAndClockRollbackNeverRevivesIt() {
    var b = register(request("first")).getSource();
    var expired = expired(b);
    clock.advance(-60);
    assertEquals(expired, get(b).source());
    assertEquals(expired, get(b).source());
  }

  @Test
  void transactionFailureRollsBackAnchorSourceHeadAndOperationAndNeverReusesBinding() {
    var exposed = new java.util.concurrent.atomic.AtomicReference<NetworkAgentSource>();
    assertThrows(
        IllegalStateException.class,
        () ->
            tx(
                () -> {
                  exposed.set(
                      sources
                          .register(request("aborted"), fixture(connection, registration))
                          .getSource());
                  throw new IllegalStateException("fault before commit");
                }));
    for (String table : tables()) assertEquals(0, count(table));
    assertNull(tx(() -> sources.registerReceipt(request("aborted"))));
    var committed = register(request("aborted")).getSource();
    assertNotEquals(exposed.get().getSourceBinding(), committed.getSourceBinding());
    var stop = revokeRequest(committed, "aborted-stop");
    assertThrows(
        IllegalStateException.class,
        () ->
            tx(
                () -> {
                  sources.revoke(stop);
                  throw new IllegalStateException("fault after stop");
                }));
    assertEquals(1, get(committed).source().getStateValue());
    assertFalse(get(committed).stopped());
    assertEquals(1, count("network_source_operation"));
  }

  @Test
  void reconnectMayReadSameIdentityButCannotRewriteOriginalConnection() {
    var r = request("first");
    var b = register(r).getSource();
    var c2 = connection.toBuilder().setIngress("b".repeat(26)).setClientId("2").build();
    tx(
        () -> {
          ingress(c2);
          return null;
        });
    var view =
        tx(
            () ->
                sources.get(
                    GetAgentSourceRequest.newBuilder()
                        .setSourceBinding(b.getSourceBinding())
                        .setConnection(c2)
                        .setRegistration(registration)
                        .build(),
                    fixture(c2, registration)));
    assertEquals(b, view.source());
    code(
        "common.conflict",
        () -> tx(() -> sources.registerReceipt(r.toBuilder().setConnection(c2).build())));
    code(
        "common.forbidden",
        () ->
            tx(
                () ->
                    sources.revoke(
                        revokeRequest(b, "unverified-reconnect").toBuilder()
                            .setConnection(c2)
                            .build())));
  }

  @Test
  void staleOrMissingSnapshotsAndBadDeadlinesCannotCreateAnything() {
    var snapshot = tx(() -> fixture(connection, registration));
    code("common.unavailable", () -> tx(() -> sources.register(request("stale"), snapshot)));
    code(
        "common.validation_failed",
        () ->
            tx(
                () ->
                    sources.register(
                        request("bad-time"),
                        sources.verified(
                            connection, registration, owner, device, session, clock.instant()))));
    code(
        "common.validation_failed",
        () ->
            tx(
                () ->
                    sources.register(
                        request("fractional"),
                        sources.verified(
                            connection,
                            registration,
                            owner,
                            device,
                            session,
                            clock.instant().plusNanos(1)))));
    assertEquals(0, count("network_source"));
  }

  @Test
  void faultInsideOperationInsertRollsBackEveryEarlierWrite() {
    ownerJdbc.execute(
        "CREATE FUNCTION iam.source_test_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test operation write fault'; END $$");
    ownerJdbc.execute(
        "CREATE TRIGGER source_test_fail BEFORE INSERT ON iam.network_source_operation FOR EACH ROW EXECUTE FUNCTION iam.source_test_fail()");
    try {
      tx(
          () -> {
            assertThrows(
                org.springframework.dao.DataAccessException.class,
                () ->
                    sources.register(request("injected-fault"), fixture(connection, registration)));
            // A caller catching the error cannot commit the earlier anchor/source/head writes.
            for (String table : tables()) assertEquals(0, count(table));
            return null;
          });
      for (String table : tables()) assertEquals(0, count(table));
    } finally {
      ownerJdbc.execute("DROP TRIGGER source_test_fail ON iam.network_source_operation");
      ownerJdbc.execute("DROP FUNCTION iam.source_test_fail()");
    }
    assertNotNull(register(request("injected-fault")));
  }

  @Test
  void runtimeCannotRebindOrDeleteFactsDisableRlsOrInsertOtherTenant() {
    var b = register(request("first")).getSource();
    for (String table : tables()) {
      sqlState(
          "23514",
          () -> tx(() -> jdbc.update("DELETE FROM iam." + table + " WHERE tenant_id=?", tenant)));
    }
    sqlState(
        "23514",
        () ->
            tx(
                () ->
                    jdbc.update(
                        "UPDATE iam.network_source SET fact='\\x00'::bytea WHERE tenant_id=?",
                        tenant)));
    sqlState(
        "42501",
        () ->
            tx(
                () ->
                    jdbc.update(
                        "INSERT INTO iam.network_source_anchor(id,tenant_id,ingress_id,connection,realm,node,owner_id,device_id,session_id) VALUES (?,?,?,?::bytea,1,1,?,?,?)",
                        UuidV7.next(),
                        UuidV7.next(),
                        UuidV7.next(),
                        new byte[] {1},
                        owner,
                        device,
                        session)));
    sqlState(
        "42501", () -> jdbc.execute("ALTER TABLE iam.network_source DISABLE ROW LEVEL SECURITY"));
    var terminal = tx(() -> sources.revoke(revokeRequest(b, "stop"))).getSource();
    sqlState(
        "23514",
        () ->
            tx(
                () ->
                    jdbc.update(
                        "UPDATE iam.network_source SET state=1,revision=revision+1 WHERE tenant_id=?",
                        tenant)));
    assertEquals(terminal, get(b).source());
  }

  static void sqlState(String expected, Runnable work) {
    var failure = assertThrows(org.springframework.dao.DataAccessException.class, work::run);
    assertEquals(
        expected,
        assertInstanceOf(java.sql.SQLException.class, failure.getMostSpecificCause())
            .getSQLState());
  }

  @Test
  void exactRevokeOperationReceiptSurvivesLaterCasAndMethodNamespacesAreIndependent() {
    var r = request("same-id");
    var b = register(r).getSource();
    var revoke = revokeRequest(b, "same-id");
    var response = tx(() -> sources.revoke(revoke));
    assertEquals(2, response.getSource().getStateValue());
    assertEquals(2, count("network_source_operation"));
    assertEquals(response, tx(() -> sources.revoke(revoke)));
    code(
        "common.conflict",
        () -> tx(() -> sources.revoke(revoke.toBuilder().setExpectedRevision(2).build())));
    code(
        "common.conflict",
        () -> tx(() -> sources.revoke(revoke.toBuilder().setOperationId("stale-cas").build())));
    assertEquals(b, tx(() -> sources.registerReceipt(r)).getSource());
  }

  @Test
  void fullResolveTargetAndSourceRegisterBothCommitWithContendedIdentity() throws Exception {
    resolveWithContendedSource(false);
  }

  @Test
  void fullResolveTargetAndSourceGetBothCommitWithContendedIdentity() throws Exception {
    resolveWithContendedSource(true);
  }

  private void resolveWithContendedSource(boolean reading) throws Exception {
    prepareLiveBacking();
    var originalRequest = request("lock-order");
    var original = reading ? register(originalRequest).getSource() : null;
    var authorityLocked = new CountDownLatch(1);
    var sessionLocked = new CountDownLatch(1);
    var resolvePid = new java.util.concurrent.atomic.AtomicInteger();
    var credentialReleased = new java.util.concurrent.atomic.AtomicBoolean();
    var credentials = spy(realCredentials());
    doAnswer(
            invocation -> {
              var result = invocation.callRealMethod();
              assertNotNull(result);
              store.requireTransactionReleased(); // Actual credential transaction has completed.
              credentialReleased.set(true);
              return result;
            })
        .when(credentials)
        .credential(tenant.toString(), owner.toString(), device.toString());
    var observedStore = spy(store);
    doAnswer(
            invocation -> {
              invocation
                  .callRealMethod(); // Observe the real final authority advisory lock, not fake
              // SQL.
              assertTrue(credentialReleased.get());
              resolvePid.set(jdbc.queryForObject("SELECT pg_backend_pid()", Integer.class));
              authorityLocked.countDown();
              awaitLatch(sessionLocked);
              return null;
            })
        .when(observedStore)
        .lock("network-authority");
    var network = new IamNetworkAuthority(observedStore, credentials, null, clock, Set.of("test"));
    try (var executor = Executors.newFixedThreadPool(2)) {
      var source =
          executor.submit(
              () -> {
                awaitLatch(authorityLocked);
                return tx(
                    () -> {
                      int sourcePid = jdbc.queryForObject("SELECT pg_backend_pid()", Integer.class);
                      store.byId("session", session.toString(), true);
                      sessionLocked.countDown();
                      // Poll in THIS transaction: the identity lock remains held and no extra pool
                      // slot is needed.
                      // Both old and fixed implementations wait for session; never wait for the old
                      // ingress stage.
                      org.awaitility.Awaitility.await()
                          .pollInSameThread()
                          .atMost(Duration.ofSeconds(5))
                          .until(
                              () ->
                                  Boolean.TRUE.equals(
                                      jdbc.queryForObject(
                                          "SELECT EXISTS(SELECT 1 FROM unnest(pg_blocking_pids(?)) AS p(pid) WHERE pid=?)",
                                          Boolean.class,
                                          resolvePid.get(),
                                          sourcePid)));
                      if (reading)
                        return sources
                            .get(
                                GetAgentSourceRequest.newBuilder()
                                    .setSourceBinding(original.getSourceBinding())
                                    .setConnection(connection)
                                    .setRegistration(registration)
                                    .build(),
                                fixture(connection, registration))
                            .source();
                      return sources
                          .register(originalRequest, fixture(connection, registration))
                          .getSource();
                    });
              });
      var resolve = executor.submit(() -> resolve(network));
      try {
        // Deadlock/lock timeout is a failure, never an accepted alternative result or retry.
        var result = source.get(15, TimeUnit.SECONDS);
        assertEquals(clock.instant().plusSeconds(1800), resolve.get(15, TimeUnit.SECONDS));
        assertEquals(reading ? original : result, get(result).source());
        assertEquals(result, tx(() -> sources.registerReceipt(originalRequest)).getSource());
        for (String table : tables()) assertEquals(1, count(table));
        verify(credentials).credential(tenant.toString(), owner.toString(), device.toString());
      } finally {
        authorityLocked.countDown();
        sessionLocked.countDown();
        source.cancel(true);
        resolve.cancel(true);
      }
    }
  }

  @Test
  void fullResolveTargetStillRejectsIdentityIngressAndDeadlineChangesDuringCredentialCheck() {
    prepareLiveBacking();
    UUID otherOwner = UuidV7.next();
    tx(
        () -> {
          store.insert("account", otherOwner.toString(), Json.object("status", "active"), null);
          return null;
        });
    assertEquals(
        clock.instant().plusSeconds(1800),
        resolve(new IamNetworkAuthority(store, realCredentials(), null, clock, Set.of("test"))));
    for (String[] change :
        List.of(
            new String[] {"device", device.toString(), "accountId", otherOwner.toString()},
            new String[] {"device", device.toString(), "sessionId", UuidV7.next().toString()},
            new String[] {"session", session.toString(), "accountId", otherOwner.toString()},
            new String[] {
              "session", session.toString(), "idleExpiresAt", clock.instant().toString()
            },
            new String[] {"account", owner.toString(), "status", "disabled"},
            new String[] {"network_ingress", connection.getIngress(), "clientId", "2"},
            new String[] {
              "network_ingress", connection.getIngress(), "expiresAt", clock.instant().toString()
            },
            new String[] {
              "network_ingress",
              connection.getIngress(),
              "expiresAt",
              clock.instant().plusSeconds(7200).toString()
            })) {
      var before = tx(() -> store.get(change[0], change[1], false));
      var credentials = spy(realCredentials());
      doAnswer(
              invocation -> {
                var result = invocation.callRealMethod();
                assertNotNull(result);
                store.requireTransactionReleased();
                tx(
                    () -> {
                      var row = store.get(change[0], change[1], true);
                      row.put(change[2], change[3]);
                      store.save(change[0], row);
                      return null;
                    });
                return result;
              })
          .when(credentials)
          .credential(tenant.toString(), owner.toString(), device.toString());
      try {
        assertNull(
            resolve(new IamNetworkAuthority(store, credentials, null, clock, Set.of("test"))),
            change[0] + "." + change[2]);
      } finally {
        tx(
            () -> {
              store.save(change[0], before);
              return null;
            });
      }
    }
  }

  private void prepareLiveBacking() {
    tx(
        () -> {
          var row = store.byId("session", session.toString(), true);
          row.put("tenantId", tenant.toString());
          row.put("audience", "portal");
          row.put("expiresAt", clock.instant().plusSeconds(3600).toString());
          row.put("idleExpiresAt", clock.instant().plusSeconds(1800).toString());
          store.save("session", row);
          var d = store.byId("device", device.toString(), true);
          d.put("scope", "auriga.connect");
          store.save("device", d);
          return null;
        });
  }

  private DeviceAccessAuthority realCredentials() {
    return new DeviceAccessAuthority(
        store, new SessionManager(store, null, null, clock, null, null), null);
  }

  private Instant resolve(IamNetworkAuthority network) {
    var c = connection;
    return new IamGrpcDevicesSpringTest()
        .as(
            "svc:collab",
            tenant,
            () ->
                network.resolveTarget(
                    new IamNetworkAuthority.TargetBinding(
                        c.getRealm(),
                        c.getNode(),
                        c.getIngress(),
                        c.getServerId(),
                        c.getClientId(),
                        c.getClientAccount())));
  }

  private static void awaitLatch(CountDownLatch latch) {
    try {
      assertTrue(
          latch.await(10, TimeUnit.SECONDS), "Expected real transaction stage was not reached");
    } catch (InterruptedException e) {
      Thread.currentThread().interrupt();
      throw new IllegalStateException(e);
    }
  }

  int count(String table) {
    return tx(
        () ->
            jdbc.queryForObject(
                "SELECT count(*) FROM iam." + table + " WHERE tenant_id=?", Integer.class, tenant));
  }

  List<String> tables() {
    return List.of(
        "network_source_chain",
        "network_source_anchor",
        "network_source",
        "network_source_operation");
  }

  String outcome(Runnable work) {
    try {
      work.run();
      return "ok";
    } catch (AurigaException e) {
      return e.errorCode().code();
    }
  }

  <T> List<T> race(Callable<T> first, Callable<T> second) throws Exception {
    try (var executor = Executors.newFixedThreadPool(2)) {
      var gate = new CountDownLatch(1);
      var a =
          executor.submit(
              () -> {
                gate.await();
                return first.call();
              });
      var b =
          executor.submit(
              () -> {
                gate.await();
                return second.call();
              });
      gate.countDown();
      return List.of(a.get(15, TimeUnit.SECONDS), b.get(15, TimeUnit.SECONDS));
    }
  }

  static final class TestClock extends Clock {
    volatile Instant now = Instant.parse("2026-10-26T00:00:00Z");

    void advance(long seconds) {
      now = now.plusSeconds(seconds);
    }

    @Override
    public ZoneId getZone() {
      return ZoneOffset.UTC;
    }

    @Override
    public Clock withZone(ZoneId zone) {
      return this;
    }

    @Override
    public Instant instant() {
      return now;
    }
  }
}
