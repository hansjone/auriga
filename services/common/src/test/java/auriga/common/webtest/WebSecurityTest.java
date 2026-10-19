package auriga.common.webtest;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.context.RequestContext;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.security.AdminOnly;
import auriga.common.security.AuthenticatedOnly;
import auriga.common.security.AuthorizationRules;
import auriga.common.security.PublicEndpoint;
import auriga.common.security.RequiresPermission;
import auriga.common.security.RequiresRecentStepUp;
import auriga.common.testing.TestTokens;
import jakarta.validation.Valid;
import jakarta.validation.constraints.Email;
import jakarta.validation.constraints.NotBlank;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.time.Instant;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.AfterAll;
import org.junit.jupiter.api.Test;
import org.springframework.beans.factory.annotation.Value;
import org.springframework.boot.SpringBootConfiguration;
import org.springframework.boot.autoconfigure.EnableAutoConfiguration;
import org.springframework.boot.jdbc.autoconfigure.DataSourceAutoConfiguration;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.context.annotation.Import;
import org.springframework.test.annotation.DirtiesContext;
import org.springframework.test.context.DynamicPropertyRegistry;
import org.springframework.test.context.DynamicPropertySource;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RestController;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.json.JsonMapper;

@SpringBootTest(
    classes = WebSecurityTest.App.class,
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT)
@DirtiesContext
class WebSecurityTest {
  private static final JsonMapper JSON = JsonMapper.builder().build();
  private static final TestTokens TOKENS;

  static {
    try {
      TOKENS = new TestTokens();
    } catch (Exception e) {
      throw new ExceptionInInitializerError(e);
    }
  }

  @Value("${local.server.port}")
  private int port;

  @DynamicPropertySource
  static void props(DynamicPropertyRegistry registry) {
    registry.add("auriga.security.jwt.issuer", () -> TestTokens.ISSUER);
    registry.add("auriga.security.jwt.jwk-set-uri", () -> TOKENS.jwksUri().toString());
  }

  @AfterAll
  static void stop() {
    TOKENS.close();
  }

  @SpringBootConfiguration
  @EnableAutoConfiguration(exclude = DataSourceAutoConfiguration.class)
  @Import({DemoController.class, PublicController.class})
  static class App {}

  record Item(@NotBlank String name, @Email String email) {}

  @RestController
  static class DemoController {
    @GetMapping("/api/demo/v1/me")
    @AuthenticatedOnly
    Map<String, Object> me() {
      return Map.of(
          "subject", AuthorizationRules.currentPrincipal().orElseThrow().subject(),
          "tenant", TenantContext.require().toString(),
          "requestId", RequestContext.current().orElseThrow().requestId(),
          "traceId", RequestContext.current().orElseThrow().trace().traceId());
    }

    @GetMapping("/api/demo/v1/members")
    @RequiresPermission("org.members.write")
    Map<String, Object> members() {
      return Map.of("ok", true);
    }

    @GetMapping("/api/demo/v1/admin/things")
    @AuthenticatedOnly
    Map<String, Object> adminThings() {
      return Map.of("ok", true);
    }

    @GetMapping("/api/demo/v1/annotated")
    @AdminOnly
    Map<String, Object> annotated() {
      return Map.of("ok", true);
    }

    @PostMapping("/api/demo/v1/admin/sensitive")
    @AuthenticatedOnly
    @RequiresRecentStepUp
    Map<String, Object> sensitive() {
      return Map.of("ok", true);
    }

    @PostMapping("/api/demo/v1/items")
    @AuthenticatedOnly
    Map<String, Object> create(@Valid @RequestBody Item item) {
      return Map.of("name", item.name());
    }

    @GetMapping("/api/demo/v1/things/{id}")
    @AuthenticatedOnly
    Map<String, Object> thing(@PathVariable String id) {
      throw AurigaException.of(CommonErrors.NOT_FOUND, "id", id);
    }

    @GetMapping("/api/demo/v1/boom")
    @AuthenticatedOnly
    Map<String, Object> boom() {
      throw new IllegalStateException("secret internal detail");
    }
  }

  @RestController
  static class PublicController {
    @GetMapping("/api/demo/v1/public/ping")
    @PublicEndpoint
    Map<String, Object> ping() {
      return Map.of("ok", true);
    }
  }

  /** Forgot to declare how {@code /forgotten} is authorized. */
  @RestController
  static class UndeclaredController {
    @GetMapping("/api/demo/v1/forgotten")
    Map<String, Object> forgotten() {
      return Map.of("ok", true);
    }
  }

  @RestController
  static class PublicAdminController {
    @GetMapping("/api/demo/v1/admin/open")
    @PublicEndpoint
    Map<String, Object> open() {
      return Map.of("ok", true);
    }
  }

