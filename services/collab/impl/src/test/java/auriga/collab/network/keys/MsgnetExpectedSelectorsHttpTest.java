package auriga.collab.network.keys;

import static auriga.collab.network.keys.MsgnetV2Verifier.*;
import static org.junit.jupiter.api.Assertions.*;

import auriga.collab.api.NetworkRenewal;
import auriga.collab.app.NetworkJson;
import auriga.testsupport.PostgresExtension;
import com.nimbusds.jose.*;
import com.nimbusds.jose.crypto.ECDSASigner;
import com.nimbusds.jose.jwk.*;
import com.nimbusds.jose.jwk.gen.ECKeyGenerator;
import com.nimbusds.jwt.*;
import com.sun.net.httpserver.HttpServer;
import java.net.*;
import java.net.http.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.security.KeyStore;
import java.time.Instant;
import java.util.*;
import javax.net.ssl.*;
import org.junit.jupiter.api.AfterAll;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.beans.factory.annotation.Value;
import org.springframework.boot.SpringBootConfiguration;
import org.springframework.boot.autoconfigure.EnableAutoConfiguration;
import org.springframework.boot.jdbc.autoconfigure.DataSourceAutoConfiguration;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Import;
import org.springframework.test.annotation.DirtiesContext;
import org.springframework.test.context.DynamicPropertyRegistry;
import org.springframework.test.context.DynamicPropertySource;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** Real random HTTPS port, verified client certificate, JWT permission checks and runtime PG. */
@ExtendWith(PostgresExtension.class)
@SpringBootTest(
    classes = MsgnetExpectedSelectorsHttpTest.App.class,
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT)
@DirtiesContext
class MsgnetExpectedSelectorsHttpTest {
  private static final String PREFIX = "/api/collab/v2/network/transport/";
  private static Fixture fixture;

  @Value("${local.server.port}")
  int port;

  static synchronized Fixture fixture() {
    if (fixture == null) {
      try {
        fixture = new Fixture();
      } catch (Exception e) {
        throw new IllegalStateException(e);
      }
    }
    return fixture;
  }

  @DynamicPropertySource
  static void properties(DynamicPropertyRegistry registry) {
    var f = fixture();
    registry.add("auriga.service.schema", () -> "collab");
    registry.add("auriga.collab.network.auto-renew.enabled", () -> "true");
    registry.add("auriga.security.jwt.issuer", () -> "https://auriga.example.com");
    registry.add(
        "auriga.security.jwt.jwk-set-uri",
        () -> "http://127.0.0.1:" + f.jwks.getAddress().getPort() + "/jwks");
    registry.add("server.ssl.enabled", () -> "true");
    registry.add("server.ssl.key-store", () -> f.serverFile.toUri().toString());
    registry.add("server.ssl.key-store-type", () -> "PKCS12");
    registry.add("server.ssl.key-store-password", () -> f.password);
    registry.add("server.ssl.trust-store", () -> f.trustFile.toUri().toString());
    registry.add("server.ssl.trust-store-type", () -> "PKCS12");
    registry.add("server.ssl.trust-store-password", () -> f.password);
    registry.add("server.ssl.client-auth", () -> "want");
  }

  @AfterAll
  static void stop() throws Exception {
    if (fixture != null) fixture.close();
  }

  @SpringBootConfiguration
  @EnableAutoConfiguration(exclude = DataSourceAutoConfiguration.class)
  @Import(MsgnetRenewalController.class)
  static class App {
    @Bean
    MsgnetRenewal renewal() {
      return fixture().renewal;
    }

    @Bean
    MsgnetPki pki() {
      return fixture().pki;
    }

    @Bean
    MsgnetControlLedger controls() {
      return fixture().controls;
    }

    @Bean
    NetworkRenewal api() {
      return fixture().api;
    }
  }

  static final class Fixture implements AutoCloseable {
    final MsgnetRenewalPostgresTest base = new MsgnetRenewalPostgresTest();
    final MsgnetRenewal renewal;
    final MsgnetPki pki;
    final MsgnetControlLedger controls;
    final NetworkRenewal api;
    final ECKey jwtKey =
        new ECKeyGenerator(Curve.P_256).keyID(UUID.randomUUID().toString()).generate();
    final HttpServer jwks;
    final Path serverFile, trustFile;
    final String password = UUID.randomUUID().toString();
    final SSLContext client, noCertificate;
    final FaultJdbc reads;

