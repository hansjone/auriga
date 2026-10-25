package auriga.collab.network.local;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.collab.api.NetworkSourceAuthority;
import auriga.collab.app.NetworkJson;
import auriga.common.context.RequestContext;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.persistence.CommonMigrations;
import auriga.common.persistence.TenantTransactionManager;
import auriga.common.persistence.UuidV7;
import auriga.common.security.*;
import auriga.iam.api.NetworkAuthority;
import auriga.testsupport.PostgresExtension;
import java.time.*;
import java.util.*;
import java.util.concurrent.*;
import java.util.function.Supplier;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.transaction.support.TransactionSynchronizationManager;
import org.springframework.transaction.support.TransactionTemplate;
import tools.jackson.databind.node.ObjectNode;

@ExtendWith(PostgresExtension.class)
class LocalRegistryPostgresTest {
  static JdbcTemplate jdbc;
  static TenantTransactionManager manager;
  static final Instant NOW = Instant.parse("2026-10-01T00:00:00Z");
  static final String FP = "sha256:" + "a".repeat(64);
  static final UUID LEGACY_TENANT = UUID.randomUUID();
  static final AurigaPrincipal GATEWAY =
      new AurigaPrincipal(
          "svc:ai-gateway",
          TenantContext.PLATFORM_TENANT,
          PrincipalType.INTERNAL_SERVICE,
          TokenContext.SERVICE,
          null,
          null,
          null,
          List.of(),
          Set.of(),
          null,
          null,
          "test");
  final UUID tenant = UUID.randomUUID();
  NetworkSourceAuthority authority;
  NetworkAuthority iam;
  LocalRegistry registry;
  volatile long epoch = 1;

  @BeforeAll
  static void migrate() {
    var roles =
        PostgresExtension.serviceRoles(
            LocalRegistryPostgresTest.class, "local_owner", "local_app", "collab");
    CommonMigrations.migrate(roles.owner().dataSource(), "collab");
    var configuration =
        Flyway.configure()
            .dataSource(roles.owner().dataSource())
            .schemas("collab")
            .defaultSchema("collab")
            .locations("classpath:db/collab");
    CommonMigrations.serviceFlyway(configuration.target("5").load()).migrate();
    jdbc = new JdbcTemplate(roles.runtime().dataSource());
    manager = new TenantTransactionManager(roles.runtime().dataSource());
    TenantContext.run(
        LEGACY_TENANT,
        () ->
            new TransactionTemplate(manager)
                .executeWithoutResult(
                    s ->
                        jdbc.update(
                            "INSERT INTO collab.network_node(tenant_id,realm,node_id) VALUES (?,'2','20')",
                            LEGACY_TENANT)));
    CommonMigrations.serviceFlyway(configuration.target("latest").load()).migrate();
  }

  @BeforeEach
  void setup() {
    authority = mock(NetworkSourceAuthority.class);
    when(authority.authorizeDelivery(eq(tenant), anyString(), anyString(), eq("20")))
        .thenReturn(NOW.plusSeconds(600));
    when(authority.currentTarget(tenant))
        .thenAnswer(
            i ->
                new NetworkSourceAuthority.Target(
                    "2", "20", "incarnation", epoch, "nonce", "request", NOW.plusSeconds(3600)));
    iam = mock(NetworkAuthority.class);
    when(iam.active(anyString(), anyString()))
        .thenAnswer(
            i -> {
              assertFalse(TransactionSynchronizationManager.isActualTransactionActive());
              assertFalse(TransactionSynchronizationManager.hasResource(jdbc.getDataSource()));
              return true;
            });
    when(iam.check(any()))
        .thenAnswer(
            i -> {
              assertFalse(TransactionSynchronizationManager.isActualTransactionActive());
              return new NetworkAuthority.Decision(true, "allowed", Map.of());
            });
    registry = new LocalRegistry(jdbc, manager, authority, iam, Clock.fixed(NOW, ZoneOffset.UTC));
    sql(
        () ->
            jdbc.update(
                "INSERT INTO collab.network_node(tenant_id,realm,node_id,connection_epoch) VALUES (?,'2','20',1)",
                tenant));
  }

