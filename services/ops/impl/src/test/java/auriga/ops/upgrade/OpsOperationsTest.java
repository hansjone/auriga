package auriga.ops.upgrade;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.error.AurigaException;
import auriga.common.security.*;
import auriga.ops.backup.*;
import java.nio.file.*;
import java.time.*;
import java.util.*;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.io.TempDir;

class OpsOperationsTest {
  @TempDir Path directory;
  final UUID tenant = UUID.randomUUID();
  final MutableClock clock = new MutableClock();
  OpsRuntimeStore store;
  OpsUpgradeService upgrades;
  OpsBackupService backups;
  AurigaPrincipal admin;

  @BeforeEach
  void setup() throws Exception {
    store = new OpsRuntimeStore(directory);
    upgrades = new OpsUpgradeService(store, tenant, clock);
    backups = new OpsBackupService(upgrades);
    admin =
        principal(
            tenant,
            "alice",
            "session-one",
            TokenContext.ADMIN,
            Set.of("ops.upgrades.write"),
            clock.instant());
    store.change(
        s -> {
          var inventory = OpsRuntimeStore.JSON.createObjectNode();
          inventory.put("reportedAt", clock.instant().toString());
          inventory.putArray("items");
          s.set("inventory", inventory);
          s.putArray("available")
              .addObject()
              .put("id", "approved")
              .put("label", "Approved")
              .put("image", "example.com/platform@sha256:" + "a".repeat(64));
          return null;
        });
  }

  @Test
  void retentionMustCoverIntervalAndManualCapacityMatchesWorker() {
    assertThrows(AurigaException.class, () -> backups.configure(admin, true, 720, 0, 7, 29, 1024));
    assertThrows(AurigaException.class, () -> backups.configure(admin, false, 25, 0, 7, 1, 1024));
    assertThrows(
        AurigaException.class, () -> backups.configure(admin, true, 24, 0, 7, 30, 1024, 0));
    assertThrows(
        AurigaException.class, () -> backups.configure(admin, true, 24, 0, 7, 30, 1024, 8));
    assertEquals(1, backups.configure(admin, true, 720, 0, 7, 30, 1024).path("revision").asInt());
    store.change(
        s -> {
          for (int i = 0; i < 1000; i++) s.withArray("backups").addObject().put("status", "failed");
          return null;
        });
    assertThrows(AurigaException.class, () -> backups.trigger(admin, "capacity-request-001"));
  }

  @Test
  void deletionRevokesTicketsAndDownloadAuditRecordsOutcome() throws Exception {
    UUID first = complete();
    UUID second = complete();
    String token = backups.ticket(admin, first).path("token").asString();
    assertThrows(AurigaException.class, () -> backups.download(admin, second, token));
    try (var input = backups.download(admin, first, token)) {
      input.readAllBytes();
    }
    var audit = backups.status(admin).path("audit");
    assertEquals("completed", audit.get(audit.size() - 1).path("result").asString());
    assertEquals(5, audit.get(audit.size() - 1).path("bytes").asLong());
    String blocked = backups.ticket(admin, first).path("token").asString();
    backups.delete(admin, first);
    assertThrows(AurigaException.class, () -> backups.download(admin, first, blocked));
    assertThrows(AurigaException.class, () -> backups.configure(admin, true, 24, 0, 0, 30, 1024));
    assertThrows(AurigaException.class, () -> backups.configure(admin, true, 24, 0, 7, 0, 1024));
    assertThrows(AurigaException.class, () -> backups.configure(admin, true, 24, 0, 7, 30, 63));
  }

  @Test
  void installationCannotBeReboundToAnotherTenant() {
    assertThrows(
        IllegalStateException.class, () -> new OpsUpgradeService(store, UUID.randomUUID(), clock));
  }

  @Test
  void failedDownloadConsumesAuthorization() throws Exception {
    UUID id = complete();
    String token = backups.ticket(admin, id).path("token").asString();
    Files.delete(directory.resolve("backups").resolve(id + ".dump"));
    assertThrows(AurigaException.class, () -> backups.download(admin, id, token));
    Files.writeString(directory.resolve("backups").resolve(id + ".dump"), "PGDMP");
    assertThrows(AurigaException.class, () -> backups.download(admin, id, token));
  }

  @Test
  void persistsUpgradeAndIdempotencyWithoutExecuting() throws Exception {
    var request = upgrades.register(admin, "approved", "request-key-00001");
    assertEquals(request, upgrades.register(admin, "approved", "request-key-00001"));
    var restart = new OpsUpgradeService(new OpsRuntimeStore(directory), tenant, clock);
    assertEquals(request.path("id"), restart.status(admin).path("requests").get(0).path("id"));
    assertEquals("queued", request.path("status").asString());
    assertThrows(
        AurigaException.class, () -> upgrades.register(admin, "approved", "request-key-00002"));
    assertThrows(
        AurigaException.class, () -> upgrades.register(admin, "unapproved", "request-key-00001"));
  }

