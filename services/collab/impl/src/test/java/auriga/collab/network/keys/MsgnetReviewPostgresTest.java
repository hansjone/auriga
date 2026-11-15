package auriga.collab.network.keys;

import static auriga.collab.network.keys.MsgnetV2Verifier.*;
import static org.junit.jupiter.api.Assertions.*;

import auriga.collab.app.NetworkJson;
import auriga.common.context.RequestContext;
import auriga.common.error.AurigaException;
import auriga.common.security.*;
import auriga.testsupport.PostgresExtension;
import java.nio.charset.StandardCharsets;
import java.security.KeyPair;
import java.security.Signature;
import java.util.Base64;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.concurrent.*;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.jdbc.core.RowMapper;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

@ExtendWith(PostgresExtension.class)
class MsgnetReviewPostgresTest {
  @BeforeAll
  static void migrate() throws Exception {
    MsgnetRenewalPostgresTest.migrate();
  }

  static final class Fixture implements AutoCloseable {
    final MsgnetRenewalPostgresTest base = new MsgnetRenewalPostgresTest();
    final KeyPair signing = KeyTestMaterial.pair();
    final KeyPair sealing;
    final MsgnetRenewal renewal;
    final MsgnetPki pki;
    final GateJdbc reads;
    final MsgnetControlLedger.Identity pin;
    final MsgnetControlLedger ledger;
    final MsgnetMaterials materials;

    Fixture() throws Exception {
      base.install();
      renewal = base.service();
      renewal.reconcile();
      pki = base.pki(renewal);
      pki.reconcile();
      sealing = java.security.KeyPairGenerator.getInstance("X25519").generateKeyPair();
      pin = pin("synthetic-projector", signing, sealing);
      reads = new GateJdbc(MsgnetRenewalPostgresTest.jdbc.getDataSource());
      ledger = ledger(pin);
      materials = materials(ledger);
    }

    MsgnetControlLedger.Identity pin(String id, KeyPair signer, KeyPair sealer) {
      return new MsgnetControlLedger.Identity(
          "target",
          id,
          encode(signer.getPublic().getEncoded()),
          "network",
          "gateway",
          encode(sealer.getPublic().getEncoded()),
          "CN=projector");
    }

    MsgnetControlLedger ledger(MsgnetControlLedger.Identity identity) {
      return new MsgnetControlLedger(renewal, reads, base.tenant, "1", Map.of("target", identity));
    }

    MsgnetMaterials materials(MsgnetControlLedger controls) {
      return new MsgnetMaterials(
          renewal,
          controls,
          new MsgnetPrivateKeyStore(
              MsgnetRenewalPostgresTest.jdbc,
              MsgnetRenewalPostgresTest.manager,
              base.tenant,
              "1",
              base.ring),
          MsgnetRenewalPostgresTest.jdbc,
          base.tenant,
          "1",
          pki);
    }

    ObjectNode confirmControl(
        MsgnetControlLedger controls, MsgnetControlLedger.Identity identity, KeyPair signer)
        throws Exception {
      var pack = controls.create(base.controlRequest("target"));
      var receipt = MsgnetRenewal.object();
      for (String field :
          List.of("package_id", "tenant_id", "realm", "target_id", "challenge", "control_digest"))
        receipt.set(field, pack.path(field));
      receipt
          .put("identity_id", identity.identityId())
          .put("control_revision", text(pack.path("control").path("payload"), "control_revision"))
          .put("result", "installed")
          .put("persisted_at", base.wall.now.toString());
      sign(receipt, signer, identity.identityId(), "NetworkControlReceipt");
      return controls.confirm(receipt);
    }

    void ready() throws Exception {
      var confirmed = confirmControl(ledger, pin, signing);
      ledger.enforce(confirmed.deepCopy().put("state", "ready").put("new_chain_ready", true));
    }

