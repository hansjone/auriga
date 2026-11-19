package auriga.workspace.app;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.mock;

import auriga.contracts.deploymentconfig.DeploymentAccess;
import auriga.contracts.node.v1.*;
import io.nats.client.Connection;
import java.net.*;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.file.*;
import java.security.KeyPairGenerator;
import java.time.*;
import java.util.*;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;
import tools.jackson.databind.json.JsonMapper;
import tools.jackson.databind.node.ObjectNode;

/** Explicit IT: build the Edge Go test binary, then pass -Dauriga.profile.edge-fixture=ABS_PATH. */
class SharedDshEdgeProfileIT {
  @TempDir Path directory;

  @Test
  void productionWorkspaceTransportCrossesRealEdgeAndMtlsNodeProfilesForBothModes()
      throws Exception {
    String executable = System.getProperty("auriga.profile.edge-fixture", "");
    assertFalse(executable.isBlank(), "Build the Edge fixture and supply its absolute path");
    assertTrue(Path.of(executable).isAbsolute());
    var state = new SharedDshReadyResourceMarketTest.Fixture();
    UUID pool = auriga.common.persistence.UuidV7.next();
    var keys = KeyPairGenerator.getInstance("Ed25519").generateKeyPair();
    var json = JsonMapper.builder().build();
    Files.write(directory.resolve("public-key.der"), keys.getPublic().getEncoded());
    Files.writeString(directory.resolve("control.json"), "{\"failure\":\"\"}");
    var command =
        new ProcessBuilder(
                executable,
                "-test.run=^TestWorkspaceProfileHTTPFixture$",
                "-test.timeout=50s",
                "-test.v")
            .directory(Path.of("../../../infra/edge").toAbsolutePath().normalize().toFile())
            .redirectErrorStream(true)
            .redirectOutput(directory.resolve("edge.log").toFile());
    command.environment().put("AURIGA_PROFILE_FIXTURE_DIR", directory.toString());
    command.environment().put("AURIGA_PROFILE_TENANT", state.state.tenant.toString());
    command.environment().put("AURIGA_PROFILE_POOL", pool.toString());
    command.environment().put("GOMAXPROCS", "2");
    var process = command.start();
    try {
      var ready = directory.resolve("ready.json");
      long deadline = System.nanoTime() + Duration.ofSeconds(10).toNanos();
      while (!Files.exists(ready) && process.isAlive() && System.nanoTime() < deadline)
        Thread.sleep(20);
      assertTrue(Files.exists(ready), () -> "Edge fixture not ready: " + readLog());
      var portal =
          URI.create(json.readTree(Files.readAllBytes(ready)).path("portalUrl").asString());
      var document =
          (ObjectNode)
              json.readTree(
                  Files.readAllBytes(
                      Path.of("../../../contracts/deployment/fixtures/ip-http.json")));
      document.put("tenantId", state.state.tenant.toString());
      for (var name : List.of("portalOrigin", "issuerOrigin")) {
        var origin = (ObjectNode) document.path(name);
        origin.put("host", portal.getHost()).put("port", portal.getPort());
      }
      ((ObjectNode) document.path("adminOrigin")).put("host", portal.getHost());
      ((ObjectNode) document.path("listeners").path("portal")).put("port", portal.getPort());
      var access = DeploymentAccess.load(json.writeValueAsBytes(document));
      var direct =
          new ProxySelector() {
            public List<Proxy> select(URI uri) {
              return List.of(Proxy.NO_PROXY);
            }

            public void connectFailed(
                URI uri, SocketAddress address, java.io.IOException failure) {}
          };
      var http =
          HttpClient.newBuilder()
              .followRedirects(HttpClient.Redirect.NEVER)
              .connectTimeout(Duration.ofSeconds(2))
              .proxy(direct)
              .build();
      try (var transport =
          new SharedDshEntryTransport(
              access,
              mock(Connection.class),
              state.state.store,
              new SharedDshTickets.SigningKey("cross-language", keys.getPrivate()),
              Clock.systemUTC(),
              http)) {
        var queries =
            new SharedDshResourceMarketQueries(
                state.state.store, state.state.authority, transport, Clock.systemUTC());
        for (boolean dedicated : new boolean[] {false, true}) {
          var binding =
              state.binding.toBuilder()
                  .setPhysicalInstanceId(pool.toString())
                  .setMode(
                      dedicated
                          ? DshInstanceMode.DSH_INSTANCE_MODE_DEDICATED
                          : DshInstanceMode.DSH_INSTANCE_MODE_SHARED)
                  .build();
          var view = state.view.toBuilder().setBinding(binding).build();
          var report =
              state.report.toBuilder()
                  .setBinding(binding)
                  .setObservedAt(Instant.now().getEpochSecond())
                  .build();
          state.state.row.put("mode", dedicated ? "dedicated" : "shared");
          state.state.row.put("pool_id", pool);
          state.state.row.put("payload", view.toByteArray());
          state.state.row.put("report", report.toByteArray());
          control("");
          var observed = state.state.run(() -> queries.report(view));
          if (observed.isEmpty()) {
            var claims = new TreeMap<String, Object>();
            long now = Instant.now().getEpochSecond();
            claims.put("schemaVersion", 1);
            claims.put("audience", "auriga.shared.profile-probe");
            claims.put(
                "binding", new TreeMap<>(auriga.workspace.domain.SharedDshEvents.binding(binding)));
            claims.put("issuedAt", now);
            claims.put("expiresAt", now + 10);
            claims.put("jti", UUID.randomUUID().toString());
            var proof =
                SharedDshTickets.signProfileProbe("cross-language", claims, keys.getPrivate());
            var response =
                http.send(
                    HttpRequest.newBuilder(
                            URI.create(
                                access.portalUrl()
                                    + "/i/"
                                    + binding.getPhysicalInstanceId()
                                    + (dedicated
                                        ? SharedDshEntryTransport.DEDICATED_PROFILE_PATH
                                        : SharedDshEntryTransport.PROFILE_PATH)))
                        .header(SharedDshEntryTransport.PROFILE_HEADER, proof)
                        .GET()
                        .build(),
                    HttpResponse.BodyHandlers.ofString());
            fail(
                "Entry observation rejected; HTTP="
                    + response.statusCode()
                    + " body="
                    + response.body()
                    + "\n"
                    + readLog());
          }
          assertEquals(Optional.of(report), observed);
          for (var failure :
              List.of(
                  "other-profile",
                  "extra",
                  "cookie",
                  "redirect",
                  "oversize",
                  "wrong-pool",
                  "unavailable")) {
            control(failure);
            assertTrue(state.state.run(() -> queries.report(view)).isEmpty(), failure);
          }
        }
        assertEquals(16, Integer.parseInt(Files.readString(directory.resolve("requests"))));
      }
    } finally {
      Files.writeString(directory.resolve("stop"), "stop");
      if (!process.waitFor(10, java.util.concurrent.TimeUnit.SECONDS)) {
        process.destroyForcibly();
        assertTrue(
            process.waitFor(5, java.util.concurrent.TimeUnit.SECONDS), "Edge fixture did not exit");
      }
    }
    assertEquals(0, process.exitValue(), this::readLog);
  }

  private void control(String failure) throws Exception {
    var temporary = directory.resolve("control-next.json");
    Files.writeString(temporary, "{\"failure\":\"" + failure + "\"}");
    Files.move(
        temporary,
        directory.resolve("control.json"),
        StandardCopyOption.REPLACE_EXISTING,
        StandardCopyOption.ATOMIC_MOVE);
  }

  private String readLog() {
    try {
      return Files.readString(directory.resolve("edge.log"));
    } catch (Exception failure) {
      return "Edge fixture log unavailable";
    }
  }
}
