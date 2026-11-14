package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.error.AurigaException;
import com.sun.net.httpserver.HttpServer;
import java.net.*;
import java.nio.charset.StandardCharsets;
import java.util.*;
import java.util.concurrent.atomic.*;
import org.junit.jupiter.api.Test;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

class UacConfiguredAdapterTest {
  @Test
  void qrTimeoutIsConfigurableAndCancelsTheUpstreamRequest() throws Exception {
    var server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
    var interrupted = new java.util.concurrent.CountDownLatch(1);
    server.createContext(
        "/qr",
        exchange -> {
          try {
            interrupted.await(5, java.util.concurrent.TimeUnit.SECONDS);
            exchange.sendResponseHeaders(200, 0);
          } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
          } finally {
            exchange.close();
          }
        });
    server.start();
    try {
      String origin = "http://127.0.0.1:" + server.getAddress().getPort();
      var settings = settings(origin);
      settings.withObject("/qr").put("timeoutSeconds", 1);
      var adapter = new UacConfiguredAdapter(settings, true);
      long start = System.nanoTime();
      var result =
          adapter.scan(
              Json.object("baseUrl", origin, "systemCode", "test-system"),
              Json.object("x", "0".repeat(32), "y", "0".repeat(32)));
      assertEquals("unavailable", result.path("state").asString());
      assertTrue(java.time.Duration.ofNanos(System.nanoTime() - start).toSeconds() < 4);
      settings.withObject("/qr").put("timeoutSeconds", 0);
      assertThrows(IllegalArgumentException.class, () -> new UacConfiguredAdapter(settings, true));
    } finally {
      interrupted.countDown();
      server.stop(0);
    }
  }

  @Test
  void privateDerivedQrIntegrityAndStaticIpAreValidatedWithoutBrowserOverrides() throws Exception {
    var server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
    AtomicReference<JsonNode> observed = new AtomicReference<>();
    server.createContext(
        "/qr",
        exchange -> {
          observed.set(Json.MAPPER.readTree(exchange.getRequestBody().readAllBytes()));
          byte[] body = Json.object("state", "waiting").toString().getBytes(StandardCharsets.UTF_8);
          exchange.sendResponseHeaders(200, body.length);
          exchange.getResponseBody().write(body);
          exchange.close();
        });
    server.start();
    try {
      String origin = "http://127.0.0.1:" + server.getAddress().getPort();
      var privateConfig = settings(origin);
      privateConfig.put("clientIP", "192.0.2.10");
      privateConfig.set("constants", Json.object("origin", ""));
      var qr = (ObjectNode) privateConfig.path("qr");
      qr.set(
          "derived",
          Json.object(
              "integrity",
              Json.object(
                  "algorithm", "md5", "template", "${x}${y}${clientIP}${systemCode}${origin}")));
      ((ObjectNode) qr.path("body")).put("checksum", "${integrity}");
      var adapter = new UacConfiguredAdapter(privateConfig, true);
      assertEquals(
          Set.of("portalCookieConfigured", "udsCookieConfigured"),
          adapter.diagnosticCapabilities().keySet());
      privateConfig.set("diagnostics", Json.object("originSystemCodeEmpty", true));
      assertEquals(
          Boolean.TRUE,
          new UacConfiguredAdapter(privateConfig, true)
              .diagnosticCapabilities()
              .get("originSystemCodeEmpty"));
      var config = Json.object("baseUrl", origin, "systemCode", "test-system");
      adapter.validateConfig(config);
      String x = Secrets.hash(Secrets.random()).substring(0, 32),
          y = Secrets.hash(Secrets.random()).substring(0, 32);
      adapter.scan(config, Json.object("x", x, "y", y));
      assertEquals(
          HexFormat.of()
              .formatHex(
                  java.security.MessageDigest.getInstance("MD5")
                      .digest(
                          (x + y + "192.0.2.10" + "test-system").getBytes(StandardCharsets.UTF_8))),
          observed.get().path("checksum").asString());
      ((ObjectNode) qr.path("derived").path("integrity"))
          .put("template", "${untrustedBrowserValue}");
      assertEquals(
          "common.validation_failed",
          assertThrows(
                  AurigaException.class,
                  () -> new UacConfiguredAdapter(privateConfig, true).validateConfig(config))
              .errorCode()
              .code());
      for (String invalid : List.of("example.com", "1.2.3.999", "127.0.0.1%zone"))
        assertThrows(
            IllegalArgumentException.class,
            () -> new UacConfiguredAdapter(settings(origin).put("clientIP", invalid), true));
      var escaped = settings(origin);
      escaped.put("tokenVerifyUrl", "https://elsewhere.example.com/verify");
      assertEquals(
          "common.validation_failed",
          assertThrows(
                  AurigaException.class,
                  () -> new UacConfiguredAdapter(escaped, true).validateConfig(config))
              .errorCode()
              .code());
    } finally {
      server.stop(0);
    }
  }

  static ObjectNode settings(String origin) {
    return Json.object(
        "allowedDestinations",
        List.of(Json.object("origin", origin, "addresses", List.of("127.0.0.1"))),
        "clientIP",
        "",
        "tokenVerifyUrl",
        origin + "/verify",
        "qr",
        Json.object(
            "path",
            "/qr",
            "contentTemplate",
            "uac-test:${x}:${y}:${systemCode}",
            "body",
            Json.object("first", "${x}", "second", "${y}", "system", "${systemCode}"),
            "statusPointer",
            "/state",
            "waiting",
            "waiting",
            "scanned",
            "scanned",
            "success",
            "success",
            "tokenPointer",
            "/token",
            "empNoPointer",
            "/employee"),
        "department",
        Json.object(
            "url",
            origin + "/department",
            "body",
            Json.object("token", "${token}", "employee", "${empNo}"),
            "successPointer",
            "/ok",
            "successValue",
            true,
            "listPointer",
            "/rows",
            "empNoPointer",
            "/employee",
            "departmentPointers",
            List.of("/department", "/fallback")),
        "cookies",
        Json.object(
            "portal",
            Json.object(
                "token",
                Json.object("cookie", "test_portal_token"),
                "account",
                Json.object("cookie", "test_portal_account")),
            "uds",
            Json.object(
                "token",
                Json.object("cookie", "test_uds_token"),
                "account",
                Json.object("cookie", "test_uds_account"))));
  }

  @Test
  void translatesTrustedQrDepartmentAndTokenVerifyWithRealHttp() throws Exception {
    var server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
    String origin = "http://127.0.0.1:" + server.getAddress().getPort(), token = Secrets.random();
    var requests = new AtomicInteger();
    var reply = new AtomicReference<JsonNode>(Json.object("state", "waiting"));
    server.createContext(
        "/",
        exchange -> {
          requests.incrementAndGet();
          JsonNode body = Json.MAPPER.readTree(exchange.getRequestBody().readAllBytes());
          JsonNode result = reply.get();
          if (exchange.getRequestURI().getPath().equals("/verify")) {
            assertEquals("001Ab", exchange.getRequestHeaders().getFirst("X-Emp-No"));
            assertEquals(5, body.size());
            assertTrue(body.has("clientIP"));
            result =
                Json.object(
                    "code",
                    Json.object("code", "0000"),
                    "bo",
                    Json.object("code", "0000", "user", "001aB", "token", token));
          }
          byte[] bytes = result.toString().getBytes(StandardCharsets.UTF_8);
          exchange.sendResponseHeaders(200, bytes.length);
          exchange.getResponseBody().write(bytes);
          exchange.close();
        });
    server.start();
    try {
      var adapter = new UacConfiguredAdapter(settings(origin), true);
      var config = Json.object("baseUrl", origin, "systemCode", "test-system");
      adapter.validateConfig(config);
      new ContractSchemas()
          .validate(
              "identity/connector.schema.json#/$defs/ConnectorDescriptor", adapter.descriptor());
      var coordinates = Json.object("x", "coordinate-x", "y", "coordinate-y");
      assertEquals(
          "uac-test:coordinate-x:coordinate-y:test-system", adapter.qrContent(config, coordinates));
      assertEquals("waiting", adapter.scan(config, coordinates).path("state").asString());
      reply.set(Json.object("state", "scanned"));
      assertEquals("scanned", adapter.scan(config, coordinates).path("state").asString());
      reply.set(Json.object("state", "success", "employee", " 001Ab ", "token", token));
      var credential = adapter.scan(config, coordinates);
      assertEquals("001Ab", credential.path("empNo").asString());
      assertEquals(token, adapter.verify(config, credential).path("token").asString());
      reply.set(
          Json.object(
              "ok",
              true,
              "rows",
              List.of(Json.object("employee", "001Ab", "fallback", "Test department"))));
      assertTrue(
          adapter.department(config, credential, "login_info").path("available").asBoolean());
      reply.set(
          Json.object(
              "ok",
              true,
              "rows",
              List.of(
                  Json.object("employee", "001Ab", "department", "A"),
                  Json.object("employee", "001Ab", "department", "B"))));
      assertFalse(
          adapter.department(config, credential, "revalidate").path("available").asBoolean());
      reply.set(Json.object("state", "success", "employee", "001Ab"));
      assertEquals(
          "missing_token", adapter.scan(config, coordinates).path("failureCode").asString());
      assertEquals(7, requests.get());
    } finally {
      server.stop(0);
    }
  }

  @Test
  void ssoUsesCompletePortalPairFirstAndNeverMixesOrIgnoresDuplicateCookies() {
    var adapter = new UacConfiguredAdapter(settings("https://idp.example.com"), false);
    var cookies =
        Json.object(
            "test_portal_token",
            List.of("portal-token"),
            "test_uds_account",
            List.of("uds-account"));
    assertTrue(adapter.ssoCookies(cookies).isEmpty());
    cookies.set("test_uds_token", Json.MAPPER.valueToTree(List.of("uds-token")));
    assertEquals("uds-token", adapter.ssoCookies(cookies).path("token").asString());
    cookies.set("test_portal_account", Json.MAPPER.valueToTree(List.of("portal-account")));
    assertEquals("portal-token", adapter.ssoCookies(cookies).path("token").asString());
    cookies.set("test_portal_token", Json.MAPPER.valueToTree(List.of("first", "second")));
    assertTrue(adapter.ssoCookies(cookies).isEmpty());
  }

  @Test
  void malformedPresentPortalCookieDoesNotSilentlySwitchToUds() {
    var mapping = settings("https://idp.example.com");
    ((ObjectNode) mapping.path("cookies").path("portal").path("account"))
        .put("pointer", "/account");
    var adapter = new UacConfiguredAdapter(mapping, false);
    var cookies =
        Json.object(
            "test_portal_token",
            List.of("portal-token"),
            "test_portal_account",
            List.of("invalid-json"),
            "test_uds_token",
            List.of("uds-token"),
            "test_uds_account",
            List.of("uds-account"));
    assertTrue(adapter.ssoCookies(cookies).isEmpty());
  }

  @Test
  void destinationAndAddressAllowlistFailClosedAndRedirectsAreNotFollowed() throws Exception {
    var server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
    var reached = new AtomicInteger();
    server.createContext(
        "/",
        e -> {
          reached.incrementAndGet();
          e.getResponseHeaders().set("Location", "/unexpected");
          e.sendResponseHeaders(302, -1);
          e.close();
        });
    server.start();
    try {
      String origin = "http://127.0.0.1:" + server.getAddress().getPort();
      var settings = settings(origin);
      assertThrows(
          AurigaException.class,
          () -> new UacHttp(settings, false).validate(URI.create(origin), true));
      var http = new UacHttp(settings, true);
      assertThrows(
          AurigaException.class, () -> http.validate(URI.create(origin + "?secret=1"), true));
      assertThrows(
          AurigaException.class,
          () ->
              http.validate(
                  URI.create("http://localhost:" + server.getAddress().getPort()), false));
      assertEquals(302, http.post(URI.create(origin), Json.object(), Map.of(), 3).status());
      assertEquals(1, reached.get());
      settings.set(
          "allowedDestinations",
          Json.MAPPER.valueToTree(
              List.of(Json.object("origin", origin, "addresses", List.of("192.0.2.1")))));
      assertThrows(
          java.io.IOException.class,
          () -> new UacHttp(settings, true).post(URI.create(origin), Json.object(), Map.of(), 3));
      assertEquals(1, reached.get());
    } finally {
      server.stop(0);
    }
  }
}