    ObjectNode request(MsgnetControlLedger.Identity identity, KeyPair signer) throws Exception {
      var payload = base.payload();
      var request =
          MsgnetRenewal.object()
              .put("tenant_id", base.tenant.toString())
              .put("realm", "1")
              .put("installation_id", identity.identityId())
              .put("component", "network")
              .put("gateway_id", "gateway")
              .put("nonce", encode(MsgnetKeyEncryptionTest.kek()))
              .put("expected_generation", text(payload, "generation"))
              .put("expected_keyset_version", text(payload, "keyset_version"));
      sign(request, signer, identity.identityId(), "NetworkProjectionRequest");
      return request;
    }

    JsonNode call(String entry, ObjectNode request) {
      var principal =
          new AurigaPrincipal(
              "svc:projector",
              base.tenant,
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
      return RequestContext.start("review", null)
          .withPrincipal(principal)
          .call(
              () ->
                  switch (entry) {
                    case "protocol" -> materials.protocol(request);
                    case "transport" -> materials.material(request);
                    case "sealed" -> materials.sealed(request);
                    default -> throw new AssertionError(entry);
                  });
    }

    JsonNode open(JsonNode sealed, KeyPair sealer) {
      byte[] bytes = MsgnetHpke.open(sealed, sealer.getPrivate(), sealer.getPublic());
      try {
        return NetworkJson.parse(new String(bytes, StandardCharsets.UTF_8), 1048576);
      } finally {
        java.util.Arrays.fill(bytes, (byte) 0);
      }
    }

    ObjectNode receipt(JsonNode material, String revision, String phase, KeyPair signer, String id)
        throws Exception {
      var snapshot =
          MsgnetProjectionBinding.snapshot(material, NetworkJson.parse(renewal.publication()));
      var manifest = snapshot.deepCopy().put("material_revision", revision);
      var selector = manifest.deepCopy();
      selector.remove("files");
      selector.put("manifest_digest", digest(manifest));
      var receipt = snapshot.deepCopy();
      receipt.remove("files");
      receipt.set("local_selector", selector);
      receipt.put("phase", phase).put("observed_at", base.wall.now.toString());
      sign(receipt, signer, id, "NetworkProjectionReceipt");
      return receipt;
    }

    @Override
    public void close() {
      base.ring.close();
    }
  }

  static final class GateJdbc extends JdbcTemplate {
    volatile CountDownLatch entered, release;

    GateJdbc(javax.sql.DataSource source) {
      super(source);
    }

    @Override
    public <T> List<T> query(String sql, RowMapper<T> mapper, Object... args) {
      var rows = super.query(sql, mapper, args);
      var gate = entered;
      if (gate != null && sql.startsWith("SELECT control_package, receipt, isolation_record")) {
        entered = null;
        gate.countDown();
        try {
          assertTrue(release.await(20, TimeUnit.SECONDS), "authorization gate timeout");
        } catch (InterruptedException e) {
          Thread.currentThread().interrupt();
          throw new AssertionError(e);
        }
      }
      return rows;
    }
  }

  @Test
  void cleanInstallationProjectsAndHandshakesBeforeBusinessReadiness() throws Exception {
    try (var f = new Fixture()) {
      assertEquals(
          "collab.msgnet_identity_quarantined",
          assertThrows(AurigaException.class, () -> f.materials.sealed(f.request(f.pin, f.signing)))
              .errorCode()
              .code());
      recover(f, f.ledger, f.materials, f.pin, f.signing, f.sealing);
    }
  }