  @Test
  void migrationIsolatesLegacyIdentityWithoutInventingHistoryUnderRls() {
    TenantContext.run(
        LEGACY_TENANT,
        () ->
            new TransactionTemplate(manager)
                .executeWithoutResult(
                    s -> {
                      var legacy =
                          jdbc.queryForMap(
                              "SELECT * FROM collab.network_node WHERE tenant_id=?", LEGACY_TENANT);
                      assertEquals("recovery_conflict", legacy.get("local_identity_status"));
                      assertEquals(
                          "network.recovery_conflict", legacy.get("local_identity_reason"));
                      assertNotNull(legacy.get("local_identity_observed_at"));
                      assertEquals(
                          0,
                          jdbc.queryForObject(
                              "SELECT count(*) FROM collab.network_local_registration",
                              Integer.class));
                    }));
    assertEquals("healthy", state("local_identity_status"));
  }

  @Test
  void registrationRetryReturnsOriginalWitnessEvenWithStaleRevisionAndAfterRestart() {
    var first = register("7", "op", FP, "0");
    register("8", "next", FP, "1");
    registry = new LocalRegistry(jdbc, manager, authority, iam, Clock.fixed(NOW, ZoneOffset.UTC));
    var retry = register("7", "op", FP, "0");
    assertEquals(first.registrationId(), retry.registrationId());
    assertEquals("existing", retry.result());
    assertEquals("1", retry.registrationRevision());
    assertEquals("7", retry.localHighWater());
    assertEquals("2", state("registration_revision"));
  }

  @Test
  void supportsBurnedGapsAndUint64MaximumButNeverWraps() {
    register("7", "op", FP, "0");
    register("18446744073709551615", "last", FP, "1");
    assertEquals("18446744073709551615", state("local_high_water"));
    error("network.protocol_error", () -> register("18446744073709551616", "overflow", FP, "2"));
    error("network.local_allocation_conflict", () -> register("1", "wrapped", FP, "2"));
  }

  @Test
  void revisionCasFailureDoesNotBurnOrQuarantineANewLocal() {
    register("7", "op", FP, "0");
    error("network.conflict", () -> register("8", "next", FP, "0"));
    assertEquals("healthy", state("local_identity_status"));
    assertEquals("registered", register("8", "next", FP, "1").result());
  }

  @Test
  void alteredOperationOrFingerprintPermanentlyQuarantinesIdentityAcrossEpochs() {
    register("7", "op", FP, "0");
    error("network.local_allocation_conflict", () -> register("7", "clone", FP, "1"));
    assertEquals("node_identity_conflict", state("local_identity_status"));
    takeover();
    error("network.local_allocation_conflict", () -> register("8", "next", FP, "1"));
    assertEquals("existing", register("7", "op", FP, "0").result());
    error(
        "network.local_allocation_conflict",
        () -> call(() -> registry.requireRegistered(tenant, "2", "20", "7", null, null)));
  }

  @Test
  void operationCannotMoveToAnotherLocalOrChangeFingerprint() {
    register("7", "op", FP, "0");
    error(
        "network.local_allocation_conflict",
        () -> register("8", "op", "sha256:" + "b".repeat(64), "1"));
    assertEquals("7", state("local_high_water"));
  }

  @Test
  void racingClonesHaveOneImmutableWinnerAndDurableConflict() throws Exception {
    var outcomes =
        race(
            () -> register("7", "clone-a", FP, "0"),
            () -> register("7", "clone-b", "sha256:" + "b".repeat(64), "0"));
    assertEquals(1, outcomes.stream().filter(LocalRegistry.Registration.class::isInstance).count());
    assertEquals(
        List.of("network.local_allocation_conflict"),
        outcomes.stream().filter(String.class::isInstance).toList());
    assertEquals("node_identity_conflict", state("local_identity_status"));
  }

  @Test
  void staleEpochAndChangedBootCannotRegisterOrAssign() {
    register("7", "op", FP, "0");
    error(
        "network.stale_epoch",
        () ->
            call(
                () ->
                    registry.register(
                        tenant, frame("8", "next", FP, "1").put("epoch", "2").toString())));
    error(
        "network.stale_epoch",
        () ->
            call(
                () ->
                    registry.register(
                        tenant, frame("8", "next", FP, "1").put("boot_id", "other").toString())));
    takeover();
    assertEquals("registered", register("8", "next", FP, "1").result());
  }