    Fixture() throws Exception {
      MsgnetRenewalPostgresTest.migrate();
      base.install();
      renewal = base.service();
      renewal.reconcile();
      reads = new FaultJdbc(MsgnetRenewalPostgresTest.jdbc.getDataSource());
      var config =
          MsgnetRenewalPostgresTest.template
              .path("boundary_cases")
              .valueStream()
              .filter(v -> "config-defaults".equals(text(v, "id")))
              .findFirst()
              .orElseThrow()
              .path("input")
              .path("config");
      pki =
          new MsgnetPki(
              renewal,
              new MsgnetPrivateKeyStore(
                  MsgnetRenewalPostgresTest.jdbc,
                  MsgnetRenewalPostgresTest.manager,
                  base.tenant,
                  "1",
                  base.ring),
              reads,
              base.tenant,
              "1",
              config);
      pki.reconcile();
      var signer = KeyTestMaterial.pair();
      var pin =
          new MsgnetControlLedger.Identity(
              "target",
              "synthetic-projector",
              Base64.getUrlEncoder()
                  .withoutPadding()
                  .encodeToString(signer.getPublic().getEncoded()),
              "network",
              "gateway",
              "",
              "CN=projector");
      controls =
          new MsgnetControlLedger(
              renewal, MsgnetRenewalPostgresTest.jdbc, base.tenant, "1", Map.of("target", pin));
      api =
          new MsgnetNetworkRenewal(
              renewal,
              controls,
              new MsgnetMaterials(
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
                  pki));
      jwks = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
      jwks.createContext(
          "/jwks",
          exchange -> {
            byte[] body =
                new JWKSet(jwtKey.toPublicJWK()).toString().getBytes(StandardCharsets.UTF_8);
            exchange.getResponseHeaders().add("Content-Type", "application/json");
            exchange.sendResponseHeaders(200, body.length);
            exchange.getResponseBody().write(body);
            exchange.close();
          });
      jwks.start();
      Instant now = Instant.now();
      var ca =
          MsgnetCertificates.issue(
              "test-ca",
              now.minusSeconds(60),
              now.plusSeconds(86400),
              List.of(),
              List.of(),
              null,
              null);
      var server =
          MsgnetCertificates.issue(
              "localhost",
              now.minusSeconds(30),
              now.plusSeconds(3600),
              List.of("127.0.0.1"),
              List.of("serverAuth"),
              ca.certificate(),
              ca.pair().getPrivate());
      var peer =
          MsgnetCertificates.issue(
              "projector",
              now.minusSeconds(30),
              now.plusSeconds(3600),
              List.of("localhost"),
              List.of("clientAuth"),
              ca.certificate(),
              ca.pair().getPrivate());
      var trust = KeyStore.getInstance("PKCS12");
      trust.load(null, null);
      trust.setCertificateEntry("ca", ca.certificate());
      var keys = KeyStore.getInstance("PKCS12");
      keys.load(null, null);
      keys.setKeyEntry(
          "server",
          server.pair().getPrivate(),
          password.toCharArray(),
          new java.security.cert.Certificate[] {server.certificate(), ca.certificate()});
      serverFile = protectedStore(keys, "server");
      trustFile = protectedStore(trust, "trust");
      var peers = KeyStore.getInstance("PKCS12");
      peers.load(null, null);
      peers.setKeyEntry(
          "peer",
          peer.pair().getPrivate(),
          password.toCharArray(),
          new java.security.cert.Certificate[] {peer.certificate(), ca.certificate()});
      var km = KeyManagerFactory.getInstance(KeyManagerFactory.getDefaultAlgorithm());
      km.init(peers, password.toCharArray());
      var tm = TrustManagerFactory.getInstance(TrustManagerFactory.getDefaultAlgorithm());
      tm.init(trust);
      client = SSLContext.getInstance("TLS");
      client.init(km.getKeyManagers(), tm.getTrustManagers(), null);
      noCertificate = SSLContext.getInstance("TLS");
      noCertificate.init(new KeyManager[0], tm.getTrustManagers(), null);
    }

    Path protectedStore(KeyStore store, String name) throws Exception {
      var bytes = new java.io.ByteArrayOutputStream();
      store.store(bytes, password.toCharArray());
      Path path =
          Path.of(".dev/msgnet-c")
              .toAbsolutePath()
              .resolve("k4-http-" + name + "-" + UUID.randomUUID() + ".p12");
      Files.createDirectories(path.getParent());
      return NetworkKeyConfigurationTest.protectedFile(path, bytes.toByteArray());
    }