  @Test
  void independentlyEnrolledReplacementRecoversAfterRootEventWhileOldIdentityStaysRevoked()
      throws Exception {
    try (var f = new Fixture()) {
      f.ready();
      var oldBundle = f.pki.bundle();
      var oldPublication = NetworkJson.parse(f.renewal.publication());
      var signer = KeyTestMaterial.pair();
      var sealer = java.security.KeyPairGenerator.getInstance("X25519").generateKeyPair();
      var pin = f.pin("replacement-projector", signer, sealer);
      var runtime = KeyTestMaterial.pair();
      var a =
          (ObjectNode)
              f.renewal
                  .run(() -> f.renewal.requiredState().path("authority").path("payload"))
                  .deepCopy();
      String oldAuthority = text(a, "authority_id");
      a.put("authority_id", "recovery")
          .put("runtime_kid", "recovery-runtime")
          .put("public_key_spki", encode(runtime.getPublic().getEncoded()));
      ((ObjectNode) a.path("transport_scope").get(0)).put("identity_id", pin.identityId());
      var authority = f.base.rootSigned(a, "NetworkRuntimeAuthority");
      var c = (ObjectNode) NetworkJson.parse(f.renewal.publicControl()).path("payload").deepCopy();
      c.put("control_revision", "3")
          .put("minimum_generation", "2")
          .put("minimum_keyset_version", "1");
      c.putArray("allowed_authorities")
          .addObject()
          .put("authority_id", "recovery")
          .put("authority_digest", digest(authority));
      c.putArray("revoked_authority_ids").add(oldAuthority);
      c.putArray("revoked_installation_ids").add(f.pin.identityId());
      c.putObject("generation_transition")
          .put("predecessor_digest", digest(oldPublication.path("keyset")))
          .put("from_generation", "1")
          .put("to_generation", "2")
          .put("authority_id", "recovery")
          .put("authority_digest", digest(authority))
          .put("first_version", "1");
      c.putObject("transport_transition")
          .put("mode", "emergency_recovery")
          .put("predecessor_digest", digest(oldBundle))
          .put("from_generation", "1")
          .put("to_generation", "2")
          .put("authority_id", "recovery")
          .put("authority_digest", digest(authority))
          .put("first_bundle_version", "1");
      f.renewal.install(
          authority, f.base.rootSigned(c, "NetworkRootControl"), runtime.getPrivate().getEncoded());
      f.renewal.reconcile();
      assertThrows(AurigaException.class, f.pki::reconcile);
      f.ledger.create(f.base.controlRequest("target"));
      var isolation = (ObjectNode) f.ledger.isolation("target").deepCopy();
      f.ledger.enforce(isolation.put("identity_compromised", true));
      f.pki.reconcile();
      assertEquals("2", text(f.pki.bundle().path("payload"), "generation"));
      var controls = f.ledger(pin);
      var materials = f.materials(controls);
      recover(f, controls, materials, pin, signer, sealer);
      for (String entry : List.of("protocol", "transport", "sealed"))
        assertEquals(
            "collab.msgnet_identity_quarantined",
            assertThrows(AurigaException.class, () -> f.call(entry, f.request(f.pin, f.signing)))
                .errorCode()
                .code());
      assertFalse(f.ledger.permitted(f.base.tenant.toString(), "1", f.pin.identityId(), "target"));
      assertEquals(oldBundle, f.pki.history(digest(oldBundle)).path("bundle"));
    }
  }

  private void recover(
      Fixture f,
      MsgnetControlLedger ledger,
      MsgnetMaterials materials,
      MsgnetControlLedger.Identity pin,
      KeyPair signer,
      KeyPair sealer)
      throws Exception {
    var confirmed = f.confirmControl(ledger, pin, signer);
    assertEquals("confirmed", text(confirmed, "state"));
    assertFalse(confirmed.path("new_chain_ready").asBoolean());
    assertFalse(ledger.permitted(f.base.tenant.toString(), "1", pin.identityId(), "target"));
    assertTrue(
        ledger.projectionPermitted(f.base.tenant.toString(), "1", pin.identityId(), "target"));
    var beans = new org.springframework.beans.factory.support.StaticListableBeanFactory();
    beans.addBean("controls", ledger);
    var access =
        new MsgnetRenewalConfiguration()
            .msgnetInstallationAccess(beans.getBeanProvider(MsgnetControlLedger.class));
    assertTrue(access.permitted(f.base.tenant.toString(), "1", pin.identityId(), "target"));
    assertFalse(access.permitted(f.base.tenant.toString(), "2", pin.identityId(), "target"));
    var request = f.request(pin, signer);
    assertTrue(
        ledger.claim(
            KeyTestMaterial.JSON.treeToValue(
                request, auriga.contracts.msgnet.model.ProjectionRequest.class)));
    var material = f.open(materials.sealed(request), sealer);
    materials.confirm(f.receipt(material, "1", "loaded", signer, pin.identityId()));
    assertFalse(ledger.permitted(f.base.tenant.toString(), "1", pin.identityId(), "target"));
    f.pki.reconcile();
    assertEquals("new_leaves", text(f.pki.bundle().path("payload"), "phase"));
    material = f.open(materials.sealed(f.request(pin, signer)), sealer);
    assertFalse(material.path("leaves").isEmpty());
    materials.confirm(f.receipt(material, "2", "loaded", signer, pin.identityId()));
    assertFalse(f.pki.confirmation(f.base.confirmationQuery(f.pki.bundle())).has("runtime_auth"));
    handshake(material);
    materials.confirm(f.receipt(material, "2", "handshake_verified", signer, pin.identityId()));
    assertTrue(f.pki.confirmation(f.base.confirmationQuery(f.pki.bundle())).has("runtime_auth"));
    assertFalse(ledger.permitted(f.base.tenant.toString(), "1", pin.identityId(), "target"));
    var ready = (ObjectNode) ledger.isolation("target").deepCopy();
    ledger.enforce(ready.put("state", "ready").put("new_chain_ready", true));
    assertTrue(ledger.permitted(f.base.tenant.toString(), "1", pin.identityId(), "target"));
    f.pki.reconcile();
    assertEquals("retire_old", text(f.pki.bundle().path("payload"), "phase"));
  }

