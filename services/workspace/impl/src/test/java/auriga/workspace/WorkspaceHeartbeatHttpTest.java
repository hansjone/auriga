package auriga.workspace;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import auriga.common.error.*;
import auriga.common.events.EventPublisher;
import auriga.workspace.api.WorkspaceInputs;
import auriga.workspace.app.WorkspaceService;
import auriga.workspace.app.WorkspaceTemplates;
import auriga.workspace.domain.*;
import auriga.workspace.persistence.WorkspaceStore;
import auriga.workspace.web.*;
import auriga.workspace.web.model.*;
import jakarta.servlet.http.HttpServletRequest;
import java.io.ByteArrayInputStream;
import java.net.*;
import java.net.http.*;
import java.nio.charset.StandardCharsets;
import java.time.Clock;
import java.time.Duration;
import java.util.*;
import org.junit.jupiter.api.*;
import org.springframework.boot.tomcat.servlet.TomcatServletWebServerFactory;
import org.springframework.boot.web.server.WebServer;
import org.springframework.context.annotation.*;
import org.springframework.core.env.MapPropertySource;
import org.springframework.web.context.support.AnnotationConfigWebApplicationContext;
import org.springframework.web.servlet.DispatcherServlet;
import org.springframework.web.servlet.config.annotation.EnableWebMvc;

class WorkspaceHeartbeatHttpTest {
  static final List<String> COUNTERS =
      List.of("runningTurns", "openTerminals", "activeImBots", "connectedClients");
  static WorkspaceService service;
  static WebServer server;
  static AnnotationConfigWebApplicationContext context;
  static URI uri;
  static final HttpClient HTTP =
      HttpClient.newBuilder().connectTimeout(Duration.ofSeconds(5)).build();

  @Configuration(proxyBeanMethods = false)
  @EnableWebMvc
  static class Mvc {
    @Bean
    WorkspacePluginController controller(HttpServletRequest request) {
      return new WorkspacePluginController(
          service, mock(WorkspaceInputs.class), request, new ConnectManifest());
    }

    @Bean
    WorkspaceResponseAdvice responseAdvice() {
      return new WorkspaceResponseAdvice();
    }

    @Bean
    WorkspaceHeartbeatRequestAdvice requestAdvice() {
      return new WorkspaceHeartbeatRequestAdvice();
    }

    @Bean
    ProblemExceptionHandler problems() {
      return new ProblemExceptionHandler(
          new Problems(new ErrorMessages(List.of(), Locale.ENGLISH)));
    }
  }

  @BeforeAll
  static void start() throws Exception {
    service =
        spy(
            new WorkspaceService(
                mock(WorkspaceStore.class),
                mock(WorkspaceInputs.class),
                mock(WorkspaceTemplates.class),
                mock(WorkspaceCrypto.class),
                mock(EventPublisher.class),
                Clock.systemUTC()));
    doReturn(Map.of("id", UUID.randomUUID(), "tenant_id", UUID.randomUUID(), "phase", "running"))
        .when(service)
        .authenticateInstance("http-test-token");
    doNothing().when(service).heartbeat(anyMap(), any(HeartbeatRequest.class));
    context = new AnnotationConfigWebApplicationContext();
    context
        .getEnvironment()
        .getPropertySources()
        .addFirst(new MapPropertySource("test", Map.of("auriga.platform.combined", "true")));
    context.register(Mvc.class);
    var factory = new TomcatServletWebServerFactory(0);
    factory.setAddress(InetAddress.getLoopbackAddress());
    server =
        factory.getWebServer(
            servletContext -> {
              var dispatcher =
                  servletContext.addServlet("dispatcher", new DispatcherServlet(context));
              dispatcher.setLoadOnStartup(1);
              dispatcher.addMapping("/");
            });
    server.start();
    uri =
        URI.create("http://127.0.0.1:" + server.getPort() + "/api/workspace/v1/instance/heartbeat");
  }

  @AfterAll
  static void stop() {
    HTTP.close();
    if (server != null) server.stop();
    if (context != null) context.close();
  }