  private HttpResponse<String> call(
      String method, String path, String token, Map<String, String> headers, String body)
      throws Exception {
    HttpRequest.Builder b =
        HttpRequest.newBuilder(URI.create("http://127.0.0.1:" + port + path))
            .method(
                method,
                body == null
                    ? HttpRequest.BodyPublishers.noBody()
                    : HttpRequest.BodyPublishers.ofString(body));
    if (token != null) b.header("Authorization", "Bearer " + token);
    if (body != null) b.header("Content-Type", "application/json");
    headers.forEach(b::header);
    try (HttpClient client = HttpClient.newHttpClient()) {
      return client.send(b.build(), HttpResponse.BodyHandlers.ofString());
    }
  }

  private HttpResponse<String> get(String path, String token) throws Exception {
    return call("GET", path, token, Map.of(), null);
  }

  private static JsonNode problem(HttpResponse<String> response, int status, String code) {
    assertEquals(status, response.statusCode(), response.body());
    assertEquals(
        "application/problem+json",
        response.headers().firstValue("Content-Type").orElse("").split(";")[0]);
    JsonNode json = JSON.readTree(response.body());
    assertEquals(code, json.get("code").asString());
    assertEquals(status, json.get("status").asInt());
    assertNotNull(json.get("title"));
    assertNotNull(json.get("detail"));
    assertTrue(json.get("traceId").asString().matches("[0-9a-f]{32}"), response.body());
    return json;
  }

  @Test
  void missingOrInvalidTokenIs401Problem() throws Exception {
    problem(get("/api/demo/v1/me", null), 401, "common.unauthenticated");
    HttpResponse<String> bad = get("/api/demo/v1/me", "not-a-jwt");
    problem(bad, 401, "common.unauthenticated");
    assertTrue(bad.headers().firstValue("WWW-Authenticate").orElse("").startsWith("Bearer"));
    problem(
        get("/api/demo/v1/me", TOKENS.token(c -> c.audience("third-party"))),
        401,
        "common.unauthenticated");
  }

  @Test
  void validTokenBindsPrincipalTenantAndCorrelation() throws Exception {
    HttpResponse<String> r =
        call(
            "GET",
            "/api/demo/v1/me",
            TOKENS.token(),
            Map.of(
                "X-Request-Id", "req-123",
                "traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"),
            null);
    assertEquals(200, r.statusCode(), r.body());
    JsonNode json = JSON.readTree(r.body());
    assertEquals(TestTokens.TENANT_A.toString(), json.get("tenant").asString());
    assertEquals("req-123", json.get("requestId").asString());
    assertEquals("4bf92f3577b34da6a3ce929d0e0e4736", json.get("traceId").asString());
    assertEquals("req-123", r.headers().firstValue("X-Request-Id").orElseThrow());
  }

  @Test
  void tenantHeaderCannotOverrideTokenTenant() throws Exception {
    HttpResponse<String> r =
        call(
            "GET",
            "/api/demo/v1/me",
            TOKENS.token(),
            Map.of("auriga-tenant-id", TestTokens.TENANT_B.toString()),
            null);
    assertEquals(TestTokens.TENANT_A.toString(), JSON.readTree(r.body()).get("tenant").asString());
  }

  @Test
  void missingPermissionIs403WithLocalizedDetail() throws Exception {
    JsonNode zh =
        problem(
            call(
                "GET",
                "/api/demo/v1/members",
                TOKENS.token(),
                Map.of("Accept-Language", "zh-CN"),
                null),
            403,
            "common.forbidden");
    assertEquals("没有权限执行这个操作。", zh.get("detail").asString());
    assertEquals("org.members.write", zh.get("params").get("permission").asString());
    JsonNode en = problem(get("/api/demo/v1/members", TOKENS.token()), 403, "common.forbidden");
    assertEquals("You do not have permission to perform this action.", en.get("detail").asString());
    assertEquals(
        200,
        get(
                "/api/demo/v1/members",
                TOKENS.token(c -> c.claim("perms", List.of("org.members.write"))))
            .statusCode());
  }

  @Test
  void adminPathsRequireAdminContext() throws Exception {
    problem(get("/api/demo/v1/admin/things", null), 401, "common.unauthenticated");
    JsonNode denied =
        problem(get("/api/demo/v1/admin/things", TOKENS.token()), 403, "common.forbidden");
    assertEquals("admin_only", denied.get("params").get("reason").asString());
    for (String ctx : List.of("device", "service")) {
      problem(
          get("/api/demo/v1/admin/things", TOKENS.token(c -> c.claim("ctx", ctx))),
          403,
          "common.forbidden");
    }
    assertEquals(
        200,
        get("/api/demo/v1/admin/things", TOKENS.token(c -> c.claim("ctx", "admin"))).statusCode());
    // trailing slash / case variations do not reach admin handlers unauthenticated
    assertNotEquals(200, get("/api/demo/v1/admin/things/", TOKENS.token()).statusCode());
    assertNotEquals(200, get("/api/demo/v1//admin/things", TOKENS.token()).statusCode());
    assertNotEquals(200, get("/api/demo/v1/./admin/things", TOKENS.token()).statusCode());
    assertNotEquals(200, get("/api/demo/v1/admin;x=1/things", TOKENS.token()).statusCode());
    assertNotEquals(200, get("/api/demo/v1/%61dmin/things", TOKENS.token()).statusCode());
  }

