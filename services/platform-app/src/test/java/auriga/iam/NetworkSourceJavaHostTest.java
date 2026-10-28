package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.collab.api.NetworkSourceAuthority.Target;
import auriga.collab.grpc.NetworkAuthorityClient;
import auriga.collab.network.keys.NetworkKeySettings;
import auriga.common.context.TenantContext;
import auriga.common.persistence.TenantTransactionManager;
import auriga.contracts.iam.v1.NetworkAuthorityConnection;
import com.nimbusds.jose.jwk.*;
import com.nimbusds.jose.jwk.gen.ECKeyGenerator;
import java.net.URI;
import java.net.http.*;
import java.nio.file.*;
import java.nio.file.attribute.PosixFilePermissions;
import java.security.*;
import java.security.spec.PKCS8EncodedKeySpec;
import java.security.spec.X509EncodedKeySpec;
import java.sql.Connection;
import java.sql.SQLException;
import java.time.Duration;
import java.time.Instant;
import java.util.*;
import java.util.concurrent.TimeUnit;
import javax.net.ssl.SSLHandshakeException;
import org.junit.jupiter.api.Test;
import org.springframework.jdbc.datasource.AbstractDataSource;
import org.springframework.transaction.CannotCreateTransactionException;

class NetworkSourceJavaHostTest {
  @Test
  void dedicatedEdgeHopHasClientEkuAndMatchingPrivateKey() throws Exception {
    var host = new NetworkSourceJavaHost();
    Path certPath;
    try (host) {
      host.directory = NetworkSourceJavaHost.privateDirectory();
      var tls = new NetworkSourceJavaTls(host.directory);
      certPath = tls.edgeCert;
      var factory = java.security.cert.CertificateFactory.getInstance("X.509");
      java.security.cert.X509Certificate cert, ca;
      try (var input = Files.newInputStream(tls.edgeCert)) {
        cert = (java.security.cert.X509Certificate) factory.generateCertificate(input);
      }
      try (var input = Files.newInputStream(tls.ca)) {
        ca = (java.security.cert.X509Certificate) factory.generateCertificate(input);
      }
      cert.verify(ca.getPublicKey());
      assertEquals(List.of("1.3.6.1.5.5.7.3.2"), cert.getExtendedKeyUsage());
      var key =
          KeyFactory.getInstance("EC")
              .generatePrivate(
                  new PKCS8EncodedKeySpec(
                      Base64.getMimeDecoder()
                          .decode(
                              Files.readString(tls.edgeKey)
                                  .replace(NetworkSourceJavaHost.PRIVATE_KEY_BEGIN, "")
                                  .replace("-----END " + "PRIVATE KEY-----", ""))));
      var signature = Signature.getInstance("SHA256withECDSA");
      signature.initSign(key);
      signature.update(new byte[] {1, 2, 3});
      var proof = signature.sign();
      signature.initVerify(cert);
      signature.update(new byte[] {1, 2, 3});
      assertTrue(signature.verify(proof));
    }
    assertFalse(Files.exists(certPath));
  }

  @Test
  void ordinaryMailboxTargetUsesDurableLocalDirectory() {
    // No SQL rows or permissions are mocked. The real directory must reach the
    // JDBC boundary; the probe refuses a connection before any PG work starts.
    var data =
        new AbstractDataSource() {
          @Override
          public Connection getConnection() throws SQLException {
            throw new SQLException("durable local directory connection probe");
          }

          @Override
          public Connection getConnection(String username, String password) throws SQLException {
            return getConnection();
          }
        };
    var authority =
        NetworkSourceJavaHost.mailboxAuthority(
            mock(NetworkAuthorityClient.class), new TenantTransactionManager(data));
    var tenant = UUID.randomUUID();
    var connection =
        NetworkAuthorityConnection.newBuilder()
            .setTenantId(tenant.toString())
            .setRealm("1")
            .setNode("2")
            .build();
    var target =
        new Target("1", "2", "incarnation", 1, "nonce", "fetch", Instant.now().plusSeconds(30));
    TenantContext.run(
        tenant,
        () ->
            authority.withTarget(
                connection,
                target,
                () -> {
                  assertEquals("system", authority.localSnapshot(tenant, "1", "2", "0").type());
                  var failure =
                      assertThrows(
                          CannotCreateTransactionException.class,
                          () -> authority.localSnapshot(tenant, "1", "2", "1"));
                  assertEquals(
                      "durable local directory connection probe",
                      failure.getMostSpecificCause().getMessage());
                  return null;
                }));
  }