  @Test
  void rejectsWrongTenantAndNonAdmin() {
    assertThrows(
        AurigaException.class,
        () ->
            upgrades.status(
                principal(
                    UUID.randomUUID(),
                    "alice",
                    "session-one",
                    TokenContext.ADMIN,
                    Set.of(),
                    clock.instant())));
    assertThrows(
        AurigaException.class,
        () ->
            backups.status(
                principal(
                    tenant,
                    "alice",
                    "session-one",
                    TokenContext.PORTAL,
                    Set.of(),
                    clock.instant())));
  }

  @Test
  void rejectsStaleWorkerAndInvalidScheduleOrRevision() {
    clock.time = clock.time.plusSeconds(181);
    assertThrows(
        AurigaException.class, () -> upgrades.register(admin, "approved", "request-key-00001"));
    assertThrows(AurigaException.class, () -> backups.trigger(admin, "request-key-00001"));
    for (int interval : new int[] {0, 721})
      assertThrows(AurigaException.class, () -> backups.configure(admin, true, interval, 0));
    assertEquals(1, backups.configure(admin, true, 1, 0).path("revision").asInt());
    assertThrows(AurigaException.class, () -> backups.configure(admin, true, 24, 0));
  }

  @Test
  void backupTriggerIsDurableAndDeduplicated() {
    var request = backups.trigger(admin, "request-key-00001");
    assertEquals(request, backups.trigger(admin, "request-key-00001"));
    assertThrows(AurigaException.class, () -> backups.trigger(admin, "request-key-00002"));
  }

  @Test
  void ticketIsSessionBoundExpiresAndIsConsumedExactlyOnce() throws Exception {
    UUID id = complete();
    String token = backups.ticket(admin, id).path("token").asString();
    assertFalse(Files.readString(directory.resolve("state.json")).contains(token));
    assertThrows(
        AurigaException.class,
        () ->
            backups.download(
                principal(
                    tenant,
                    "alice",
                    "another-session",
                    TokenContext.ADMIN,
                    Set.of(),
                    clock.instant()),
                id,
                token));
    assertThrows(
        AurigaException.class,
        () ->
            backups.download(
                principal(
                    tenant, "bob", "session-one", TokenContext.ADMIN, Set.of(), clock.instant()),
                id,
                token));
    try (var stream = backups.download(admin, id, token)) {
      assertEquals(
          "PGDMP", new String(stream.readAllBytes(), java.nio.charset.StandardCharsets.UTF_8));
    }
    assertThrows(AurigaException.class, () -> backups.download(admin, id, token));
    String expired = backups.ticket(admin, id).path("token").asString();
    clock.time = clock.time.plusSeconds(61);
    assertThrows(AurigaException.class, () -> backups.download(admin, id, expired));
  }

  @Test
  void ticketOnlyAllowsSuccessfulBackup() {
    assertThrows(AurigaException.class, () -> backups.ticket(admin, UUID.randomUUID()));
  }

  @Test
  void controllerRequiresPermissionAdminAndRecentVerification() throws Exception {
    var method = OpsBackupController.class.getMethod("ticket", UUID.class, String.class);
    var rules = AuthorizationRules.of(method, OpsBackupController.class);
    assertThrows(AurigaException.class, () -> rules.check(admin, clock));
    var stale =
        principal(
            tenant,
            "alice",
            "session-one",
            TokenContext.ADMIN,
            Set.of("ops.backups.download"),
            clock.instant().minusSeconds(601));
    assertThrows(AurigaException.class, () -> rules.check(stale, clock));
    rules.check(
        principal(
            tenant,
            "alice",
            "session-one",
            TokenContext.ADMIN,
            Set.of("ops.backups.download"),
            clock.instant()),
        clock);
  }

  private UUID complete() throws Exception {
    UUID id = UUID.randomUUID();
    Files.createDirectories(directory.resolve("backups"));
    Files.writeString(directory.resolve("backups").resolve(id + ".dump"), "PGDMP");
    store.change(
        s -> {
          s.withArray("backups")
              .addObject()
              .put("id", id.toString())
              .put("status", "succeeded")
              .put("createdAt", clock.instant().toString());
          return null;
        });
    return id;
  }

  static AurigaPrincipal principal(
      UUID tenant,
      String subject,
      String session,
      TokenContext context,
      Set<String> permissions,
      Instant stepup) {
    return new AurigaPrincipal(
        subject,
        tenant,
        PrincipalType.USER,
        context,
        session,
        null,
        "member",
        List.of("owner"),
        permissions,
        stepup,
        stepup,
        "test");
  }

  static final class MutableClock extends Clock {
    Instant time = Instant.parse("2026-11-11T00:00:00Z");

    public java.time.ZoneId getZone() {
      return ZoneOffset.UTC;
    }

    public Clock withZone(java.time.ZoneId zone) {
      return this;
    }

    public Instant instant() {
      return time;
    }
  }
}
