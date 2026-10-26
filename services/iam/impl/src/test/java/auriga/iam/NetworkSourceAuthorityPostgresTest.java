package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.audit.AuditLogger;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.events.EventPublisher;
import auriga.common.grpc.*;
import auriga.common.persistence.*;
import auriga.common.security.*;
import auriga.contracts.iam.v1.*;
import auriga.iam.api.NetworkAuthority.*;
import auriga.testsupport.PostgresExtension;
import com.nimbusds.jose.jwk.Curve;
import com.nimbusds.jose.jwk.ECKey;
import com.nimbusds.jose.jwk.JWKSet;
import com.nimbusds.jose.jwk.gen.ECKeyGenerator;
import java.time.*;
import java.util.*;
import java.util.concurrent.TimeUnit;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.security.core.context.SecurityContextHolder;

/** Real OAuth/redeem/session/PG; org adapter proves the boundary, not live org connectivity. */
@ExtendWith(PostgresExtension.class)
class NetworkSourceAuthorityPostgresTest {
  static IamStore store;
  final UUID tenant = UUID.randomUUID(), account = UUID.randomUUID();
  final NetworkAuthorityTest.Time clock = new NetworkAuthorityTest.Time();
  final IamGrpcDevicesSpringTest callers = new IamGrpcDevicesSpringTest();
  SessionManager sessions;
  TokenService tokens;
  OAuthProvider oauth;
  IamNetworkAuthority authority;
  String device, sessionId, access;
  Node node;
  Ticket ticket;
  IngressBinding binding;
  NetworkAuthorityConnection connection;
  long transportClient = 42;
  ECKey key;
  IamNetworkSourceAuthority sources;
  auriga.iam.api.InstanceAiAuthorization organization =
      request -> {
        store.requireTransactionReleased();
        assertEquals(tenant.toString(), request.getTenantId());
        assertEquals(account.toString(), request.getAccountId());
        return allowed();
      };

  static auriga.contracts.org.v1.GetEffectivePermissionsResponse allowed() {
    return auriga.contracts.org.v1.GetEffectivePermissionsResponse.newBuilder()
        .setMemberTypeValue(1)
        .setStatusValue(1)
        .addPermissions("auriga.connect.collab.use")
        .build();
  }

  @AfterEach
  void clear() {
    SecurityContextHolder.clearContext();
  }

  <T> T call(java.util.function.Supplier<T> work) {
    return callers.as("svc:collab", tenant, work);
  }

  <T> T tx(java.util.function.Supplier<T> work) {
    return store.tenant(tenant, work);
  }

  RegisterAgentSourceRequest request(String op) {
    return RegisterAgentSourceRequest.newBuilder()
        .setConnection(connection)
        .setScopeValue(1)
        .setOperationId(op)
        .setRegistration(
            NetworkAgentSourceRegistration.newBuilder()
                .setRegistrationId("registration")
                .setAllocationOpId("allocation")
                .setAllocationFingerprint("sha256:" + "a".repeat(64))
                .setRegistrationRevision(1)
                .setRealm(connection.getRealm())
                .setNode(connection.getNode())
                .setLocal("1")
                .setAddressType("session"))
        .build();
  }

  GetAgentSourceRequest getRequest(NetworkAgentSource s) {
    return GetAgentSourceRequest.newBuilder()
        .setSourceBinding(s.getSourceBinding())
        .setConnection(connection)
        .setRegistration(s.getRegistration())
        .build();
  }

  RevokeAgentSourceRequest stop(NetworkAgentSource s, String op) {
    return RevokeAgentSourceRequest.newBuilder()
        .setSourceBinding(s.getSourceBinding())
        .setConnection(s.getOriginalConnection())
        .setExpectedRevision(s.getRevision())
        .setOperationId(op)
        .build();
  }

  void mutate(String table, String id, String field, Object value) {
    tx(
        () -> {
          var row = store.byId(table, id, true);
          assertNotNull(row);
          row.set(field, Json.MAPPER.valueToTree(value));
          store.save(table, row);
          return null;
        });
  }

  void error(String code, Runnable work) {
    IamGrpcDevicesSpringTest.error(code, work);
  }

  IngressBinding newTransport(String rawTicket) {
    var initial = NetworkAuthorityTest.binding(rawTicket);
    return new IngressBinding(
        "N" + "B".repeat(55),
        Long.toString(++transportClient),
        initial.userNkey(),
        initial.ingress(),
        initial.account());
  }

  String anotherActiveAccount() {
    String id = UUID.randomUUID().toString();
    tx(() -> store.insert("account", id, Json.object("status", "active"), null));
    return id;
  }

  void cleanExpiredOriginalIngress() {
    var original = tx(() -> store.get("network_ingress", connection.getIngress(), false));
    assertNotNull(original, "Original ingress must exist before V7 cleanup");
    var expires =
        tx(
            () ->
                store
                    .networkJdbc()
                    .queryForObject(
                        "SELECT expires_at FROM iam.network_ingress WHERE tenant_id=? AND lookup_key=?",
                        java.sql.Timestamp.class,
                        tenant,
                        connection.getIngress()));
    assertNotNull(expires);
    assertFalse(expires.toInstant().isAfter(clock.instant()), "Original ingress must be expired");
    Long tenants =
        store
            .networkJdbc()
            .queryForObject("SELECT count(*) FROM iam.network_ingress_tenant", Long.class);
    assertNotNull(tenants);
    assertTrue(tenants > 0);
    long maximumSweeps = (tenants - 1) / 32 + 1;
    var cleanup = new NetworkIngressCleanup(store, clock);
    for (long sweep = 0; sweep < maximumSweeps; sweep++) {
      cleanup.sweepOnce();
      if (tx(() -> store.get("network_ingress", connection.getIngress(), false)) == null) break;
    }
    assertNull(
        tx(() -> store.get("network_ingress", connection.getIngress(), false)),
        "V7 cleanup must actually remove this tenant's original ingress within the directory bound");
  }

  @Test
  void revisionIndependentRevokeAndTerminalPresence() {
    var first = call(() -> sources.register(request("first"))).getSource();
    var active = call(() -> sources.get(getRequest(first)));
    assertTrue(active.hasLineageStopped());
    assertFalse(active.getLineageStopped());
    clock.advance(31);
    var expired = call(() -> sources.get(getRequest(first)));
    assertTrue(expired.hasLineageStopped());
    assertFalse(expired.getLineageStopped());
    assertEquals("", expired.getValidUntil());
    var next =
        request("next").toBuilder()
            .setPredecessorBinding(first.getSourceBinding())
            .setExpectedRevision(expired.getSource().getRevision())
            .build();
    var head = call(() -> sources.register(next)).getSource();
    var revoke = stop(first, "zero-stop").toBuilder().clearExpectedRevision().build();
    var receipt = call(() -> sources.revoke(revoke));
    assertEquals(expired.getSource(), receipt.getSource());
    assertEquals(receipt, call(() -> sources.revoke(revoke)));
    var stopped = call(() -> sources.get(getRequest(first)));
    assertTrue(stopped.hasLineageStopped());
    assertTrue(stopped.getLineageStopped());
    assertEquals(expired.getSource(), stopped.getSource());
    assertEquals("", stopped.getValidUntil());
    var revoked = call(() -> sources.get(getRequest(head)));
    assertTrue(revoked.getLineageStopped());
    assertEquals(2, revoked.getSource().getStateValue());
    assertEquals(2, revoked.getSource().getRevision());
    assertEquals("", revoked.getValidUntil());
    for (long revision : new long[] {0, 1, -1L}) {
      var retry =
          stop(first, "stop-" + Long.toUnsignedString(revision)).toBuilder()
              .setExpectedRevision(revision)
              .build();
      assertEquals(expired.getSource(), call(() -> sources.revoke(retry)).getSource());
    }
  }

  @Test
  void definitiveRejectionsCommitOriginalReceiptsAndNeverIssueOnReplay() {
    var first = call(() -> sources.register(request("first"))).getSource();
    clock.advance(31);
    var expired = call(() -> sources.get(getRequest(first))).getSource();
    var stale =
        request("stale").toBuilder()
            .setPredecessorBinding(first.getSourceBinding())
            .setExpectedRevision(1)
            .build();
    var rejected = call(() -> sources.register(stale));
    assertRejection(stale, rejected, "revision_mismatch");
    var next =
        stale.toBuilder().setOperationId("next").setExpectedRevision(expired.getRevision()).build();
    var head = call(() -> sources.register(next)).getSource();
    var nonHead = next.toBuilder().setOperationId("non-head").build();
    assertRejection(nonHead, call(() -> sources.register(nonHead)), "predecessor_not_head");
    call(() -> sources.revoke(stop(first, "stop")));
    var stopped =
        request("stopped").toBuilder()
            .setPredecessorBinding(head.getSourceBinding())
            .setExpectedRevision(1)
            .build();
    assertRejection(stopped, call(() -> sources.register(stopped)), "lineage_stopped");
    clock.advance(3601);
    cleanExpiredOriginalIngress();
    assertEquals(rejected, call(() -> sources.register(stale)));
    error(
        "common.conflict",
        () -> call(() -> sources.register(stale.toBuilder().setExpectedRevision(2).build())));
    mutate("device", device, "accountId", anotherActiveAccount());
    error("common.forbidden", () -> call(() -> sources.register(stale)));
  }