  @Test
  void adminOnlyAnnotationOutsideAdminPath() throws Exception {
    problem(get("/api/demo/v1/annotated", TOKENS.token()), 403, "common.forbidden");
    assertEquals(
        200,
        get("/api/demo/v1/annotated", TOKENS.token(c -> c.claim("ctx", "admin"))).statusCode());
  }

  @Test
  void stepUpRequired() throws Exception {
    long now = Instant.now().getEpochSecond();
    problem(
        call(
            "POST",
            "/api/demo/v1/admin/sensitive",
            TOKENS.token(c -> c.claim("ctx", "admin")),
            Map.of(),
            null),
        403,
        "iam.step_up_required");
    problem(
        call(
            "POST",
            "/api/demo/v1/admin/sensitive",
            TOKENS.token(c -> c.claim("ctx", "admin").claim("stepup_time", now - 700)),
            Map.of(),
            null),
        403,
        "iam.step_up_required");
    assertEquals(
        200,
        call(
                "POST",
                "/api/demo/v1/admin/sensitive",
                TOKENS.token(c -> c.claim("ctx", "admin").claim("stepup_time", now - 60)),
                Map.of(),
                null)
            .statusCode());
  }

  @Test
  void validationErrorsListFields() throws Exception {
    JsonNode json =
        problem(
            call(
                "POST",
                "/api/demo/v1/items",
                TOKENS.token(),
                Map.of(),
                "{\"name\":\"\",\"email\":\"x\"}"),
            400,
            "common.validation_failed");
    List<String> fields =
        json.get("errors").valueStream().map(e -> e.get("field").asString()).sorted().toList();
    assertEquals(List.of("/email", "/name"), fields);
    problem(
        call("POST", "/api/demo/v1/items", TOKENS.token(), Map.of(), "{broken"),
        400,
        "common.validation_failed");
  }

  @Test
  void businessAndUnexpectedErrors() throws Exception {
    JsonNode notFound =
        problem(get("/api/demo/v1/things/42", TOKENS.token()), 404, "common.not_found");
    assertEquals("42", notFound.get("params").get("id").asString());
    assertEquals("/api/demo/v1/things/42", notFound.get("instance").asString());
    HttpResponse<String> boom = get("/api/demo/v1/boom", TOKENS.token());
    problem(boom, 500, "common.internal");
    assertFalse(boom.body().contains("secret internal detail"));
    problem(get("/api/demo/v1/nothing-here", TOKENS.token()), 404, "common.not_found");
  }

  @Test
  void healthIsPublic() throws Exception {
    // actuator is not on this test classpath; the public path must not demand a token
    assertNotEquals(401, get("/actuator/health", null).statusCode());
  }

  @Test
  void publicEndpointNeedsNoToken() throws Exception {
    assertEquals(200, get("/api/demo/v1/public/ping", null).statusCode());
  }

  @Test
  void internalServiceTokensAreRejectedByTheHttpApi() throws Exception {
    problem(
        get("/api/demo/v1/me", TOKENS.token(TestTokens::internalService)),
        401,
        "common.unauthenticated");
    problem(
        get(
            "/api/demo/v1/me",
            TOKENS.token(
                c -> TestTokens.internalService(c).audience(List.of("auriga", "auriga.internal")))),
        401,
        "common.unauthenticated");
  }

  @org.springframework.context.annotation.Configuration(proxyBeanMethods = false)
  @EnableAutoConfiguration(exclude = DataSourceAutoConfiguration.class)
  static class Base {}

  private static org.springframework.boot.test.context.runner.WebApplicationContextRunner runner() {
    return new org.springframework.boot.test.context.runner.WebApplicationContextRunner()
        .withUserConfiguration(Base.class)
        .withPropertyValues(
            "auriga.security.jwt.issuer=" + TestTokens.ISSUER,
            "auriga.security.jwt.jwk-set-uri=" + TOKENS.jwksUri());
  }

  @Test
  void endpointWithoutAuthorizationDeclarationFailsStartup() {
    runner()
        .withUserConfiguration(UndeclaredController.class)
        .run(
            ctx -> {
              Throwable failure = ctx.getStartupFailure();
              assertNotNull(failure);
              assertTrue(
                  String.valueOf(
                          org.springframework.core.NestedExceptionUtils.getMostSpecificCause(
                                  failure)
                              .getMessage())
                      .contains("forgotten"),
                  String.valueOf(failure));
            });
    runner()
        .withUserConfiguration(PublicAdminController.class)
        .run(ctx -> assertNotNull(ctx.getStartupFailure()));
    runner()
        .withUserConfiguration(DemoController.class, PublicController.class)
        .run(ctx -> assertNull(ctx.getStartupFailure()));
  }
}
