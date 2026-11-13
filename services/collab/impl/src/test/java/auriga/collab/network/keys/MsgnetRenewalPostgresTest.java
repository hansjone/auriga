package auriga.collab.network.keys;

import static auriga.collab.network.keys.MsgnetV2Verifier.*;
import static org.junit.jupiter.api.Assertions.*;

import auriga.collab.app.NetworkJson;
import auriga.collab.app.NetworkSigningKeys;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.persistence.CommonMigrations;
import auriga.common.persistence.TenantTransactionManager;
import auriga.testsupport.PostgresExtension;
import java.nio.charset.StandardCharsets;
import java.security.KeyPair;
import java.security.Signature;
import java.time.Duration;
import java.util.Base64;
import java.util.Map;
import java.util.UUID;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.transaction.support.TransactionTemplate;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

@ExtendWith(PostgresExtension.class)
class MsgnetRenewalPostgresTest {
  static JdbcTemplate jdbc;
  static TenantTransactionManager manager;
  static JsonNode template;
  final UUID tenant = UUID.randomUUID();
  final KeyPair root = KeyTestMaterial.pair();
  final KeyPair runtime = KeyTestMaterial.pair();
  final MsgnetClockTest.MutableClock wall = new MsgnetClockTest.MutableClock();
  long tick;
  final MsgnetClock guard = new MsgnetClock(wall, () -> tick);
  final MsgnetKeyEncryption ring =
      new MsgnetKeyEncryption("current", Map.of("current", MsgnetKeyEncryptionTest.kek()));

