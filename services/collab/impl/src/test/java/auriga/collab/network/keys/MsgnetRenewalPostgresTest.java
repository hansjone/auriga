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
      assertEquals("dual_trust", text(pki.bundle().path("payload"), "phase"));
      receipt.put("phase", "loaded");
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

  final Map<String, KeyPair> transportSigners = new java.util.HashMap<>();
  final Map<String, KeyPair> transportSealers = new java.util.HashMap<>();

  private void confirmAllTransport(
      MsgnetPki pki, ObjectNode receipt, MsgnetControlLedger.Identity ignored) throws Exception {
    for (String component : java.util.List.of("network", "edge", "broker")) {
      String id = "network".equals(component) ? "synthetic-projector" : component + "-projector";
      project(pki, component, id, text(receipt, "phase"));
    }
  }

  private void ready(MsgnetControlLedger ledger, MsgnetControlLedger.Identity pin, KeyPair signing)
      throws Exception {
    var request = controlRequest(pin.targetId());
    var pack = ledger.create(request);
    var receipt = MsgnetRenewal.object();
    for (String field :
        java.util.List.of(
            "package_id", "tenant_id", "realm", "target_id", "challenge", "control_digest"))
      receipt.set(field, pack.path(field));
    receipt
        .put("identity_id", pin.identityId())
        .put("control_revision", text(pack.path("control").path("payload"), "control_revision"))
        .put("result", "installed")
        .put("persisted_at", wall.now.toString());
    identitySign(receipt, signing, pin.identityId(), "NetworkControlReceipt");
    var confirmed = ledger.confirm(receipt);
    ledger.enforce(confirmed.deepCopy().put("state", "ready").put("new_chain_ready", true));
  }

  ObjectNode controlRequest(String target) {
    var control = NetworkJson.parse(service().publicControl(), 1048576);
    var request =
        MsgnetRenewal.object()
            .put("package_id", "request")
            .put("tenant_id", tenant.toString())
            .put("realm", "1")
            .put("target_id", target)
            .put("challenge", encode(MsgnetKeyEncryptionTest.kek()))
            .put("control_digest", digest(control));
    request.set("control", control);
    return request;
  }

  private JsonNode project(MsgnetPki pki, String component, String id, String phase)
      throws Exception {
    var signer = transportSigners.computeIfAbsent(id, k -> KeyTestMaterial.pair());
    var sealer = transportSealers.get(id);
    if (sealer == null) {
      sealer = java.security.KeyPairGenerator.getInstance("X25519").generateKeyPair();
      transportSealers.put(id, sealer);
    }
    var pin =
        new MsgnetControlLedger.Identity(
            id,
            id,
            encode(signer.getPublic().getEncoded()),
            component,
            "gateway",
            encode(sealer.getPublic().getEncoded()),
            "CN=projector");
    var renewal = service();
    var ledger = new MsgnetControlLedger(renewal, jdbc, tenant, "1", Map.of(id, pin));
    ready(ledger, pin, signer);
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
            .put("installation_id", id)
            .put("component", component)
            .put("gateway_id", "gateway")
            .put("nonce", encode(MsgnetKeyEncryptionTest.kek()))
            .put("expected_generation", text(payload(), "generation"))
            .put("expected_keyset_version", text(payload(), "keyset_version"));
    identitySign(request, signer, id, "NetworkProjectionRequest");
    assertTrue(
        ledger.claim(
            KeyTestMaterial.JSON.treeToValue(
                request, auriga.contracts.msgnet.model.ProjectionRequest.class)));
    assertFalse(
        ledger.claim(
            KeyTestMaterial.JSON.treeToValue(
                request, auriga.contracts.msgnet.model.ProjectionRequest.class)));
    var sealed = materials.sealed(request);
    byte[] bytes = MsgnetHpke.open(sealed, sealer.getPrivate(), sealer.getPublic());
    JsonNode material;
    try {
      material = NetworkJson.parse(new String(bytes, StandardCharsets.UTF_8), 1048576);
    } finally {
      java.util.Arrays.fill(bytes, (byte) 0);
    }
    var snapshot =
        MsgnetProjectionBinding.snapshot(
            material, NetworkJson.parse(renewal.publication(), 1048576));
    String revision =
        renewal.run(
            () -> {
              var rows =
                  jdbc.queryForList(
                      "SELECT selector FROM collab.msgnet_projection_head WHERE tenant_id = ? AND realm = '1' AND installation_id = ? AND component = ?",
                      String.class,
                      tenant,
                      id,
                      component);
              return rows.isEmpty()
                  ? "1"
                  : number(NetworkJson.parse(rows.getFirst(), 1048576), "material_revision")
                      .add(java.math.BigInteger.ONE)
                      .toString();
            });
    var manifest = snapshot.deepCopy().put("material_revision", revision);
    var selector = snapshot.deepCopy();
    selector.remove("files");
    selector.put("material_revision", revision).put("manifest_digest", digest(manifest));
    var receipt = snapshot.deepCopy();
    receipt.remove("files");
    receipt.set("local_selector", selector);
    receipt
        .put("phase", "handshake_verified".equals(phase) ? "loaded" : phase)
        .put("observed_at", wall.now.toString());
    identitySign(receipt, signer, id, "NetworkProjectionReceipt");
    materials.confirm(receipt);
    if ("handshake_verified".equals(phase)) {
      receipt.remove("identity_auth");
      receipt.put("phase", phase);
      identitySign(receipt, signer, id, "NetworkProjectionReceipt");
      materials.confirm(receipt);
    }
    return selector;
  }

  MsgnetPki pki(MsgnetRenewal renewal) {
    var config =
        template
            .path("boundary_cases")
            .valueStream()
            .filter(v -> "config-defaults".equals(text(v, "id")))
            .findFirst()
            .orElseThrow()
            .path("input")
            .path("config");
    return new MsgnetPki(
        renewal,
        new MsgnetPrivateKeyStore(jdbc, manager, tenant, "1", ring),
        jdbc,
        tenant,
        "1",
        config);
  }

  ObjectNode confirmationQuery(JsonNode bundle) {
    return MsgnetRenewal.object()
        .put("tenant_id", tenant.toString())
        .put("realm", "1")
        .put("bundle_version", text(bundle.path("payload"), "bundle_version"))
        .put("bundle_digest", digest(bundle))
        .put("challenge", encode(MsgnetKeyEncryptionTest.kek()));
  }

  @Test
  void transportConfirmationUsesExactDurableSelectorsAndFreshChallengeWithoutWriting()
      throws Exception {
    try (ring) {
      install();
      var renewal = service();
      renewal.reconcile();
      var pki = pki(renewal);
      pki.reconcile();
      var initial = pki.bundle();
      var query = confirmationQuery(initial);
      assertEquals(
          "collab.msgnet_projection_pending",
          assertThrows(AurigaException.class, () -> pki.confirmation(query)).errorCode().code());
      var selector = project(pki, "network", "synthetic-projector", "loaded");
      int nonces = count("msgnet_projection_nonce");
      var evidence = pki.confirmation(query);
      var expected = MsgnetRenewal.object().put("synthetic-projector", digest(selector));
      renewal.run(
          () -> {
            renewal
                .transportVerifier()
                .confirmation(
                    evidence,
                    initial,
                    renewal.requiredState().path("authority"),
                    renewal.requiredState().path("control"),
                    text(query, "challenge"),
                    wall.instant(),
                    "loaded",
                    expected);
            return null;
          });
      assertEquals(nonces, count("msgnet_projection_nonce"));
      var retry = query.deepCopy().put("challenge", encode(MsgnetKeyEncryptionTest.kek()));
      assertNotEquals(evidence, pki.confirmation(retry));
      assertEquals(
          "collab.msgnet_scope_denied",
          assertThrows(
                  AurigaException.class,
                  () ->
                      pki.confirmation(
                          query.deepCopy().put("tenant_id", UUID.randomUUID().toString())))
              .errorCode()
              .code());
      assertEquals(
          "collab.msgnet_same_version_conflict",
          assertThrows(
                  AurigaException.class,
                  () ->
                      pki.confirmation(
                          query.deepCopy().put("bundle_digest", "sha256:" + "0".repeat(64))))
              .errorCode()
              .code());
      pki.reconcile();
      assertEquals("new_leaves", text(pki.bundle().path("payload"), "phase"));
      // Reissuing leaves supersedes live nonces, but the completed prerequisite remains immutable.
      assertEquals(
          evidence.path("payload").path("readers"),
          pki.confirmation(query).path("payload").path("readers"));
      assertEquals(initial, pki.history(digest(initial)).path("bundle"));
      var immutable =
          assertThrows(
              org.springframework.dao.DataAccessException.class,
              () ->
                  renewal.run(
                      () ->
                          jdbc.update(
                              "UPDATE collab.msgnet_transport_history SET predecessor = '{}' WHERE tenant_id = ?",
                              tenant)));
      assertEquals(
          "55000",
          assertInstanceOf(java.sql.SQLException.class, immutable.getCause()).getSQLState());
      var control =
          (ObjectNode) NetworkJson.parse(renewal.publicControl()).path("payload").deepCopy();
      control.put("control_revision", "3");
      renewal.installControl(rootSigned(control, "NetworkRootControl"));
      var fresh = pki.confirmation(query);
      assertEquals("3", text(fresh.path("payload"), "control_revision"));
      renewal.run(
          () -> {
            assertEquals(
                "collab.msgnet_control_required",
                assertThrows(
                        AurigaException.class,
                        () ->
                            renewal
                                .transportVerifier()
                                .confirmation(
                                    evidence,
                                    initial,
                                    renewal.requiredState().path("authority"),
                                    renewal.requiredState().path("control"),
                                    text(query, "challenge"),
                                    wall.instant(),
                                    "loaded",
                                    expected))
                    .errorCode()
                    .code());
            return null;
          });
    }
  }

  @Test
  void transportFinishesOldCycleThenUsesExactRootAnchorForAuthorityHandover() throws Exception {
    try (ring) {
      install();
      var renewal = service();
      renewal.reconcile();
      var pki = pki(renewal);
      pki.reconcile();
      var old = pki.bundle();
      var replacement = KeyTestMaterial.pair();
      var a =
          (ObjectNode)
              renewal
                  .run(() -> renewal.requiredState().path("authority").path("payload"))
                  .deepCopy();
      a.put("authority_id", "replacement")
          .put("runtime_kid", "replacement-runtime")
          .put("public_key_spki", encode(replacement.getPublic().getEncoded()));
      var authority = rootSigned(a, "NetworkRuntimeAuthority");
      var c = (ObjectNode) NetworkJson.parse(renewal.publicControl()).path("payload").deepCopy();
      c.put("control_revision", "3");
      ((tools.jackson.databind.node.ArrayNode) c.path("allowed_authorities"))
          .addObject()
          .put("authority_id", "replacement")
          .put("authority_digest", digest(authority));
      renewal.install(
          authority, rootSigned(c, "NetworkRootControl"), replacement.getPrivate().getEncoded());
      renewal.reconcile();
      project(pki, "network", "synthetic-projector", "loaded");
      var confirmation = pki.confirmation(confirmationQuery(old));
      assertEquals(
          old.path("runtime_auth").path("kid"), confirmation.path("runtime_auth").path("kid"));
      assertEquals(
          old.path("payload").path("authority_digest"),
          confirmation.path("payload").path("authority_digest"));
      pki.reconcile();
      assertEquals(
          text(old.path("payload"), "authority_id"),
          text(pki.bundle().path("payload"), "authority_id"));
      project(pki, "network", "synthetic-projector", "handshake_verified");
      pki.reconcile();
      var retired = pki.bundle();
      assertEquals("retire_old", text(retired.path("payload"), "phase"));
      assertThrows(AurigaException.class, pki::reconcile);
      assertEquals(retired, pki.bundle());
      c.put("control_revision", "4");
      c.putObject("transport_transition")
          .put("mode", "authority_transition")
          .put("predecessor_digest", digest(retired))
          .put("from_generation", "1")
          .put("to_generation", "1")
          .put("authority_id", "replacement")
          .put("authority_digest", digest(authority))
          .put("first_bundle_version", "4");
      renewal.installControl(rootSigned(c, "NetworkRootControl"));
      renewal.reconcile();
      pki.reconcile();
      var next = pki.bundle();
      assertEquals("4", text(next.path("payload"), "bundle_version"));
      assertEquals("dual_trust", text(next.path("payload"), "phase"));
      for (var ca : retired.path("payload").path("cas"))
        assertTrue(list(next.path("payload").path("cas")).contains(ca));
      var proof = pki.transitionProof(digest(authority));
      assertEquals(next, proof.path("first_bundle"));
      assertEquals(retired, proof.path("predecessor").path("bundle"));
      renewal.run(
          () -> {
            renewal
                .transportVerifier()
                .transition(
                    next,
                    authority,
                    renewal.requiredState().path("control"),
                    proof.path("predecessor"),
                    new MsgnetTransportVerifier.SeenTransport("1", "3", digest(retired)),
                    false);
            return null;
          });
      pki.reconcile();
      assertEquals(next, pki.bundle());
      assertEquals(1, count("msgnet_transport_transition"));
    }
  }

  @Test
  void emergencyTransportRequiresCompletedIsolationAndKeepsOldPublicHistory() throws Exception {
    try (ring) {
      install();
      var renewal = service();
      renewal.reconcile();
      var pki = pki(renewal);
      pki.reconcile();
      var old = pki.bundle();
      var publication = NetworkJson.parse(renewal.publication(), 1048576);
      var replacement = KeyTestMaterial.pair();
      var a =
          (ObjectNode)
              renewal
                  .run(() -> renewal.requiredState().path("authority").path("payload"))
                  .deepCopy();
      String oldAuthority = text(a, "authority_id");
      a.put("authority_id", "recovery")
          .put("runtime_kid", "recovery-runtime")
          .put("public_key_spki", encode(replacement.getPublic().getEncoded()));
      var authority = rootSigned(a, "NetworkRuntimeAuthority");
      var c = (ObjectNode) NetworkJson.parse(renewal.publicControl()).path("payload").deepCopy();
      c.put("control_revision", "3")
          .put("minimum_generation", "2")
          .put("minimum_keyset_version", "1");
      c.putArray("allowed_authorities")
          .addObject()
          .put("authority_id", "recovery")
          .put("authority_digest", digest(authority));
      c.putArray("revoked_authority_ids").add(oldAuthority);
      c.putObject("generation_transition")
          .put("predecessor_digest", digest(publication.path("keyset")))
          .put("from_generation", "1")
          .put("to_generation", "2")
          .put("authority_id", "recovery")
          .put("authority_digest", digest(authority))
          .put("first_version", "1");
      c.putObject("transport_transition")
          .put("mode", "emergency_recovery")
          .put("predecessor_digest", digest(old))
          .put("from_generation", "1")
          .put("to_generation", "2")
          .put("authority_id", "recovery")
          .put("authority_digest", digest(authority))
          .put("first_bundle_version", "1");
      renewal.install(
          authority, rootSigned(c, "NetworkRootControl"), replacement.getPrivate().getEncoded());
      renewal.reconcile();
      assertEquals(
          "collab.msgnet_identity_quarantined",
          assertThrows(AurigaException.class, pki::reconcile).errorCode().code());
      var signer = KeyTestMaterial.pair();
      var pin =
          new MsgnetControlLedger.Identity(
              "target",
              "synthetic-projector",
              encode(signer.getPublic().getEncoded()),
              "network",
              "gateway",
              "",
              "CN=projector");
      var ledger = new MsgnetControlLedger(renewal, jdbc, tenant, "1", Map.of("target", pin));
      ledger.create(controlRequest("target"));
      var isolated = (ObjectNode) ledger.isolation("target").deepCopy();
      ledger.enforce(isolated);
      pki.reconcile();
      var current = pki.bundle();
      assertEquals("2", text(current.path("payload"), "generation"));
      assertEquals("1", text(current.path("payload"), "bundle_version"));
      for (var ca : current.path("payload").path("cas"))
        for (var previous : old.path("payload").path("cas"))
          assertNotEquals(text(ca, "certificate_der"), text(previous, "certificate_der"));
      assertEquals(old, pki.history(digest(old)).path("bundle"));
      assertEquals(0, pki.leaves(pin).size());
    }
  }

  ObjectNode nodePins(String node, String identity, String revision, KeyPair signer) {
    var source =
        MsgnetRenewal.object()
            .put("schema_version", 2)
            .put("tenant_id", tenant.toString())
            .put("realm", "1");
    source
        .putArray("pins")
        .addObject()
        .put("target_id", "node-target")
        .put("node_id", node)
        .put("identity_id", identity)
        .put("identity_revision", revision)
        .put("signing_spki", encode(signer.getPublic().getEncoded()));
    return source;
  }

  ObjectNode installationReceipt(JsonNode pack, String identity, KeyPair signer) throws Exception {
    var receipt = MsgnetRenewal.object();
    for (String field :
        java.util.List.of(
            "package_id", "tenant_id", "realm", "target_id", "challenge", "control_digest"))
      receipt.set(field, pack.path(field));
    receipt
        .put("identity_id", identity)
        .put("control_revision", text(pack.path("control").path("payload"), "control_revision"))
        .put("result", "installed")
        .put("persisted_at", wall.now.toString());
    identitySign(receipt, signer, identity, "NetworkControlReceipt");
    return receipt;
  }

  @Test
  void
      nodePinsBindAuthenticatedNodeAndRequireIndependentHigherRevisionToReplaceCompromisedIdentity()
          throws Exception {
    try (ring) {
      install();
      var renewal = service();
      renewal.reconcile();
      String node = UUID.randomUUID().toString();
      var signer = KeyTestMaterial.pair();
      var pins = nodePins(node, "node-identity", "1", signer);
      var entries = MsgnetNodePins.read(pins, tenant, "1");
      var ledger = new MsgnetControlLedger(renewal, jdbc, tenant, "1", Map.of(), entries);
      ledger.refreshTargets();
      var pack = ledger.pendingNode(node);
      var receipt = installationReceipt(pack, "node-identity", signer);
      assertEquals(
          "collab.msgnet_scope_denied",
          assertThrows(AurigaException.class, () -> ledger.confirm(receipt)).errorCode().code());
      assertEquals(
          "collab.msgnet_scope_denied",
          assertThrows(
                  AurigaException.class,
                  () -> ledger.confirmNode(UUID.randomUUID().toString(), "1", receipt))
              .errorCode()
              .code());
      assertEquals(
          "collab.msgnet_version_rollback",
          assertThrows(AurigaException.class, () -> ledger.confirmNode(node, "2", receipt))
              .errorCode()
              .code());
      assertEquals("confirmed", text(ledger.confirmNode(node, "1", receipt), "state"));
      assertFalse(ledger.permitted(tenant.toString(), "1", "node-identity", "node-target"));
      assertThrows(AurigaException.class, () -> ledger.identity("node-identity"));
      var isolated = (ObjectNode) ledger.isolation("node-target").deepCopy();
      isolated.put("state", "quarantined").put("identity_compromised", true);
      ledger.enforce(isolated);
      assertEquals(
          "collab.msgnet_identity_quarantined",
          assertThrows(AurigaException.class, () -> ledger.confirmNode(node, "1", receipt))
              .errorCode()
              .code());
      var replacement = KeyTestMaterial.pair();
      var nextPins = nodePins(node, "new-node-identity", "2", replacement);
      var next =
          new MsgnetControlLedger(
              renewal, jdbc, tenant, "1", Map.of(), MsgnetNodePins.read(nextPins, tenant, "1"));
      assertEquals(
          "collab.msgnet_identity_quarantined",
          assertThrows(AurigaException.class, next::refreshTargets).errorCode().code());
      var control =
          (ObjectNode) NetworkJson.parse(renewal.publicControl()).path("payload").deepCopy();
      control.put("control_revision", "3");
      ((tools.jackson.databind.node.ArrayNode) control.path("revoked_installation_ids"))
          .add("node-identity");
      renewal.installControl(rootSigned(control, "NetworkRootControl"));
      next.refreshTargets();
      assertEquals("quarantined", text(next.isolation("node-target"), "state"));
      var newReceipt =
          installationReceipt(next.pendingNode(node), "new-node-identity", replacement);
      assertEquals("confirmed", text(next.confirmNode(node, newReceipt), "state"));
      assertEquals(
          "collab.msgnet_version_rollback",
          assertThrows(AurigaException.class, ledger::refreshTargets).errorCode().code());
      var unknownFields = nextPins.deepCopy();
      ((ObjectNode) unknownFields.path("pins").get(0))
          .put("sealing_spki", encode(replacement.getPublic().getEncoded()));
      assertThrows(AurigaException.class, () -> MsgnetNodePins.read(unknownFields, tenant, "1"));
      var duplicate = nextPins.deepCopy();
      ((tools.jackson.databind.node.ArrayNode) duplicate.path("pins"))
          .add(duplicate.path("pins").get(0).deepCopy());
      assertThrows(AurigaException.class, () -> MsgnetNodePins.read(duplicate, tenant, "1"));
    }
  }

  @Test
  void pendingControlQueuePreservesOldChallengesAndOldReceiptCannotReadyLatestControl()
      throws Exception {
    try (ring) {
      install();
      var renewal = service();
      renewal.reconcile();
      var signer = KeyTestMaterial.pair();
      var pin =
          new MsgnetControlLedger.Identity(
              "target",
              "identity",
              encode(signer.getPublic().getEncoded()),
              "network",
              "gateway",
              "",
              "CN=projector");
      var ledger = new MsgnetControlLedger(renewal, jdbc, tenant, "1", Map.of("target", pin));
      ledger.refreshTargets();
      var first = ledger.pending("target");
      var control =
          (ObjectNode) NetworkJson.parse(renewal.publicControl()).path("payload").deepCopy();
      control.put("control_revision", "3");
      renewal.installControl(rootSigned(control, "NetworkRootControl"));
      ledger.refreshTargets();
      assertEquals(first, ledger.pending("target"));
      assertEquals(
          "quarantined",
          text(ledger.confirm(installationReceipt(first, "identity", signer)), "state"));
      var second = ledger.pending("target");
      assertNotEquals(first, second);
      var confirmed = ledger.confirm(installationReceipt(second, "identity", signer));
      assertEquals("confirmed", text(confirmed, "state"));
      assertThrows(AurigaException.class, () -> ledger.pending("target"));
      assertFalse(ledger.permitted(tenant.toString(), "1", "identity", "target"));
      assertEquals(2, count("msgnet_control_queue"));
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
      assertFalse(ledger.permitted(tenant.toString(), "1", pin.identityId(), pin.targetId()));
      ready(ledger, pin, signing);
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
      var waiting = NetworkJson.parse(service().status());
      assertEquals("degraded", text(waiting, "state"));
      assertEquals("collab.msgnet_version_rollback", text(waiting, "failure_code"));
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