  static Map<String, Object> body(Object value) {
    var activity = new LinkedHashMap<String, Object>();
    COUNTERS.forEach(name -> activity.put(name, value));
    return new LinkedHashMap<>(
        Map.of(
            "pluginVersion",
            "test",
            "dshVersion",
            "test",
            "activity",
            activity,
            "health",
            Map.of("ok", true)));
  }

  static HttpResponse<String> post(String json, boolean authenticated) throws Exception {
    return post(HttpRequest.BodyPublishers.ofString(json), authenticated);
  }

  static HttpResponse<String> post(HttpRequest.BodyPublisher body, boolean authenticated)
      throws Exception {
    var request =
        HttpRequest.newBuilder(uri)
            .timeout(Duration.ofSeconds(5))
            .version(HttpClient.Version.HTTP_1_1)
            .header("Content-Type", "application/json")
            .header("Auriga-Protocol", "1")
            .POST(body);
    if (authenticated) request.header("Authorization", "Bearer http-test-token");
    return HTTP.send(request.build(), HttpResponse.BodyHandlers.ofString());
  }

  static void invalid(HttpResponse<String> response) {
    assertEquals(400, response.statusCode(), response.body());
    assertEquals(
        "common.validation_failed",
        WorkspaceService.JSON.readTree(response.body()).get("code").asString());
  }

  @Test
  void explicitNullAndExistingIntegerBodiesReachAuthenticatedController() throws Exception {
    for (Object value : Arrays.asList(null, 0, 1)) {
      var response = post(WorkspaceService.JSON.writeValueAsString(body(value)), true);
      assertEquals(200, response.statusCode(), response.body());
    }
    var response = post(WorkspaceService.JSON.writeValueAsString(body(null)), false);
    assertEquals(401, response.statusCode());
    assertEquals(
        "common.unauthenticated",
        WorkspaceService.JSON.readTree(response.body()).get("code").asString());
  }

  @Test
  void everyMissingCounterAndEveryNegativeCounterAreRejected() throws Exception {
    for (String field : COUNTERS) {
      var missing = body(0);
      ((Map<?, ?>) missing.get("activity")).remove(field);
      invalid(post(WorkspaceService.JSON.writeValueAsString(missing), true));
      var negative = body(0);
      @SuppressWarnings("unchecked")
      var counts = (Map<String, Object>) negative.get("activity");
      counts.put(field, -1);
      invalid(post(WorkspaceService.JSON.writeValueAsString(negative), true));
    }
  }

  @Test
  void malformedJsonWrongCounterTypesAndMissingHealthAreRejected() throws Exception {
    invalid(post("{", true));
    invalid(post("", true));
    invalid(post(WorkspaceService.JSON.writeValueAsString(body(1.5)), true));
    invalid(post(WorkspaceService.JSON.writeValueAsString(body("0")), true));
    var absentHealth = body(0);
    absentHealth.remove("health");
    invalid(post(WorkspaceService.JSON.writeValueAsString(absentHealth), true));
  }

  @Test
  void bodyLimitChecksActualBytesWithContentLengthAndUnknownLengthChunked() throws Exception {
    var sized = body(0);
    sized.put("pluginVersion", "");
    int overhead = WorkspaceService.JSON.writeValueAsBytes(sized).length;
    sized.put("pluginVersion", "v".repeat(64 * 1024 - overhead));
    String exact = WorkspaceService.JSON.writeValueAsString(sized);
    assertEquals(64 * 1024, exact.getBytes(StandardCharsets.UTF_8).length);
    assertEquals(200, post(exact, true).statusCode());
    sized.put("pluginVersion", sized.get("pluginVersion") + "v");
    byte[] oversized = WorkspaceService.JSON.writeValueAsBytes(sized);
    assertEquals(64 * 1024 + 1, oversized.length);
    var known = HttpRequest.BodyPublishers.ofByteArray(oversized);
    assertEquals(oversized.length, known.contentLength());
    invalid(post(known, true));
    var chunked =
        HttpRequest.BodyPublishers.ofInputStream(() -> new ByteArrayInputStream(oversized));
    assertEquals(-1, chunked.contentLength());
    invalid(post(chunked, true));
  }
}