  void assertRejection(
      RegisterAgentSourceRequest request, RegisterAgentSourceResponse response, String reason) {
    assertFalse(response.hasSource());
    assertTrue(response.getDefinitivelyUncommitted());
    assertEquals(request.getOperationId(), response.getOperationId());
    assertEquals(reason, response.getRejectionReason());
    assertTrue(response.getRequestDigest().matches("sha256:[0-9a-f]{64}"));
    assertEquals(response, call(() -> sources.register(request)));
    assertEquals(
        1,
        tx(
            () ->
                store
                    .networkJdbc()
                    .queryForObject(
                        "SELECT count(*) FROM iam.network_source_operation WHERE tenant_id=? AND method='Register' AND operation_id=?",
                        Integer.class,
                        tenant,
                        request.getOperationId())));
  }

  @Test
  void unknownPredecessorAndDuplicateFirstAreDefinitivelyRejectedAtTheRpc() {
    withRpc(
        stub ->
            TenantContext.run(
                tenant,
                () -> {
                  var unknown =
                      request("unknown-predecessor").toBuilder()
                          .setPredecessorBinding("unknown")
                          .setExpectedRevision(1)
                          .build();
                  var rejected = stub.registerAgentSource(unknown);
                  assertRejection(unknown, rejected, "predecessor_not_head");
                  var first = stub.registerAgentSource(request("first")).getSource();
                  var duplicate = request("duplicate-first");
                  assertRejection(
                      duplicate, stub.registerAgentSource(duplicate), "predecessor_not_head");
                  var active = stub.getAgentSource(getRequest(first));
                  assertTrue(active.hasLineageStopped());
                  assertFalse(active.getLineageStopped());
                  assertFalse(active.getValidUntil().isEmpty());
                  var stopped =
                      stub.revokeAgentSource(
                          stop(first, "stop").toBuilder().clearExpectedRevision().build());
                  assertEquals(2, stopped.getSource().getStateValue());
                  var terminal = stub.getAgentSource(getRequest(first));
                  assertTrue(terminal.hasLineageStopped());
                  assertTrue(terminal.getLineageStopped());
                  assertEquals("", terminal.getValidUntil());
                }));
  }

  @org.junit.jupiter.params.ParameterizedTest
  @org.junit.jupiter.params.provider.ValueSource(booleans = {false, true})
  void registerAndRevisionlessRevokeSerializeOnRealLineageLock(boolean registerFirst)
      throws Exception {
    var first = call(() -> sources.register(request("first"))).getSource();
    clock.advance(31);
    var expired = call(() -> sources.get(getRequest(first))).getSource();
    var next =
        request("next").toBuilder()
            .setPredecessorBinding(first.getSourceBinding())
            .setExpectedRevision(expired.getRevision())
            .build();
    var stop = stop(first, "stop").toBuilder().clearExpectedRevision().build();
    var locked = new java.util.concurrent.CountDownLatch(1);
    var unlock = new java.util.concurrent.CountDownLatch(1);
    try (var executor = java.util.concurrent.Executors.newVirtualThreadPerTaskExecutor()) {
      var holder =
          executor.submit(
              () ->
                  tx(
                      () -> {
                        store
                            .networkJdbc()
                            .queryForObject(
                                "SELECT head::text FROM iam.network_source_chain WHERE tenant_id=? FOR UPDATE",
                                String.class,
                                tenant);
                        locked.countDown();
                        try {
                          assertTrue(unlock.await(10, TimeUnit.SECONDS));
                        } catch (InterruptedException e) {
                          throw new AssertionError(e);
                        }
                        return null;
                      }));
      try {
        assertTrue(locked.await(5, TimeUnit.SECONDS));
        var pending =
            executor.submit(
                () ->
                    registerFirst
                        ? call(() -> sources.register(next))
                        : call(() -> sources.revoke(stop)));
        org.awaitility.Awaitility.await()
            .atMost(Duration.ofSeconds(3))
            .until(
                () -> {
                  try (var c =
                          PostgresExtension.database(NetworkSourceAuthorityPostgresTest.class)
                              .dataSource()
                              .getConnection();
                      var statement = c.createStatement();
                      var rs =
                          statement.executeQuery(
                              "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database()"
                                  + " AND wait_event_type='Lock' AND query LIKE '%network_source_chain%' AND query LIKE '%FOR UPDATE%'")) {
                    return rs.next() && rs.getLong(1) > 0;
                  }
                });
        assertFalse(pending.isDone());
        var started = new java.util.concurrent.CountDownLatch(1);
        var following =
            executor.submit(
                () -> {
                  started.countDown();
                  return registerFirst
                      ? call(() -> sources.revoke(stop))
                      : call(() -> sources.register(next));
                });
        assertTrue(started.await(5, TimeUnit.SECONDS));
        unlock.countDown();
        holder.get(5, TimeUnit.SECONDS);
        var result =
            registerFirst ? pending.get(10, TimeUnit.SECONDS) : following.get(10, TimeUnit.SECONDS);
        var issued = assertInstanceOf(RegisterAgentSourceResponse.class, result);
        var revoked =
            registerFirst ? following.get(10, TimeUnit.SECONDS) : pending.get(10, TimeUnit.SECONDS);
        assertEquals(
            expired, assertInstanceOf(RevokeAgentSourceResponse.class, revoked).getSource());
        if (registerFirst) {
          assertTrue(issued.hasSource());
          var head = call(() -> sources.get(getRequest(issued.getSource())));
          assertEquals(2, head.getSource().getStateValue());
          assertTrue(head.getLineageStopped());
          assertEquals("", head.getValidUntil());
        } else assertRejection(next, issued, "lineage_stopped");
        assertEquals(
            0,
            tx(
                () ->
                    store
                        .networkJdbc()
                        .queryForObject(
                            "SELECT count(*) FROM iam.network_source WHERE tenant_id=? AND state=1",
                            Integer.class,
                            tenant)));
        assertTrue(call(() -> sources.get(getRequest(first))).getLineageStopped());
      } finally {
        unlock.countDown();
      }
    }
  }

  @Test
  void realAuthorityReconnectAndImmutableReceiptWholeSecondDeadlines() {
    call(
        () -> {
          var original = request("first");
          var s = sources.register(original).getSource();
          assertEquals(account.toString(), s.getOwnerUserId());
          assertEquals(0, Instant.parse(s.getExpiresAt()).getNano());
          assertEquals(
              clock.instant().plusSeconds(30).truncatedTo(java.time.temporal.ChronoUnit.SECONDS),
              Instant.parse(s.getExpiresAt()));
          var view = sources.get(getRequest(s));
          assertEquals(
              clock.instant().plusSeconds(5).truncatedTo(java.time.temporal.ChronoUnit.SECONDS),
              Instant.parse(view.getValidUntil()));
          assertTrue(view.getPolicyRevision() > 0);
          assertEquals(s, sources.register(original).getSource());
          var next = authority.prepare(access, node.node(), "server-a");
          var b = newTransport(next.ticket());
          callers.as(
              "svc:ai-gateway",
              tenant,
              () -> authority.redeem(next.ticket(), node.node(), device, "server-a", b));
          connection =
              connection.toBuilder()
                  .setIngress(b.ingress())
                  .setServerId(b.serverId())
                  .setClientId(b.clientId())
                  .setClientAccount(b.account())
                  .build();
          var reconnect = sources.get(getRequest(s));
          assertEquals(connection, reconnect.getConnection());
          assertEquals(s.getOriginalConnection(), reconnect.getSource().getOriginalConnection());
          mutate(
              "session",
              sessionId,
              "idleExpiresAt",
              clock.instant().plusSeconds(2).plusMillis(900).toString());
          assertEquals(
              clock.instant().plusSeconds(2).truncatedTo(java.time.temporal.ChronoUnit.SECONDS),
              Instant.parse(sources.get(getRequest(s)).getValidUntil()));
          error(
              "common.conflict",
              () -> sources.register(original.toBuilder().setConnection(connection).build()));
          return null;
        });
  }

  @Test
  void everyRealTupleFieldAndOwnerDeviceBackingSessionIsChecked() {
    call(
        () -> {
          var source = sources.register(request("first")).getSource();
          for (var field : connection.getDescriptorForType().getFields()) {
            String bad =
                switch (field.getName()) {
                  case "tenant_id" -> UUID.randomUUID().toString();
                  case "realm", "node", "client_id" -> "18446744073709551615";
                  case "ingress" -> "b".repeat(26);
                  case "server_id" -> "N" + "B".repeat(55);
                  default -> "OTHER";
                };
            var c = connection.toBuilder().setField(field, bad).build();
            error(
                "common.forbidden",
                () -> sources.get(getRequest(source).toBuilder().setConnection(c).build()));
          }
          for (var change :
              List.of(
                  new String[] {"device", device, "revoked", "true"},
                  new String[] {"account", account.toString(), "status", "disabled"},
                  new String[] {"session", sessionId, "expiresAt", clock.instant().toString()},
                  new String[] {"session", sessionId, "idleExpiresAt", clock.instant().toString()},
                  new String[] {"device", device, "accountId", anotherActiveAccount()},
                  new String[] {"device", device, "sessionId", UUID.randomUUID().toString()})) {
            var saved = tx(() -> store.byId(change[0], change[1], false));
            mutate(change[0], change[1], change[2], change[3].equals("true") ? true : change[3]);
            error("common.forbidden", () -> sources.get(getRequest(source)));
            tx(
                () -> {
                  store.save(change[0], saved);
                  return null;
                });
          }
          return null;
        });
  }