  @Test
  void lostRegistrationResponseAndUncommittedHeadAutomaticallyReconcile() {
    register("7", "op", FP, "0");
    takeover();
    var recovery = recover("7", "0", pending("7", "op", FP));
    assertEquals("ready", recovery.status());
    assertEquals("existing", recovery.pendingRegistration().result());
    assertEquals("healthy", state("local_identity_status"));
    var uncommitted = recover("8", "1", pending("8", "next", FP));
    assertEquals("ready", uncommitted.status());
    assertNull(uncommitted.pendingRegistration());
    assertEquals("registered", register("8", "next", FP, "1").result());
  }

  @Test
  void unexplainedOldSnapshotIsIsolatedAndCannotBeWashedByRaisingCounters() {
    register("7", "op", FP, "0");
    assertEquals("identity_rollback", recover("0", "0", null).status());
    assertEquals("identity_rollback", recover("7", "1", null).status());
    error("network.identity_rollback", () -> register("8", "next", FP, "1"));
  }

  @Test
  void platformBackupRegressionOrForgedPendingEvidenceFailsClosed() {
    assertEquals("recovery_conflict", recover("7", "1", null).status());
    assertEquals("recovery_conflict", recover("0", "0", null).status());
    error("network.recovery_conflict", () -> register("8", "op", FP, "0"));
  }

  @Test
  void mismatchedPendingFingerprintIsNotRepaired() {
    register("7", "op", FP, "0");
    assertEquals(
        "recovery_conflict",
        recover("7", "0", pending("7", "op", "sha256:" + "b".repeat(64))).status());
  }

  @Test
  void contactGetsExactlyOneCreationPermitAcrossRetryRestartAndTakeover() {
    var registered = register("7", "op", FP, "0");
    UUID contact = contact("allowed", "conversation", NOW.plusSeconds(600));
    var first = assign(contact, "7", registered.registrationId(), "create");
    assertTrue(first.creationPermit());
    assertEquals("assigned", first.result());
    assertFalse(assign(contact, "7", registered.registrationId(), "create").creationPermit());
    takeover();
    var retry = assign(contact, "7", registered.registrationId(), "new-attempt");
    assertFalse(retry.creationPermit());
    assertEquals("create", retry.creationAttemptId());
    assertEquals("existing", retry.result());
  }

  @Test
  void racingContactAssignmentsOnlyIssueOnePermit() throws Exception {
    var reg = register("7", "op", FP, "0");
    UUID contact = contact("allowed", "conversation", NOW.plusSeconds(600));
    var outcomes =
        race(
            () -> assign(contact, "7", reg.registrationId(), "a"),
            () -> assign(contact, "7", reg.registrationId(), "b"));
    assertEquals(
        1,
        outcomes.stream()
            .map(LocalRegistry.Assignment.class::cast)
            .filter(LocalRegistry.Assignment::creationPermit)
            .count());
  }

  @Test
  void waitingForContactDoesNotHoldNodeAndTakeoverFencesTheWaitingAssignment() throws Exception {
    var reg = register("7", "op", FP, "0");
    UUID contact = contact("allowed", "conversation", NOW.plusSeconds(600));
    try (var pool = Executors.newVirtualThreadPerTaskExecutor()) {
      var submitted = new CountDownLatch(1);
      var waiting = new java.util.concurrent.atomic.AtomicReference<Future<String>>();
      sql(
          () -> {
            jdbc.queryForMap(
                "SELECT id FROM collab.network_contact WHERE tenant_id=? AND id=? FOR UPDATE",
                tenant,
                contact);
            waiting.set(
                pool.submit(
                    () -> {
                      submitted.countDown();
                      try {
                        assign(contact, "7", reg.registrationId(), "create");
                        return "unexpected_permit";
                      } catch (AurigaException e) {
                        return e.errorCode().code();
                      }
                    }));
            try {
              assertTrue(submitted.await(10, TimeUnit.SECONDS));
            } catch (InterruptedException e) {
              Thread.currentThread().interrupt();
              throw new AssertionError(e);
            }
            long deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(10);
            boolean blocked = false;
            do {
              jdbc.execute("SELECT pg_stat_clear_snapshot()");
              blocked =
                  jdbc.queryForObject(
                      "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE usename=current_user AND pid<>pg_backend_pid() AND query LIKE '%network_contact WHERE%' AND wait_event_type='Lock')",
                      Boolean.class);
              if (!blocked) Thread.yield();
            } while (!blocked && System.nanoTime() < deadline);
            assertTrue(blocked, "assignment must be waiting on the held contact");
            jdbc.queryForMap(
                "SELECT node_id FROM collab.network_node WHERE tenant_id=? AND realm='2' AND node_id='20' FOR UPDATE NOWAIT",
                tenant);
            jdbc.update(
                "UPDATE collab.network_node SET connection_epoch=2 WHERE tenant_id=? AND realm='2' AND node_id='20'",
                tenant);
            return null;
          });
      assertEquals("network.stale_epoch", waiting.get().get(20, TimeUnit.SECONDS));
    }
  }