  static void assertPublishedProtocolKey(
      tools.jackson.databind.JsonNode config,
      String pem,
      tools.jackson.databind.JsonNode publication)
      throws Exception {
    var metadata = config.path("protocolSigning");
    var keyset = publication.path("keyset");
    assertEquals(keyset.path("keyset_version"), metadata.path("keysetVersion"));
    tools.jackson.databind.JsonNode selected = null;
    for (var key : keyset.path("keys"))
      if (key.path("kid").asString().equals(metadata.path("keyId").asString())) selected = key;
    assertNotNull(selected);
    assertEquals("protocol_response", selected.path("purpose").asString());
    assertEquals(selected.path("not_before"), metadata.path("notBefore"));
    assertEquals(selected.path("not_after"), metadata.path("notAfter"));
    var factory = KeyFactory.getInstance("Ed25519");
    var privateKey =
        factory.generatePrivate(
            new PKCS8EncodedKeySpec(
                Base64.getMimeDecoder()
                    .decode(
                        pem.replace(NetworkSourceJavaHost.PRIVATE_KEY_BEGIN, "")
                            .replace("-----END PRIVATE KEY-----", ""))));
    var publicKey =
        factory.generatePublic(
            new X509EncodedKeySpec(
                Base64.getUrlDecoder().decode(selected.path("public_key_spki").asString())));
    var signature = Signature.getInstance("Ed25519");
    signature.initSign(privateKey);
    signature.update(new byte[] {4, 5, 6});
    var proof = signature.sign();
    signature.initVerify(publicKey);
    signature.update(new byte[] {4, 5, 6});
    assertTrue(signature.verify(proof), "Export must match the actually published protocol key");
  }

  @Test
  void protocolSignerExportIsPrivatePairedAndRemoved() throws Exception {
    var host = new NetworkSourceJavaHost();
    Path file;
    try (host) {
      host.directory = NetworkSourceJavaHost.privateDirectory();
      var pair = KeyPairGenerator.getInstance("Ed25519").generateKeyPair();
      var begin = Instant.now().minusSeconds(5);
      var end = begin.plusSeconds(3600);
      host.exportProtocolSigner(pair, "export-test", "1", begin, end);
      file = Path.of(host.protocolSigning.path("privateKeyFile").asString());
      assertEquals(host.directory, file.getParent());
      NetworkSourceJavaPrivateFiles.requirePrivate(host.directory);
      NetworkSourceJavaPrivateFiles.requirePrivateFile(file);
      if (Files.getFileStore(file).supportsFileAttributeView("posix"))
        assertEquals(
            PosixFilePermissions.fromString("rw-------"), Files.getPosixFilePermissions(file));
      String pem = Files.readString(file);
      var key =
          KeyFactory.getInstance("Ed25519")
              .generatePrivate(
                  new PKCS8EncodedKeySpec(
                      Base64.getMimeDecoder()
                          .decode(
                              pem.replace(NetworkSourceJavaHost.PRIVATE_KEY_BEGIN, "")
                                  .replace("-----END PRIVATE KEY-----", ""))));
      var signature = Signature.getInstance("Ed25519");
      signature.initSign(key);
      signature.update(new byte[] {1, 2, 3});
      var proof = signature.sign();
      signature.initVerify(pair.getPublic());
      signature.update(new byte[] {1, 2, 3});
      assertTrue(signature.verify(proof));
      assertEquals("export-test", host.protocolSigning.path("keyId").asString());
      assertEquals("1", host.protocolSigning.path("keysetVersion").asString());
      assertEquals(begin.toString(), host.protocolSigning.path("notBefore").asString());
      assertEquals(end.toString(), host.protocolSigning.path("notAfter").asString());
      assertFalse(host.protocolSigning.toString().contains("PRIVATE KEY"));
      assertFalse(
          host.protocolSigning
              .toString()
              .contains(Base64.getEncoder().encodeToString(pair.getPrivate().getEncoded())));
    }
    assertFalse(Files.exists(file));
    host.assertClosed();
  }

  @Test
  void privateCredentialFilesAreReplacedAndRemoved() throws Exception {
    var host = new NetworkSourceJavaHost();
    try (host) {
      host.directory = NetworkSourceJavaHost.privateDirectory();
      NetworkSourceJavaPrivateFiles.requirePrivate(host.directory);
      var file = host.directory.resolve("fixture.jwt");
      NetworkSourceJavaPrivateFiles.replace(file, "first-placeholder");
      NetworkSourceJavaPrivateFiles.replace(file, "second-placeholder");
      assertEquals("second-placeholder", Files.readString(file));
    }
    host.assertClosed();
  }

  @Test
  void unprotectedRootAndInvalidRootAreRejectedWithoutCredentials() throws Exception {
    var host = new NetworkSourceJavaHost();
    try (host) {
      host.directory = Files.createTempDirectory("auriga-unprotected-root-");
      boolean posix =
          host.directory.getFileSystem().supportedFileAttributeViews().contains("posix");
      if (posix)
        Files.setPosixFilePermissions(host.directory, PosixFilePermissions.fromString("rwxr-xr-x"));
      var failure =
          assertThrows(
              java.io.IOException.class,
              () ->
                  NetworkSourceJavaPrivateFiles.replace(
                      host.directory.resolve("fixture.jwt"), "placeholder"));
      assertEquals(
          posix
              ? "Host credential root is not private"
              : "Host credential root ACL allows other readers",
          failure.getMessage());
      assertFalse(Files.exists(host.directory.resolve("fixture.jwt")));
      var file = Files.createFile(host.directory.resolve("plain-file"));
      var unreadable =
          assertThrows(
              java.io.IOException.class, () -> NetworkSourceJavaPrivateFiles.requirePrivate(file));
      assertEquals("Host credential root is not a plain directory", unreadable.getMessage());
    }
    host.assertClosed();
  }