  @Test
  void currentOrgNegativesUnknownAndNoConfiguredPermissionFallback() {
    call(
        () -> {
          for (var response :
              List.of(
                  allowed().toBuilder().clearPermissions().build(),
                  allowed().toBuilder().setStatusValue(2).build(),
                  allowed().toBuilder().setStatusValue(3).build())) {
            organization = r -> response;
            error("common.forbidden", () -> sources.register(request("new")));
          }
          for (var response :
              List.of(
                  allowed().toBuilder().setStatusValue(0).build(),
                  allowed().toBuilder().setStatusValue(999).build(),
                  allowed().toBuilder().setMemberTypeValue(999).build())) {
            organization = r -> response;
            error("common.unavailable", () -> sources.register(request("new")));
          }
          organization = r -> null;
          error("common.unavailable", () -> sources.register(request("new")));
          organization =
              r -> {
                throw io.grpc.Status.DEADLINE_EXCEEDED.asRuntimeException();
              };
          error("common.unavailable", () -> sources.register(request("new")));
          organization = r -> allowed().toBuilder().setMemberTypeValue(2).build();
          assertEquals(1, sources.register(request("new")).getSource().getStateValue());
          return null;
        });
  }

  @org.junit.jupiter.params.ParameterizedTest
  @org.junit.jupiter.params.provider.ValueSource(strings = {"revoked", "expired"})
  void committedReceiptAndStopSurviveLogoutCleanupButGetDoesNot(String state) {
    call(
        () -> {
          var r = request("first");
          var receipt = sources.register(r);
          NetworkAgentSource terminal;
          if (state.equals("revoked"))
            terminal = sources.revoke(stop(receipt.getSource(), "first-stop")).getSource();
          else {
            clock.advance(31);
            terminal = sources.get(getRequest(receipt.getSource())).getSource();
          }
          clock.advance(3601);
          cleanExpiredOriginalIngress();
          mutate("session", sessionId, "revoked", true);
          mutate("device", device, "revoked", true);
          mutate("account", account.toString(), "status", "disabled");
          sources = authority(new IamNetworkSourceAuthority.Policy(0, 0));
          organization =
              req -> {
                fail(
                    "receipt/stop must not call org; Get must reject the missing connection first");
                return null;
              };
          assertEquals(receipt, sources.register(r));
          error("common.forbidden", () -> sources.get(getRequest(receipt.getSource())));
          var revoke = stop(terminal, "stop");
          var stopped = sources.revoke(revoke);
          assertEquals(stopped, sources.revoke(revoke));
          assertEquals(receipt, sources.register(r));
          error(
              "common.conflict",
              () ->
                  sources.register(
                      r.toBuilder().setExpectedRevision(2).setPredecessorBinding("other").build()));
          error("common.unavailable", () -> sources.register(request("unknown")));
          sources = authority(new IamNetworkSourceAuthority.Policy(30, 5));
          error("common.forbidden", () -> sources.register(request("unknown")));
          mutate("device", device, "accountId", anotherActiveAccount());
          error("common.forbidden", () -> sources.register(r));
          error("common.forbidden", () -> sources.get(getRequest(receipt.getSource())));
          error("common.forbidden", () -> sources.revoke(revoke));
          return null;
        });
  }

  NetworkAgentSource terminalSource(String state) {
    var original = sources.register(request("terminal")).getSource();
    if (state.equals("revoked")) return sources.revoke(stop(original, "terminal-stop")).getSource();
    clock.advance(31);
    return sources.get(getRequest(original)).getSource();
  }

  static java.util.stream.Stream<org.junit.jupiter.params.provider.Arguments> terminalCases() {
    return java.util.stream.Stream.of("revoked", "expired")
        .flatMap(
            state ->
                java.util.stream.Stream.of(
                        new long[] {30, 5},
                        new long[] {0, 5},
                        new long[] {30, 0},
                        new long[] {0, 0})
                    .flatMap(
                        policy ->
                            java.util.stream.Stream.of(
                                    "session-revoked",
                                    "idle",
                                    "absolute",
                                    "device",
                                    "account",
                                    "missing-ingress",
                                    "expired-ingress",
                                    "backing-session",
                                    "org-disabled",
                                    "org-permission",
                                    "org-unknown")
                                .map(
                                    failure ->
                                        org.junit.jupiter.params.provider.Arguments.of(
                                            state, policy[0], policy[1], failure))));
  }

  @org.junit.jupiter.params.ParameterizedTest
  @org.junit.jupiter.params.provider.MethodSource("terminalCases")
  void terminalGetStillRequiresCurrentAuthorityAndNeverLeaksSource(
      String state, long maximum, long decision, String failure) {
    var terminal = call(() -> terminalSource(state));
    sources = authority(new IamNetworkSourceAuthority.Policy(maximum, decision));
    switch (failure) {
      case "session-revoked" -> mutate("session", sessionId, "revoked", true);
      case "idle" -> mutate("session", sessionId, "idleExpiresAt", clock.instant().toString());
      case "absolute" -> mutate("session", sessionId, "expiresAt", clock.instant().toString());
      case "device" -> mutate("device", device, "revoked", true);
      case "account" -> mutate("account", account.toString(), "status", "disabled");
      case "missing-ingress" ->
          tx(
              () -> {
                store.delete("network_ingress", connection.getIngress());
                return null;
              });
      case "expired-ingress" ->
          tx(
              () -> {
                var row = store.get("network_ingress", connection.getIngress(), true);
                row.put("expiresAt", clock.instant().toString());
                store.save("network_ingress", row);
                return null;
              });
      case "backing-session" -> {
        var replacement =
            tx(
                () -> {
                  var session = store.byId("session", sessionId, false);
                  return sessions.create(
                      account,
                      UUID.fromString(session.path("providerId").asString()),
                      "portal",
                      true,
                      Duration.ofHours(2),
                      Duration.ofHours(1));
                });
        mutate("device", device, "sessionId", replacement.session().path("sessionId").asString());
      }
      case "org-disabled" ->
          organization = request -> allowed().toBuilder().setStatusValue(2).build();
      case "org-permission" ->
          organization = request -> allowed().toBuilder().clearPermissions().build();
      default -> organization = request -> allowed().toBuilder().setStatusValue(999).build();
    }
    String expected = failure.equals("org-unknown") ? "common.unavailable" : "common.forbidden";
    call(
        () -> {
          error(expected, () -> sources.get(getRequest(terminal)));
          return null;
        });
    withRpc(
        stub ->
            TenantContext.run(
                tenant,
                () -> {
                  var rejected =
                      assertThrows(
                          io.grpc.StatusRuntimeException.class,
                          () -> stub.getAgentSource(getRequest(terminal)));
                  assertEquals(
                      failure.equals("org-unknown")
                          ? io.grpc.Status.Code.UNAVAILABLE
                          : io.grpc.Status.Code.PERMISSION_DENIED,
                      rejected.getStatus().getCode());
                  assertFalse(rejected.toString().contains(terminal.getSourceBinding()));
                  assertFalse(rejected.toString().contains(account.toString()));
                }));
  }

  static java.util.stream.Stream<org.junit.jupiter.params.provider.Arguments> terminalPolicies() {
    return java.util.stream.Stream.of("revoked", "expired")
        .flatMap(
            state ->
                java.util.stream.Stream.of(
                        new long[] {30, 5},
                        new long[] {0, 5},
                        new long[] {30, 0},
                        new long[] {0, 0})
                    .map(
                        policy ->
                            org.junit.jupiter.params.provider.Arguments.of(
                                state, policy[0], policy[1])));
  }

  @org.junit.jupiter.params.ParameterizedTest
  @org.junit.jupiter.params.provider.MethodSource("terminalPolicies")
  void legitimateTerminalGetAndReconnectCheckOrgEvenWithIssuanceDisabled(
      String state, long maximum, long decision) {
    call(
        () -> {
          var terminal = terminalSource(state);
          sources = authority(new IamNetworkSourceAuthority.Policy(maximum, decision));
          var checks = new java.util.concurrent.atomic.AtomicInteger();
          organization =
              request -> {
                store.requireTransactionReleased();
                checks.incrementAndGet();
                return allowed();
              };
          var originalConnection = connection;
          var original = sources.get(getRequest(terminal));
          assertEquals(terminal, original.getSource());
          assertEquals("", original.getValidUntil());
          assertEquals(1, checks.get());
          var next = authority.prepare(access, node.node(), "server-a");
          var b = newTransport(next.ticket());
          callers.as(
              "svc:ai-gateway",
              tenant,
              () -> authority.redeem(next.ticket(), node.node(), device, "server-a", b));
          connection =
              connection.toBuilder()
                  .setIngress(b.ingress())
                  .setServerId(b.serverId())
                  .setClientId(b.clientId())
                  .setClientAccount(b.account())
                  .build();
          var reconnected = sources.get(getRequest(terminal));
          assertEquals(originalConnection, reconnected.getSource().getOriginalConnection());
          assertEquals(connection, reconnected.getConnection());
          assertEquals("", reconnected.getValidUntil());
          assertEquals(2, checks.get());
          var badRegistration =
              getRequest(terminal).toBuilder()
                  .setRegistration(
                      terminal.getRegistration().toBuilder().setRegistrationRevision(2))
                  .build();
          error("common.forbidden", () -> sources.get(badRegistration));
          return null;
        });
  }

  @org.junit.jupiter.params.ParameterizedTest
  @org.junit.jupiter.params.provider.MethodSource("terminalPolicies")
  void terminalGetMustRecheckFactsAndPolicyAfterOrg(String state, long maximum, long decision) {
    call(
        () -> {
          var terminal = terminalSource(state);
          sources = authority(new IamNetworkSourceAuthority.Policy(maximum, decision));
          organization =
              request -> {
                store.requireTransactionReleased();
                mutate("session", sessionId, "changedDuringOrg", true);
                return allowed();
              };
          error("common.forbidden", () -> sources.get(getRequest(terminal)));
          organization =
              request -> {
                store.requireTransactionReleased();
                tx(
                    () -> {
                      store.lock("network-authority");
                      new IamNetworkPolicyRevision(store).bump();
                      return null;
                    });
                return allowed();
              };
          error("common.forbidden", () -> sources.get(getRequest(terminal)));
          return null;
        });
  }