  private void handshake(JsonNode material) throws Exception {
    var leaf = material.path("leaves").get(0);
    var ca =
        find(material.path("bundle").path("payload").path("cas"), "ca_id", text(leaf, "ca_id"));
    var factory = java.security.cert.CertificateFactory.getInstance("X.509");
    var certificate =
        factory.generateCertificate(
            new java.io.ByteArrayInputStream(
                Base64.getUrlDecoder().decode(text(leaf, "certificate_der"))));
    var issuer =
        factory.generateCertificate(
            new java.io.ByteArrayInputStream(
                Base64.getUrlDecoder().decode(text(ca, "certificate_der"))));
    var key =
        java.security.KeyFactory.getInstance("Ed25519")
            .generatePrivate(
                new java.security.spec.PKCS8EncodedKeySpec(
                    Base64.getUrlDecoder().decode(text(leaf, "private_pkcs8"))));
    var keys = java.security.KeyStore.getInstance("PKCS12");
    keys.load(null, null);
    keys.setKeyEntry(
        "identity", key, new char[0], new java.security.cert.Certificate[] {certificate, issuer});
    var km =
        javax.net.ssl.KeyManagerFactory.getInstance(
            javax.net.ssl.KeyManagerFactory.getDefaultAlgorithm());
    km.init(keys, new char[0]);
    var trust = java.security.KeyStore.getInstance("PKCS12");
    trust.load(null, null);
    trust.setCertificateEntry("ca", issuer);
    var tm =
        javax.net.ssl.TrustManagerFactory.getInstance(
            javax.net.ssl.TrustManagerFactory.getDefaultAlgorithm());
    tm.init(trust);
    var server = javax.net.ssl.SSLContext.getInstance("TLS");
    server.init(km.getKeyManagers(), null, null);
    var client = javax.net.ssl.SSLContext.getInstance("TLS");
    client.init(null, tm.getTrustManagers(), null);
    try (var listener =
            (javax.net.ssl.SSLServerSocket)
                server
                    .getServerSocketFactory()
                    .createServerSocket(0, 1, java.net.InetAddress.getLoopbackAddress());
        var workers = Executors.newVirtualThreadPerTaskExecutor()) {
      listener.setSoTimeout(10000);
      var accepted =
          workers.submit(
              () -> {
                try (var socket = (javax.net.ssl.SSLSocket) listener.accept()) {
                  socket.setSoTimeout(10000);
                  socket.startHandshake();
                }
                return true;
              });
      try (var connection =
              new java.net.Socket(
                  java.net.InetAddress.getLoopbackAddress(), listener.getLocalPort());
          var socket =
              (javax.net.ssl.SSLSocket)
                  client
                      .getSocketFactory()
                      .createSocket(
                          connection, "submit.example.com", listener.getLocalPort(), true)) {
        var parameters = socket.getSSLParameters();
        parameters.setEndpointIdentificationAlgorithm("HTTPS");
        parameters.setServerNames(List.of(new javax.net.ssl.SNIHostName("submit.example.com")));
        socket.setSSLParameters(parameters);
        socket.setSoTimeout(10000);
        socket.startHandshake();
        assertEquals(certificate, socket.getSession().getPeerCertificates()[0]);
      }
      accepted.get(15, TimeUnit.SECONDS);
    }
  }

