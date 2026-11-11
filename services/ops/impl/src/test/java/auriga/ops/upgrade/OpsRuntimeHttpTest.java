package auriga.ops.upgrade;

import static org.junit.jupiter.api.Assertions.*;

import auriga.ops.backup.OpsBackupController;
import java.net.URI;
import java.net.http.*;
import java.nio.file.*;
import java.time.Instant;
import java.util.*;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;
import org.springframework.beans.factory.annotation.*;
import org.springframework.boot.autoconfigure.EnableAutoConfiguration;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.context.annotation.*;
import org.springframework.security.oauth2.jwt.*;
import org.springframework.test.annotation.DirtiesContext;
import org.springframework.test.context.*;

/** Actual HTTP routing, validation, authorization advisor and single-use streaming download. */
@SpringBootTest(
    classes = OpsRuntimeHttpTest.App.class,
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = {
      "auriga.ops.runtime.enabled=true",
      "auriga.services.ops.enabled=true",
      "auriga.grpc.server.enabled=false",
      "auriga.logging.json=false",
      "server.address=127.0.0.1",
      "spring.autoconfigure.exclude=org.springframework.boot.jdbc.autoconfigure.DataSourceAutoConfiguration,org.springframework.boot.flyway.autoconfigure.FlywayAutoConfiguration,auriga.common.autoconfigure.AurigaPersistenceAutoConfiguration,auriga.common.autoconfigure.AurigaEventsAutoConfiguration"
    })
@DirtiesContext
class OpsRuntimeHttpTest {
  static final UUID TENANT = UUID.fromString("00000000-0000-0000-0000-000000000001");
  @TempDir static Path directory;

  @Value("${local.server.port}")
  int port;

  @Autowired OpsRuntimeStore store;

  @DynamicPropertySource
  static void properties(DynamicPropertyRegistry registry) {
    registry.add("auriga.ops.runtime.directory", () -> directory.toString());
    registry.add("auriga.ops.runtime.tenant-id", TENANT::toString);
  }

  @EnableAutoConfiguration
  @Import({OpsRuntimeConfiguration.class, OpsUpgradeController.class, OpsBackupController.class})
  static class App {
    // Signature verification belongs to the shared decoder tests; keep real filters/advisors here.
    @Bean
    JwtDecoder decoder() {
      return token ->
          Jwt.withTokenValue(token)
              .header("alg", "test")
              .subject("alice")
              .claim(
                  "tid",
                  token.equals("other-tenant") ? UUID.randomUUID().toString() : TENANT.toString())
              .claim("ptyp", "user")
              .claim("ctx", token.equals("portal") ? "portal" : "admin")
              .claim("sid", "test-session")
              .claim("mtyp", "member")
              .claim(
                  "perms",
                  token.equals("no-permission")
                      ? List.of()
                      : List.of(
                          "ops.backups.read",
                          "ops.backups.write",
                          "ops.backups.download",
                          "ops.upgrades.read",
                          "ops.upgrades.write"))
              .claim(
                  "stepup_time",
                  Instant.now().minusSeconds(token.equals("stale") ? 601 : 0).getEpochSecond())
              .issuedAt(Instant.now())
              .expiresAt(Instant.now().plusSeconds(300))
              .build();
    }
  }

  HttpResponse<String> request(String method, String path, String token, String body, String ticket)
      throws Exception {
    var builder =
        HttpRequest.newBuilder(
            URI.create("http://127.0.0.1:" + port + "/api/workspace/v1/admin/ops" + path));
    if (token != null) builder.header("Authorization", "Bearer " + token);
    if (ticket != null) builder.header("X-Auriga-Download", ticket);
    builder.header("Content-Type", "application/json");
    builder.method(
        method,
        body == null
            ? HttpRequest.BodyPublishers.noBody()
            : HttpRequest.BodyPublishers.ofString(body));
    try (var client = HttpClient.newHttpClient()) {
      return client.send(builder.build(), HttpResponse.BodyHandlers.ofString());
    }
  }

  @Test
  void enforcesAdminTenantPermissionAndStepUpOverHttp() throws Exception {
    assertEquals(401, request("GET", "/backups", null, null, null).statusCode());
    for (String denied : List.of("portal", "other-tenant", "no-permission"))
      assertEquals(403, request("GET", "/backups", denied, null, null).statusCode());
    assertEquals(200, request("GET", "/backups", "admin", null, null).statusCode());
    var stale =
        request(
            "PUT",
            "/backups/plan",
            "stale",
            "{\"enabled\":true,\"intervalHours\":24,\"revision\":0}",
            null);
    assertEquals(403, stale.statusCode());
    assertTrue(stale.body().contains("step_up_required"));
    assertEquals(
        400,
        request(
                "PUT",
                "/backups/plan",
                "admin",
                "{\"enabled\":true,\"intervalHours\":0,\"revision\":0}",
                null)
            .statusCode());
    assertEquals(400, request("PUT", "/backups/plan", "admin", "{}", null).statusCode());
  }

  @Test
  void downloadRequiresFreshTicketAndCannotReplay() throws Exception {
    UUID id = UUID.randomUUID();
    Files.createDirectories(directory.resolve("backups"));
    Files.writeString(directory.resolve("backups").resolve(id + ".dump"), "PGDMP-example");
    store.change(
        state -> {
          state
              .withArray("backups")
              .addObject()
              .put("id", id.toString())
              .put("status", "succeeded");
          return null;
        });
    var issued = request("POST", "/backups/" + id + "/tickets", "admin", null, null);
    assertEquals(200, issued.statusCode());
    assertEquals("no-store", issued.headers().firstValue("Cache-Control").orElseThrow());
    String ticket = OpsRuntimeStore.JSON.readTree(issued.body()).path("token").asString();
    assertEquals(
        403, request("GET", "/backups/" + id + "/download", "stale", null, ticket).statusCode());
    var downloaded = request("GET", "/backups/" + id + "/download", "admin", null, ticket);
    assertEquals(200, downloaded.statusCode());
    assertEquals("PGDMP-example", downloaded.body());
    assertEquals("no-store", downloaded.headers().firstValue("Cache-Control").orElseThrow());
    assertEquals(
        403, request("GET", "/backups/" + id + "/download", "admin", null, ticket).statusCode());
  }
}
