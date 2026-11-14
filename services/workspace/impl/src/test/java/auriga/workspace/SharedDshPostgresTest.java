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

/** Requires the root-owned real PostgreSQL slot. Never included in worker's selected unit run. */
@ExtendWith(PostgresExtension.class)
class SharedDshPostgresTest {
  static auriga.workspace.persistence.WorkspaceStore store;
  UUID tenant = UUID.randomUUID(), session = UUID.randomUUID(), actor = UUID.randomUUID();
  long seconds = 1000;
  boolean authorityAvailable = true, probe = true;
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
          if (!authorityAvailable) throw new IllegalStateException("authority unavailable");
        }

        public void requireSession(UUID t, UUID m, UUID s) {
          requireMember(t, m);
        }

        public String authorizationSetDigest(UUID t, UUID m) {
          return "sha256:" + "a".repeat(64);
        }

        public SharedDshResourceView resourceView(SharedDshBinding b) {
          throw new UnsupportedOperationException();
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
                                    "SELECT intent FROM shared_dsh_operation WHERE tenant_id=? AND id=?",
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
}
