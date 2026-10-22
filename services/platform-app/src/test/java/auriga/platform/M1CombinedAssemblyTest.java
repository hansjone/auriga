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
  @Autowired private auriga.iam.TokenService tokens;
  @Autowired private org.springframework.security.oauth2.jwt.JwtDecoder decoder;
  @Autowired private auriga.common.security.InternalTokenVerifier internal;
  @Autowired private org.springframework.context.ApplicationContext context;
  private static final String EDGE_SECRET = auriga.iam.Secrets.random();

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
    try {
      String fixture =
          Files.readString(Path.of("../../contracts/deployment/fixtures/domain-proxy.json"))
              .replace("019a0000-0000-7000-8000-000000000001", TENANT.toString())
              .replace("\"https\"", "\"http\"")
              .replace("443", "80")
              .replace("\"insecureHttpAcknowledged\": false", "\"insecureHttpAcknowledged\": true");
      Path access = tempDir.resolve("deployment-access.json");
      Files.writeString(access, fixture);
      registry.add("auriga.iam.deployment-access-file", access::toString);
    } catch (java.io.IOException e) {
      throw new java.io.UncheckedIOException(e);
    }

    registry.add("auriga.security.jwt.issuer", () -> "http://portal.example.com");
    // A network JWKS fetch must never be needed in the combined IAM process.
    registry.add("auriga.security.jwt.jwk-set-uri", () -> "http://127.0.0.1:1/unused-jwks");
    registry.add("auriga.security.jwt.jwks-min-refresh-interval", () -> "1ms");
    registry.add(
        "auriga.iam.internal-secret-hashes.edge", () -> auriga.iam.Secrets.hash(EDGE_SECRET));
    registry.add("auriga.security.jwt.allow-insecure-jwks-uri", () -> "true");
    // Do not set auriga.events.nats-url — absence disables NATS transport (no ctl in unit test).
  }

  @Test
  void localJwksBeansValidateBothAudiencesAndPublishedRotationWithoutHttp() {
    assertFalse(context.containsBean("iamEdgeTokenFilePublisher"));
    assertTrue(context.containsBean("iamAccessTokenDecoder"));
    assertFalse(context.containsBean("aurigaAccessTokenDecoder"));
    assertTrue(context.containsBean("iamInternalTokenVerifier"));
    assertFalse(context.containsBean("aurigaInternalTokenVerifier"));
    String subject = UUID.randomUUID().toString();
    java.util.function.Function<String, String> access =
        aud ->
            tokens.access(
                TENANT,
                subject,
                aud,
                "user",
                "admin",
                UUID.randomUUID(),
                null,
                java.time.Instant.now(),
                null,
                "openid",
                "member");
    String old = access.apply("auriga");
    assertEquals(subject, decoder.decode(old).getSubject());
    assertThrows(
        org.springframework.security.oauth2.jwt.JwtException.class,
        () -> decoder.decode(access.apply("third-party")));
    String service = tokens.issue("edge", EDGE_SECRET);
    assertEquals("svc:edge", internal.verifier().verify(service).subject());
    assertThrows(
        org.springframework.security.oauth2.jwt.JwtException.class, () -> decoder.decode(service));
    assertThrows(auriga.common.error.AurigaException.class, () -> internal.verifier().verify(old));
    String next = keys.publishNext();
    iamStore.isolated(
        SigningKeys.PLATFORM,
        () -> {
          var row = iamStore.get("signing_key", next, true);
          row.put("publishedAt", java.time.Instant.now().minusSeconds(60).toString());
          iamStore.save("signing_key", row);
          return null;
        });
    keys.activate(next);
    assertEquals(next, decoder.decode(access.apply("auriga")).getHeaders().get("kid"));
    assertEquals(subject, decoder.decode(old).getSubject());
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

    // This exercises the explicitly configured loopback development hop, not a browser ingress.
    try (var client = HttpClient.newHttpClient()) {
      var health =
          client.send(
              HttpRequest.newBuilder(URI.create("http://127.0.0.1:" + port + "/actuator/health"))
                  .GET()
                  .build(),
              HttpResponse.BodyHandlers.ofString());
      assertEquals(200, health.statusCode());

      String origin = "http://portal.example.com";
      String raw =
          "GET /api/iam/v1/login/providers HTTP/1.1\r\nHost: portal.example.com\r\n"
              + "X-Auriga-Entry-Origin: "
              + origin
              + "\r\nX-Auriga-Entry-Role: portal\r\n"
              + "X-Auriga-Client-IP: 192.0.2.10\r\nConnection: close\r\n\r\n";
      String providersWire;
      try (var socket = new java.net.Socket("127.0.0.1", port)) {
        socket.setSoTimeout(5000);
        socket.getOutputStream().write(raw.getBytes(java.nio.charset.StandardCharsets.US_ASCII));
        providersWire =
            new String(
                socket.getInputStream().readAllBytes(), java.nio.charset.StandardCharsets.UTF_8);
      }
      assertTrue(providersWire.startsWith("HTTP/1.1 200"), providersWire);
      assertTrue(providersWire.contains("team-account"), providersWire);
      assertTrue(providersWire.contains("emergency"), providersWire);
      var providersResp =
          client.send(
              HttpRequest.newBuilder(
                      URI.create("http://127.0.0.1:" + port + "/api/iam/v1/login/providers"))
                  .GET()
                  .build(),
              HttpResponse.BodyHandlers.ofString());
      assertEquals(403, providersResp.statusCode(), providersResp.body());
    }
  }
}
