package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.collab.network.keys.NetworkKeySettings;
import com.nimbusds.jose.jwk.*;
import com.nimbusds.jose.jwk.gen.ECKeyGenerator;
import java.net.URI;
import java.net.http.*;
import java.nio.file.*;
import java.time.Duration;
import java.util.*;
import javax.net.ssl.SSLHandshakeException;
import org.junit.jupiter.api.Test;

class NetworkSourceJavaHostTest {
  @Test
  void privateCredentialFilesAreReplacedOrUnsafeRootIsRejected() throws Exception {
    var host = new NetworkSourceJavaHost();
    try (host) {
      host.directory = NetworkSourceJavaHost.privateDirectory();
      try {
        NetworkSourceJavaPrivateFiles.requirePrivate(host.directory);
      } catch (java.io.IOException e) {
        // Restricted Windows workers may inherit sandbox identities. Never change their ACLs
        // or create credentials there. The normal private-root path is exercised on POSIX.
        assertTrue(System.getProperty("os.name").startsWith("Windows"));
        assertEquals("Host credential root ACL allows other readers", e.getMessage());
        try (var files = Files.list(host.directory)) {
          assertEquals(0, files.count());
        }
        host.close();
        host.assertClosed();
        return;
      }
      var file = host.directory.resolve("fixture.jwt");
      NetworkSourceJavaPrivateFiles.replace(file, "first-placeholder");
      NetworkSourceJavaPrivateFiles.replace(file, "second-placeholder");
      assertEquals("second-placeholder", Files.readString(file));
    }
    host.assertClosed();
  }

  @Test
  void productionHttpsRejectsMissingAndMalformedBearerWithoutPg() throws Exception {
    // Decoder-only fixtures: this test proves real TLS + common HTTP filtering, not login,
    // service issuance, Org authorization or successful register/ticket (those need PG).
    var host = new NetworkSourceJavaHost();
    try (host) {
      host.directory = Files.createTempDirectory("auriga-java-tls-test-");
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
