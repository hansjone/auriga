package auriga.workspace;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.context.TenantContext;
import auriga.common.persistence.*;
import auriga.contracts.node.v1.*;
import auriga.testsupport.PostgresExtension;
import auriga.workspace.api.SharedDshAuthority;
import auriga.workspace.app.SharedDshPlacements;
import java.time.*;
import java.util.*;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;

/** Real temporary PostgreSQL, non-owner RLS role; external IAM/E/R ports use controlled doubles. */
@ExtendWith(PostgresExtension.class)
class SharedDshPostgresTest {
  static auriga.workspace.persistence.WorkspaceStore store;
  UUID tenant = UUID.randomUUID(), session = UUID.randomUUID(), actor = UUID.randomUUID();
  long seconds = 1000;
  boolean authorityAvailable = true, probe = true, expiredResources = false;
  final Set<UUID> disabled = new HashSet<>();
  final Map<UUID, String> digests = new HashMap<>();
  final Clock clock =
      new Clock() {
        public ZoneId getZone() {
          return ZoneOffset.UTC;
        }

        public Clock withZone(ZoneId zone) {
          return this;
        }

        public Instant instant() {
          return Instant.ofEpochSecond(seconds);
        }
      };
  final SharedDshAuthority.Inputs inputs =
      new SharedDshAuthority.Inputs() {
        public void requireMember(UUID t, UUID m) {
          if (disabled.contains(m))
            throw new auriga.common.error.AurigaException(
                auriga.common.error.CommonErrors.FORBIDDEN);
          if (!authorityAvailable) throw new IllegalStateException("authority unavailable");
        }

        public void requireSession(UUID t, UUID m, UUID s) {
          requireMember(t, m);
        }

        public String authorizationSetDigest(UUID t, UUID m) {
          return digests.getOrDefault(m, "sha256:" + "a".repeat(64));
        }

        public SharedDshResourceView resourceView(SharedDshBinding b) {
          return SharedDshResourceView.newBuilder()
              .setSchemaVersion(1)
              .setBinding(b)
              .setViewId(b.getPlacementId())
              .setAuthorizationSetDigest(
                  authorizationSetDigest(
                      UUID.fromString(b.getTenantId()), UUID.fromString(b.getMemberId())))
              .setSource("platform")
              .addAllResources(
                  expiredResources
                      ? List.of(
                          SharedDshResource.newBuilder()
                              .setResourceId(UUID.randomUUID().toString())
                              .setDigest("sha256:" + "d".repeat(64))
                              .setLeaseId(UUID.randomUUID().toString())
                              .setExpiresAt(seconds - 1)
                              .build())
                      : List.of())
              .build();
        }

        public void requirePlacementWrite(UUID t, UUID a, UUID s, long now) {
          requireMember(t, a);
        }
      };
  SharedDshPlacements service;