    String token(UUID tenant, boolean permission) throws Exception {
      Instant now = Instant.now();
      var claims =
          new JWTClaimsSet.Builder()
              .issuer("https://auriga.example.com")
              .subject(UUID.randomUUID().toString())
              .audience("auriga")
              .issueTime(Date.from(now))
              .notBeforeTime(Date.from(now))
              .expirationTime(Date.from(now.plusSeconds(300)))
              .jwtID(UUID.randomUUID().toString())
              .claim("tid", tenant.toString())
              .claim("ptyp", "user")
              .claim("ctx", "portal")
              .claim("sid", UUID.randomUUID().toString())
              .claim("roles", List.of("member"))
              .claim("perms", permission ? List.of("collab.msgnet.read") : List.of())
              .build();
      var signed =
          new SignedJWT(
              new JWSHeader.Builder(JWSAlgorithm.ES256)
                  .type(new JOSEObjectType("at+jwt"))
                  .keyID(jwtKey.getKeyID())
                  .build(),
              claims);
      signed.sign(new ECDSASigner(jwtKey));
      return signed.serialize();
    }

    @Override
    public void close() throws Exception {
      jwks.stop(0);
      base.ring.close();
      Files.deleteIfExists(serverFile);
      Files.deleteIfExists(trustFile);
    }
  }

  static final class FaultJdbc extends org.springframework.jdbc.core.JdbcTemplate {
    volatile boolean unavailable;
    volatile java.util.concurrent.CountDownLatch entered, release;

    FaultJdbc(javax.sql.DataSource source) {
      super(source);
    }