  @Test
  void localCannotBeSharedAcrossContactsAndContactCannotChangeLocal() {
    var a = register("7", "a", FP, "0");
    var b = register("8", "b", FP, "1");
    UUID first = contact("allowed", "conversation", NOW.plusSeconds(600));
    UUID second = contact("allowed", "conversation", NOW.plusSeconds(600));
    assign(first, "7", a.registrationId(), "create");
    error("network.conflict", () -> assign(second, "7", a.registrationId(), "create"));
    error("network.conflict", () -> assign(first, "8", b.registrationId(), "other"));
  }

  @Test
  void assignmentRejectsMissingWrongRegistrationAndWrongAddress() {
    var reg = register("7", "op", FP, "0");
    UUID contact = contact("allowed", "conversation", NOW.plusSeconds(600));
    error(
        "network.local_registration_required",
        () -> assign(contact, "8", reg.registrationId(), "create"));
    error(
        "network.local_registration_required",
        () -> assign(contact, "7", UUID.randomUUID(), "create"));
    var request = assignment(contact, "7", reg.registrationId(), "create");
    ((ObjectNode) request.path("assigned_address")).put("node", "21");
    error("network.unauthorized", () -> call(() -> registry.assign(tenant, request.toString())));
    ((ObjectNode) request.path("assigned_address")).put("node", "20").put("type", "subagent");
    error("network.protocol_error", () -> call(() -> registry.assign(tenant, request.toString())));
  }

  @Test
  void assignmentRechecksDecisionVersionExpiryScopeAndRevocationEvenOnRetry() {
    var reg = register("7", "op", FP, "0");
    UUID pending = contact("pending", "conversation", NOW.plusSeconds(600));
    error("network.contact_denied", () -> assign(pending, "7", reg.registrationId(), "create"));
    UUID expired = contact("allowed", "conversation", NOW);
    error("network.contact_expired", () -> assign(expired, "7", reg.registrationId(), "create"));
    UUID human = contact("allowed", "human_dialogue", NOW.plusSeconds(600));
    error("network.protocol_error", () -> assign(human, "7", reg.registrationId(), "create"));
    UUID valid = contact("allowed", "conversation", NOW.plusSeconds(600));
    var old = assignment(valid, "7", reg.registrationId(), "create").put("decision_version", "1");
    error("network.conflict", () -> call(() -> registry.assign(tenant, old.toString())));
    assign(valid, "7", reg.registrationId(), "create");
    sql(
        () ->
            jdbc.update(
                "UPDATE collab.network_contact SET status='denied',decision_version=3 WHERE tenant_id=? AND id=?",
                tenant,
                valid));
    error("network.contact_denied", () -> assign(valid, "7", reg.registrationId(), "create"));
  }

  @Test
  void authorityIamAndTenantBoundariesFailClosedWithoutWrites() {
    when(iam.active("2", "20")).thenReturn(false);
    error("network.unauthorized", () -> register("7", "op", FP, "0"));
    when(iam.active("2", "20")).thenThrow(new IllegalStateException("offline"));
    error("common.unavailable", () -> register("7", "op", FP, "0"));
    doReturn(true).when(iam).active("2", "20");
    when(iam.check(any())).thenReturn(new NetworkAuthority.Decision(false, "denied", Map.of()));
    error("network.unauthorized", () -> register("7", "op", FP, "0"));
    when(authority.currentTarget(tenant)).thenReturn(null);
    error("network.unauthorized", () -> register("7", "op", FP, "0"));
    error(
        "common.unauthenticated",
        () -> registry.register(tenant, frame("7", "op", FP, "0").toString()));
    error(
        "common.forbidden",
        () ->
            call(() -> registry.register(UUID.randomUUID(), frame("7", "op", FP, "0").toString())));
    assertEquals("0", state("local_high_water"));
  }

