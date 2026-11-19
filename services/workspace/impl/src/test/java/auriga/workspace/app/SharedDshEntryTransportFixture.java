package auriga.workspace.app;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import auriga.contracts.deploymentconfig.DeploymentAccess;
import auriga.workspace.persistence.WorkspaceStore;
import io.nats.client.Connection;
import java.net.URI;
import java.net.http.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.security.*;
import java.time.Clock;
import java.util.*;
import java.util.function.IntSupplier;
import java.util.function.UnaryOperator;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.json.JsonMapper;
import tools.jackson.databind.node.ObjectNode;

/** Actual W entry transport; only the external Edge/Host HTTP response is controlled. */
public final class SharedDshEntryTransportFixture {
  public final SharedDshEntryTransport transport;
  public int status = 200, requests;
  public IntSupplier responseStatus = () -> status;
  public Map<String, List<String>> headers = Map.of("Content-Type", List.of("application/json"));
  public UnaryOperator<ObjectNode> profile = value -> value;
  public Runnable duringProbe = () -> {};
  public JsonNode claims;

  @SuppressWarnings("unchecked")
  public SharedDshEntryTransportFixture(WorkspaceStore store, Clock clock, UUID tenant) {
    try {
      var json = JsonMapper.builder().build();
      var document =
          (ObjectNode)
              json.readTree(
                  Files.readAllBytes(
                      Path.of("../../../contracts/deployment/fixtures/ip-http.json")));
      document.put("tenantId", tenant.toString());
      var access = DeploymentAccess.load(json.writeValueAsBytes(document));
      var keys = KeyPairGenerator.getInstance("Ed25519").generateKeyPair();
      var http = mock(HttpClient.class);
      when(http.send(any(HttpRequest.class), any(HttpResponse.BodyHandler.class)))
          .thenAnswer(
              call -> {
                HttpRequest request = call.getArgument(0);
                requests++;
                assertEquals("GET", request.method());
                assertTrue(request.headers().firstValue("Cookie").isEmpty());
                assertTrue(request.headers().firstValue("Authorization").isEmpty());
                var parts =
                    request
                        .headers()
                        .firstValue(SharedDshEntryTransport.PROFILE_HEADER)
                        .orElseThrow()
                        .split("\\.");
                assertEquals(3, parts.length);
                var decoder = Base64.getUrlDecoder();
                var header = json.readTree(decoder.decode(parts[0]));
                assertEquals("auriga-shared-profile-probe+jwt", header.path("typ").asString());
                var signature = Signature.getInstance("Ed25519");
                signature.initVerify(keys.getPublic());
                signature.update((parts[0] + "." + parts[1]).getBytes(StandardCharsets.US_ASCII));
                assertTrue(signature.verify(decoder.decode(parts[2])));
                claims = json.readTree(decoder.decode(parts[1]));
                assertEquals("auriga.shared.profile-probe", claims.path("audience").asString());
                assertEquals(clock.instant().getEpochSecond(), claims.path("issuedAt").asLong());
                assertEquals(
                    clock.instant().getEpochSecond() + 10, claims.path("expiresAt").asLong());
                String pool = claims.path("binding").path("physicalInstanceId").asString();
                boolean dedicated =
                    claims.path("binding").path("mode").asString().equals("dedicated");
                assertEquals(
                    URI.create(
                        access.portalUrl()
                            + "/i/"
                            + pool
                            + (dedicated
                                ? SharedDshEntryTransport.DEDICATED_PROFILE_PATH
                                : SharedDshEntryTransport.PROFILE_PATH)),
                    request.uri());
                var body =
                    json.createObjectNode()
                        .put("schemaVersion", 1)
                        .put("physicalInstanceId", pool)
                        .put("profile", dedicated ? "auriga.dedicated.v1" : "auriga.shared.v1");
                var capabilities = body.putObject("capabilities").put("toolPresentation", "native");
                if (dedicated) {
                  capabilities
                      .put("pluginInstall", true)
                      .put("privateSkills", true)
                      .put("includeDefaultRoots", true);
                } else {
                  for (var name :
                      List.of(
                          "shell",
                          "terminal",
                          "jobs",
                          "subagent",
                          "stdioMcp",
                          "pluginInstall",
                          "privateSkills",
                          "personalKeys",
                          "includeDefaultRoots")) capabilities.put(name, false);
                }
                HttpResponse<byte[]> response = mock(HttpResponse.class);
                when(response.statusCode()).thenReturn(responseStatus.getAsInt());
                when(response.headers()).thenReturn(HttpHeaders.of(headers, (name, value) -> true));
                when(response.body()).thenReturn(json.writeValueAsBytes(profile.apply(body)));
                duringProbe.run();
                return response;
              });
      transport =
          new SharedDshEntryTransport(
              access,
              mock(Connection.class),
              store,
              new SharedDshTickets.SigningKey("ready-fixture", keys.getPrivate()),
              clock,
              http);
    } catch (Exception failure) {
      throw new IllegalStateException("Unable to construct the ready-entry fixture", failure);
    }
  }
}
