package auriga.workspace.app;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.events.EventPublisher;
import auriga.contracts.iam.v1.IssueManagedDeviceResponse;
import auriga.workspace.domain.WorkspaceCrypto;
import auriga.workspace.persistence.WorkspaceStore;
import com.sun.net.httpserver.HttpServer;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.time.Clock;
import java.util.*;
import java.util.concurrent.atomic.AtomicInteger;
import org.junit.jupiter.api.Test;

class ManagedModelCatalogTest {
  @Test
  void projectsAuthorizedLimitsAndOnlyThePublicAlias() {
    var source =
        WorkspaceService.JSON.readTree(
            """
        [{"id":"public-deepseek","context_window":32768,"max_output_tokens":4096,
          "provider":"private-provider","upstream_model":"real-gpt"}]
        """);
    var models = ManagedModelCatalog.fromGateway(source);
    assertEquals("public-deepseek", models.get(0).path("id").asString());
    assertEquals(32768, models.get(0).path("contextWindow").asLong());
    assertEquals(4096, models.get(0).path("maxOutputTokens").asLong());
    assertEquals(3, models.get(0).size());
    assertFalse(models.toString().contains("real-gpt"));
    assertTrue(
        ManagedModelCatalog.hasCatalog(
            WorkspaceService.JSON.createObjectNode().set("models", models)));
  }

  @Test
  void unknownAndLargeCapabilitiesRemainOptionalWithoutInventingDefaults() {
    var models =
        ManagedModelCatalog.fromGateway(
            WorkspaceService.JSON.readTree(
                """
      [{"id":"unknown"}, {"id":"context","context_window":2000000},
       {"id":"output","max_output_tokens":131072},
       {"id":"large","context_window":2000000,"max_output_tokens":131072}]
      """));
    assertEquals(4, models.size());
    assertEquals(1, models.get(0).size());
    assertFalse(models.get(1).has("maxOutputTokens"));
    assertFalse(models.get(2).has("contextWindow"));
    assertEquals(2000000, models.get(3).path("contextWindow").asLong());
    assertEquals(131072, models.get(3).path("maxOutputTokens").asLong());
    assertTrue(
        ManagedModelCatalog.hasCatalog(
            WorkspaceService.JSON.createObjectNode().set("models", models)));
  }

  @Test
  void invalidProvidedCapabilitiesFailWithoutInventingDefaults() {
    for (String fields :
        List.of(
            ",\"context_window\":0,\"max_output_tokens\":1",
            ",\"context_window\":1",
            ",\"context_window\":-1",
            ",\"max_output_tokens\":-1",
            ",\"context_window\":32768,\"max_output_tokens\":0",
            ",\"context_window\":4096,\"max_output_tokens\":4096",
            ",\"context_window\":4096,\"max_output_tokens\":8192",
            ",\"context_window\":9007199254740992",
            ",\"max_output_tokens\":9007199254740992",
            ",\"context_window\":32768,\"max_output_tokens\":1.5",
            ",\"context_window\":\"32768\",\"max_output_tokens\":4096",
            ",\"context_window\":null,\"max_output_tokens\":4096")) {
      assertThrows(
          IllegalArgumentException.class,
          () ->
              ManagedModelCatalog.fromGateway(
                  WorkspaceService.JSON.readTree("[{\"id\":\"public\"" + fields + "}]")),
          fields);
    }
    assertTrue(
        ManagedModelCatalog.hasCatalog(
            WorkspaceService.JSON.readTree("{\"models\":[{\"id\":\"legacy\"}]}")));
    for (String invalid :
        List.of("[]", "[{\"id\":\"same\"},{\"id\":\"same\",\"context_window\":2000000}]")) {
      assertThrows(
          IllegalArgumentException.class,
          () -> ManagedModelCatalog.fromGateway(WorkspaceService.JSON.readTree(invalid)));
    }
  }

  @Test
  void invalidExistingMaterialRefreshesOnceButUnknownCapabilitiesDoNotForceRotation()
      throws Exception {
    var tenant = UUID.randomUUID();
    var instance = UUID.randomUUID();
    var crypto = new WorkspaceCrypto(new byte[32]);
    var store = mock(WorkspaceStore.class);
    when(store.tenant()).thenReturn(tenant);
    var service =
        new WorkspaceService(
            store, null, null, null, mock(EventPublisher.class), Clock.systemUTC());
    var calls = new AtomicInteger();
    var server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
    server.createContext(
        "/ai/v1/models",
        exchange -> {
          calls.incrementAndGet();
          byte[] body =
              "{\"data\":[{\"id\":\"public\"},{\"id\":\"large\",\"context_window\":2000000,\"max_output_tokens\":131072}]}"
                  .getBytes(StandardCharsets.UTF_8);
          exchange.sendResponseHeaders(200, body.length);
          try (var output = exchange.getResponseBody()) {
            output.write(body);
          }
        });
    server.start();
    try {
      var models =
          new WorkspaceManagedModels(
              service,
              crypto,
              WorkspaceService.JSON
                  .createObjectNode()
                  .put(
                      "modelBaseUrl",
                      "http://127.0.0.1:" + server.getAddress().getPort() + "/ai/v1"));
      var row = new HashMap<String, Object>();
      row.put("id", instance);
      row.put("owner_id", UUID.randomUUID());
      row.put("managed_credential_version", 1L);
      row.put(
          "managed_model_credential",
          crypto.encrypt(
              "{\"models\":[{\"id\":\"public\"}]}".getBytes(StandardCharsets.UTF_8),
              tenant + "/managed-model/" + instance));
      var credential =
          IssueManagedDeviceResponse.newBuilder().setModelCredential("fixture").build();
      models.reconcile(row, credential);
      assertEquals(0, calls.get(), "a valid unknown capability does not require material rotation");
      row.put(
          "managed_model_credential",
          crypto.encrypt(
              "{\"models\":[{\"id\":\"public\",\"contextWindow\":0}]}"
                  .getBytes(StandardCharsets.UTF_8),
              tenant + "/managed-model/" + instance));
      models.reconcile(row, credential);
      assertEquals(1, calls.get(), "invalid material refreshes through the normal catalog path");
      verify(store).tx(any());
      row.put(
          "managed_model_credential",
          crypto.encrypt(
              "{\"models\":[{\"id\":\"public\",\"contextWindow\":32768,\"maxOutputTokens\":4096}]}"
                  .getBytes(StandardCharsets.UTF_8),
              tenant + "/managed-model/" + instance));
      models.reconcile(row, credential);
      assertEquals(1, calls.get(), "already migrated material must not continually rotate");
    } finally {
      server.stop(0);
    }
  }
}