  @Test
  void unavailableAuthorityAtEveryIngressNeverWritesWitnessOrPermit() {
    registry = new LocalRegistry(jdbc, manager, null, iam, Clock.fixed(NOW, ZoneOffset.UTC));
    unavailableIngresses("common.unavailable");
    registry = new LocalRegistry(jdbc, manager, authority, iam, Clock.fixed(NOW, ZoneOffset.UTC));
    doReturn(null).when(authority).currentTarget(tenant);
    unavailableIngresses("network.unauthorized");
    doThrow(new IllegalStateException("offline-private-detail"))
        .when(authority)
        .currentTarget(tenant);
    unavailableIngresses("common.unavailable");
    assertNoNewIdentityWrites();
    verify(iam, never()).active(anyString(), anyString());
    verify(iam, never()).check(any());
  }

  @Test
  void iamOutageOrMissingDecisionAtEveryIngressNeverWritesWitnessOrPermit() {
    doThrow(new IllegalStateException("offline")).when(iam).active("2", "20");
    unavailableIngresses("common.unavailable");
    doReturn(true).when(iam).active("2", "20");
    doThrow(new IllegalStateException("offline")).when(iam).check(any());
    unavailableIngresses("common.unavailable");
    doReturn(null).when(iam).check(any());
    unavailableIngresses("common.unavailable");
    assertNoNewIdentityWrites();
  }

  @Test
  void deliveryAuthorityOutageRollsBackBootBindingAndNeverIssuesPermit() {
    var registered = register("7", "op", FP, "0");
    UUID contact = contact("allowed", "conversation", NOW.plusSeconds(600));
    takeover();
    doThrow(new IllegalStateException("offline-private-detail"))
        .when(authority)
        .authorizeDelivery(eq(tenant), anyString(), anyString(), eq("20"));
    error("common.unavailable", () -> assign(contact, "7", registered.registrationId(), "create"));
    doReturn(null)
        .when(authority)
        .authorizeDelivery(eq(tenant), anyString(), anyString(), eq("20"));
    error(
        "network.unauthorized", () -> assign(contact, "7", registered.registrationId(), "create"));
    assertEquals("1", state("local_boot_epoch"));
    assertEquals("1", state("registration_revision"));
    assertEquals("7", state("local_high_water"));
    assertEquals("healthy", state("local_identity_status"));
    assertEquals(
        0,
        sql(
            () ->
                jdbc.queryForObject(
                    "SELECT count(*) FROM collab.network_contact_assignment WHERE tenant_id=?",
                    Integer.class,
                    tenant)));
    assertEquals(
        0,
        sql(
            () ->
                jdbc.queryForObject(
                    "SELECT count(*) FROM collab.outbox_event WHERE tenant_id=?",
                    Integer.class,
                    tenant)));
  }

  void unavailableIngresses(String code) {
    error(code, () -> register("7", "op", FP, "0"));
    error(code, () -> assign(UUID.randomUUID(), "7", UUID.randomUUID(), "create"));
    error(code, () -> recover("0", "0", null));
  }

  void assertNoNewIdentityWrites() {
    assertEquals("0", state("local_high_water"));
    assertEquals("0", state("registration_revision"));
    assertEquals("healthy", state("local_identity_status"));
    assertNull(state("local_boot_epoch"));
    assertNull(state("local_boot_id"));
    sql(
        () -> {
          assertEquals(
              1,
              jdbc.queryForObject(
                  "SELECT count(*) FROM collab.network_node WHERE tenant_id=?",
                  Integer.class,
                  tenant));
          assertEquals(
              0,
              jdbc.queryForObject(
                  "SELECT count(*) FROM collab.network_local_registration WHERE tenant_id=?",
                  Integer.class,
                  tenant));
          assertEquals(
              0,
              jdbc.queryForObject(
                  "SELECT count(*) FROM collab.network_contact_assignment WHERE tenant_id=?",
                  Integer.class,
                  tenant));
          assertEquals(
              0,
              jdbc.queryForObject(
                  "SELECT count(*) FROM collab.outbox_event WHERE tenant_id=?",
                  Integer.class,
                  tenant));
          return null;
        });
  }

