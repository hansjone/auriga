package auriga.workspace.app;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.events.EventPublisher;
import auriga.common.observability.JsonLogFormatter;
import auriga.contracts.iam.v1.IssueManagedDeviceResponse;
import auriga.workspace.persistence.WorkspaceStore;
import ch.qos.logback.classic.Logger;
import ch.qos.logback.core.read.ListAppender;
import com.sun.net.httpserver.HttpServer;
import java.lang.reflect.InvocationTargetException;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.time.Clock;
import java.util.*;
import org.junit.jupiter.api.Test;
import org.slf4j.LoggerFactory;

class ManagedModelDiagnosticTest {
  @Test
  void realCatalogReportsHttpAndValidationFailuresWithoutBodyOrCredential() throws Exception {
    var store = mock(WorkspaceStore.class);
    when(store.tenant()).thenReturn(UUID.fromString("00000000-0000-4000-8000-000000000001"));
    var service =
        new WorkspaceService(
            store, null, null, null, mock(EventPublisher.class), Clock.systemUTC());
    var row =
        Map.<String, Object>of(
            "id",
            UUID.randomUUID(),
            "owner_id",
            UUID.randomUUID(),
            "generation",
            210L,
            "managed_credential_version",
            7L);
    var response =
        IssueManagedDeviceResponse.newBuilder()
            .setModelCredential("sentinel-managed-credential")
            .build();
    var method =
        WorkspaceManagedModels.class.getDeclaredMethod(
            "catalog", IssueManagedDeviceResponse.class, Map.class);
    method.setAccessible(true);
    var logger = (Logger) LoggerFactory.getLogger(WorkspaceManagedModels.class);
    var appender = new ListAppender<ch.qos.logback.classic.spi.ILoggingEvent>();
    appender.start();
    logger.addAppender(appender);
    try {
      for (String scenario :
          List.of("200", "401", "403", "503", "empty", "invalid", "duplicate", "json")) {
        appender.list.clear();
        var server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        server.createContext(
            "/ai/v1/models",
            exchange -> {
              String body =
                  switch (scenario) {
                    case "empty" -> "{\"data\":[]}";
                    case "invalid" -> "{\"data\":false}";
                    case "duplicate" ->
                        "{\"data\":[{\"id\":\"pool\",\"context_window\":32768,\"max_output_tokens\":4096},{\"id\":\"pool\",\"context_window\":32768,\"max_output_tokens\":4096}]}";
                    case "json" -> "sentinel-directory-body";
                    default ->
                        "{\"data\":[{\"id\":\"pool\",\"context_window\":32768,\"max_output_tokens\":4096}]}";
                  };
              byte[] raw = body.getBytes(StandardCharsets.UTF_8);
              exchange.getResponseHeaders().set("X-Trace-Id", "1234567890abcdef1234567890abcdef");
              exchange.sendResponseHeaders(
                  scenario.matches("[245]\\d\\d") ? Integer.parseInt(scenario) : 200, raw.length);
              try (var out = exchange.getResponseBody()) {
                out.write(raw);
              }
              exchange.close();
            });
        server.start();
        try {
          var models =
              new WorkspaceManagedModels(
                  service,
                  null,
                  WorkspaceService.JSON
                      .createObjectNode()
                      .put(
                          "modelBaseUrl",
                          "http://127.0.0.1:" + server.getAddress().getPort() + "/ai/v1"));
          if (scenario.equals("200")) {
            assertNotNull(method.invoke(models, response, row));
            assertTrue(appender.list.isEmpty());
          } else {
            assertThrows(
                InvocationTargetException.class, () -> method.invoke(models, response, row));
            String json = new JsonLogFormatter().format(appender.list.getFirst());
            assertTrue(json.contains(row.get("id").toString()));
            assertTrue(json.contains("generation=210"));
            String reason =
                switch (scenario) {
                  case "empty" -> "empty_catalog";
                  case "invalid" -> "invalid_catalog";
                  case "duplicate" -> "duplicate_model_id";
                  case "json" -> "invalid_json";
                  default -> "http_status=" + scenario;
                };
            assertTrue(json.contains(reason), json);
            assertFalse(json.contains("sentinel-managed-credential"));
            assertFalse(json.contains("sentinel-directory-body"));
            assertTrue(json.contains("upstream_trace=1234567890abcdef1234567890abcdef"));
            assertFalse(json.contains("{\\\"data\\\""));
          }
        } finally {
          server.stop(0);
        }
      }
      appender.list.clear();
      new WorkspaceManagedModels(
              null,
              null,
              WorkspaceService.JSON
                  .createObjectNode()
                  .put("modelBaseUrl", "http://127.0.0.1:1/ai/v1"))
          .reconcile(row, IssueManagedDeviceResponse.getDefaultInstance());
      assertTrue(
          appender.list.getFirst().getFormattedMessage().contains("model_credential_not_issued"));
      appender.list.clear();
      ManagedModelDiagnostic.failure(
          row,
          "reconcile",
          "transport",
          0,
          System.nanoTime(),
          new IllegalStateException(
              "connection reset password=sentinel-password ticket=\"sentinel ticket with spaces\" AURIGA_FOO_TOKEN=sentinel-env Bearer sentinel-bearer",
              new RuntimeException("credential=sentinel-credential")),
          "");
      String json = new JsonLogFormatter().format(appender.list.getFirst());
      assertFalse(json.contains("sentinel-"), json);
      assertTrue(json.contains("connection reset"));
      assertTrue(json.contains("Caused by"));
      assertTrue(json.contains("ManagedModelDiagnosticTest"));
    } finally {
      logger.detachAppender(appender);
      appender.stop();
    }
  }
}