  @BeforeAll
  static void database() {
    var roles =
        PostgresExtension.serviceRoles(
            SharedDshPostgresTest.class, "workspace_owner", "workspace_app", "workspace");
    CommonMigrations.migrate(roles.owner().dataSource(), "workspace");
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas("workspace")
                .defaultSchema("workspace")
                .locations("classpath:db/workspace")
                .load())
        .migrate();
    store =
        new auriga.workspace.persistence.WorkspaceStore(
            new JdbcTemplate(roles.runtime().dataSource()),
            new TenantTransactionManager(roles.runtime().dataSource()));
  }

  @BeforeEach
  void setup() {
    service = new SharedDshPlacements(store, inputs, b -> probe, clock);
  }

  <T> T run(java.util.function.Supplier<T> work) {
    return TenantContext.call(tenant, work::get);
  }

  SharedDshAuthority.Placement allocate(UUID member) {
    return run(() -> service.allocate(member, session));
  }

  SharedDshAuthority.Placement ready(UUID member) {
    var p = allocate(member);
    var i =
        auriga.workspace.domain.SharedDshPolicy.intent(
            p.binding().getPlacementId(), p.binding(), "prepare-empty");
    assertTrue(run(() -> service.confirmInitial(ack(i).setWorkspaceEmpty(true).build())));
    return run(
        () -> service.resolve(member, UUID.fromString(p.binding().getPlacementId()), session));
  }

  SharedDshRuntimeReceipt.Builder ack(SharedDshRuntimeIntent i) {
    return SharedDshRuntimeReceipt.newBuilder()
        .setSchemaVersion(1)
        .setOperationId(i.getOperationId())
        .setBinding(i.getBinding())
        .setReceiptId(UUID.randomUUID().toString())
        .setAction(i.getAction())
        .setOutcome("confirmed")
        .setObservedAt(seconds);
  }

  SharedDshRuntimeIntent command(UUID operation) {
    return run(
        () ->
            store.tx(
                () -> {
                  try {
                    return SharedDshRuntimeIntent.parseFrom(
                        (byte[])
                            store
                                .one(
                                    "SELECT intent FROM shared_dsh_operation WHERE tenant_id=? AND"
                                        + " id=?",
                                    tenant,
                                    operation)
                                .get("intent"));
                  } catch (Exception e) {
                    throw new IllegalStateException(e);
                  }
                }));
  }

  @Test
  void tenantRlsAndTwentyRegisteredFourActiveOnePerMember() {
    List<UUID> members = new ArrayList<>();
    String pool = null;
    for (int n = 0; n < 20; n++) {
      UUID m = UUID.randomUUID();
      members.add(m);
      var p = ready(m);
      if (pool == null) pool = p.binding().getPhysicalInstanceId();
      else assertEquals(pool, p.binding().getPhysicalInstanceId());
    }
    assertNotEquals(pool, allocate(UUID.randomUUID()).binding().getPhysicalInstanceId());
    for (int n = 0; n < 4; n++) {
      UUID m = members.get(n);
      run(
          () -> {
            service.admit(m, session, UUID.randomUUID());
            return null;
          });
    }
    assertThrows(
        RuntimeException.class,
        () ->
            run(
                () -> {
                  service.admit(members.get(4), session, UUID.randomUUID());
                  return null;
                }));
    assertThrows(
        RuntimeException.class,
        () ->
            run(
                () -> {
                  service.admit(members.getFirst(), session, UUID.randomUUID());
                  return null;
                }));
    UUID foreign = UUID.randomUUID();
    assertThrows(
        RuntimeException.class,
        () ->
            TenantContext.call(
                foreign, () -> service.resolve(members.getFirst(), UUID.randomUUID(), session)));
    assertNotEquals(
        pool,
        TenantContext.call(foreign, () -> service.allocate(UUID.randomUUID(), session))
            .binding()
            .getPhysicalInstanceId());
  }

  @Test
  void timeoutIsDurableBlockedUntilExternalStopReceiptAndNeverRestoresSessions() {
    UUID member = UUID.randomUUID();
    var p = ready(member);
    UUID op =
        run(
            () ->
                service.switchMode(
                    actor, session, member, "dedicated", 1, "switch-1", "test switch"));
    assertEquals(
        op,
        run(
            () ->
                service.switchMode(
                    actor, session, member, "dedicated", 1, "switch-1", "test switch")));
    assertThrows(
        RuntimeException.class,
        () ->
            run(
                () ->
                    service.switchMode(
                        actor, session, member, "dedicated", 1, "switch-2", "test switch")));
    var cancel = command(op);
    seconds += 30;
    service = new SharedDshPlacements(store, inputs, b -> probe, clock);
    run(
        () -> {
          service.recover();
          return null;
        });
    var stop = command(op);
    assertEquals("stop-pool", stop.getAction());
    assertFalse(run(() -> service.receipt(ack(cancel).build())));
    assertFalse(run(() -> service.receipt(ack(stop).build())));
    assertThrows(
        RuntimeException.class,
        () ->
            run(
                () ->
                    service.resolve(
                        member, UUID.fromString(p.binding().getPlacementId()), session)));
    assertTrue(run(() -> service.receipt(ack(stop).setExecutionTreeStopped(true).build())));
    var discard = command(op);
    assertEquals("discard-sessions", discard.getAction());
    assertTrue(run(() -> service.receipt(ack(discard).setSessionsDiscarded(true).build())));
    var archive = command(op);
    assertEquals("archive-reset", archive.getAction());
    assertFalse(run(() -> service.receipt(ack(archive).setWorkspaceEmpty(true).build())));
    assertTrue(
        run(
            () ->
                service.receipt(
                    ack(archive)
                        .setWorkspaceEmpty(true)
                        .setArchiveId(UUID.randomUUID().toString())
                        .setArchiveExpiresAt(seconds + 2592000)
                        .build())));
    var prepare = command(op);
    assertEquals("prepare-empty", prepare.getAction());
    assertTrue(run(() -> service.receipt(ack(prepare).setWorkspaceEmpty(true).build())));
    probe = false;
    assertFalse(run(() -> service.commit(op)));
    probe = true;
    assertTrue(run(() -> service.commit(op)));
    var next =
        run(() -> service.resolve(member, UUID.fromString(p.binding().getPlacementId()), session));
    assertEquals(2, next.binding().getEpoch());
    assertEquals(DshInstanceMode.DSH_INSTANCE_MODE_DEDICATED, next.binding().getMode());
    assertFalse(run(() -> service.receipt(ack(cancel).build())));
    authorityAvailable = false;
    assertThrows(
        RuntimeException.class,
        () ->
            run(
                () ->
                    service.resolve(
                        member, UUID.fromString(p.binding().getPlacementId()), session)));
  }

  UUID change(UUID member, String target, String key) {
    var p = run(() -> service.current(member, session));
    return run(
        () ->
            service.switchMode(
                actor, session, member, target, p.binding().getRevision(), key, "test"));
  }

  void finish(UUID operation) {
    for (int n = 0; n < 6; n++) {
      var cmd = command(operation);
      var ack = ack(cmd);
      switch (cmd.getAction()) {
        case "cooperative-cancel" -> {}
        case "stop-pool" -> ack.setExecutionTreeStopped(true);
        case "discard-sessions" -> ack.setSessionsDiscarded(true);
        case "archive-reset" ->
            ack.setWorkspaceEmpty(true)
                .setArchiveId(UUID.randomUUID().toString())
                .setArchiveExpiresAt(seconds + 2592000);
        case "prepare-empty" -> ack.setWorkspaceEmpty(true);
        default -> fail("Unknown action " + cmd.getAction());
      }
      assertTrue(run(() -> service.receipt(ack.build())));
      if (cmd.getAction().equals("prepare-empty")) {
        assertTrue(run(() -> service.commit(operation)));
        return;
      }
    }
    fail("Rebuild did not finish");
  }

  SharedDshSessionBinding sessionBinding(SharedDshAuthority.Placement p, UUID id) {
    var b = p.binding();
    return SharedDshSessionBinding.newBuilder()
        .setTenantId(b.getTenantId())
        .setMemberId(b.getMemberId())
        .setPlacementId(b.getPlacementId())
        .setPhysicalInstanceId(b.getPhysicalInstanceId())
        .setMode(b.getMode())
        .setEpoch(b.getEpoch())
        .setRevision(b.getRevision())
        .setAuthGeneration(p.authGeneration())
        .setPlatformSessionId(session.toString())
        .setSessionId(id.toString())
        .build();
  }

  @Test
  void cooperativeSwitchBothDirectionsArchivesAndNeverRestoresOldSessions() {
    UUID member = UUID.randomUUID();
    var old = ready(member);
    UUID oldSession = UUID.randomUUID();
    var registered = sessionBinding(old, oldSession);
    assertEquals(registered, run(() -> service.register(registered, "")));
    assertEquals(registered, run(() -> service.register(registered, "")));
    var child = sessionBinding(old, UUID.randomUUID());
    assertEquals(child, run(() -> service.register(child, oldSession.toString())));
    var dedicated = change(member, "dedicated", "promote");
    assertThrows(RuntimeException.class, () -> run(() -> service.resolveSession(oldSession)));
    var wrong = ack(command(dedicated)).setBinding(old.binding().toBuilder().setEpoch(99)).build();
    assertFalse(run(() -> service.receipt(wrong)));
    finish(dedicated);
    assertThrows(RuntimeException.class, () -> run(() -> service.resolveSession(oldSession)));
    assertThrows(RuntimeException.class, () -> run(() -> service.register(registered, "")));
    var archive =
        run(
            () ->
                store.tx(
                    () ->
                        store.one(
                            "SELECT * FROM shared_dsh_operation WHERE tenant_id=? AND id=?",
                            tenant,
                            dedicated)));
    assertNotNull(archive.get("archive_id"));
    assertEquals(seconds + 2592000, ((Number) archive.get("archive_expires_at")).longValue());
    var demote = change(member, "shared", "demote");
    finish(demote);
    var result = run(() -> service.current(member, session));
    assertEquals(DshInstanceMode.DSH_INSTANCE_MODE_SHARED, result.binding().getMode());
    assertEquals(3, result.binding().getEpoch());
    assertEquals(
        0,
        run(
            () ->
                store.tx(
                    () ->
                        store.jdbc.queryForObject(
                            "SELECT count(*) FROM shared_dsh_session WHERE tenant_id=?",
                            Long.class,
                            tenant))));
  }

  @Test
  void concurrentSwitchesHaveOneWinnerAndOneReservation() throws Exception {
    UUID member = UUID.randomUUID();
    ready(member);
    var barrier = new java.util.concurrent.CyclicBarrier(2);
    try (var executor = java.util.concurrent.Executors.newFixedThreadPool(2)) {
      var futures = new ArrayList<java.util.concurrent.Future<Boolean>>();
      for (int n = 0; n < 2; n++) {
        String key = "race-" + n;
        futures.add(
            executor.submit(
                () -> {
                  barrier.await();
                  try {
                    run(
                        () ->
                            service.switchMode(
                                actor, session, member, "dedicated", 1, key, "race"));
                    return true;
                  } catch (auriga.common.error.AurigaException e) {
                    assertTrue(
                        Set.of(
                                auriga.common.error.CommonErrors.CONFLICT,
                                auriga.common.error.CommonErrors.PRECONDITION_FAILED)
                            .contains(e.errorCode()));
                    return false;
                  }
                }));
      }
      int winners = 0;
      for (var future : futures)
        if (future.get(20, java.util.concurrent.TimeUnit.SECONDS)) winners++;
      assertEquals(1, winners);
    }
    assertEquals(
        1,
        run(
            () ->
                store.tx(
                    () ->
                        store.jdbc.queryForObject(
                            "SELECT count(*) FROM shared_dsh_operation WHERE tenant_id=?",
                            Long.class,
                            tenant))));
    assertEquals(
        2,
        run(
            () ->
                store.tx(
                    () ->
                        store.jdbc.queryForObject(
                            "SELECT count(*) FROM shared_dsh_pool WHERE tenant_id=?",
                            Long.class,
                            tenant))));
  }

  @Test
  void authorizationSetsAndMemoryCannotBeUnionedOrSilentlyMoveExistingMembers() {
    UUID a = UUID.randomUUID(), b = UUID.randomUUID();
    var first = ready(a);
    digests.put(b, "sha256:" + "b".repeat(64));
    var other = ready(b);
    assertNotEquals(
        first.binding().getPhysicalInstanceId(), other.binding().getPhysicalInstanceId());
    run(
        () -> {
          service.memory(UUID.fromString(first.binding().getPhysicalInstanceId()), 80);
          return null;
        });
    assertThrows(
        RuntimeException.class,
        () ->
            run(
                () -> {
                  service.admit(a, session, UUID.randomUUID());
                  return null;
                }));
    var expanded = allocate(UUID.randomUUID());
    assertNotEquals(
        first.binding().getPhysicalInstanceId(), expanded.binding().getPhysicalInstanceId());
    assertEquals(first.binding(), run(() -> service.current(a, session)).binding());
    digests.put(a, "sha256:" + "c".repeat(64));
    assertThrows(
        RuntimeException.class,
        () ->
            run(
                () ->
                    service.resolve(
                        a, UUID.fromString(first.binding().getPlacementId()), session)));
  }

  @Test
  void fifthActiveMemberDoesNotCauseAnExistingSessionToMove() {
    List<UUID> members = new ArrayList<>();
    String pool = null;
    for (int n = 0; n < 5; n++) {
      UUID member = UUID.randomUUID();
      members.add(member);
      var p = ready(member);
      pool = p.binding().getPhysicalInstanceId();
    }
    for (int n = 0; n < 4; n++) {
      UUID member = members.get(n);
      run(
          () -> {
            service.admit(member, session, UUID.randomUUID());
            return null;
          });
    }
    String original = pool;
    assertThrows(
        RuntimeException.class,
        () ->
            run(
                () -> {
                  service.admit(members.get(4), session, UUID.randomUUID());
                  return null;
                }));
    assertEquals(
        original,
        run(() -> service.current(members.get(4), session)).binding().getPhysicalInstanceId());
    assertEquals(original, allocate(UUID.randomUUID()).binding().getPhysicalInstanceId());
  }

  @Test
  void crashAtEveryStepResendsSameIntentAndRejectsLateAck() {
    UUID member = UUID.randomUUID();
    ready(member);
    UUID op = change(member, "dedicated", "crash");
    for (int n = 0; n < 4; n++) {
      var before = command(op);
      run(
          () -> {
            for (var delivery : service.pending()) service.delivered(delivery.id());
            return null;
          });
      service = new SharedDshPlacements(store, inputs, b -> probe, clock);
      run(
          () -> {
            service.recover();
            return null;
          });
      assertEquals(before, command(op));
      assertTrue(
          run(() -> service.pending()).stream()
              .anyMatch(d -> Arrays.equals(d.payload(), before.toByteArray())));
      var receipt = ack(before);
      switch (before.getAction()) {
        case "discard-sessions" -> receipt.setSessionsDiscarded(true);
        case "archive-reset" ->
            receipt
                .setWorkspaceEmpty(true)
                .setArchiveId(UUID.randomUUID().toString())
                .setArchiveExpiresAt(seconds + 2592000);
        case "prepare-empty" -> receipt.setWorkspaceEmpty(true);
        default -> {}
      }
      assertTrue(run(() -> service.receipt(receipt.build())));
      assertFalse(run(() -> service.receipt(receipt.build())));
    }
    service = new SharedDshPlacements(store, inputs, b -> probe, clock);
    assertTrue(run(() -> service.commit(op)));
    assertTrue(run(() -> service.commit(op)));
    assertEquals(2, run(() -> service.current(member, session)).binding().getEpoch());
  }

  UUID legacy(UUID member) {
    UUID id = UUID.randomUUID();
    byte[] random = new byte[32];
    new java.security.SecureRandom().nextBytes(random);
    var spec =
        InstanceSpec.newBuilder()
            .setInstanceId(id.toString())
            .setTenantId(tenant.toString())
            .setOwnerId(member.toString())
            .setGeneration(1)
            .setDesired(DesiredState.DESIRED_STATE_RUNNING)
            .build();
    run(
        () ->
            store.tx(
                () ->
                    store.jdbc.update(
                        "INSERT INTO"
                            + " instance(id,tenant_id,owner_id,name,spec,generation,token_hash,token_encrypted,last_activity_at,created_at,updated_at)"
                            + " VALUES(?,?,?,?,?,1,?,?,?,?,?)",
                        id,
                        tenant,
                        member,
                        "legacy",
                        spec.toByteArray(),
                        random,
                        random,
                        java.sql.Timestamp.from(clock.instant()),
                        java.sql.Timestamp.from(clock.instant()),
                        java.sql.Timestamp.from(clock.instant()))));
    return id;
  }

  auriga.workspace.app.SharedDshMaintenance maintenance() {
    return new auriga.workspace.app.SharedDshMaintenance(store, service, inputs, clock);
  }

  @Test
  void explicitMaintenanceExportWindowBulkRebuildAndContinueDedicatedSurviveRestart() {
    UUID shared = UUID.randomUUID(), dedicated = UUID.randomUUID();
    UUID a = legacy(shared), b = legacy(dedicated), batch = UUID.randomUUID();
    assertThrows(RuntimeException.class, () -> allocate(shared));
    var choices =
        List.of(
            new auriga.workspace.app.SharedDshMaintenance.Choice(shared, a, false),
            new auriga.workspace.app.SharedDshMaintenance.Choice(dedicated, b, true));
    run(
        () -> {
          maintenance().plan(actor, session, batch, 1030, choices);
          return null;
        });
    run(
        () -> {
          maintenance().plan(actor, session, batch, 1030, choices);
          return null;
        });
    assertThrows(
        RuntimeException.class, () -> run(() -> maintenance().start(actor, session, batch)));
    assertFalse(run(() -> store.tx(() -> (Boolean) store.instance(a).get("shared_rebuild_hold"))));
    seconds = 1030;
    var started = run(() -> maintenance().start(actor, session, batch));
    assertEquals(2, started.size());
    assertEquals(started, run(() -> maintenance().start(actor, session, batch)));
    assertTrue(run(() -> store.tx(() -> (Boolean) store.instance(a).get("shared_rebuild_hold"))));
    assertEquals(
        DesiredState.DESIRED_STATE_STOPPED,
        run(
            () ->
                store.tx(
                    () ->
                        auriga.workspace.app.WorkspaceService.spec(store.instance(a))
                            .getDesired())));
    service = new SharedDshPlacements(store, inputs, c -> probe, clock);
    finish(started.getFirst().operation());
    var mid = run(() -> maintenance().progress(actor, session, batch));
    assertEquals(1, mid.stream().filter(p -> p.state().equals("succeeded")).count());
    finish(started.getLast().operation());
    var completed = run(() -> maintenance().progress(actor, session, batch));
    assertTrue(
        completed.stream()
            .allMatch(
                p ->
                    p.state().equals("succeeded")
                        && p.archive() != null
                        && p.archiveExpiresAt() == seconds + 2592000));
    assertEquals(
        DshInstanceMode.DSH_INSTANCE_MODE_SHARED,
        run(() -> service.current(shared, session)).binding().getMode());
    assertEquals(
        DshInstanceMode.DSH_INSTANCE_MODE_DEDICATED,
        run(() -> service.current(dedicated, session)).binding().getMode());
    assertNotEquals(
        a.toString(),
        run(() -> service.current(shared, session)).binding().getPhysicalInstanceId());
    assertNotEquals(
        b.toString(),
        run(() -> service.current(dedicated, session)).binding().getPhysicalInstanceId());
    assertEquals(
        2,
        run(
            () ->
                store.tx(
                    () ->
                        store.jdbc.queryForObject(
                            "SELECT count(*) FROM instance WHERE tenant_id=? AND deleted AND"
                                + " shared_rebuild_hold AND NOT purge_data",
                            Long.class,
                            tenant))));
    assertThrows(
        RuntimeException.class,
        () ->
            TenantContext.call(
                UUID.randomUUID(), () -> maintenance().progress(actor, session, batch)));
  }

  @Test
  void maintenanceChoiceCannotBeChangedOrTargetForeignLegacyInstance() {
    UUID member = UUID.randomUUID(), id = legacy(member), batch = UUID.randomUUID();
    run(
        () -> {
          maintenance()
              .plan(
                  actor,
                  session,
                  batch,
                  1000,
                  List.of(new auriga.workspace.app.SharedDshMaintenance.Choice(member, id, true)));
          return null;
        });
    assertThrows(
        RuntimeException.class,
        () ->
            run(
                () -> {
                  maintenance()
                      .plan(
                          actor,
                          session,
                          batch,
                          1000,
                          List.of(
                              new auriga.workspace.app.SharedDshMaintenance.Choice(
                                  member, id, false)));
                  return null;
                }));
    UUID stranger = UUID.randomUUID();
    assertThrows(
        RuntimeException.class,
        () ->
            run(
                () -> {
                  maintenance()
                      .plan(
                          actor,
                          session,
                          UUID.randomUUID(),
                          1000,
                          List.of(
                              new auriga.workspace.app.SharedDshMaintenance.Choice(
                                  stranger, id, false)));
                  return null;
                }));
    disabled.add(member);
    assertEquals(
        "blocked", run(() -> maintenance().start(actor, session, batch)).getFirst().state());
    assertFalse(run(() -> store.tx(() -> (Boolean) store.instance(id).get("shared_rebuild_hold"))));
  }

  @Test
  void ticketIdempotencyExpiresAndConsumedOrRevokedTicketsAreNeverReissued() throws Exception {
    UUID member = UUID.randomUUID();
    ready(member);
    var pair = java.security.KeyPairGenerator.getInstance("Ed25519").generateKeyPair();
    var consumed = new HashSet<UUID>();
    var entry =
        new auriga.workspace.app.SharedDshTickets.Entry() {
          public java.net.URI exchangeUrl(SharedDshBinding b) {
            return java.net.URI.create(
                "https://127.0.0.1/i/" + b.getPhysicalInstanceId() + "/_auriga/placement-ticket");
          }

          public boolean consumed(UUID jti) {
            return consumed.contains(jti);
          }
        };
    var tickets =
        new auriga.workspace.app.SharedDshTickets(
            store, service, entry, clock, "test", pair.getPrivate());
    var first = run(() -> tickets.open(member, session, "open-key"));
    assertEquals(first, run(() -> tickets.open(member, session, "open-key")));
    var json = tools.jackson.databind.json.JsonMapper.builder().build();
    var claims = json.readTree(Base64.getUrlDecoder().decode(first.ticket().split("\\.")[1]));
    consumed.add(UUID.fromString(claims.get("jti").asText()));
    assertThrows(
        RuntimeException.class, () -> run(() -> tickets.open(member, session, "open-key")));
    var second = run(() -> tickets.open(member, session, "new-key"));
    assertNotEquals(first.ticket(), second.ticket());
    seconds += 60;
    assertThrows(RuntimeException.class, () -> run(() -> tickets.open(member, session, "new-key")));
    change(member, "dedicated", "revoke-open");
    assertThrows(
        RuntimeException.class, () -> run(() -> tickets.open(member, session, "after-switch")));
  }

  @Test
  void unconfirmedStopFencesWholePoolAndSurvivesAnotherRestart() {
    UUID member = UUID.randomUUID(), sibling = UUID.randomUUID();
    ready(member);
    var other = ready(sibling);
    UUID op = change(member, "dedicated", "whole-pool");
    seconds += 30;
    run(
        () -> {
          service.recover();
          return null;
        });
    assertEquals("blocked", run(() -> service.switchView(op)).getState().getValue());
    assertEquals("stop-unconfirmed", run(() -> service.switchView(op)).getFailureCode().getValue());
    assertThrows(
        RuntimeException.class,
        () ->
            run(
                () ->
                    service.resolve(
                        sibling, UUID.fromString(other.binding().getPlacementId()), session)));
    var current = run(() -> service.current(sibling, session));
    assertTrue(current.binding().getEpoch() > other.binding().getEpoch());
    service = new SharedDshPlacements(store, inputs, b -> probe, clock);
    run(
        () -> {
          service.recover();
          return null;
        });
    assertFalse(run(() -> service.receipt(ack(command(op)).setOutcome("unconfirmed").build())));
    assertEquals("blocked", run(() -> service.switchView(op)).getState().getValue());
    assertThrows(RuntimeException.class, () -> run(() -> service.commit(op)));
  }

  @Test
  void foreignParentAndRevokedGenerationDoNotRegisterOrCommit() {
    UUID a = UUID.randomUUID(), b = UUID.randomUUID();
    var p = ready(a);
    var q = ready(b);
    var parent = sessionBinding(p, UUID.randomUUID());
    run(() -> service.register(parent, ""));
    var child = sessionBinding(q, UUID.randomUUID());
    assertThrows(
        RuntimeException.class, () -> run(() -> service.register(child, parent.getSessionId())));
    UUID op = change(a, "dedicated", "revoked-switch");
    var current = run(() -> service.current(a, session));
    run(
        () ->
            service.revoke(
                SharedDshRevocation.newBuilder()
                    .setSchemaVersion(1)
                    .setTenantId(tenant.toString())
                    .setMemberId(a.toString())
                    .setPlacementId(current.binding().getPlacementId())
                    .setEpoch(current.binding().getEpoch())
                    .setRevision(current.binding().getRevision())
                    .setAuthGeneration(current.authGeneration())
                    .setReason("member-disabled")
                    .setRevokedAt(seconds)
                    .build()));
    assertFalse(run(() -> service.receipt(ack(command(op)).build())));
    assertTrue(
        run(() -> service.current(a, session)).binding().getEpoch() > current.binding().getEpoch());
    assertThrows(RuntimeException.class, () -> run(() -> service.register(parent, "")));
  }

  Map<String, Object> op(UUID id) {
    return run(
        () ->
            store.tx(
                () ->
                    store.one(
                        "SELECT * FROM shared_dsh_operation WHERE tenant_id=? AND id=?",
                        tenant,
                        id)));
  }

  Map<String, Object> poolRow(String id) {
    return run(
        () ->
            store.tx(
                () ->
                    store.one(
                        "SELECT * FROM shared_dsh_pool WHERE tenant_id=? AND id=?",
                        tenant,
                        UUID.fromString(id))));
  }

  void revokeMember(UUID member, String reason) {
    var p = run(() -> service.current(member, session));
    var b = p.binding();
    run(
        () ->
            service.revoke(
                SharedDshRevocation.newBuilder()
                    .setSchemaVersion(1)
                    .setTenantId(tenant.toString())
                    .setMemberId(member.toString())
                    .setPlacementId(b.getPlacementId())
                    .setEpoch(b.getEpoch())
                    .setRevision(b.getRevision())
                    .setAuthGeneration(p.authGeneration())
                    .setReason(reason)
                    .setRevokedAt(seconds)
                    .build()));
  }

  void confirmPending(UUID member) {
    var p = run(() -> service.current(member, session));
    assertEquals("pending", p.state());
    var i =
        auriga.workspace.domain.SharedDshPolicy.intent(
            p.binding().getPlacementId(), p.binding(), "prepare-empty");
    assertTrue(run(() -> service.confirmInitial(ack(i).setWorkspaceEmpty(true).build())));
  }

  void finishRecovery(UUID id) throws Exception {
    run(
        () -> {
          service.retryRecovery(id);
          return null;
        });
    for (int n = 0; n < 9; n++) {
      var row = op(id);
      if (row.get("recovery_stage").equals("probe")) {
        assertTrue(run(() -> service.commitRecovery(id)));
        return;
      }
      var i = SharedDshRuntimeIntent.parseFrom((byte[]) row.get("recovery_intent"));
      var r = ack(i);
      switch (i.getAction()) {
        case "stop-pool" -> r.setExecutionTreeStopped(true);
        case "discard-sessions" -> r.setSessionsDiscarded(true);
        case "archive-reset" ->
            r.setWorkspaceEmpty(true)
                .setArchiveId(UUID.randomUUID().toString())
                .setArchiveExpiresAt(seconds + 2592000);
        case "prepare-empty" -> r.setWorkspaceEmpty(true);
        default -> {}
      }
      assertTrue(run(() -> service.receipt(r.build())), row.get("recovery_stage").toString());
      assertFalse(run(() -> service.receipt(r.build())));
    }
    fail("No rollback completion");
  }

  @Test
  void logoutAndCredentialRecoveryCanOpenAgainWithoutRestoringOldGeneration() {
    UUID member = UUID.randomUUID();
    var old = ready(member);
    var registered = sessionBinding(old, UUID.randomUUID());
    run(() -> service.register(registered, ""));
    revokeMember(member, "logout");
    var reopened = allocate(member);
    assertEquals("ready", reopened.state());
    assertTrue(reopened.binding().getEpoch() > old.binding().getEpoch());
    assertThrows(
        auriga.common.error.AurigaException.class,
        () -> run(() -> service.resolveSession(UUID.fromString(registered.getSessionId()))));
    revokeMember(member, "credential-invalid");
    assertEquals("pending", allocate(member).state());
    confirmPending(member);
    assertEquals(
        "ready",
        run(() ->
                service.resolve(
                    member, UUID.fromString(old.binding().getPlacementId()), UUID.randomUUID()))
            .state());
  }

  @Test
  void wholePoolStopReceiptRecoversSiblingOnlyThroughFreshPreparation() {
    UUID a = UUID.randomUUID(), b = UUID.randomUUID();
    var source = ready(a);
    var sibling = ready(b);
    UUID id = change(a, "dedicated", "stop-recover");
    seconds += 30;
    run(
        () -> {
          service.recover();
          return null;
        });
    var stop = command(id);
    assertFalse(run(() -> service.receipt(ack(stop).build())));
    assertEquals("blocked", poolRow(source.binding().getPhysicalInstanceId()).get("state"));
    assertTrue(run(() -> service.receipt(ack(stop).setExecutionTreeStopped(true).build())));
    finish(id);
    assertEquals("pending", run(() -> service.current(b, session)).state());
    confirmPending(b);
    var current = run(() -> service.current(b, session));
    assertTrue(current.binding().getEpoch() > sibling.binding().getEpoch());
    assertEquals(
        "ready",
        run(() -> service.resolve(b, UUID.fromString(current.binding().getPlacementId()), session))
            .state());
    assertNotNull(change(b, "dedicated", "sibling-promote"));
  }

  @Test
  void guardianStopIdAndReceiptPersistAcrossRestartAndRequireRenewedResources() throws Exception {
    UUID member = UUID.randomUUID();
    var p = ready(member);
    var view = run(() -> service.resourceView(p.binding()));
    run(
        () ->
            service.reportResource(
                SharedDshResourceReport.newBuilder()
                    .setSchemaVersion(1)
                    .setBinding(p.binding())
                    .setViewId(view.getViewId())
                    .setStatus("rejected")
                    .setReason("cleanup-unconfirmed")
                    .setObservedAt(seconds)
                    .build()));
    var stop =
        run(
            () ->
                store.tx(
                    () ->
                        store.one("SELECT * FROM shared_dsh_pool_stop WHERE tenant_id=?", tenant)));
    assertEquals("credential-stop-unconfirmed", stop.get("reason"));
    var intent = SharedDshRuntimeIntent.parseFrom((byte[]) stop.get("intent"));
    service = new SharedDshPlacements(store, inputs, b -> probe, clock);
    assertTrue(run(() -> service.receipt(ack(intent).setExecutionTreeStopped(true).build())));
    assertFalse(run(() -> service.receipt(ack(intent).setExecutionTreeStopped(true).build())));
    confirmPending(member);
    assertEquals("ready", allocate(member).state());
  }

  @Test
  void revokedSwitchFailsReleasesInflightSlotAndRollsBackWithHigherEpoch() throws Exception {
    UUID member = UUID.randomUUID();
    var old = ready(member);
    UUID id = change(member, "dedicated", "interrupted");
    var stale = command(id);
    revokeMember(member, "member-disabled");
    assertEquals("failed", op(id).get("state"));
    assertEquals("revoked", op(id).get("failure_code"));
    seconds += 31;
    run(
        () -> {
          service.recover();
          return null;
        });
    assertEquals(
        0L,
        run(
            () ->
                store.tx(
                    () ->
                        store.jdbc.queryForObject(
                            "SELECT count(*) FROM shared_dsh_pool_stop WHERE tenant_id=?",
                            Long.class,
                            tenant))));
    assertFalse(run(() -> service.receipt(ack(stale).build())));
    finishRecovery(id);
    assertEquals("failed", op(id).get("state"));
    assertEquals(false, op(id).get("recovery_pending"));
    var recovered = allocate(member);
    assertEquals("ready", recovered.state());
    assertEquals(old.binding().getMode(), recovered.binding().getMode());
    assertTrue(recovered.binding().getEpoch() > old.binding().getEpoch() + 1);
    assertNotNull(change(member, "dedicated", "retry-after-rollback"));
  }

  @Test
  void failedTargetRollsBackToEmptySourceModeAndEmitsAuditableContractEvents() throws Exception {
    UUID member = UUID.randomUUID();
    ready(member);
    UUID id = change(member, "dedicated", "failed-target");
    for (int n = 0; n < 3; n++) {
      var i = command(id);
      var r = ack(i);
      if (i.getAction().equals("discard-sessions")) r.setSessionsDiscarded(true);
      if (i.getAction().equals("archive-reset"))
        r.setWorkspaceEmpty(true)
            .setArchiveId(UUID.randomUUID().toString())
            .setArchiveExpiresAt(seconds + 2592000);
      assertTrue(run(() -> service.receipt(r.build())));
    }
    var prepare = command(id);
    assertEquals("prepare-empty", prepare.getAction());
    assertTrue(run(() -> service.receipt(ack(prepare).setOutcome("unconfirmed").build())));
    assertEquals("failed", op(id).get("state"));
    finishRecovery(id);
    assertEquals(
        "shared",
        auriga.workspace.domain.SharedDshPolicy.mode(allocate(member).binding().getMode()));
    var rows =
        run(
            () ->
                store.tx(
                    () ->
                        store.jdbc.queryForList(
                            "SELECT * FROM shared_dsh_outbox WHERE tenant_id=? AND"
                                + " kind='placement.switch_failed'",
                            tenant)));
    assertFalse(rows.isEmpty());
    var data =
        auriga.workspace.app.WorkspaceService.JSON.readTree(
            (byte[]) rows.getFirst().get("payload"));
    assertEquals(id.toString(), data.get("operationId").asText());
    assertEquals("prepare-failed", data.get("failureCode").asText());
    var audit =
        run(
            () ->
                store.tx(
                    () ->
                        store.one(
                            "SELECT * FROM shared_dsh_audit WHERE tenant_id=? AND operation_id=?"
                                + " AND result='accepted'",
                            tenant,
                            id)));
    var body = auriga.workspace.app.WorkspaceService.JSON.readTree(audit.get("payload").toString());
    assertPublicEventsConform();
    assertEquals("test", body.get("reason").asText());
    assertEquals(session.toString(), body.get("stepUpRef").asText());
  }

  @Test
  void dedicatedSourceCannotCommitUntilExecutionTreeStopsAndPoolRetires() {
    UUID member = UUID.randomUUID();
    ready(member);
    finish(change(member, "dedicated", "first"));
    var before = allocate(member);
    UUID id = change(member, "shared", "second");
    assertTrue(run(() -> service.receipt(ack(command(id)).build())));
    assertEquals("stop-pool", command(id).getAction());
    var error =
        assertThrows(
            auriga.common.error.AurigaException.class, () -> run(() -> service.commit(id)));
    assertEquals("shared.stop_unconfirmed", error.errorCode().code());
    assertFalse(run(() -> service.receipt(ack(command(id)).build())));
    assertTrue(run(() -> service.receipt(ack(command(id)).setExecutionTreeStopped(true).build())));
    assertEquals("retired", poolRow(before.binding().getPhysicalInstanceId()).get("state"));
    finish(id);
    assertEquals(
        "shared",
        auriga.workspace.domain.SharedDshPolicy.mode(allocate(member).binding().getMode()));
    var success =
        run(
            () ->
                store.tx(
                    () ->
                        store.jdbc.queryForList(
                            "SELECT payload FROM shared_dsh_outbox WHERE tenant_id=? AND"
                                + " kind='placement.switched' ORDER BY created_at,id",
                            tenant)));
    var data =
        auriga.workspace.app.WorkspaceService.JSON.readTree(
            (byte[]) success.getLast().get("payload"));
    for (String field :
        List.of(
            "operationId",
            "memberId",
            "placementId",
            "fromMode",
            "targetMode",
            "epoch",
            "revision",
            "sessionsDiscarded",
            "actorId")) assertTrue(data.has(field));
    assertFalse(data.has("failureCode"));
    assertTrue(data.get("sessionsDiscarded").asBoolean());
  }

  @Test
  void errorsDistinguishWrongBindingRevokedEpochAndUnconfirmedStop() {
    UUID member = UUID.randomUUID();
    var p = ready(member);
    var wrong =
        sessionBinding(p, UUID.randomUUID()).toBuilder()
            .setPhysicalInstanceId(UUID.randomUUID().toString())
            .build();
    assertEquals(
        "shared.binding_mismatch",
        assertThrows(
                auriga.common.error.AurigaException.class,
                () -> run(() -> service.register(wrong, "")))
            .errorCode()
            .code());
    var old = sessionBinding(p, UUID.randomUUID());
    run(() -> service.register(old, ""));
    revokeMember(member, "logout");
    assertEquals(
        "shared.revoked",
        assertThrows(
                auriga.common.error.AurigaException.class,
                () -> run(() -> service.resolveSession(UUID.fromString(old.getSessionId()))))
            .errorCode()
            .code());
  }

  @Test
  void maintenanceProgressIsIndependentAndDeletedLegacyDoesNotBlockOtherMembers() {
    UUID a = UUID.randomUUID(), b = UUID.randomUUID(), batch = UUID.randomUUID();
    UUID ia = legacy(a), ib = legacy(b);
    run(
        () -> {
          maintenance()
              .plan(
                  actor,
                  session,
                  batch,
                  seconds,
                  List.of(
                      new auriga.workspace.app.SharedDshMaintenance.Choice(a, ia, false),
                      new auriga.workspace.app.SharedDshMaintenance.Choice(b, ib, true)));
          return null;
        });
    run(
        () ->
            store.tx(
                () ->
                    store.jdbc.update(
                        "UPDATE instance SET deleted=true WHERE tenant_id=? AND id=?",
                        tenant,
                        ia)));
    var progress = run(() -> maintenance().start(actor, session, batch));
    var failed = progress.stream().filter(p -> p.member().equals(a)).findFirst().orElseThrow();
    assertEquals("blocked", failed.state());
    assertEquals("common.not_found", failed.failureCode());
    var active = progress.stream().filter(p -> p.member().equals(b)).findFirst().orElseThrow();
    assertNotNull(active.operation());
    finish(active.operation());
    assertEquals(
        1,
        run(() -> maintenance().progress(actor, session, batch)).stream()
            .filter(p -> p.state().equals("succeeded"))
            .count());
    assertEquals(
        false,
        run(
            () ->
                store.tx(
                    () ->
                        store
                            .one("SELECT * FROM instance WHERE tenant_id=? AND id=?", tenant, ib)
                            .get("deletion_event_sent"))));
  }

  @Test
  void expiredResourceKeepsPoolBlockedAfterProofUntilLeaseIsRevalidated() throws Exception {
    UUID member = UUID.randomUUID();
    var p = ready(member);
    var v = run(() -> service.resourceView(p.binding()));
    run(
        () ->
            service.reportResource(
                SharedDshResourceReport.newBuilder()
                    .setSchemaVersion(1)
                    .setBinding(p.binding())
                    .setViewId(v.getViewId())
                    .setStatus("rejected")
                    .setReason("expired")
                    .setObservedAt(seconds)
                    .build()));
    var stop =
        run(
            () ->
                store.tx(
                    () ->
                        store.one("SELECT * FROM shared_dsh_pool_stop WHERE tenant_id=?", tenant)));
    var i = SharedDshRuntimeIntent.parseFrom((byte[]) stop.get("intent"));
    expiredResources = true;
    assertTrue(run(() -> service.receipt(ack(i).setExecutionTreeStopped(true).build())));
    assertEquals("blocked", poolRow(p.binding().getPhysicalInstanceId()).get("state"));
    expiredResources = false;
    run(
        () -> {
          service.recover();
          return null;
        });
    confirmPending(member);
    assertEquals("ready", allocate(member).state());
  }

  @Test
  void anotherRevocationDuringRollbackRejectsPreviouslyPreparedAckAndCanRetry() throws Exception {
    UUID member = UUID.randomUUID();
    ready(member);
    UUID id = change(member, "dedicated", "twice");
    revokeMember(member, "logout");
    run(
        () -> {
          service.retryRecovery(id);
          return null;
        });
    var prior = SharedDshRuntimeIntent.parseFrom((byte[]) op(id).get("recovery_intent"));
    revokeMember(member, "credential-invalid");
    assertFalse(run(() -> service.receipt(ack(prior).build())));
    finishRecovery(id);
    assertEquals("ready", allocate(member).state());
  }

  void assertPublicEventsConform() throws Exception {
    var data =
        run(
            () ->
                store.tx(
                    () ->
                        store.jdbc.queryForList(
                            "SELECT kind,payload FROM shared_dsh_outbox WHERE tenant_id=? AND"
                                + " kind<>'runtime'",
                            tenant)));
    var values = new ArrayList<Map<String, Object>>();
    for (var row : data)
      values.add(
          Map.of(
              "kind",
              row.get("kind"),
              "data",
              auriga.workspace.app.WorkspaceService.JSON.readTree((byte[]) row.get("payload"))));
    var root = java.nio.file.Path.of("").toAbsolutePath();
    while (!java.nio.file.Files.exists(root.resolve("contracts/shared-dsh/ports.mjs")))
      root = root.getParent();
    String node =
        root.resolve(
                "services/contracts/target/node/"
                    + (System.getProperty("os.name").startsWith("Windows") ? "node.exe" : "node"))
            .toString();
    String js =
        "import {validate} from './contracts/shared-dsh/ports.mjs';import {readFileSync} from"
            + " 'node:fs';import Ajv from"
            + " './services/contracts/node_modules/ajv/dist/2020.js';import formats from"
            + " './services/contracts/node_modules/ajv-formats/dist/index.js';const ajv=new"
            + " Ajv({strict:false,allErrors:true});formats(ajv);const"
            + " schema=JSON.parse(readFileSync('contracts/events/placement.schema.json','utf8'));ajv.addSchema(schema,'placement');for(const"
            + " v of JSON.parse(readFileSync(0,'utf8'))){const"
            + " type={'placement.revoked':'Revocation','placement.resource_reported':'ResourceReport','placement.guardian_impact':'GuardianImpact'}[v.kind];if(type)validate(type,v.data);else{const"
            + " def=v.kind==='placement.switched'?'PlacementSwitched':'PlacementSwitchFailed';const"
            + " check=ajv.getSchema('placement#/$defs/'+def);if(!check(v.data))throw"
            + " Error(JSON.stringify(check.errors));}}";
    var process =
        new ProcessBuilder(node, "--input-type=module", "-e", js)
            .directory(root.toFile())
            .redirectErrorStream(true)
            .start();
    try {
      try (var stdin = process.getOutputStream()) {
        stdin.write(auriga.workspace.app.WorkspaceService.JSON.writeValueAsBytes(values));
      }
      assertTrue(process.waitFor(20, java.util.concurrent.TimeUnit.SECONDS));
      assertEquals(
          0,
          process.exitValue(),
          new String(
              process.getInputStream().readAllBytes(), java.nio.charset.StandardCharsets.UTF_8));
    } finally {
      process.destroyForcibly();
    }
  }

  @Test
  void allPublicOutboxKindsAndPayloadsPassTheFrozenCatalogSchemas() throws Exception {
    UUID member = UUID.randomUUID();
    ready(member);
    finish(change(member, "dedicated", "schema-promote"));
    finish(change(member, "shared", "schema-demote"));
    UUID failed = change(member, "dedicated", "schema-fail");
    run(
        () -> {
          service.failOperation(failed, "prepare-failed");
          return null;
        });
    assertPublicEventsConform();
  }

  @Test
  void authorityFailureDoesNotRollBackFailedOperationOrConfirmedStop() throws Exception {
    UUID member = UUID.randomUUID();
    ready(member);
    UUID id = change(member, "dedicated", "authority-fail");
    for (int n = 0; n < 3; n++) {
      var i = command(id);
      var r = ack(i);
      if (i.getAction().equals("discard-sessions")) r.setSessionsDiscarded(true);
      if (i.getAction().equals("archive-reset"))
        r.setWorkspaceEmpty(true)
            .setArchiveId(UUID.randomUUID().toString())
            .setArchiveExpiresAt(seconds + 2592000);
      assertTrue(run(() -> service.receipt(r.build())));
    }
    var prepare = command(id);
    authorityAvailable = false;
    assertTrue(run(() -> service.receipt(ack(prepare).setOutcome("unconfirmed").build())));
    assertEquals("failed", op(id).get("state"));
    authorityAvailable = true;
    finishRecovery(id);
    var p = allocate(member);
    var v = run(() -> service.resourceView(p.binding()));
    run(
        () ->
            service.reportResource(
                SharedDshResourceReport.newBuilder()
                    .setSchemaVersion(1)
                    .setBinding(p.binding())
                    .setViewId(v.getViewId())
                    .setStatus("rejected")
                    .setReason("revoked")
                    .setObservedAt(seconds)
                    .build()));
    var pending =
        run(
            () ->
                store.tx(
                    () ->
                        store.one(
                            "SELECT * FROM shared_dsh_pool_stop WHERE tenant_id=? AND"
                                + " state='waiting'",
                            tenant)));
    var stop = SharedDshRuntimeIntent.parseFrom((byte[]) pending.get("intent"));
    authorityAvailable = false;
    assertTrue(run(() -> service.receipt(ack(stop).setExecutionTreeStopped(true).build())));
    assertEquals(
        "stopped",
        run(() ->
                store.tx(
                    () ->
                        store.one(
                            "SELECT * FROM shared_dsh_pool_stop WHERE tenant_id=? AND id=?",
                            tenant,
                            pending.get("id"))))
            .get("state"));
    authorityAvailable = true;
    run(
        () -> {
          service.recover();
          return null;
        });
    confirmPending(member);
  }

  @Test
  void reconcileMarksAuthorityRevokedCommittingOperationFailedWithoutRollbackOnly() {
    UUID member = UUID.randomUUID();
    ready(member);
    UUID id = change(member, "dedicated", "disabled-at-probe");
    for (int n = 0; n < 4; n++) {
      var i = command(id);
      var r = ack(i);
      if (i.getAction().equals("discard-sessions")) r.setSessionsDiscarded(true);
      if (i.getAction().equals("archive-reset"))
        r.setWorkspaceEmpty(true)
            .setArchiveId(UUID.randomUUID().toString())
            .setArchiveExpiresAt(seconds + 2592000);
      if (i.getAction().equals("prepare-empty")) r.setWorkspaceEmpty(true);
      assertTrue(run(() -> service.receipt(r.build())));
    }
    disabled.add(member);
    run(
        () -> {
          service.recover();
          return null;
        });
    assertEquals("failed", op(id).get("state"));
    assertEquals("revoked", op(id).get("failure_code"));
  }

  @Test
  void maintenanceRollbackRetiresLegacyMetadataAfterConfirmedStops() throws Exception {
    UUID member = UUID.randomUUID(), old = legacy(member), batch = UUID.randomUUID();
    run(
        () -> {
          maintenance()
              .plan(
                  actor,
                  session,
                  batch,
                  seconds,
                  List.of(new auriga.workspace.app.SharedDshMaintenance.Choice(member, old, true)));
          return null;
        });
    UUID id = run(() -> maintenance().start(actor, session, batch)).getFirst().operation();
    run(
        () -> {
          service.failOperation(id, "prepare-failed");
          return null;
        });
    finishRecovery(id);
    var legacy =
        run(
            () ->
                store.tx(
                    () ->
                        store.one(
                            "SELECT * FROM instance WHERE tenant_id=? AND id=?", tenant, old)));
    assertEquals(true, legacy.get("deleted"));
    assertEquals(false, legacy.get("deletion_event_sent"));
    assertEquals("retired", poolRow(old.toString()).get("state"));
    assertEquals("ready", allocate(member).state());
    assertEquals("failed", op(id).get("state"));
  }
}