  @Test
  void registeredBindingMustKeepOriginalRegistrationAndFingerprint() {
    var reg = register("7", "op", FP, "0");
    assertEquals(
        reg.registrationId(),
        call(() -> registry.requireRegistered(tenant, "2", "20", "7", reg.registrationId(), FP))
            .registrationId());
    error(
        "network.local_registration_required",
        () ->
            call(
                () ->
                    registry.requireRegistered(
                        tenant, "2", "20", "7", reg.registrationId(), "sha256:" + "b".repeat(64))));
  }

  @Test
  void rlsHidesWitnessesAndAssignmentsFromOtherTenants() {
    var reg = register("7", "op", FP, "0");
    assign(
        contact("allowed", "conversation", NOW.plusSeconds(600)),
        "7",
        reg.registrationId(),
        "create");
    TenantContext.run(
        UUID.randomUUID(),
        () ->
            new TransactionTemplate(manager)
                .execute(
                    s -> {
                      assertEquals(
                          0,
                          jdbc.queryForObject(
                              "SELECT count(*) FROM collab.network_local_registration",
                              Integer.class));
                      assertEquals(
                          0,
                          jdbc.queryForObject(
                              "SELECT count(*) FROM collab.network_contact_assignment",
                              Integer.class));
                      return null;
                    }));
  }

  @Test
  void systemHealthGateAndAmbientTransactionsCannotBypassDurableIsolation() {
    call(
        () -> {
          registry.requireHealthy(tenant, "2", "20");
          return null;
        });
    error(
        "common.unavailable",
        () ->
            call(() -> sql(() -> registry.register(tenant, frame("7", "op", FP, "0").toString()))));
    register("7", "op", FP, "0");
    error("network.local_allocation_conflict", () -> register("7", "clone", FP, "1"));
    error(
        "network.local_allocation_conflict",
        () ->
            call(
                () -> {
                  registry.requireHealthy(tenant, "2", "20");
                  return null;
                }));
  }

  @Test
  void assignmentChecksActualAgentGrantAndCurrentSourceAuthorization() {
    var reg = register("7", "op", FP, "0");
    UUID contact = contact("allowed", "conversation", NOW.plusSeconds(600));
    when(iam.check(
            argThat(
                c ->
                    c != null
                        && c.operation() == NetworkAuthority.Operation.FIRST_CONTACT
                        && c.sender() == NetworkAuthority.Sender.AGENT)))
        .thenReturn(new NetworkAuthority.Decision(false, "denied", Map.of()));
    error("network.unauthorized", () -> assign(contact, "7", reg.registrationId(), "create"));
    when(authority.authorizeDelivery(eq(tenant), anyString(), anyString(), eq("20")))
        .thenReturn(null);
    error("network.unauthorized", () -> assign(contact, "7", reg.registrationId(), "create"));
    assertEquals(
        0,
        sql(
            () ->
                jdbc.queryForObject(
                    "SELECT count(*) FROM collab.network_contact_assignment WHERE tenant_id=?",
                    Integer.class,
                    tenant)));
  }

  @Test
  void contactExpiringDuringIamCheckDoesNotReceiveCreationPermit() {
    var reg = register("7", "op", FP, "0");
    UUID contact = contact("allowed", "conversation", NOW.plusSeconds(10));
    var clock = mock(Clock.class);
    when(clock.instant()).thenReturn(NOW);
    registry = new LocalRegistry(jdbc, manager, authority, iam, clock);
    when(iam.check(
            argThat(c -> c != null && c.operation() == NetworkAuthority.Operation.FIRST_CONTACT)))
        .thenAnswer(
            i -> {
              when(clock.instant()).thenReturn(NOW.plusSeconds(10));
              return new NetworkAuthority.Decision(true, "allowed", Map.of());
            });
    error("network.contact_expired", () -> assign(contact, "7", reg.registrationId(), "create"));
  }

  LocalRegistry.Registration register(String local, String op, String fp, String revision) {
    return call(() -> registry.register(tenant, frame(local, op, fp, revision).toString()));
  }

  ObjectNode frame(String local, String op, String fp, String revision) {
    var frame = (ObjectNode) NetworkJson.parse(LocalFramesTest.register(local, revision));
    return frame
        .put("epoch", Long.toString(epoch))
        .put("allocation_op_id", op)
        .put("allocation_fingerprint", fp);
  }