    @Override
    public List<Map<String, Object>> queryForList(String sql, Object... args) {
      if (sql.startsWith("SELECT reader_id")) {
        if (unavailable)
          throw new org.springframework.dao.DataAccessResourceFailureException(
              "test ledger unavailable");
        var gate = entered;
        if (gate != null) {
          entered = null;
          gate.countDown();
          try {
            if (!release.await(15, java.util.concurrent.TimeUnit.SECONDS))
              throw new AssertionError("snapshot gate timeout");
          } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
            throw new AssertionError(e);
          }
        }
      }
      return super.queryForList(sql, args);
    }
  }

  HttpResponse<String> post(String endpoint, JsonNode query, String token, SSLContext ssl)
      throws Exception {
    var request =
        HttpRequest.newBuilder(URI.create("https://127.0.0.1:" + port + PREFIX + endpoint))
            .timeout(java.time.Duration.ofSeconds(30))
            .header("Content-Type", "application/json")
            .POST(HttpRequest.BodyPublishers.ofString(MsgnetRenewal.canonical(query)));
    if (token != null) request.header("Authorization", "Bearer " + token);
    try (var client = HttpClient.newBuilder().sslContext(ssl).build()) {
      return client.send(request.build(), HttpResponse.BodyHandlers.ofString());
    }
  }

  @Test
  void authenticatedReadOnlyPortKeepsNullDistinctThenBindsExactMaterialsAndFreshControl()
      throws Exception {
    var f = fixture();
    var base = f.base;
    var bundle = f.pki.bundle();
    var control = NetworkJson.parse(f.renewal.publicControl());
    var query =
        base.confirmationQuery(bundle)
            .put("control_revision", text(control.path("payload"), "control_revision"))
            .put("control_digest", digest(control));
    String token = f.token(base.tenant, true);
    assertEquals(401, post("expected-selectors", query, null, f.client).statusCode());
    assertEquals(
        403, post("expected-selectors", query, f.token(base.tenant, false), f.client).statusCode());
    assertEquals(
        403,
        post("expected-selectors", query, f.token(UUID.randomUUID(), true), f.client).statusCode());
    assertEquals(
        403,
        post("expected-selectors", query.deepCopy().put("realm", "9"), token, f.client)
            .statusCode());
    assertEquals(403, post("expected-selectors", query, token, f.noCertificate).statusCode());
    int nonces = base.count("msgnet_projection_nonce"),
        approvals = base.count("msgnet_transport_selector"),
        history = base.count("msgnet_transport_history");
    var response = post("expected-selectors", query, token, f.client);
    assertEquals(200, response.statusCode());
    assertTrue(response.headers().firstValue("Cache-Control").orElseThrow().contains("no-store"));
    var expected = NetworkJson.parse(response.body(), 1048576);
    var unassigned =
        MsgnetExpectedSelectors.verify(
            expected,
            query,
            bundle,
            control,
            base.tenant.toString(),
            "1",
            base.wall.instant(),
            true);
    assertTrue(expected.path("expected_selectors").has("synthetic-projector"));
    assertTrue(expected.path("expected_selectors").path("synthetic-projector").isNull());
    var cq = base.confirmationQuery(bundle);
    var pending = post("confirmation", cq, token, f.client);
    assertEquals(202, pending.statusCode());
    var body = NetworkJson.parse(pending.body());
    schema("TransportConfirmationPending", body, "invalid_chain");
    assertFalse(body.has("runtime_auth"));
    assertEquals("pending", text(body.path("readers").get(0), "phase"));
    assertEquals(nonces, base.count("msgnet_projection_nonce"));
    assertEquals(approvals, base.count("msgnet_transport_selector"));
    assertEquals(history, base.count("msgnet_transport_history"));
    assertEquals(
        409,
        post(
                "expected-selectors",
                query.deepCopy().put("control_digest", "sha256:" + "0".repeat(64)),
                token,
                f.client)
            .statusCode());
    assertEquals(
        409,
        post(
                "expected-selectors",
                query.deepCopy().put("bundle_digest", "sha256:" + "0".repeat(64)),
                token,
                f.client)
            .statusCode());
    assertEquals(
        404,
        post(
                "expected-selectors",
                query
                    .deepCopy()
                    .put("bundle_version", "999")
                    .put("bundle_digest", "sha256:" + "0".repeat(64)),
                token,
                f.client)
            .statusCode());
    assertEquals(
        400,
        post(
                "expected-selectors",
                query.deepCopy().put("authenticated_c_ledger", true),
                token,
                f.client)
            .statusCode());
    f.reads.unavailable = true;
    try {
      assertEquals(503, post("expected-selectors", query, token, f.client).statusCode());
    } finally {
      f.reads.unavailable = false;
    }
    var selector = base.project(f.pki, "network", "synthetic-projector", "persisted");
    expected = NetworkJson.parse(post("expected-selectors", query, token, f.client).body());
    assertEquals(
        digest(selector), text(expected.path("expected_selectors"), "synthetic-projector"));
    assertNotEquals(
        text(selector, "manifest_digest"),
        text(expected.path("expected_selectors"), "synthetic-projector"));
    assertEquals(202, post("confirmation", cq, token, f.client).statusCode());
    base.project(f.pki, "network", "synthetic-projector", "loaded");
    var completed = post("confirmation", cq, token, f.client);
    assertEquals(200, completed.statusCode());
    var signed = NetworkJson.parse(completed.body());
    var authority = f.renewal.run(() -> f.renewal.requiredState().path("authority"));
    base.projectedReceipts.clear();
    var refreshed = base.project(f.pki, "network", "synthetic-projector", "loaded");
    assertNotEquals(selector, refreshed);
    var unchanged = NetworkJson.parse(post("expected-selectors", query, token, f.client).body());
    assertEquals(expected.path("expected_selectors"), unchanged.path("expected_selectors"));
    assertThrows(
        auriga.common.error.AurigaException.class,
        () ->
            unassigned.confirmation(
                f.renewal.transportVerifier(),
                signed,
                authority,
                control,
                text(cq, "challenge"),
                base.wall.instant(),
                "loaded"));
    var verified =
        MsgnetExpectedSelectors.verify(
            expected,
            query,
            bundle,
            control,
            base.tenant.toString(),
            "1",
            base.wall.instant(),
            true);
    f.renewal
        .transportVerifier()
        .confirmation(
            signed,
            authority,
            control,
            text(cq, "challenge"),
            base.wall.instant(),
            "loaded",
            verified);
    assertThrows(
        auriga.common.error.AurigaException.class,
        () ->
            f.renewal
                .transportVerifier()
                .confirmation(
                    signed,
                    authority,
                    control,
                    text(cq, "challenge"),
                    base.wall.instant(),
                    "loaded",
                    (MsgnetExpectedSelectors.Snapshot) null));
    var changed = (ObjectNode) control.path("payload").deepCopy();
    changed.put("control_revision", "3");
    var nextControl = base.rootSigned(changed, "NetworkRootControl");
    try (var pool = java.util.concurrent.Executors.newFixedThreadPool(2)) {
      f.reads.entered = new java.util.concurrent.CountDownLatch(1);
      f.reads.release = new java.util.concurrent.CountDownLatch(1);
      var entered = f.reads.entered;
      var inFlight = pool.submit(() -> post("expected-selectors", query, token, f.client));
      assertTrue(entered.await(15, java.util.concurrent.TimeUnit.SECONDS));
      try {
        var writer = pool.submit(() -> f.renewal.installControl(nextControl));
        f.reads.release.countDown();
        var consistent = inFlight.get(30, java.util.concurrent.TimeUnit.SECONDS);
        assertEquals(200, consistent.statusCode());
        assertEquals(digest(control), text(NetworkJson.parse(consistent.body()), "control_digest"));
        writer.get(30, java.util.concurrent.TimeUnit.SECONDS);
      } finally {
        f.reads.release.countDown();
      }
    }
    assertEquals(409, post("expected-selectors", query, token, f.client).statusCode());
    var latest = NetworkJson.parse(f.renewal.publicControl());
    assertThrows(
        auriga.common.error.AurigaException.class,
        () ->
            verified.confirmation(
                f.renewal.transportVerifier(),
                signed,
                authority,
                latest,
                text(cq, "challenge"),
                base.wall.instant(),
                "loaded"));
  }
}