  @Test
  void remoteCheckReleasesAllResourcesAndChangedLocalFactsCannotCommit() throws Exception {
    for (String table :
        List.of(
            "account",
            "session",
            "device",
            "network_ingress",
            "revision",
            "indexed-session-expiry",
            "indexed-device-expiry")) {
      var entered = new java.util.concurrent.CountDownLatch(1);
      var release = new java.util.concurrent.CountDownLatch(1);
      organization =
          req -> {
            store.requireTransactionReleased();
            assertFalse(
                org.springframework.transaction.support.TransactionSynchronizationManager
                    .isSynchronizationActive());
            assertFalse(
                org.springframework.transaction.support.TransactionSynchronizationManager
                    .hasResource(store.networkJdbc().getDataSource()));
            entered.countDown();
            try {
              assertTrue(release.await(10, TimeUnit.SECONDS));
            } catch (InterruptedException e) {
              throw new AssertionError(e);
            }
            return allowed();
          };
      try (var executor = java.util.concurrent.Executors.newVirtualThreadPerTaskExecutor()) {
        var future = executor.submit(() -> call(() -> sources.register(request("race-" + table))));
        assertTrue(entered.await(10, TimeUnit.SECONDS));
        try {
          if (table.startsWith("indexed-"))
            tx(
                () -> {
                  String target = table.equals("indexed-session-expiry") ? "session" : "device";
                  String id = target.equals("session") ? sessionId : device;
                  store
                      .networkJdbc()
                      .update(
                          "UPDATE iam." + target + " SET expires_at=? WHERE tenant_id=? AND id=?",
                          java.sql.Timestamp.from(clock.instant().plusSeconds(1800)),
                          tenant,
                          UUID.fromString(id));
                  return null;
                });
          else if (table.equals("revision"))
            tx(
                () -> {
                  store.lock("network-authority");
                  new IamNetworkPolicyRevision(store).bump();
                  return null;
                });
          else {
            String id =
                switch (table) {
                  case "account" -> account.toString();
                  case "session" -> sessionId;
                  case "device" -> device;
                  default ->
                      tx(() -> store.get("network_ingress", connection.getIngress(), false))
                          .path("id")
                          .asString();
                };
            mutate(table, id, "versionWitness", table);
          }
        } finally {
          release.countDown();
        }
        var error =
            assertThrows(
                java.util.concurrent.ExecutionException.class,
                () -> future.get(15, TimeUnit.SECONDS));
        assertEquals("common.forbidden", ((AurigaException) error.getCause()).errorCode().code());
        assertEquals(
            0,
            tx(
                () ->
                    store
                        .networkJdbc()
                        .queryForObject(
                            "SELECT count(*) FROM iam.network_source WHERE tenant_id=?",
                            Integer.class,
                            tenant)));
      }
    }
  }

  @Test
  void successorCasStopAndClockRollbackNeverReviveOriginal() throws Exception {
    var first = call(() -> sources.register(request("first"))).getSource();
    clock.advance(31);
    var expired = call(() -> sources.get(getRequest(first))).getSource();
    assertEquals(3, expired.getStateValue());
    var next =
        request("next").toBuilder()
            .setPredecessorBinding(first.getSourceBinding())
            .setExpectedRevision(expired.getRevision())
            .build();
    try (var executor = java.util.concurrent.Executors.newVirtualThreadPerTaskExecutor()) {
      var gate = new java.util.concurrent.CountDownLatch(1);
      var jobs = new ArrayList<java.util.concurrent.Future<Boolean>>();
      for (String op : List.of("a", "b"))
        jobs.add(
            executor.submit(
                () -> {
                  assertTrue(gate.await(5, TimeUnit.SECONDS));
                  var result =
                      call(() -> sources.register(next.toBuilder().setOperationId(op).build()));
                  if (!result.hasSource())
                    assertEquals("predecessor_not_head", result.getRejectionReason());
                  return result.hasSource();
                }));
      gate.countDown();
      int success = 0;
      for (var job : jobs) if (job.get(15, TimeUnit.SECONDS)) success++;
      assertEquals(1, success);
    }
    call(() -> sources.revoke(stop(expired, "stop-old")));
    var head =
        tx(
            () ->
                store
                    .networkJdbc()
                    .queryForObject(
                        "SELECT head::text FROM iam.network_source_chain WHERE tenant_id=?",
                        String.class,
                        tenant));
    var headRequest = getRequest(first).toBuilder().setSourceBinding(head).build();
    assertEquals("", call(() -> sources.get(headRequest)).getValidUntil());
    clock.advance(-100);
    assertEquals(3, call(() -> sources.get(getRequest(first))).getSource().getStateValue());
    assertEquals(2, call(() -> sources.get(headRequest)).getSource().getStateValue());
    assertEquals(first, call(() -> sources.register(request("first"))).getSource());
  }

  IamNetworkSourceAuthority authority(IamNetworkSourceAuthority.Policy policy) {
    return new IamNetworkSourceAuthority(
        store,
        authority,
        new DeviceAccessAuthority(store, sessions, tokens),
        r -> organization.getEffectivePermissions(r),
        clock,
        policy);
  }