  @Test
  void committedRevocationWinsAllThreeMaterialRacesWithoutConsumingNonce() throws Exception {
    for (String entry : List.of("protocol", "transport", "sealed")) {
      try (var f = new Fixture();
          var workers = Executors.newVirtualThreadPerTaskExecutor()) {
        f.ready();
        var legal = f.request(f.pin, f.signing);
        var material = f.call(entry, legal);
        schema(
            "protocol".equals(entry)
                ? "ProtocolMaterial"
                : "transport".equals(entry) ? "ProjectionMaterial" : "SealedMaterial",
            material,
            "invalid_chain");
        assertEquals(
            "collab.msgnet_invalid_transition",
            assertThrows(AurigaException.class, () -> f.call(entry, legal)).errorCode().code());
        var racing = f.request(f.pin, f.signing);
        int nonces = f.base.count("msgnet_projection_nonce");
        f.reads.entered = new CountDownLatch(1);
        f.reads.release = new CountDownLatch(1);
        var gate = f.reads.entered;
        var denied =
            workers.submit(() -> assertThrows(AurigaException.class, () -> f.call(entry, racing)));
        try {
          assertTrue(gate.await(20, TimeUnit.SECONDS));
          var changed =
              (ObjectNode) NetworkJson.parse(f.renewal.publicControl()).path("payload").deepCopy();
          changed.put("control_revision", "3");
          ((tools.jackson.databind.node.ArrayNode) changed.path("revoked_installation_ids"))
              .add(f.pin.identityId());
          f.renewal.installControl(f.base.rootSigned(changed, "NetworkRootControl"));
        } finally {
          f.reads.release.countDown();
        }
        assertEquals(
            "collab.msgnet_identity_quarantined",
            denied.get(30, TimeUnit.SECONDS).errorCode().code());
        assertEquals(nonces, f.base.count("msgnet_projection_nonce"));
        assertEquals(1, f.base.count("msgnet_transport_selector"));
        assertFalse(
            f.ledger.projectionPermitted(
                f.base.tenant.toString(), "1", f.pin.identityId(), "target"));
      }
    }
  }

  @Test
  void refreshAfterPersistedUsesNewBundleAndKeepsOldSelectorImmutable() throws Exception {
    try (var f = new Fixture()) {
      f.ready();
      var old = f.pki.bundle();
      var material = f.open(f.materials.sealed(f.request(f.pin, f.signing)), f.sealing);
      var persisted = f.receipt(material, "1", "persisted", f.signing, f.pin.identityId());
      f.materials.confirm(persisted);
      var expected = f.pki.expectedSelectors(f.base.expectedQuery(old));
      f.base.advance(13 * 3600);
      f.renewal.reconcile();
      var late = persisted.deepCopy();
      late.remove("identity_auth");
      late.put("phase", "loaded");
      sign(late, f.signing, f.pin.identityId(), "NetworkProjectionReceipt");
      assertEquals(
          "collab.msgnet_invalid_transition",
          assertThrows(AurigaException.class, () -> f.materials.confirm(late)).errorCode().code());
      f.pki.reconcile();
      var current = f.pki.bundle();
      assertEquals("dual_trust", text(current.path("payload"), "phase"));
      assertEquals("2", text(current.path("payload"), "bundle_version"));
      assertEquals(
          expected.path("expected_selectors"),
          f.pki.expectedSelectors(f.base.expectedQuery(old)).path("expected_selectors"));
      var fresh = f.open(f.materials.sealed(f.request(f.pin, f.signing)), f.sealing);
      f.materials.confirm(f.receipt(fresh, "2", "loaded", f.signing, f.pin.identityId()));
      assertTrue(f.pki.confirmation(f.base.confirmationQuery(current)).has("runtime_auth"));
      f.pki.reconcile();
      assertEquals("new_leaves", text(f.pki.bundle().path("payload"), "phase"));
      assertEquals(digest(current), text(f.pki.bundle().path("payload"), "previous_bundle_digest"));
      assertEquals(old, f.pki.history(digest(old)).path("bundle"));
    }
  }

