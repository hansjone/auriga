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
    install(false);
  }

  void install(boolean allTransportComponents) throws Exception {
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
    if (allTransportComponents) {
      var scopes = (tools.jackson.databind.node.ArrayNode) a.path("transport_scope");
      ((tools.jackson.databind.node.ArrayNode) scopes.get(0).path("ekus")).add("clientAuth");
      var edge = (ObjectNode) scopes.get(0).deepCopy();
      edge.put("component", "edge").put("identity_id", "edge-projector");
      edge.putArray("sans").add("edge.example.com");
      scopes.add(edge);
      var broker = (ObjectNode) scopes.get(0).deepCopy();
      broker.put("component", "broker").put("identity_id", "broker-projector");
      broker.putArray("sans").add("broker.example.com");
      broker.putArray("ekus").add("serverAuth");
      scopes.add(broker);
    }
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
        ("Auriga." + kind + ".v2\0" + MsgnetRenewal.canonical(payload))
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
      install(true);
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
                  MsgnetV2Verifier.digest(
                      NetworkJson.parse(service.publication(), 1048576).path("keyset")))
              .put("phase", "persisted");
      confirmAllTransport(pki, receipt, identity);
      pki.reconcile();
      assertEquals("new_leaves", text(pki.bundle().path("payload"), "phase"));
      var leaves = pki.leaves(identity);
      assertEquals(2, leaves.size());
      for (var leaf : leaves) assertNotEquals("edge-presence", text(leaf, "role"));
      var edgeLeaves =
          pki.leaves(
              new MsgnetControlLedger.Identity(
                  "edge", "edge-projector", "", "edge", "gateway", "", "subject"));
      assertEquals(
          java.util.Set.of("edge", "edge-presence"),
          edgeLeaves
              .valueStream()
              .map(l -> text(l, "role"))
              .collect(java.util.stream.Collectors.toSet()));
      assertEquals(
          1,
          pki.leaves(
                  new MsgnetControlLedger.Identity(
                      "broker", "broker-projector", "", "broker", "gateway", "", "subject"))
              .size());
      assertFalse(MsgnetRenewal.canonical(pki.bundle()).contains("private_pkcs8"));
      receipt
          .put("bundle_version", "2")
          .put("bundle_digest", NetworkJson.digest(pki.bundle()))
          .put("phase", "handshake_verified");
      confirmAllTransport(pki, receipt, identity);
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

  private static void confirmAllTransport(
      MsgnetPki pki, ObjectNode receipt, MsgnetControlLedger.Identity network) {
    pki.confirm(receipt, network);
    pki.confirm(
        receipt,
        new MsgnetControlLedger.Identity(
            "edge", "edge-projector", "", "edge", "gateway", "", "subject"));
    pki.confirm(
        receipt,
        new MsgnetControlLedger.Identity(
            "broker", "broker-projector", "", "broker", "gateway", "", "subject"));
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

  @Test
  void unpublishedStatusHasNoInventedVersionOrSuccess() throws Exception {
    try (ring) {
      install();
      var status = NetworkJson.parse(service().status());
      assertEquals("renewing", text(status, "state"));
      assertFalse(status.path("has_publication").asBoolean());
      assertFalse(status.has("generation"));
      assertFalse(status.has("last_success_at"));
      service().paused(true);
      assertEquals("paused", text(NetworkJson.parse(service().status()), "state"));
    }
  }

  @Test
  void bootstrapAuthenticatesWithoutJwtAndBindsEveryReceiptToIssuedBytes() throws Exception {
    try (ring) {
      install();
      var renewal = service();
      renewal.reconcile();
      var signing = KeyTestMaterial.pair();
      var sealing = java.security.KeyPairGenerator.getInstance("X25519").generateKeyPair();
      var pin =
          new MsgnetControlLedger.Identity(
              "target",
              "synthetic-projector",
              encode(signing.getPublic().getEncoded()),
              "network",
              "gateway",
              encode(sealing.getPublic().getEncoded()),
              "CN=projector");
      var ledger = new MsgnetControlLedger(renewal, jdbc, tenant, "1", Map.of("target", pin));
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
              renewal,
              new MsgnetPrivateKeyStore(jdbc, manager, tenant, "1", ring),
              jdbc,
              tenant,
              "1",
              config);
      pki.reconcile();
      var materials =
          new MsgnetMaterials(
              renewal,
              ledger,
              new MsgnetPrivateKeyStore(jdbc, manager, tenant, "1", ring),
              jdbc,
              tenant,
              "1",
              pki);
      var request =
          MsgnetRenewal.object()
              .put("tenant_id", tenant.toString())
              .put("realm", "1")
              .put("installation_id", pin.identityId())
              .put("component", "network")
              .put("gateway_id", "gateway")
              .put("nonce", encode(MsgnetKeyEncryptionTest.kek()))
              .put("expected_generation", text(payload(), "generation"))
              .put("expected_keyset_version", text(payload(), "keyset_version"));
      identitySign(request, signing, pin.identityId(), "NetworkProjectionRequest");
      var controller =
          new MsgnetRenewalController(
              renewal, new MsgnetNetworkRenewal(renewal, ledger, materials), pki, ledger);
      var http = new org.springframework.mock.web.MockHttpServletRequest();
      assertThrows(
          AurigaException.class,
          () ->
              controller.sealed(
                  MsgnetRenewal.canonical(request).getBytes(StandardCharsets.UTF_8), http));
      http.setSecure(true);
      var response =
          controller.sealed(
              MsgnetRenewal.canonical(request).getBytes(StandardCharsets.UTF_8), http);
      assertEquals(200, response.getStatusCode().value());
      var sealed = NetworkJson.parse(response.getBody(), 1048576);
      byte[] clear = MsgnetHpke.open(sealed, sealing.getPrivate(), sealing.getPublic());
      JsonNode material;
      try {
        material = NetworkJson.parse(new String(clear, StandardCharsets.UTF_8), 1048576);
      } finally {
        java.util.Arrays.fill(clear, (byte) 0);
      }
      assertThrows(AurigaException.class, () -> materials.sealed(request));
      var snapshot =
          MsgnetProjectionBinding.snapshot(
              material, NetworkJson.parse(renewal.publication(), 1048576));
      var manifest = snapshot.deepCopy().put("material_revision", "1");
      var selector = snapshot.deepCopy();
      selector.remove("files");
      selector
          .put("material_revision", "1")
          .put("manifest_digest", MsgnetV2Verifier.digest(manifest));
      var receipt = selector.deepCopy();
      receipt.remove("material_revision");
      receipt.remove("manifest_digest");
      receipt.put("phase", "handshake_verified").put("observed_at", wall.now.toString());
      receipt.set("local_selector", selector);
      identitySign(receipt, signing, pin.identityId(), "NetworkProjectionReceipt");
      assertThrows(AurigaException.class, () -> materials.confirm(receipt));
      receipt.remove("identity_auth");
      receipt.put("phase", "loaded");
      identitySign(receipt, signing, pin.identityId(), "NetworkProjectionReceipt");
      assertEquals(receipt, materials.confirm(receipt));
      assertEquals(receipt, materials.confirm(receipt));
      var conflicting = receipt.deepCopy();
      ((ObjectNode) conflicting.path("local_selector")).put("material_revision", "2");
      conflicting.remove("identity_auth");
      identitySign(conflicting, signing, pin.identityId(), "NetworkProjectionReceipt");
      assertThrows(AurigaException.class, () -> materials.confirm(conflicting));
      receipt.remove("identity_auth");
      receipt.put("phase", "handshake_verified");
      identitySign(receipt, signing, pin.identityId(), "NetworkProjectionReceipt");
      assertEquals(receipt, materials.confirm(receipt));
      var wrongKid = request.deepCopy().put("nonce", encode(MsgnetKeyEncryptionTest.kek()));
      wrongKid.remove("identity_auth");
      identitySign(wrongKid, signing, "other", "NetworkProjectionRequest");
      assertThrows(AurigaException.class, () -> materials.sealed(wrongKid));
    }
  }

  private static String encode(byte[] bytes) {
    return Base64.getUrlEncoder().withoutPadding().encodeToString(bytes);
  }

  private static void identitySign(ObjectNode body, KeyPair pair, String kid, String type)
      throws Exception {
    var signer = Signature.getInstance("Ed25519");
    signer.initSign(pair.getPrivate());
    signer.update(
        ("Auriga." + type + ".v2\0" + MsgnetRenewal.canonical(body))
            .getBytes(StandardCharsets.UTF_8));
    body.putObject("identity_auth")
        .put("alg", "Ed25519")
        .put("kid", kid)
        .put("signature", encode(signer.sign()));
  }

  record MigrationInput(
      JsonNode permit,
      JsonNode authority,
      JsonNode control,
      JsonNode ancestor,
      MsgnetMigration.Member member,
      RootKeyPublication legacy) {}

  MigrationInput migrationInput() throws Exception {
    return migrationInput(false);
  }

  MigrationInput migrationInput(boolean staggered) throws Exception {
    install();
    var authority = service().run(() -> service().requiredState().path("authority"));
    var old =
        MsgnetRenewal.object()
            .put("schema_version", 1)
            .put("realm", "1")
            .put("keyset_version", "9")
            .put("issued_at", wall.now.toString())
            .put("valid_until", wall.now.plusSeconds(86400).toString());
    var keys = old.putArray("keys");
    var privateKeys = new java.util.HashMap<String, byte[]>();
    for (String purpose :
        java.util.List.of("business_delivery", "management_state", "protocol_response")) {
      var pair = KeyTestMaterial.pair();
      privateKeys.put(purpose, pair.getPrivate().getEncoded());
      java.time.Instant before = wall.now.minusSeconds(3600),
          after = wall.now.plusSeconds(7 * 86400);
      if (staggered) {
        before = "management_state".equals(purpose) ? wall.now.minusSeconds(3 * 86400) : wall.now;
        after =
            wall.now.plusSeconds(
                "management_state".equals(purpose)
                    ? 4 * 86400
                    : "business_delivery".equals(purpose) ? 2 * 86400 : 84 * 3600);
      }
      keys.addObject()
          .put("kid", purpose)
          .put("alg", "Ed25519")
          .put("purpose", purpose)
          .put("state", "active")
          .put("not_before", before.toString())
          .put("not_after", after.toString())
          .put("public_key_spki", encode(pair.getPublic().getEncoded()));
    }
    var release = OfflineKeySigning.sign(MsgnetRenewal.canonical(old), "root", root.getPrivate());
    var legacy =
        new RootKeyPublication(
            "1", "root", root.getPublic(), RootKeyPublication.fingerprint(root.getPublic()), wall);
    new NetworkKeyOperations(jdbc, manager, tenant, "1", legacy, "1", wall).publish(release);
    var ancestor = MsgnetRenewal.object().putArray("archive");
    var entry = ancestor.addObject();
    entry.set("signed_keyset", NetworkJson.parse(release.signedKeyset()));
    entry.set("announcement", NetworkJson.parse(release.announcement()));
    var materials = MsgnetRenewal.object().put("schema_version", 2);
    var materialKeys = materials.putArray("keys");
    keys.valueStream()
        .sorted(java.util.Comparator.comparing(k -> text(k, "kid")))
        .forEach(
            key -> {
              var material = materialKeys.addObject();
              for (String field :
                  java.util.List.of("kid", "purpose", "public_key_spki", "not_before", "not_after"))
                material.set(field, key.path(field));
            });
    var members = MsgnetRenewal.object().putArray("members");
    members
        .addObject()
        .put("version", "9")
        .put("pair_digest", MsgnetV2Verifier.digest(NetworkJson.parse(release.signedKeyset())))
        .put("material_digest", MsgnetV2Verifier.digest(materials));
    var permit =
        MsgnetRenewal.object()
            .put("schema_version", 2)
            .put("tenant_id", tenant.toString())
            .put("realm", "1")
            .put("permit_id", "migration")
            .put("ancestor_digest", MsgnetV2Verifier.digest(ancestor))
            .put("queue_commitment_digest", MsgnetV2Verifier.digest(members))
            .put("maximum_committed_v1_version", "9")
            .put("minimum_v2_version", "10")
            .put("minimum_generation", "1");
    var control =
        (ObjectNode) NetworkJson.parse(service().publicControl()).path("payload").deepCopy();
    control
        .put(
            "control_revision",
            number(control, "control_revision").add(java.math.BigInteger.ONE).toString())
        .put("minimum_keyset_version", "10");
    return new MigrationInput(
        MsgnetOfflineRoot.sign("migration", permit, "root", root.getPrivate()),
        authority,
        MsgnetOfflineRoot.sign("control", control, "root", root.getPrivate()),
        ancestor,
        new MsgnetMigration.Member(release, privateKeys),
        legacy);
  }

  @Test
  void migrationVerifiesRealMaterialsAndAtomicallyPreservesActiveAndLegacyHistory()
      throws Exception {
    try (ring;
        var signingKeys = new NetworkSigningKeys(wall, 1)) {
      var input = migrationInput();
      var migrate = new MsgnetMigration(service(), jdbc, tenant, "1", input.legacy(), wall);
      var invalid = new java.util.HashMap<>(input.member().privateKeys());
      invalid.put("business_delivery", KeyTestMaterial.pair().getPrivate().getEncoded());
      assertThrows(
          IllegalArgumentException.class,
          () ->
              migrate.migrate(
                  input.permit(),
                  input.authority(),
                  input.control(),
                  runtime.getPrivate().getEncoded(),
                  input.ancestor(),
                  java.util.List.of(
                      new MsgnetMigration.Member(input.member().release(), invalid))));
      assertEquals(0, count("msgnet_publication_history"));
      var receipt =
          migrate.migrate(
              input.permit(),
              input.authority(),
              input.control(),
              runtime.getPrivate().getEncoded(),
              input.ancestor(),
              java.util.List.of(input.member()));
      assertTrue(receipt.path("barrier_committed").asBoolean());
      assertEquals("migration", text(payload(), "mode"));
      assertEquals("10", text(payload(), "keyset_version"));
      for (var key : payload().path("keys"))
        if ("active".equals(text(key, "state")))
          assertEquals(text(key, "purpose"), text(key, "kid"));
      assertEquals(1, count("msgnet_migration"));
      assertEquals(
          MsgnetRenewal.canonical(receipt),
          MsgnetRenewal.canonical(
              migrate.migrate(
                  input.permit(),
                  input.authority(),
                  input.control(),
                  runtime.getPrivate().getEncoded(),
                  input.ancestor(),
                  java.util.List.of(input.member()))));
      service().reload(signingKeys);
      assertNotNull(signingKeys.publicKey("business_delivery"));
      assertEquals(
          input.member().release().signedKeyset(),
          service()
              .run(
                  () ->
                      jdbc.queryForObject(
                          "SELECT signed_keyset FROM collab.network_key_publication WHERE tenant_id = ?",
                          String.class,
                          tenant)));
      assertThrows(
          org.springframework.dao.DataAccessException.class,
          () ->
              service()
                  .run(
                      () ->
                          jdbc.update(
                              "UPDATE collab.network_key_publication SET pending_push = true WHERE tenant_id = ?",
                              tenant)));
    }
  }

  @Test
  void migrationTimeoutRollsBackEntireBarrierAndUnfreezesLegacyWriter() throws Exception {
    try (ring) {
      var input = migrationInput();
      var calls = new java.util.concurrent.atomic.AtomicInteger();
      var migrate =
          new MsgnetMigration(
              service(),
              jdbc,
              tenant,
              "1",
              input.legacy(),
              wall,
              () -> calls.getAndIncrement() == 0 ? 0 : 601_000_000_000L);
      var failure =
          assertThrows(
              AurigaException.class,
              () ->
                  migrate.migrate(
                      input.permit(),
                      input.authority(),
                      input.control(),
                      runtime.getPrivate().getEncoded(),
                      input.ancestor(),
                      java.util.List.of(input.member())));
      assertEquals("collab.msgnet_migration_expired", failure.errorCode().code());
      assertEquals(0, count("msgnet_publication_history"));
      assertFalse(
          service()
              .run(
                  () ->
                      jdbc.queryForObject(
                          "SELECT legacy_disabled FROM collab.msgnet_realm WHERE tenant_id = ?",
                          Boolean.class,
                          tenant)));
      assertEquals(1, count("msgnet_migration"));
      assertEquals(
          1,
          service()
              .run(
                  () ->
                      jdbc.update(
                          "UPDATE collab.network_key_publication SET pending_push = true WHERE tenant_id = ?",
                          tenant)));
    }
  }

  @Test
  void revokedPublicControlDoesNotNeedRuntimeOrKekSecretsAndKeepsItsHighWaterMark()
      throws Exception {
    try (ring) {
      install();
      var renewal = service();
      var original = NetworkJson.parse(renewal.publicControl(), 1048576);
      var body = (ObjectNode) original.path("payload").deepCopy();
      body.put(
          "control_revision",
          number(body, "control_revision").add(java.math.BigInteger.ONE).toString());
      ((tools.jackson.databind.node.ArrayNode) body.path("revoked_authority_ids"))
          .add(
              text(
                  renewal.run(() -> renewal.requiredState()).path("authority").path("payload"),
                  "authority_id"));
      var revoked = rootSigned(body, "NetworkRootControl");
      var source =
          new MsgnetPublicControl(
              jdbc,
              manager,
              tenant,
              "1",
              new MsgnetV2Verifier(tenant.toString(), "1", "root", root.getPublic()),
              null);
      source.install(revoked, false);
      assertEquals("authority_revoked", text(NetworkJson.parse(renewal.status()), "state"));
      assertThrows(AurigaException.class, () -> source.install(original, false));
      assertEquals(revoked, NetworkJson.parse(renewal.publicControl(), 1048576));
    }
  }

  @Test
  void largeRootControlRoundTripsWithoutRelaxingTheLegacyJsonLimit() throws Exception {
    try (ring) {
      install();
      var control =
          (ObjectNode) NetworkJson.parse(service().publicControl()).path("payload").deepCopy();
      control.put(
          "control_revision",
          number(control, "control_revision").add(java.math.BigInteger.ONE).toString());
      var ids = (tools.jackson.databind.node.ArrayNode) control.path("revoked_installation_ids");
      for (int n = 0; n < 2200; n++) ids.add(UUID.randomUUID().toString());
      var signed = rootSigned(control, "NetworkRootControl");
      assertTrue(MsgnetRenewal.canonical(signed).length() > 65536);
      service().installControl(signed);
      assertEquals(signed, NetworkJson.parse(service().publicControl(), 1048576));
      assertThrows(AurigaException.class, () -> NetworkJson.parse(MsgnetRenewal.canonical(signed)));
    }
  }

  @Test
  void normalAuthorityHandoverAfterDowntimeRecoversBeforeChangingSigner() throws Exception {
    try (ring) {
      install();
      var renewal = service();
      renewal.reconcile();
      var replacement = KeyTestMaterial.pair();
      var authority =
          (ObjectNode)
              renewal
                  .run(() -> renewal.requiredState().path("authority").path("payload"))
                  .deepCopy();
      authority
          .put("authority_id", "replacement")
          .put("runtime_kid", "replacement-runtime")
          .put("public_key_spki", encode(replacement.getPublic().getEncoded()));
      var signedAuthority = rootSigned(authority, "NetworkRuntimeAuthority");
      var control =
          (ObjectNode) NetworkJson.parse(renewal.publicControl()).path("payload").deepCopy();
      control.put(
          "control_revision",
          number(control, "control_revision").add(java.math.BigInteger.ONE).toString());
      ((tools.jackson.databind.node.ArrayNode) control.path("allowed_authorities"))
          .addObject()
          .put("authority_id", "replacement")
          .put("authority_digest", MsgnetV2Verifier.digest(signedAuthority));
      advance(9 * 86400L);
      renewal.install(
          signedAuthority,
          rootSigned(control, "NetworkRootControl"),
          replacement.getPrivate().getEncoded());
      renewal.reconcile();
      assertEquals("authority_transition", text(payload(), "mode"));
      assertEquals("replacement", text(payload(), "authority_id"));
      assertEquals(3, count("msgnet_publication_history"));
      var predecessor =
          NetworkJson.parse(renewal.publication(), 1048576)
              .path("predecessor")
              .path("keyset")
              .path("payload");
      assertEquals("restart_recovery", text(predecessor, "mode"));
      for (var key : payload().path("keys"))
        if ("active".equals(text(key, "state")))
          assertTrue(MsgnetV2Verifier.valid(key, wall.instant()));
    }
  }

  @Test
  void lostPgConnectionAfterPrivateAndHistoryWritesRollsBackAndRetriesWithoutHalfACommit()
      throws Exception {
    try (ring) {
      install();
      var calls = new java.util.concurrent.atomic.AtomicInteger();
      var config =
          template
              .path("boundary_cases")
              .valueStream()
              .filter(v -> "config-defaults".equals(text(v, "id")))
              .findFirst()
              .orElseThrow()
              .path("input")
              .path("config");
      var failing =
          new MsgnetRenewal(
              jdbc,
              manager,
              tenant,
              "1",
              new MsgnetV2Verifier(tenant.toString(), "1", "root", root.getPublic()),
              new MsgnetPrivateKeyStore(jdbc, manager, tenant, "1", ring),
              wall,
              guard,
              () -> {
                if (calls.incrementAndGet() == 3)
                  jdbc.execute("SELECT pg_terminate_backend(pg_backend_pid())");
                return wall.instant();
              },
              config);
      assertThrows(RuntimeException.class, failing::reconcile);
      assertEquals(0, count("msgnet_key_history"));
      assertEquals(0, count("msgnet_publication_history"));
      assertEquals(1, count("msgnet_private_key"));
      service().reconcile();
      assertEquals(1, count("msgnet_publication_history"));
      assertEquals(3, count("msgnet_key_history"));
    }
  }

  @Test
  void mixedRecoveryKeepsValidActiveActivatesMatureNextAndRestartsOnlyTheMissingPurpose()
      throws Exception {
    try (ring) {
      var input = migrationInput(true);
      new MsgnetMigration(service(), jdbc, tenant, "1", input.legacy(), wall)
          .migrate(
              input.permit(),
              input.authority(),
              input.control(),
              runtime.getPrivate().getEncoded(),
              input.ancestor(),
              java.util.List.of(input.member()));
      advance(3 * 86400L);
      service().reconcile();
      assertEquals("restart_recovery", text(payload(), "mode"));
      String kept = text(findActive(payload(), "business_delivery"), "kid");
      var mature =
          payload()
              .path("keys")
              .valueStream()
              .filter(
                  k ->
                      "management_state".equals(text(k, "purpose"))
                          && "next".equals(text(k, "state")))
              .findFirst()
              .orElseThrow();
      advance(36 * 3600L);
      service().reconcile();
      assertEquals("restart_recovery", text(payload(), "mode"));
      assertEquals(kept, text(findActive(payload(), "business_delivery"), "kid"));
      assertEquals(text(mature, "kid"), text(findActive(payload(), "management_state"), "kid"));
      assertNotEquals("protocol_response", text(findActive(payload(), "protocol_response"), "kid"));
      assertEquals(wall.instant(), time(findActive(payload(), "protocol_response"), "not_before"));
    }
  }

  private static JsonNode findActive(JsonNode payload, String purpose) {
    return payload
        .path("keys")
        .valueStream()
        .filter(k -> purpose.equals(text(k, "purpose")) && "active".equals(text(k, "state")))
        .findFirst()
        .orElseThrow();
  }

  @Test
  void servicePortDoesNotRebindAForeignCallerToTheConfiguredTenant() throws Exception {
    try (ring) {
      install();
      var api = new MsgnetNetworkRenewal(service(), null, null);
      var failure =
          assertThrows(
              AurigaException.class, () -> TenantContext.call(UUID.randomUUID(), api::status));
      assertEquals("collab.msgnet_scope_denied", failure.errorCode().code());
      assertEquals("renewing", MsgnetNetworkRenewal.tree(api.status()).path("state").asString());
    }
  }

  @Test
  void raisedRootFloorPublishesDirectlyAtTheFloorAndCannotBeRolledBack() throws Exception {
    try (ring;
        var loaded = new NetworkSigningKeys(wall, 1)) {
      install();
      service().reconcile();
      service().reload(loaded);
      var guard =
          (NetworkSigningKeys.SigningGuard)
              org.springframework.test.util.ReflectionTestUtils.getField(loaded, "runtimeGuard");
      var control =
          (ObjectNode) NetworkJson.parse(service().publicControl()).path("payload").deepCopy();
      control
          .put(
              "control_revision",
              number(control, "control_revision").add(java.math.BigInteger.ONE).toString())
          .put("minimum_keyset_version", "100");
      service().installControl(rootSigned(control, "NetworkRootControl"));
      var called = new java.util.concurrent.atomic.AtomicBoolean();
      assertEquals(
          "collab.msgnet_version_rollback",
          assertThrows(
                  AurigaException.class,
                  () ->
                      guard.sign(
                          loaded,
                          () -> {
                            called.set(true);
                            return MsgnetRenewal.object();
                          }))
              .errorCode()
              .code());
      assertFalse(called.get());
      advance(60);
      service().reconcile();
      assertEquals("100", text(payload(), "keyset_version"));
      var rollback =
          control
              .deepCopy()
              .put(
                  "control_revision",
                  number(control, "control_revision").add(java.math.BigInteger.ONE).toString())
              .put("minimum_keyset_version", "99");
      var signed = rootSigned(rollback, "NetworkRootControl");
      assertEquals(
          "collab.msgnet_control_rollback",
          assertThrows(AurigaException.class, () -> service().installControl(signed))
              .errorCode()
              .code());
    }
  }

  @Test
  void transportRejectsWrongTenantRealmAndSigningKidEvenWithAValidRuntimeSignature()
      throws Exception {
    try (ring) {
      install();
      var renewal = service();
      renewal.reconcile();
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
              renewal,
              new MsgnetPrivateKeyStore(jdbc, manager, tenant, "1", ring),
              jdbc,
              tenant,
              "1",
              config);
      pki.reconcile();
      var authority = renewal.run(() -> renewal.requiredState().path("authority"));
      var verifier = new MsgnetV2Verifier(tenant.toString(), "1", "root", root.getPublic());
      verifier.verifyTransport(pki.bundle(), authority, "0", "", false, false);
      for (String field : java.util.List.of("tenant_id", "realm")) {
        var payload = (ObjectNode) pki.bundle().path("payload").deepCopy();
        payload.put(field, "realm".equals(field) ? "2" : UUID.randomUUID().toString());
        var changed = renewal.run(() -> renewal.signTransport(payload));
        assertEquals(
            "collab.msgnet_scope_denied",
            assertThrows(
                    AurigaException.class,
                    () -> verifier.verifyTransport(changed, authority, "0", "", false, false))
                .errorCode()
                .code());
      }
      var wrongKid = (ObjectNode) pki.bundle().deepCopy();
      ((ObjectNode) wrongKid.path("runtime_auth")).put("kid", "foreign");
      assertEquals(
          "collab.msgnet_scope_denied",
          assertThrows(
                  AurigaException.class,
                  () -> verifier.verifyTransport(wrongKid, authority, "0", "", false, false))
              .errorCode()
              .code());
    }
  }

  @Test
  void durablePauseFencesAnAlreadyLoadedSignerAndRemainsPausedAfterReload() throws Exception {
    try (ring;
        var loaded = new NetworkSigningKeys(wall, 1);
        var restarted = new NetworkSigningKeys(wall, 1)) {
      install();
      service().reconcile();
      service().reload(loaded);
      var guard =
          (NetworkSigningKeys.SigningGuard)
              org.springframework.test.util.ReflectionTestUtils.getField(loaded, "runtimeGuard");
      service().paused(true);
      var invoked = new java.util.concurrent.atomic.AtomicBoolean();
      assertThrows(
          AurigaException.class,
          () ->
              guard.sign(
                  loaded,
                  () -> {
                    invoked.set(true);
                    return MsgnetRenewal.object();
                  }));
      assertFalse(invoked.get());
      assertThrows(AurigaException.class, () -> service().reload(restarted));
      assertEquals("paused", text(NetworkJson.parse(service().status()), "state"));
      service().paused(false);
      service().reload(restarted);
      var restored =
          (NetworkSigningKeys.SigningGuard)
              org.springframework.test.util.ReflectionTestUtils.getField(restarted, "runtimeGuard");
      assertNotNull(restored.sign(restarted, MsgnetRenewal::object));
    }
  }
}