  @BeforeAll
  static void migrate() {
    var roles =
        PostgresExtension.serviceRoles(
            NetworkSourceAuthorityPostgresTest.class, "sourceauth_owner", "sourceauth_app", "iam");
    CommonMigrations.migrate(roles.owner().dataSource(), "iam");
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas("iam")
                .locations("classpath:db/iam")
                .load())
        .migrate();
    store =
        new IamStore(
            new JdbcTemplate(roles.runtime().dataSource()),
            new TenantTransactionManager(roles.runtime().dataSource()));
  }

  @BeforeEach
  void issuedDeviceAndRealRedemption() throws Exception {
    key = new ECKeyGenerator(Curve.P_256).keyID(UUID.randomUUID().toString()).generate();
    var keys = mock(SigningKeys.class);
    when(keys.current()).thenReturn(key);
    when(keys.jwks()).thenReturn(Json.object("keys", List.of(key.toPublicJWK().toJSONObject())));
    tokens =
        new TokenService(
            keys,
            new ContractSchemas(),
            (t, s, c) -> Set.of("auriga.connect", "auriga.connect.collab.use"),
            clock,
            "https://portal.example.com",
            Map.of());
    sessions =
        new SessionManager(
            store,
            mock(SessionKv.class),
            tokens,
            clock,
            mock(EventPublisher.class),
            mock(AuditLogger.class));
    oauth =
        new OAuthProvider(
            store,
            sessions,
            tokens,
            clock,
            List.of(
                new OAuthClient(
                    "device-example",
                    tenant,
                    null,
                    Set.of(),
                    Set.of("auriga.connect"),
                    Set.of(OAuthProvider.DEVICE, "refresh_token"),
                    true)));
    authority =
        new IamNetworkAuthority(
            store,
            new DeviceAccessAuthority(store, sessions, tokens),
            mock(AuditLogger.class),
            clock,
            Set.of("server-a"));
    sources = authority(new IamNetworkSourceAuthority.Policy(30, 5));
    TenantContext.run(
        tenant,
        () -> {
          var issued =
              store.tx(
                  () -> {
                    var provider = UUID.randomUUID();
                    store.insert(
                        "account", account.toString(), Json.object("status", "active"), null);
                    store.insert("idp_provider", provider.toString(), Json.object(), null);
                    return sessions.create(
                        account,
                        provider,
                        "portal",
                        true,
                        Duration.ofHours(2),
                        Duration.ofHours(1));
                  });
          sessionId = issued.session().path("sessionId").asString();
          var grant =
              oauth.deviceAuthorization(
                  Map.of("client_id", "device-example", "scope", "auriga.connect"));
          oauth.decideDevice(grant.path("user_code").asString(), sessionId, true);
          var credential =
              oauth.token(
                  Map.of(
                      "grant_type",
                      OAuthProvider.DEVICE,
                      "client_id",
                      "device-example",
                      "device_code",
                      grant.path("device_code").asString()));
          assertTrue(credential.has("access_token"), credential.toString());
          access = credential.path("access_token").asString();
          device = tokens.verify(access).path("did").asString();
          node = authority.register(access);
          ticket = authority.prepare(access, node.node(), "server-a");
          binding = NetworkAuthorityTest.binding(ticket.ticket());
          callers.as(
              "svc:ai-gateway",
              tenant,
              () -> authority.redeem(ticket.ticket(), node.node(), device, "server-a", binding));
          connection =
              NetworkAuthorityConnection.newBuilder()
                  .setTenantId(tenant.toString())
                  .setRealm(node.realm())
                  .setNode(node.node())
                  .setIngress(binding.ingress())
                  .setServerId(binding.serverId())
                  .setClientId(binding.clientId())
                  .setClientAccount(binding.account())
                  .build();
        });
  }

  void withRpc(
      java.util.function.Consumer<
              NetworkSourceAuthorityServiceGrpc.NetworkSourceAuthorityServiceBlockingStub>
          work) {
    var settings =
        new AccessTokenSettings(
            tokens.issuer(),
            java.net.URI.create(tokens.issuer() + "/oauth2/jwks"),
            null,
            null,
            null,
            null,
            clock,
            false);
    var jwks =
        new com.nimbusds.jose.jwk.source.ImmutableJWKSet<com.nimbusds.jose.proc.SecurityContext>(
            new JWKSet(key.toPublicJWK()));
    var verifier = new AccessTokenVerifier(AccessTokenDecoders.createInternal(settings, jwks));
    var signing = mock(SigningKeys.class);
    when(signing.current()).thenReturn(key);
    String secret = Secrets.random();
    var internal =
        new TokenService(
            signing,
            new ContractSchemas(),
            (t, s, c) -> Set.of(),
            clock,
            tokens.issuer(),
            Map.of("collab", Secrets.hash(secret)));
    String serviceToken = internal.issue("collab", secret);
    var server =
        new GrpcServer(
            "127.0.0.1",
            0,
            List.of(
                new IamGrpcNetworkSources(sources),
                new IamGrpcNetworkAuthority(authority, clock, sources)),
            new AurigaServerInterceptor(verifier, Set.of(), Set.of("svc:collab")),
            null,
            false,
            Set.of());
    io.grpc.ManagedChannel channel = null;
    try {
      server.start();
      channel =
          GrpcChannels.open(
              "127.0.0.1:" + server.port(),
              new AurigaClientInterceptor(() -> serviceToken, Duration.ofSeconds(5)));
      // Keep this shared stub without an absolute deadline; resolveRpc bounds each new call.
      work.accept(NetworkSourceAuthorityServiceGrpc.newBlockingStub(channel));
    } finally {
      if (channel != null) channel.shutdownNow();
      server.stop();
    }
  }

  @Test
  void threeRpcsUseActualServiceAndPersistentAuthority() {
    withRpc(
        stub ->
            TenantContext.run(
                tenant,
                () -> {
                  var r = request("wire");
                  var source = stub.registerAgentSource(r).getSource();
                  assertEquals(account.toString(), source.getOwnerUserId());
                  assertEquals(connection, stub.getAgentSource(getRequest(source)).getConnection());
                  var bad =
                      getRequest(source).toBuilder()
                          .setConnection(connection.toBuilder().setClientAccount("OTHER"))
                          .build();
                  IamGrpcNetworkAuthoritySpringTest.grpc(
                      io.grpc.Status.Code.PERMISSION_DENIED, () -> stub.getAgentSource(bad));
                  var result = stub.revokeAgentSource(stop(source, "wire-stop"));
                  assertEquals(2, result.getSource().getStateValue());
                  assertEquals("", stub.getAgentSource(getRequest(source)).getValidUntil());
                  assertEquals(source, stub.registerAgentSource(r).getSource());
                }));
  }

  @Test
  void switchesLargeIntegerLimitsAndClockRollbackFailClosed() {
    call(
        () -> {
          var s = sources.register(request("first")).getSource();
          sources = authority(new IamNetworkSourceAuthority.Policy(0, 5));
          error("common.unavailable", () -> sources.get(getRequest(s)));
          error("common.unavailable", () -> sources.register(request("unknown")));
          assertEquals(s, sources.register(request("first")).getSource());
          sources = authority(new IamNetworkSourceAuthority.Policy(30, 5));
          organization =
              req -> {
                clock.advance(-1);
                return allowed();
              };
          error("common.unavailable", () -> sources.get(getRequest(s)));
          clock.advance(1);
          organization = req -> allowed();
          clock.advance(-10);
          error("common.unavailable", () -> sources.get(getRequest(s)));
          clock.advance(10);
          sources = authority(new IamNetworkSourceAuthority.Policy(Long.MAX_VALUE, 300));
          var huge =
              request("large").toBuilder()
                  .setRegistration(
                      request("large").getRegistration().toBuilder().setRegistrationId("large"))
                  .build();
          var fact = sources.register(huge).getSource();
          assertEquals(
              ticket.connectionExpiresAt().truncatedTo(java.time.temporal.ChronoUnit.SECONDS),
              Instant.parse(fact.getExpiresAt()));
          return null;
        });
  }

  void admin(Runnable work) {
    var p =
        new AurigaPrincipal(
            account.toString(),
            tenant,
            PrincipalType.USER,
            TokenContext.ADMIN,
            sessionId,
            null,
            "member",
            List.of("admin"),
            Set.of("iam.network.manage"),
            clock.instant(),
            clock.instant(),
            "policy-test");
    auriga.common.context.RequestContext.start("policy-test", null)
        .withPrincipal(p)
        .run(() -> TenantContext.run(tenant, work));
  }

  @org.junit.jupiter.params.ParameterizedTest
  @org.junit.jupiter.params.provider.ValueSource(
      strings = {"node", "config", "policy", "grant", "withdraw", "account-event"})
  void everyPolicyWriterFencesARealInFlightSourceDecision(String mutation) {
    organization =
        request -> {
          store.requireTransactionReleased();
          admin(
              () -> {
                switch (mutation) {
                  case "node" -> authority.revoke(node.node());
                  case "config" ->
                      authority.configure(new Configuration(true, true, true, Map.of()));
                  case "policy" -> authority.nodePolicy(node.node(), node.node(), false);
                  case "grant" ->
                      authority.grant(
                          node.node(),
                          Sender.AGENT,
                          "1",
                          Operation.FIRST_CONTACT,
                          clock.instant().plusSeconds(60));
                  case "withdraw" ->
                      authority.withdraw(node.node(), Sender.AGENT, "1", Operation.FIRST_CONTACT);
                  default ->
                      new IamAccountEvents(store, sessions)
                          .handle(
                              new auriga.common.events.ReceivedEvent(
                                  UUID.randomUUID(),
                                  "user.permissions_changed",
                                  "org",
                                  "account",
                                  clock.instant(),
                                  tenant,
                                  "1",
                                  null,
                                  Json.object("userId", account.toString())));
                }
              });
          return allowed();
        };
    call(
        () -> {
          error("common.forbidden", () -> sources.register(request("policy-race")));
          assertEquals(
              0,
              tx(
                  () ->
                      store
                          .networkJdbc()
                          .queryForObject(
                              "SELECT count(*) FROM iam.network_source WHERE tenant_id=?",
                              Integer.class,
                              tenant)));
          return null;
        });
  }

  @Test
  void realDifferentOwnerDeviceNodeAndNewBackingSessionNeverInherit() {
    call(
        () -> {
          var first = sources.register(request("first")).getSource();
          var savedConnection = connection;
          UUID otherAccount = UUID.randomUUID();
          var otherSession =
              tx(
                  () -> {
                    UUID provider = UUID.randomUUID();
                    store.insert(
                        "account", otherAccount.toString(), Json.object("status", "active"), null);
                    store.insert("idp_provider", provider.toString(), Json.object(), null);
                    return sessions.create(
                        otherAccount,
                        provider,
                        "portal",
                        true,
                        Duration.ofHours(2),
                        Duration.ofHours(1));
                  });
          var grant =
              oauth.deviceAuthorization(
                  Map.of("client_id", "device-example", "scope", "auriga.connect"));
          oauth.decideDevice(
              grant.path("user_code").asString(),
              otherSession.session().path("sessionId").asString(),
              true);
          var token =
              oauth
                  .token(
                      Map.of(
                          "grant_type",
                          OAuthProvider.DEVICE,
                          "client_id",
                          "device-example",
                          "device_code",
                          grant.path("device_code").asString()))
                  .path("access_token")
                  .asString();
          var otherDevice = tokens.verify(token).path("did").asString();
          var otherNode = authority.register(token);
          var ticket2 = authority.prepare(token, otherNode.node(), "server-a");
          var b = newTransport(ticket2.ticket());
          callers.as(
              "svc:ai-gateway",
              tenant,
              () ->
                  authority.redeem(ticket2.ticket(), otherNode.node(), otherDevice, "server-a", b));
          organization = req -> allowed();
          connection =
              connection.toBuilder()
                  .setNode(otherNode.node())
                  .setIngress(b.ingress())
                  .setServerId(b.serverId())
                  .setClientId(b.clientId())
                  .setClientAccount(b.account())
                  .build();
          error("common.forbidden", () -> sources.get(getRequest(first)));
          connection = savedConnection;
          var replacement =
              tx(
                  () ->
                      sessions.create(
                          account,
                          UUID.fromString(otherSession.session().path("providerId").asString()),
                          "portal",
                          true,
                          Duration.ofHours(2),
                          Duration.ofHours(1)));
          mutate("device", device, "sessionId", replacement.session().path("sessionId").asString());
          error("common.forbidden", () -> sources.get(getRequest(first)));
          error("common.forbidden", () -> sources.register(request("first")));
          return null;
        });
  }

  @Test
  void absentRetainedAnchorFailsClosedForReceiptTerminalAndStop() throws Exception {
    var r = request("first");
    var first = call(() -> sources.register(r)).getSource();
    clock.advance(31);
    // Corrupt fixture through a separate superuser connection; runtime cannot erase its anchors.
    try (var c =
            PostgresExtension.database(NetworkSourceAuthorityPostgresTest.class)
                .dataSource()
                .getConnection();
        var statement = c.createStatement();
        var delete =
            c.prepareStatement("DELETE FROM iam.network_source_anchor WHERE tenant_id=?")) {
      statement.execute("SET session_replication_role=replica");
      delete.setObject(1, tenant);
      assertEquals(1, delete.executeUpdate());
    }
    call(
        () -> {
          error("common.forbidden", () -> sources.register(r));
          error("common.forbidden", () -> sources.get(getRequest(first)));
          error("common.forbidden", () -> sources.revoke(stop(first, "stop")));
          return null;
        });
  }

  @Test
  void getAndPermanentStopSerializeAndNeverReturnATerminalLease() throws Exception {
    var first = call(() -> sources.register(request("first"))).getSource();
    try (var executor = java.util.concurrent.Executors.newVirtualThreadPerTaskExecutor()) {
      var gate = new java.util.concurrent.CountDownLatch(1);
      var get =
          executor.submit(
              () -> {
                assertTrue(gate.await(5, TimeUnit.SECONDS));
                return call(() -> sources.get(getRequest(first)));
              });
      var revoke =
          executor.submit(
              () -> {
                assertTrue(gate.await(5, TimeUnit.SECONDS));
                return call(() -> sources.revoke(stop(first, "stop")));
              });
      gate.countDown();
      assertEquals(2, revoke.get(15, TimeUnit.SECONDS).getSource().getStateValue());
      var observed = get.get(15, TimeUnit.SECONDS);
      if (observed.getSource().getStateValue() != 1) assertEquals("", observed.getValidUntil());
    }
    assertEquals("", call(() -> sources.get(getRequest(first))).getValidUntil());
  }

  /** Uses the same real OAuth/redeem/PG fixture, with a distinct target owner/device/session. */
  @Nested
  class NetworkDeliveryPostgresTest {
    NetworkAgentSource source;
    NetworkAuthorityConnection target;
    UUID targetAccount;
    String targetDevice, targetSession;
    tools.jackson.databind.node.ObjectNode message, context;

    @BeforeEach
    void delivery() {
      source = call(() -> sources.register(request("delivery"))).getSource();
      targetAccount = UUID.randomUUID();
      TenantContext.run(
          tenant,
          () -> {
            var issued =
                tx(
                    () -> {
                      UUID provider = UUID.randomUUID();
                      store.insert(
                          "account",
                          targetAccount.toString(),
                          Json.object("status", "active"),
                          null);
                      store.insert("idp_provider", provider.toString(), Json.object(), null);
                      return sessions.create(
                          targetAccount,
                          provider,
                          "portal",
                          true,
                          Duration.ofHours(2),
                          Duration.ofHours(1));
                    });
            targetSession = issued.session().path("sessionId").asString();
            var grant =
                oauth.deviceAuthorization(
                    Map.of("client_id", "device-example", "scope", "auriga.connect"));
            oauth.decideDevice(grant.path("user_code").asString(), targetSession, true);
            String token =
                oauth
                    .token(
                        Map.of(
                            "grant_type",
                            OAuthProvider.DEVICE,
                            "client_id",
                            "device-example",
                            "device_code",
                            grant.path("device_code").asString()))
                    .path("access_token")
                    .asString();
            targetDevice = tokens.verify(token).path("did").asString();
            var node2 = authority.register(token);
            var ticket2 = authority.prepare(token, node2.node(), "server-a");
            var b = newTransport(ticket2.ticket());
            callers.as(
                "svc:ai-gateway",
                tenant,
                () ->
                    authority.redeem(ticket2.ticket(), node2.node(), targetDevice, "server-a", b));
            target =
                connection.toBuilder()
                    .setNode(node2.node())
                    .setIngress(b.ingress())
                    .setServerId(b.serverId())
                    .setClientId(b.clientId())
                    .setClientAccount(b.account())
                    .build();
          });
      organization =
          r -> {
            store.requireTransactionReleased();
            assertEquals(tenant.toString(), r.getTenantId());
            assertTrue(
                Set.of(account.toString(), targetAccount.toString()).contains(r.getAccountId()));
            return allowed();
          };
      // Registering the target advances the real policy barrier; observe the current source again.
      var view = call(() -> sources.get(getRequest(source)));
      message = NetworkDeliveryJsonTest.message();
      var from = NetworkDeliveryJsonTest.address("session", node.node(), "1");
      from.put("realm", node.realm());
      message.set("from", from);
      message.set("reply_to", from.deepCopy());
      var to = NetworkDeliveryJsonTest.address("system", target.getNode(), "0");
      to.put("realm", target.getRealm());
      message.set("to", to);
      message
          .put("created_at", clock.instant().toString())
          .put("expires_at", clock.instant().plusSeconds(60).toString());
      message.set(
          "authorization_ref",
          Json.object(
              "source_binding",
              source.getSourceBinding(),
              "source_revision",
              Long.toUnsignedString(source.getRevision()),
              "policy_revision",
              Long.toUnsignedString(view.getPolicyRevision()),
              "broadcast_grant_id",
              null));
      context =
          Json.object(
              "realm",
              node.realm(),
              "node",
              node.node(),
              "user",
              account.toString(),
              "sender_kind",
              "agent",
              "display_label",
              "Alice",
              "observed_at",
              view.getObservedAt());
    }

    AuthorizeNetworkDeliveryRequest deliveryRequest() {
      context.put("message_digest", IamNetworkDeliveryJson.digest(message));
      return AuthorizeNetworkDeliveryRequest.newBuilder()
          .setConnection(target)
          .setMessageJson(com.google.protobuf.ByteString.copyFromUtf8(message.toString()))
          .setSourceContextJson(com.google.protobuf.ByteString.copyFromUtf8(context.toString()))
          .build();
    }

    void rpc(
        java.util.function.Consumer<NetworkAuthorityServiceGrpc.NetworkAuthorityServiceBlockingStub>
            work) {
      withRpc(
          stub ->
              TenantContext.run(
                  tenant,
                  () ->
                      work.accept(NetworkAuthorityServiceGrpc.newBlockingStub(stub.getChannel()))));
    }

    void denied(
        NetworkAuthorityServiceGrpc.NetworkAuthorityServiceBlockingStub stub,
        io.grpc.Status.Code code) {
      var error =
          assertThrows(
              io.grpc.StatusRuntimeException.class,
              () -> stub.authorizeNetworkDelivery(deliveryRequest()));
      assertEquals(code, error.getStatus().getCode());
      String description = Objects.toString(error.getStatus().getDescription(), "");
      for (String secret :
          List.of(
              source.getSourceBinding(),
              account.toString(),
              device,
              sessionId,
              targetAccount.toString(),
              target.getIngress(),
              "message_digest",
              "Alice")) assertFalse(description.contains(secret));
    }

    @Test
    void realFifthRpcAllowsOnlyCompleteAuthorityAndClampsEveryDeadline() {
      message.set("future", Json.object("😀", "é", "unknown", List.of(1, 2, 3)));
      var idleBefore = tx(() -> store.byId("session", sessionId, false)).path("idleExpiresAt");
      rpc(
          stub -> {
            var response = stub.authorizeNetworkDelivery(deliveryRequest());
            assertTrue(response.getAllowed());
            assertEquals(target, response.getConnection());
            assertEquals(clock.instant().plusSeconds(5).toString(), response.getValidUntil());
            assertEquals(
                idleBefore,
                tx(() -> store.byId("session", sessionId, false)).path("idleExpiresAt"));
            message.put("expires_at", clock.instant().plusSeconds(2).toString());
            assertEquals(
                clock.instant().plusSeconds(2).toString(),
                stub.authorizeNetworkDelivery(deliveryRequest()).getValidUntil());
            message.put("expires_at", clock.instant().plusSeconds(60).toString());
            mutate(
                "session",
                targetSession,
                "idleExpiresAt",
                clock.instant().plusSeconds(3).plusMillis(900).toString());
            assertEquals(
                clock.instant().plusSeconds(3).toString(),
                stub.authorizeNetworkDelivery(deliveryRequest()).getValidUntil());
            mutate(
                "session",
                sessionId,
                "idleExpiresAt",
                clock.instant().plusSeconds(1).plusMillis(900).toString());
            assertEquals(
                clock.instant().plusSeconds(1).toString(),
                stub.authorizeNetworkDelivery(deliveryRequest()).getValidUntil());
          });
    }

    @Test
    void bothRawInputsAreStrictAtTheActualRpcAndDigestCoversUnknownFields() {
      rpc(
          stub -> {
            var original = deliveryRequest();
            for (byte[] raw : NetworkDeliveryJsonTest.badRaw()) {
              var bytes = com.google.protobuf.ByteString.copyFrom(raw);
              IamGrpcNetworkAuthoritySpringTest.grpc(
                  io.grpc.Status.Code.INVALID_ARGUMENT,
                  () ->
                      stub.authorizeNetworkDelivery(
                          original.toBuilder().setMessageJson(bytes).build()));
              IamGrpcNetworkAuthoritySpringTest.grpc(
                  io.grpc.Status.Code.INVALID_ARGUMENT,
                  () ->
                      stub.authorizeNetworkDelivery(
                          original.toBuilder().setSourceContextJson(bytes).build()));
            }
            context.put("message_digest", "sha256:" + "0".repeat(64));
            IamGrpcNetworkAuthoritySpringTest.grpc(
                io.grpc.Status.Code.INVALID_ARGUMENT,
                () ->
                    stub.authorizeNetworkDelivery(
                        original.toBuilder()
                            .setSourceContextJson(
                                com.google.protobuf.ByteString.copyFromUtf8(context.toString()))
                            .build()));
            message.put("future", "preserved");
            IamGrpcNetworkAuthoritySpringTest.grpc(
                io.grpc.Status.Code.INVALID_ARGUMENT,
                () ->
                    stub.authorizeNetworkDelivery(
                        original.toBuilder()
                            .setMessageJson(
                                com.google.protobuf.ByteString.copyFromUtf8(message.toString()))
                            .build()));
          });
    }

    @Test
    void sourceLifetimeAndExactTargetIngressAlsoClampTheDecision() {
      rpc(
          stub -> {
            clock.advance(28);
            assertEquals(
                source.getExpiresAt(),
                stub.authorizeNetworkDelivery(deliveryRequest()).getValidUntil());
            var row = tx(() -> store.get("network_ingress", target.getIngress(), false));
            mutate(
                "network_ingress",
                row.path("id").asString(),
                "expiresAt",
                clock.instant().plusSeconds(1).plusMillis(900).toString());
            assertEquals(
                clock.instant().plusSeconds(1).toString(),
                stub.authorizeNetworkDelivery(deliveryRequest()).getValidUntil());
          });
    }

    @Test
    void currentCeilingsAndNodePolicyCannotBeBypassedByAnOldReference() {
      admin(
          () ->
              authority.configure(
                  new Configuration(true, true, true, Map.of(Limit.BODY_BYTES, 32L))));
      var view = call(() -> sources.get(getRequest(source)));
      ((tools.jackson.databind.node.ObjectNode) message.path("authorization_ref"))
          .put("policy_revision", Long.toUnsignedString(view.getPolicyRevision()));
      message.set("body", Json.object("subtype", "text", "text", "a".repeat(64)));
      rpc(
          stub -> {
            denied(stub, io.grpc.Status.Code.PERMISSION_DENIED);
            message.set("body", Json.object("subtype", "text", "text", "x"));
            assertTrue(stub.authorizeNetworkDelivery(deliveryRequest()).getAllowed());
            admin(() -> authority.nodePolicy(node.node(), target.getNode(), false));
            var latest = call(() -> sources.get(getRequest(source)));
            ((tools.jackson.databind.node.ObjectNode) message.path("authorization_ref"))
                .put("policy_revision", Long.toUnsignedString(latest.getPolicyRevision()));
            denied(stub, io.grpc.Status.Code.PERMISSION_DENIED);
          });
    }

    @Test
    void exactTargetTupleCannotBeReplacedByPolicyAllowOrSourceConnection() {
      rpc(
          stub -> {
            var original = target;
            target = connection;
            denied(stub, io.grpc.Status.Code.PERMISSION_DENIED);
            target = original;
            for (var field : target.getDescriptorForType().getFields()) {
              String bad =
                  switch (field.getName()) {
                    case "tenant_id" -> UUID.randomUUID().toString();
                    case "realm", "node", "client_id" -> "18446744073709551615";
                    case "ingress" -> "b".repeat(26);
                    case "server_id" -> "N" + "C".repeat(55);
                    default -> "OTHER";
                  };
              target = original.toBuilder().setField(field, bad).build();
              denied(stub, io.grpc.Status.Code.PERMISSION_DENIED);
            }
            target = original;
          });
    }

    @Test
    void jsonAgreementNeverSubstitutesForRealSourceTupleOwnerOrPositiveVersions() {
      rpc(
          stub -> {
            var original = message.deepCopy();
            var stamp = context.deepCopy();
            var auth = (tools.jackson.databind.node.ObjectNode) message.path("authorization_ref");
            auth.put("source_binding", UUID.randomUUID().toString());
            denied(stub, io.grpc.Status.Code.PERMISSION_DENIED);
            message = original.deepCopy();
            auth = (tools.jackson.databind.node.ObjectNode) message.path("authorization_ref");
            auth.put("source_revision", "2");
            denied(stub, io.grpc.Status.Code.PERMISSION_DENIED);
            auth.put("source_revision", "1");
            auth.put("policy_revision", "18446744073709551615");
            denied(stub, io.grpc.Status.Code.PERMISSION_DENIED);
            for (String field : List.of("realm", "node", "local", "type")) {
              message = original.deepCopy();
              context = stamp.deepCopy();
              var from = (tools.jackson.databind.node.ObjectNode) message.path("from");
              from.put(field, field.equals("type") ? "subagent" : "18446744073709551615");
              message.set("reply_to", from.deepCopy());
              if (Set.of("realm", "node").contains(field))
                context.put(field, from.path(field).asString());
              if (field.equals("realm"))
                ((tools.jackson.databind.node.ObjectNode) message.path("to"))
                    .put("realm", from.path("realm").asString());
              denied(
                  stub,
                  field.equals("type")
                      ? io.grpc.Status.Code.INVALID_ARGUMENT
                      : io.grpc.Status.Code.PERMISSION_DENIED);
            }
            message = original;
            context = stamp;
            context.put("user", targetAccount.toString());
            denied(stub, io.grpc.Status.Code.PERMISSION_DENIED);
          });
    }

    @Test
    void everyCurrentSourceAndTargetIdentityFailureRejectsEvenWhenPolicyAllows() {
      rpc(
          stub -> {
            for (String[] change :
                List.of(
                    new String[] {"device", device, "revoked", "true"},
                    new String[] {"account", account.toString(), "status", "disabled"},
                    new String[] {"session", sessionId, "revoked", "true"},
                    new String[] {"session", sessionId, "expiresAt", clock.instant().toString()},
                    new String[] {
                      "session", sessionId, "idleExpiresAt", clock.instant().toString()
                    },
                    new String[] {"device", device, "accountId", targetAccount.toString()},
                    new String[] {"device", device, "sessionId", targetSession},
                    new String[] {"device", targetDevice, "revoked", "true"},
                    new String[] {"device", targetDevice, "scope", "unknown"},
                    new String[] {"account", targetAccount.toString(), "status", "disabled"},
                    new String[] {"session", targetSession, "revoked", "true"},
                    new String[] {
                      "session", targetSession, "idleExpiresAt", clock.instant().toString()
                    },
                    new String[] {
                      "network_ingress",
                      target.getIngress(),
                      "expiresAt",
                      clock.instant().toString()
                    })) {
              var saved = tx(() -> store.byId(change[0], change[1], false));
              if (change[0].equals("network_ingress"))
                saved = tx(() -> store.get(change[0], change[1], false));
              mutate(
                  change[0],
                  saved.path("id").asString(),
                  change[2],
                  change[3].equals("true") ? true : change[3]);
              denied(stub, io.grpc.Status.Code.PERMISSION_DENIED);
              var restored = saved;
              tx(
                  () -> {
                    store.save(change[0], restored);
                    return null;
                  });
            }
          });
    }

    @Test
    void sourceAndTargetCurrentOrgPermissionAndUnknownEnumsFailClosed() {
      rpc(
          stub -> {
            for (String subject : List.of(account.toString(), targetAccount.toString())) {
              for (var response :
                  List.of(
                      allowed().toBuilder().clearPermissions().build(),
                      allowed().toBuilder().setStatusValue(2).build(),
                      allowed().toBuilder().setStatusValue(3).build(),
                      allowed().toBuilder().setStatusValue(999).build(),
                      allowed().toBuilder().setMemberTypeValue(0).build())) {
                organization = r -> r.getAccountId().equals(subject) ? response : allowed();
                denied(
                    stub,
                    response.getStatusValue() > 3 || response.getMemberTypeValue() == 0
                        ? io.grpc.Status.Code.UNAVAILABLE
                        : io.grpc.Status.Code.PERMISSION_DENIED);
              }
            }
            organization = r -> null;
            denied(stub, io.grpc.Status.Code.UNAVAILABLE);
            organization =
                r -> {
                  throw io.grpc.Status.DEADLINE_EXCEEDED.asRuntimeException();
                };
            denied(stub, io.grpc.Status.Code.UNAVAILABLE);
          });
    }

    @Test
    void oldGenerationCannotBorrowSuccessorExpiryAndExpiredObservationCommits() {
      rpc(
          stub -> {
            clock.advance(31);
            denied(stub, io.grpc.Status.Code.PERMISSION_DENIED);
            assertEquals(
                3,
                tx(
                    () ->
                        store
                            .networkJdbc()
                            .queryForObject(
                                "SELECT state FROM iam.network_source WHERE tenant_id=? AND id=?",
                                Integer.class,
                                tenant,
                                UUID.fromString(source.getSourceBinding()))));
            var expired = call(() -> sources.get(getRequest(source))).getSource();
            assertEquals(3, expired.getStateValue());
            assertEquals(2, expired.getRevision());
            var next =
                call(() ->
                        sources.register(
                            request("successor").toBuilder()
                                .setPredecessorBinding(source.getSourceBinding())
                                .setExpectedRevision(expired.getRevision())
                                .build()))
                    .getSource();
            denied(stub, io.grpc.Status.Code.PERMISSION_DENIED);
            assertTrue(
                Instant.parse(next.getExpiresAt()).isAfter(Instant.parse(source.getExpiresAt())));
            var auth = (tools.jackson.databind.node.ObjectNode) message.path("authorization_ref");
            auth.put("source_binding", next.getSourceBinding()).put("source_revision", "1");
            context.put("observed_at", clock.instant().toString());
            assertTrue(stub.authorizeNetworkDelivery(deliveryRequest()).getAllowed());
            call(
                () ->
                    sources.revoke(
                        stop(expired, "stop-old").toBuilder().clearExpectedRevision().build()));
            var oldView = call(() -> sources.get(getRequest(source)));
            assertEquals(3, oldView.getSource().getStateValue());
            assertTrue(oldView.hasLineageStopped());
            assertTrue(oldView.getLineageStopped());
            assertEquals("", oldView.getValidUntil());
            var headView = call(() -> sources.get(getRequest(next)));
            assertEquals(2, headView.getSource().getStateValue());
            assertTrue(headView.getLineageStopped());
            assertEquals("", headView.getValidUntil());
            denied(stub, io.grpc.Status.Code.PERMISSION_DENIED);
          });
    }

    @Test
    void originalTransportMayReconnectOnlyWithinRetainedIdentity() {
      rpc(
          stub -> {
            var next =
                TenantContext.call(
                    tenant, () -> authority.prepare(access, node.node(), "server-a"));
            var b = newTransport(next.ticket());
            callers.as(
                "svc:ai-gateway",
                tenant,
                () -> authority.redeem(next.ticket(), node.node(), device, "server-a", b));
            tx(
                () -> {
                  var old = store.get("network_ingress", connection.getIngress(), true);
                  old.put("expiresAt", clock.instant().toString());
                  store.save("network_ingress", old);
                  store
                      .networkJdbc()
                      .update(
                          "UPDATE iam.network_ingress SET expires_at=? WHERE tenant_id=? AND lookup_key=?",
                          java.sql.Timestamp.from(clock.instant()),
                          tenant,
                          connection.getIngress());
                  return null;
                });
            assertTrue(stub.authorizeNetworkDelivery(deliveryRequest()).getAllowed());
            assertEquals(connection, source.getOriginalConnection());
            mutate("device", device, "sessionId", targetSession);
            denied(stub, io.grpc.Status.Code.PERMISSION_DENIED);
          });
    }

    @Test
    void aNewLoginOfTheSameOwnerAndMissingRedeemSessionEvidenceCannotInherit() {
      rpc(
          stub -> {
            var ingress = tx(() -> store.get("network_ingress", target.getIngress(), false));
            var missing = ingress.deepCopy();
            missing.remove("sessionId");
            tx(
                () -> {
                  store.save("network_ingress", missing);
                  return null;
                });
            denied(stub, io.grpc.Status.Code.PERMISSION_DENIED);
            tx(
                () -> {
                  store.save("network_ingress", ingress);
                  return null;
                });
            for (boolean sender : List.of(true, false)) {
              String did = sender ? device : targetDevice;
              UUID owner = sender ? account : targetAccount;
              String originalSession = sender ? sessionId : targetSession;
              var old = tx(() -> store.byId("session", originalSession, false));
              var replacement =
                  tx(
                      () ->
                          sessions.create(
                              owner,
                              UUID.fromString(old.path("providerId").asString()),
                              "portal",
                              true,
                              Duration.ofHours(2),
                              Duration.ofHours(1)));
              mutate(
                  "device", did, "sessionId", replacement.session().path("sessionId").asString());
              denied(stub, io.grpc.Status.Code.PERMISSION_DENIED);
              mutate("device", did, "sessionId", originalSession);
            }
          });
    }

    @Test
    void remoteChecksHoldNoDatabaseResourcesAndChangedFactsCannotBeReused() {
      rpc(
          stub -> {
            for (String side : List.of("source", "target", "policy")) {
              var changed = new java.util.concurrent.atomic.AtomicBoolean();
              organization =
                  r -> {
                    store.requireTransactionReleased();
                    if (changed.compareAndSet(false, true)) {
                      if (side.equals("policy"))
                        tx(
                            () -> {
                              store.lock("network-authority");
                              new IamNetworkPolicyRevision(store).bump();
                              return null;
                            });
                      else
                        mutate(
                            "session",
                            side.equals("source") ? sessionId : targetSession,
                            "changedDuringOrg",
                            side);
                    }
                    return allowed();
                  };
              denied(stub, io.grpc.Status.Code.PERMISSION_DENIED);
            }
          });
    }

    @Test
    void anotherTenantsRealSourceBindingIsNotReadable() throws Exception {
      var foreign = new NetworkSourceAuthorityPostgresTest();
      foreign.issuedDeviceAndRealRedemption();
      var fact =
          foreign.call(() -> foreign.sources.register(foreign.request("foreign"))).getSource();
      ((tools.jackson.databind.node.ObjectNode) message.path("authorization_ref"))
          .put("source_binding", fact.getSourceBinding());
      rpc(stub -> denied(stub, io.grpc.Status.Code.PERMISSION_DENIED));
    }

    @Test
    void deliveryActuallyWaitsForTheNamedGenerationVersionLock() throws Exception {
      var locked = new java.util.concurrent.CountDownLatch(1);
      var unlock = new java.util.concurrent.CountDownLatch(1);
      withRpc(
          stub -> {
            try (var executor = java.util.concurrent.Executors.newVirtualThreadPerTaskExecutor()) {
              var held =
                  executor.submit(
                      () ->
                          tx(
                              () -> {
                                store
                                    .networkJdbc()
                                    .queryForObject(
                                        "SELECT revision FROM iam.network_source WHERE tenant_id=? AND id=? FOR UPDATE",
                                        Long.class,
                                        tenant,
                                        UUID.fromString(source.getSourceBinding()));
                                locked.countDown();
                                try {
                                  assertTrue(unlock.await(5, TimeUnit.SECONDS));
                                } catch (InterruptedException e) {
                                  throw new AssertionError(e);
                                }
                                return null;
                              }));
              assertTrue(locked.await(5, TimeUnit.SECONDS));
              var pending =
                  executor.submit(
                      () ->
                          TenantContext.call(
                              tenant,
                              () ->
                                  NetworkAuthorityServiceGrpc.newBlockingStub(stub.getChannel())
                                      .authorizeNetworkDelivery(deliveryRequest())));
              try {
                awaitSourceRowWait();
              } finally {
                unlock.countDown();
              }
              held.get(5, TimeUnit.SECONDS);
              assertTrue(pending.get(5, TimeUnit.SECONDS).getAllowed());
              TenantContext.run(
                  tenant, () -> stub.revokeAgentSource(stop(source, "after-delivery")));
              TenantContext.run(
                  tenant,
                  () ->
                      denied(
                          NetworkAuthorityServiceGrpc.newBlockingStub(stub.getChannel()),
                          io.grpc.Status.Code.PERMISSION_DENIED));
            } catch (Exception e) {
              throw new AssertionError(e);
            } finally {
              unlock.countDown();
            }
          });
    }

    void awaitSourceRowWait() {
      org.awaitility.Awaitility.await()
          .atMost(Duration.ofSeconds(3))
          .until(
              () -> {
                try (var c =
                        PostgresExtension.database(NetworkSourceAuthorityPostgresTest.class)
                            .dataSource()
                            .getConnection();
                    var s = c.createStatement();
                    var rs =
                        s.executeQuery(
                            "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database()"
                                + " AND wait_event_type='Lock' AND query LIKE '%network_source%' AND query LIKE '%FOR UPDATE%'")) {
                  return rs.next() && rs.getLong(1) > 0;
                }
              });
    }

    @Test
    void realVersionLockRevokeCommitsBeforeDeliveryAndNoDeadlock() throws Exception {
      var remote = new java.util.concurrent.CountDownLatch(1);
      var resume = new java.util.concurrent.CountDownLatch(1);
      organization =
          r -> {
            store.requireTransactionReleased();
            remote.countDown();
            try {
              assertTrue(resume.await(10, TimeUnit.SECONDS));
            } catch (InterruptedException e) {
              throw new AssertionError(e);
            }
            return allowed();
          };
      withRpc(
          stub -> {
            try (var executor = java.util.concurrent.Executors.newVirtualThreadPerTaskExecutor()) {
              var pending =
                  executor.submit(
                      () ->
                          TenantContext.call(
                              tenant,
                              () ->
                                  assertThrows(
                                      io.grpc.StatusRuntimeException.class,
                                      () ->
                                          NetworkAuthorityServiceGrpc.newBlockingStub(
                                                  stub.getChannel())
                                              .authorizeNetworkDelivery(deliveryRequest()))));
              assertTrue(remote.await(5, TimeUnit.SECONDS));
              var locked = new java.util.concurrent.CountDownLatch(1);
              var unlock = new java.util.concurrent.CountDownLatch(1);
              var held =
                  executor.submit(
                      () ->
                          tx(
                              () -> {
                                store
                                    .networkJdbc()
                                    .queryForObject(
                                        "SELECT revision FROM iam.network_source WHERE tenant_id=? AND id=? FOR UPDATE",
                                        Long.class,
                                        tenant,
                                        UUID.fromString(source.getSourceBinding()));
                                locked.countDown();
                                try {
                                  assertTrue(unlock.await(5, TimeUnit.SECONDS));
                                } catch (InterruptedException e) {
                                  throw new AssertionError(e);
                                }
                                return null;
                              }));
              assertTrue(locked.await(5, TimeUnit.SECONDS));
              var revoked =
                  executor.submit(
                      () ->
                          TenantContext.call(
                              tenant,
                              () ->
                                  stub.revokeAgentSource(
                                      stop(source, "contended-stop").toBuilder()
                                          .clearExpectedRevision()
                                          .build())));
              try {
                org.awaitility.Awaitility.await()
                    .atMost(Duration.ofSeconds(3))
                    .until(
                        () -> {
                          try (var c =
                                  PostgresExtension.database(
                                          NetworkSourceAuthorityPostgresTest.class)
                                      .dataSource()
                                      .getConnection();
                              var s = c.createStatement();
                              var rs =
                                  s.executeQuery(
                                      "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%network_source%' AND query LIKE '%FOR UPDATE%'")) {
                            return rs.next() && rs.getLong(1) > 0;
                          }
                        });
              } finally {
                unlock.countDown();
              }
              held.get(5, TimeUnit.SECONDS);
              assertEquals(2, revoked.get(5, TimeUnit.SECONDS).getSource().getStateValue());
              resume.countDown();
              assertEquals(
                  io.grpc.Status.Code.PERMISSION_DENIED,
                  pending.get(5, TimeUnit.SECONDS).getStatus().getCode());
            } catch (Exception e) {
              throw new AssertionError(e);
            } finally {
              resume.countDown();
            }
          });
    }
  }
}
