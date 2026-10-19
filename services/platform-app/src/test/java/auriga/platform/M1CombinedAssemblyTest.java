package auriga.platform;

import static org.junit.jupiter.api.Assertions.*;

import auriga.iam.EmergencyAccounts;
import auriga.iam.IamStore;
import auriga.iam.ProviderManager;
import auriga.iam.SigningKeys;
import auriga.org.OrgService;
import auriga.testsupport.PostgresExtension;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.file.Files;
import java.nio.file.Path;
import java.security.SecureRandom;
import java.util.Base64;
import java.util.UUID;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.junit.jupiter.api.io.TempDir;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.beans.factory.annotation.Value;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.test.annotation.DirtiesContext;
import org.springframework.test.context.ActiveProfiles;
import org.springframework.test.context.DynamicPropertyRegistry;
import org.springframework.test.context.DynamicPropertySource;

/**
 * M1 combined assembly: dual-schema DataSources, seed emergency + team-account, health and login
 * providers against the real HTTP surface (no mock).
 */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = {
      "auriga.platform.seed=true",
      "auriga.iam.session-kv-nats=false",
      "auriga.iam.jwks-propagation-seconds=30",
      "auriga.services.workspace.enabled=false",
      "auriga.services.resource.enabled=false",
      "auriga.services.knowledge.enabled=false",
      "auriga.services.collab.enabled=false",
      "auriga.services.audit.enabled=false",
      "auriga.services.ops.enabled=false",
      "auriga.grpc.server.enabled=false",
      "spring.autoconfigure.exclude="
          + "org.springframework.boot.jdbc.autoconfigure.DataSourceAutoConfiguration,"
          + "org.springframework.boot.flyway.autoconfigure.FlywayAutoConfiguration,"
          + "auriga.common.autoconfigure.AurigaPersistenceAutoConfiguration,"
          + "auriga.common.autoconfigure.AurigaEventsAutoConfiguration"
    })
@ActiveProfiles({"combined", "dev"})
@ExtendWith(PostgresExtension.class)
@DirtiesContext
class M1CombinedAssemblyTest {
  private static final UUID TENANT = UUID.fromString("00000000-0000-0000-0000-000000000001");

  @TempDir static Path tempDir;

  @Value("${local.server.port}")
  private int port;

  @Autowired private IamStore iamStore;
  @Autowired private OrgService orgService;
  @Autowired private EmergencyAccounts emergency;
  @Autowired private ProviderManager providers;
  @Autowired private SigningKeys keys;

  @DynamicPropertySource
  static void props(DynamicPropertyRegistry registry) {
    var iam = PostgresExtension.serviceRole(M1CombinedAssemblyTest.class, "auriga_iam", "iam");
    var org = PostgresExtension.serviceRole(M1CombinedAssemblyTest.class, "auriga_org", "org");
    registry.add("auriga.platform.iam.jdbc-url", iam::jdbcUrl);
    registry.add("auriga.platform.iam.username", () -> "auriga_iam");
    registry.add("auriga.platform.iam.password", () -> "");
    registry.add("auriga.platform.org.jdbc-url", org::jdbcUrl);
    registry.add("auriga.platform.org.username", () -> "auriga_org");
    registry.add("auriga.platform.org.password", () -> "");
    registry.add(
        "auriga.platform.enrollment-file", () -> tempDir.resolve("enrollment.json").toString());
    byte[] key = new byte[32];
    new SecureRandom().nextBytes(key);
    registry.add("auriga.iam.encryption-key", () -> Base64.getEncoder().encodeToString(key));
    registry.add("auriga.iam.issuer", () -> "http://portal.example.com");
    registry.add("auriga.security.jwt.issuer", () -> "http://portal.example.com");
    registry.add("auriga.security.jwt.jwk-set-uri", () -> "http://portal.example.com/oauth2/jwks");
    registry.add("auriga.security.jwt.allow-insecure-jwks-uri", () -> "true");
    // Do not set auriga.events.nats-url — absence disables NATS transport (no ctl in unit test).
  }

  @Test
  void seedsEmergencyAndTeamProviderAndServesLoginProviders() throws Exception {
    assertNotNull(orgService);
    assertNotNull(emergency);
    assertNotNull(providers);
    assertNotNull(keys);

    Path enrollment = tempDir.resolve("enrollment.json");
    assertTrue(Files.isRegularFile(enrollment), "enrollment file missing");
    String body = Files.readString(enrollment);
    assertTrue(body.contains("username"));
    assertTrue(body.contains("totpSecret"));
    assertFalse(body.contains("REDACTED"));

    boolean hasTeam =
        iamStore.tenant(
            TENANT,
            () ->
                iamStore.all("idp_provider").stream()
                    .anyMatch(p -> "team-account".equals(p.path("connectorType").asString())));
    assertTrue(hasTeam, "team-account provider should be seeded");

    // HttpClient cannot override Host; combined profile registers 127.0.0.1 → portal audience.
    try (var client = HttpClient.newHttpClient()) {
      var health =
          client.send(
              HttpRequest.newBuilder(URI.create("http://127.0.0.1:" + port + "/actuator/health"))
                  .GET()
                  .build(),
              HttpResponse.BodyHandlers.ofString());
      assertEquals(200, health.statusCode());

      var providersResp =
          client.send(
              HttpRequest.newBuilder(
                      URI.create("http://127.0.0.1:" + port + "/api/iam/v1/login/providers"))
                  .header("X-Auriga-Client-IP", "192.0.2.10")
                  .GET()
                  .build(),
              HttpResponse.BodyHandlers.ofString());
      assertEquals(200, providersResp.statusCode(), providersResp.body());
      assertTrue(providersResp.body().contains("team-account"), providersResp.body());
      assertTrue(providersResp.body().contains("emergency"), providersResp.body());
    }
  }
}