  @Test
  void childJavaCreatesPrivateCredentialsWithUnusablePowerShellModulePath() throws Exception {
    var modules = Files.createTempDirectory("auriga-empty-module-path-");
    var output = Files.createTempFile("auriga-private-root-probe-", ".log");
    Process child = null;
    try {
      var java =
          Path.of(
              System.getProperty("java.home"),
              "bin",
              System.getProperty("os.name").startsWith("Windows") ? "java.exe" : "java");
      var builder =
          new ProcessBuilder(
              java.toString(),
              "-Djava.io.tmpdir=" + System.getProperty("java.io.tmpdir"),
              "-cp",
              System.getProperty("java.class.path"),
              PrivateFilesProbe.class.getName());
      builder.environment().put("PSModulePath", modules.toString());
      builder.redirectErrorStream(true).redirectOutput(output.toFile());
      child = builder.start();
      assertTrue(child.waitFor(60, TimeUnit.SECONDS), "Private-root probe did not exit");
      assertEquals(0, child.exitValue(), Files.readString(output));
    } finally {
      if (child != null && child.isAlive()) {
        child.destroyForcibly();
        assertTrue(child.waitFor(5, TimeUnit.SECONDS), "Own private-root probe survived cleanup");
      }
      Files.deleteIfExists(output);
      Files.deleteIfExists(modules);
    }
  }

  /** Only placeholder files: no PG, OAuth, service JWT or network listener. */
  public static final class PrivateFilesProbe {
    public static void main(String[] args) throws Exception {
      var host = new NetworkSourceJavaHost();
      try (host) {
        host.directory = NetworkSourceJavaHost.privateDirectory();
        var file = host.directory.resolve("fixture.jwt");
        NetworkSourceJavaPrivateFiles.replace(file, "first-placeholder");
        NetworkSourceJavaPrivateFiles.replace(file, "second-placeholder");
        if (!Files.readString(file).equals("second-placeholder"))
          throw new java.io.IOException("Private credential replacement failed");
      }
      host.assertClosed();
    }
  }

  @Test
  void productionHttpsRejectsMissingAndMalformedBearerWithoutPg() throws Exception {
    // Decoder-only fixtures: this test proves real TLS + common HTTP filtering, not login,
    // service issuance, Org authorization or successful register/ticket (those need PG).
    var host = new NetworkSourceJavaHost();
    try (host) {
      host.directory = NetworkSourceJavaHost.privateDirectory();
      host.tls = new NetworkSourceJavaTls(host.directory);
      var key = new ECKeyGenerator(Curve.P_256).keyID("http-filter-test").generate();
      host.keys = mock(SigningKeys.class);
      when(host.keys.jwks())
          .thenReturn(Json.object("keys", List.of(key.toPublicJWK().toJSONObject())));
      host.tokens = mock(TokenService.class);
      when(host.tokens.issuer()).thenReturn("https://localhost");
      host.keySettings =
          new NetworkKeySettings(
              host.tenant, "1", "test", null, null, null, null, null, "1", Map.of());
      host.web = new NetworkSourceJavaWeb(host);
      host.web.start();
      host.ports.add(host.web.port());
      host.http =
          HttpClient.newBuilder()
              .sslContext(host.tls.clientContext())
              .connectTimeout(Duration.ofSeconds(5))
              .build();
      var missing = host.http("/api/iam/v1/network/register", "{}", null);
      assertEquals(401, missing.statusCode());
      assertTrue(missing.body().contains("common.unauthenticated"));
      assertEquals(
          401,
          host.http("/api/iam/v1/network/tickets", "{\"node\":\"1\"}", "invalid").statusCode());
      assertEquals(401, host.http("/api/collab/v1/network/keys", null, null).statusCode());
      try (var untrusted = HttpClient.newBuilder().connectTimeout(Duration.ofSeconds(5)).build()) {
        assertThrows(
            SSLHandshakeException.class,
            () ->
                untrusted.send(
                    HttpRequest.newBuilder(
                            URI.create(
                                "https://127.0.0.1:"
                                    + host.web.port()
                                    + "/api/collab/v1/network/keys"))
                        .timeout(Duration.ofSeconds(10))
                        .build(),
                    HttpResponse.BodyHandlers.discarding()));
      }
    }
    host.assertClosed();
  }
}