  @BeforeAll
  static void migrate() throws Exception {
    var roles =
        PostgresExtension.serviceRoles(
            MsgnetRenewalPostgresTest.class, "renew_owner", "renew_app", "collab");
    CommonMigrations.migrate(roles.owner().dataSource(), "collab");
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas("collab")
                .defaultSchema("collab")
                .locations("classpath:db/collab")
                .load())
        .migrate();
    jdbc = new JdbcTemplate(roles.runtime().dataSource());
    manager = new TenantTransactionManager(roles.runtime().dataSource());
    try (var input =
        MsgnetRenewalPostgresTest.class
            .getClassLoader()
            .getResourceAsStream("network-fixtures/auto-renewal-v2-vectors.json")) {
      template = KeyTestMaterial.JSON.readTree(input);
    }
  }

  MsgnetRenewal service() {
    return service(guard);
  }

  MsgnetRenewal service(MsgnetClock selectedGuard) {
    var config =
        template
            .path("boundary_cases")
            .valueStream()
            .filter(v -> "config-defaults".equals(text(v, "id")))
            .findFirst()
            .orElseThrow()
            .path("input")
            .path("config");
    return new MsgnetRenewal(
        jdbc,
        manager,
        tenant,
        "1",
        new MsgnetV2Verifier(tenant.toString(), "1", "root", root.getPublic()),
        new MsgnetPrivateKeyStore(jdbc, manager, tenant, "1", ring),
        wall,
        selectedGuard,
        () -> wall.instant(),
        config);
  }

  void install() throws Exception {
    var a =
        (ObjectNode)
            template
                .path("cases")
                .get(0)
                .path("input")
                .path("publication")
                .path("authority")
                .path("payload")
                .deepCopy();
    a.put("tenant_id", tenant.toString())
        .put("runtime_kid", "runtime")
        .put("not_before", wall.instant().minusSeconds(86400).toString())
        .put(
            "public_key_spki",
            Base64.getUrlEncoder()
                .withoutPadding()
                .encodeToString(runtime.getPublic().getEncoded()));
    var authority = rootSigned(a, "NetworkRuntimeAuthority");
    var c =
        (ObjectNode)
            template
                .path("cases")
                .get(0)
                .path("input")
                .path("latest_control")
                .path("payload")
                .deepCopy();
    c.put("tenant_id", tenant.toString());
    c.putArray("allowed_authorities")
        .addObject()
        .put("authority_id", text(a, "authority_id"))
        .put("authority_digest", NetworkJson.digest(authority));
    service()
        .install(authority, rootSigned(c, "NetworkRootControl"), runtime.getPrivate().getEncoded());
  }

  ObjectNode rootSigned(JsonNode payload, String kind) throws Exception {
    var signer = Signature.getInstance("Ed25519");
    signer.initSign(root.getPrivate());
    signer.update(
        ("Auriga." + kind + ".v2\0" + NetworkJson.canonical(payload))
            .getBytes(StandardCharsets.UTF_8));
    var signed = MsgnetRenewal.object();
    signed.set("payload", payload);
    signed
        .putObject("root_auth")
        .put("alg", "Ed25519")
        .put("kid", "root")
        .put("signature", Base64.getUrlEncoder().withoutPadding().encodeToString(signer.sign()));
    return signed;
  }

  void advance(long seconds) {
    wall.now = wall.now.plusSeconds(seconds);
    tick += Duration.ofSeconds(seconds).toNanos();
  }

  JsonNode payload() {
    return NetworkJson.parse(service().publication(), 1048576).path("keyset").path("payload");
  }

  int count(String table) {
    return TenantContext.call(
        tenant,
        () ->
            new TransactionTemplate(manager)
                .execute(
                    s ->
                        jdbc.queryForObject(
                            "SELECT count(*) FROM collab." + table + " WHERE tenant_id = ?",
                            Integer.class,
                            tenant)));
  }

  @Test
  void bootstrapPreannounceActivateAndLongDowntimeRestart() throws Exception {
    try (ring;
        var signing = new NetworkSigningKeys(wall, 1)) {
      install();
      service().reconcile();
      String first = service().publication();
      service().reconcile();
      assertEquals(first, service().publication());
      service().reload(signing);
      assertEquals(NetworkJson.canonical(payload()), signing.publicKeysJson("1"));
      advance(3 * 86400);
      service().reconcile();
      assertEquals(6, payload().path("keys").size());
      assertEquals("refresh", text(payload(), "mode"));
      advance(2 * 86400);
      service().reconcile();
      assertEquals("activate", text(payload(), "mode"));
      advance(9 * 86400);
      service().reconcile();
      assertEquals("restart_recovery", text(payload(), "mode"));
      service().reload(signing);
      assertEquals(NetworkJson.canonical(payload()), signing.publicKeysJson("1"));
      assertEquals(4, count("msgnet_publication_history"));
    }
  }

  @Test
  void clockErrorRollsBackAllFirstPublicationAndPrivateMaterial() throws Exception {
    try (ring) {
      install();
      wall.now = wall.now.plusSeconds(86400);
      tick += Duration.ofSeconds(30).toNanos();
      var error = assertThrows(AurigaException.class, () -> service().reconcile());
      assertEquals("collab.msgnet_clock_error", error.errorCode().code());
      assertEquals(0, count("msgnet_key_history"));
      assertEquals(1, count("msgnet_private_key"));
      wall.now = wall.now.minusSeconds(86400);
      assertThrows(AurigaException.class, () -> service().reconcile());
      advance(30);
      assertThrows(AurigaException.class, () -> service().reconcile());
      advance(30);
      service().reconcile();
      for (var proof : payload().path("first_publications"))
        assertEquals(wall.now.toString(), text(proof, "committed_at"));
    }
  }

  @Test
  void twoYearsHasBoundedCurrentKeysAndUnboundedAuditAndNoAnnualStop() throws Exception {
    try (ring) {
      install();
      service().reconcile();
      for (int day = 0; day < 732; day++) {
        advance(86400);
        service().reconcile();
      }
      assertTrue(count("msgnet_key_history") > 128);
      assertTrue(payload().path("keys").size() < 128);
      assertTrue(NetworkJson.parse(service().status()).path("review_required").asBoolean());
      assertEquals("no_hard_expiry", text(NetworkJson.parse(service().status()), "expiry_policy"));
    }
  }

  @Test
  void pkiPhasesRequireExactReceiptsAndLeafRenewalKeepsCaPrivate() throws Exception {
    try (ring) {
      install();
      service().reconcile();
      var service = service();
      var config =
          template
              .path("boundary_cases")
              .valueStream()
              .filter(v -> "config-defaults".equals(text(v, "id")))
              .findFirst()
              .orElseThrow()
              .path("input")
              .path("config");
      var pki =
          new MsgnetPki(
              service,
              new MsgnetPrivateKeyStore(jdbc, manager, tenant, "1", ring),
              jdbc,
              tenant,
              "1",
              config);
      var identity =
          new MsgnetControlLedger.Identity(
              "target", "synthetic-projector", "", "network", "gateway", "", "subject");
      pki.reconcile();
      assertEquals("dual_trust", text(pki.bundle().path("payload"), "phase"));
      pki.reconcile();
      assertEquals("1", text(pki.bundle().path("payload"), "bundle_version"));
      var receipt =
          MsgnetRenewal.object()
              .put("bundle_version", "1")
              .put("bundle_digest", NetworkJson.digest(pki.bundle()))
              .put("generation", text(payload(), "generation"))
              .put("keyset_version", text(payload(), "keyset_version"))
              .put(
                  "publication_digest",
                  MsgnetMaterials.digest(NetworkJson.parse(service.publication(), 1048576)))
              .put("phase", "persisted");
      pki.confirm(receipt, identity);
      pki.reconcile();
      assertEquals("new_leaves", text(pki.bundle().path("payload"), "phase"));
      var leaves = pki.leaves(identity);
      assertEquals(1, leaves.size());
      assertFalse(MsgnetRenewal.canonical(pki.bundle()).contains("private_pkcs8"));
      receipt
          .put("bundle_version", "2")
          .put("bundle_digest", NetworkJson.digest(pki.bundle()))
          .put("phase", "handshake_verified");
      pki.confirm(receipt, identity);
      pki.reconcile();
      assertEquals("retire_old", text(pki.bundle().path("payload"), "phase"));
      String oldCertificate = text(leaves.get(0), "certificate_der");
      advance(61 * 86400L);
      service.reconcile();
      pki.reconcile();
      assertNotEquals(oldCertificate, text(pki.leaves(identity).get(0), "certificate_der"));
      advance(10 * 365 * 86400L);
      service.reconcile();
      pki.reconcile();
      assertEquals("dual_trust", text(pki.bundle().path("payload"), "phase"));
      assertEquals(4, pki.bundle().path("payload").path("cas").size());
    }
  }

  @Test
  void controlReceiptPersistsConfirmationAndRejectsReplayOrCompromisedReadiness() throws Exception {
    try (ring) {
      install();
      service().reconcile();
      var device = KeyTestMaterial.pair();
      var identity =
          new MsgnetControlLedger.Identity(
              "target",
              "device",
              Base64.getUrlEncoder()
                  .withoutPadding()
                  .encodeToString(device.getPublic().getEncoded()),
              "network",
              "gateway",
              "",
              "subject");
      var ledger =
          new MsgnetControlLedger(service(), jdbc, tenant, "1", Map.of("target", identity));
      var request =
          MsgnetRenewal.object()
              .put("package_id", "request")
              .put("tenant_id", tenant.toString())
              .put("realm", "1")
              .put("target_id", "target")
              .put(
                  "challenge",
                  Base64.getUrlEncoder()
                      .withoutPadding()
                      .encodeToString(MsgnetKeyEncryptionTest.kek()));
      var control = NetworkJson.parse(service().publicControl());
      request.set("control", control);
      request.put("control_digest", NetworkJson.digest(control));
      var pack = ledger.create(request);
      var receipt = MsgnetRenewal.object();
      for (String field :
          java.util.List.of(
              "package_id", "tenant_id", "realm", "target_id", "challenge", "control_digest"))
        receipt.set(field, pack.path(field));
      receipt
          .put("identity_id", "device")
          .put("control_revision", text(control.path("payload"), "control_revision"))
          .put("result", "installed")
          .put("persisted_at", wall.now.toString());
      signReceipt(receipt, device);
      var confirmed = ledger.confirm(receipt);
      assertEquals("confirmed", text(confirmed, "state"));
      assertEquals(confirmed, ledger.confirm(receipt));
      var replay = receipt.deepCopy().put("persisted_at", wall.now.plusSeconds(1).toString());
      replay.remove("identity_auth");
      signReceipt(replay, device);
      assertThrows(AurigaException.class, () -> ledger.confirm(replay));
      var ready = confirmed.deepCopy().put("state", "ready").put("new_chain_ready", true);
      assertEquals("ready", text(ledger.enforce(ready), "state"));
      var compromised =
          ready
              .deepCopy()
              .put("state", "quarantined")
              .put("identity_compromised", true)
              .put("new_chain_ready", false);
      assertEquals("quarantined", text(ledger.enforce(compromised), "state"));
      assertThrows(AurigaException.class, () -> ledger.enforce(ready));
    }
  }

  private static void signReceipt(ObjectNode receipt, KeyPair device) throws Exception {
    var signature = Signature.getInstance("Ed25519");
    signature.initSign(device.getPrivate());
    signature.update(
        ("Auriga.NetworkControlReceipt.v2\0" + NetworkJson.canonical(receipt))
            .getBytes(StandardCharsets.UTF_8));
    receipt
        .putObject("identity_auth")
        .put("alg", "Ed25519")
        .put("kid", "device")
        .put("signature", Base64.getUrlEncoder().withoutPadding().encodeToString(signature.sign()));
  }

  @Test
  void rebootAfterClockCorrectionPublishesEarlierTimeAtAHigherVersion() throws Exception {
    try (ring;
        var keys = new NetworkSigningKeys(wall, 1)) {
      install();
      advance(5L * 365 * 86400);
      service().reconcile();
      var future = payload();
      wall.now = wall.now.minusSeconds(5L * 365 * 86400);
      var restarted = service(new MsgnetClock(wall, () -> tick));
      restarted.reconcile();
      var current =
          NetworkJson.parse(restarted.publication(), 1048576).path("keyset").path("payload");
      assertTrue(number(current, "keyset_version").compareTo(number(future, "keyset_version")) > 0);
      assertTrue(time(current, "issued_at").isBefore(time(future, "issued_at")));
      assertEquals("restart_recovery", text(current, "mode"));
      for (var proof : current.path("first_publications"))
        assertEquals(wall.now.toString(), text(proof, "committed_at"));
      restarted.reload(keys);
      assertEquals(NetworkJson.canonical(current), keys.publicKeysJson("1"));
    }
  }

  @Test
  void databaseBarrierRejectsAnOlderWriterWithoutApplicationChecks() throws Exception {
    try (ring) {
      install();
      service().reconcile();
      var error =
          assertThrows(
              org.springframework.dao.DataAccessException.class,
              () ->
                  TenantContext.call(
                      tenant,
                      () ->
                          new TransactionTemplate(manager)
                              .execute(
                                  s ->
                                      jdbc.update(
                                          """
              INSERT INTO collab.network_key_publication
                (id, tenant_id, realm, keyset_version, signed_keyset, announcement, first_published, updated_at)
              VALUES (?, ?, '1', 1, '{}', '{}', '{}'::jsonb, now())
              """,
                                          UUID.randomUUID(),
                                          tenant))));
      assertEquals(
          "55000", assertInstanceOf(java.sql.SQLException.class, error.getCause()).getSQLState());
    }
  }

  @Test
  void twoReplicasCommitOnlyOneVersionAndRlsRejectsOtherTenant() throws Exception {
    try (ring;
        var workers = java.util.concurrent.Executors.newVirtualThreadPerTaskExecutor()) {
      install();
      var start = new java.util.concurrent.CountDownLatch(1);
      var a =
          workers.submit(
              () -> {
                start.await();
                service().reconcile();
                return null;
              });
      var b =
          workers.submit(
              () -> {
                start.await();
                service().reconcile();
                return null;
              });
      start.countDown();
      a.get(20, java.util.concurrent.TimeUnit.SECONDS);
      b.get(20, java.util.concurrent.TimeUnit.SECONDS);
      assertEquals(1, count("msgnet_publication_history"));
      assertEquals(3, count("msgnet_key_history"));
      int visible =
          TenantContext.call(
              UUID.randomUUID(),
              () ->
                  new TransactionTemplate(manager)
                      .execute(
                          s ->
                              jdbc.queryForObject(
                                  "SELECT count(*) FROM collab.msgnet_realm WHERE tenant_id = ?",
                                  Integer.class,
                                  tenant)));
      assertEquals(0, visible);
    }
  }
}