  @Test
  void renewedLeafRetriesSamePhaseWithCompletedDualTrustPredecessor() throws Exception {
    try (var f = new Fixture()) {
      f.ready();
      var dual = f.pki.bundle();
      var initial = f.open(f.materials.sealed(f.request(f.pin, f.signing)), f.sealing);
      f.materials.confirm(f.receipt(initial, "1", "loaded", f.signing, f.pin.identityId()));
      f.pki.reconcile();
      var waiting = f.pki.bundle();
      assertEquals("new_leaves", text(waiting.path("payload"), "phase"));
      var oldMaterial = f.open(f.materials.sealed(f.request(f.pin, f.signing)), f.sealing);
      var persisted = f.receipt(oldMaterial, "2", "persisted", f.signing, f.pin.identityId());
      var original = persisted.path("local_selector");
      f.materials.confirm(persisted);
      var frozen = f.pki.expectedSelectors(f.base.expectedQuery(waiting));
      f.base.advance(61 * 86400L);
      f.renewal.reconcile();
      f.pki.reconcile();
      var replacement = f.pki.bundle();
      assertEquals("new_leaves", text(replacement.path("payload"), "phase"));
      assertEquals("3", text(replacement.path("payload"), "bundle_version"));
      assertEquals(digest(dual), text(replacement.path("payload"), "previous_bundle_digest"));
      assertEquals(
          frozen.path("expected_selectors"),
          f.pki.expectedSelectors(f.base.expectedQuery(waiting)).path("expected_selectors"));
      var late = persisted.deepCopy();
      late.remove("identity_auth");
      late.put("phase", "loaded");
      sign(late, f.signing, f.pin.identityId(), "NetworkProjectionReceipt");
      assertEquals(
          "collab.msgnet_invalid_transition",
          assertThrows(AurigaException.class, () -> f.materials.confirm(late)).errorCode().code());
      var material = f.open(f.materials.sealed(f.request(f.pin, f.signing)), f.sealing);
      f.materials.confirm(f.receipt(material, "3", "loaded", f.signing, f.pin.identityId()));
      var handshaken =
          f.receipt(material, "3", "handshake_verified", f.signing, f.pin.identityId());
      f.materials.confirm(handshaken);
      var renewed = handshaken.path("local_selector");
      assertNotEquals(original.path("nonce"), renewed.path("nonce"));
      var prior = f.pki.confirmation(f.base.confirmationQuery(dual));
      assertTrue(prior.has("runtime_auth"));
      var proof = f.pki.confirmation(f.base.confirmationQuery(replacement));
      assertTrue(proof.has("runtime_auth"));
      var approved = f.pki.expectedSelectors(f.base.expectedQuery(replacement));
      assertEquals(digest(renewed), text(approved.path("expected_selectors"), f.pin.identityId()));
      f.pki.reconcile();
      assertEquals("retire_old", text(f.pki.bundle().path("payload"), "phase"));
      assertEquals(
          digest(replacement), text(f.pki.bundle().path("payload"), "previous_bundle_digest"));
    }
  }

  static String encode(byte[] bytes) {
    return Base64.getUrlEncoder().withoutPadding().encodeToString(bytes);
  }

  static void sign(ObjectNode value, KeyPair pair, String kid, String domain) throws Exception {
    var signer = Signature.getInstance("Ed25519");
    signer.initSign(pair.getPrivate());
    signer.update(
        ("Auriga." + domain + ".v2\0" + MsgnetRenewal.canonical(value))
            .getBytes(StandardCharsets.UTF_8));
    value
        .putObject("identity_auth")
        .put("alg", "Ed25519")
        .put("kid", kid)
        .put("signature", encode(signer.sign()));
  }
}
