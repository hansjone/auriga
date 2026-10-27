package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.collab.network.keys.NetworkKeySettings;
import com.nimbusds.jose.jwk.*;
import com.nimbusds.jose.jwk.gen.ECKeyGenerator;
import java.net.URI;
import java.net.http.*;
import java.nio.file.*;
import java.nio.file.attribute.PosixFilePermissions;
import java.time.Duration;
import java.util.*;
import java.util.concurrent.TimeUnit;
import javax.net.ssl.SSLHandshakeException;
import org.junit.jupiter.api.Test;

class NetworkSourceJavaHostTest {
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