  ObjectNode assignment(UUID contact, String local, UUID reg, String attempt) {
    return (ObjectNode)
        NetworkJson.parse(
            """
        {"app_version":"1.0","type":"contact_assign","request_id":"request","epoch":"%s",
         "boot_id":"boot","contact_id":"%s","assigned_address":
         {"version":1,"realm":"2","type":"session","node":"20","local":"%s"},
         "registration_id":"%s","creation_attempt_id":"%s","decision_version":"2"}
        """
                .formatted(epoch, contact, local, reg, attempt));
  }

  LocalRegistry.Assignment assign(UUID contact, String local, UUID reg, String attempt) {
    return call(() -> registry.assign(tenant, assignment(contact, local, reg, attempt).toString()));
  }

  LocalRegistry.Recovery recover(String high, String revision, ObjectNode pending) {
    var ready =
        (ObjectNode)
            NetworkJson.parse(
                """
        {"app_version":"1.0","type":"ready","request_id":"request","channel_nonce":"nonce",
         "boot_id":"boot","features":[],"adapter_capabilities":[],"resume_cursor":"0",
         "local_high_water":"%s","registration_revision":"%s","receipt_checkpoint":
         {"checkpoint_seq":"0","checkpoint_revision":"0","summary_digest":"%s"},
         "keyset_version":"1","pending_registration":null}
        """
                    .formatted(high, revision, FP));
    if (pending != null) ready.set("pending_registration", pending);
    return call(() -> registry.reconcileRegistration(tenant, ready.toString()));
  }

  ObjectNode pending(String local, String op, String fp) {
    return (ObjectNode)
        NetworkJson.parse(
            """
        {"candidate_local":"%s","allocation_op_id":"%s","allocation_fingerprint":"%s"}
        """
                .formatted(local, op, fp));
  }

  void takeover() {
    epoch++;
    sql(
        () ->
            jdbc.update(
                "UPDATE collab.network_node SET connection_epoch=? WHERE tenant_id=? AND realm='2' AND node_id='20'",
                epoch,
                tenant));
  }

  UUID contact(String status, String scope, Instant expires) {
    UUID message = UuidV7.next();
    UUID contact = UuidV7.next();
    sql(
        () -> {
          jdbc.update(
              "INSERT INTO collab.network_message(id,tenant_id,realm,kind,body,source_node,source_local,message_json,source_context_json) VALUES (?,?,'2','first_contact','hello','10','1',?,?)",
              message,
              tenant,
              "{\"sender_kind\":\"agent\"}",
              "{\"sender_kind\":\"agent\",\"message_digest\":\""
                  + NetworkJson.digest(NetworkJson.parse("{\"sender_kind\":\"agent\"}"))
                  + "\"}");
          return jdbc.update(
              "INSERT INTO collab.network_contact(id,tenant_id,message_id,status,scope,source_realm,source_node,source_local,target_realm,target_node,expires_at,decision_version) VALUES (?,?,?,?,?,'2','10','1','2','20',?,2)",
              contact,
              tenant,
              message,
              status,
              scope,
              java.sql.Timestamp.from(expires));
        });
    return contact;
  }

  String state(String column) {
    return sql(
        () ->
            jdbc.queryForObject(
                "SELECT "
                    + column
                    + "::text FROM collab.network_node WHERE tenant_id=? AND realm='2' AND node_id='20'",
                String.class,
                tenant));
  }

  <T> T sql(Supplier<T> work) {
    return TenantContext.call(
        tenant, () -> new TransactionTemplate(manager).execute(s -> work.get()));
  }

  <T> T call(Supplier<T> work) {
    return RequestContext.start(null, null)
        .withPrincipal(GATEWAY)
        .call(() -> TenantContext.call(tenant, work::get));
  }

  static void error(String code, Runnable work) {
    assertEquals(code, assertThrows(AurigaException.class, work::run).errorCode().code());
  }

  List<Object> race(Supplier<?> a, Supplier<?> b) throws Exception {
    var start = new CountDownLatch(1);
    try (var pool = Executors.newVirtualThreadPerTaskExecutor()) {
      var tasks = new ArrayList<Future<Object>>();
      for (var work : List.of(a, b))
        tasks.add(
            pool.submit(
                () -> {
                  assertTrue(start.await(10, TimeUnit.SECONDS));
                  try {
                    return work.get();
                  } catch (AurigaException e) {
                    return e.errorCode().code();
                  }
                }));
      start.countDown();
      return List.of(
          tasks.get(0).get(20, TimeUnit.SECONDS), tasks.get(1).get(20, TimeUnit.SECONDS));
    }
  }
}
