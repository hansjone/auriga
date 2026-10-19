package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;

import com.nimbusds.jose.*;
import com.nimbusds.jose.crypto.MACSigner;
import com.nimbusds.jwt.JWTClaimsSet;
import com.nimbusds.jwt.SignedJWT;
import com.sun.net.httpserver.HttpServer;
import java.net.*;
import java.net.http.HttpClient;
import java.nio.charset.StandardCharsets;
import java.time.*;
import java.util.*;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.Executors;
import java.util.concurrent.atomic.AtomicReference;
import org.junit.jupiter.api.*;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

class OidcConnectorTest {
  private static final byte[] HMAC_KEY = new byte[32];

  static {
    Arrays.fill(HMAC_KEY, (byte) 7);
  }

  private final Clock clock = Clock.fixed(Instant.parse("2026-06-01T00:00:00Z"), ZoneOffset.UTC);
  private final ContractSchemas schemas = new ContractSchemas();
  private HttpServer server;
  private String issuer;
  private final Map<String, String> verifiers = new ConcurrentHashMap<>();
  private final Set<String> codes = ConcurrentHashMap.newKeySet();
  private final AtomicReference<String> expectedNonce = new AtomicReference<>("");
  private volatile boolean downDiscovery;

  @BeforeEach
  void startIdp() throws Exception {
    downDiscovery = false;
    verifiers.clear();
    codes.clear();
    expectedNonce.set("");
    server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
    server.setExecutor(Executors.newCachedThreadPool());
    issuer = "http://127.0.0.1:" + server.getAddress().getPort();
    server.createContext("/.well-known/openid-configuration", this::discovery);
    server.createContext("/token", this::token);
    server.createContext("/userinfo", this::userinfo);
    server.start();
  }

  @AfterEach
  void stopIdp() {
    if (server != null) server.stop(0);
  }

  private void discovery(com.sun.net.httpserver.HttpExchange ex) throws java.io.IOException {
    if (downDiscovery) {
      ex.sendResponseHeaders(503, -1);
      ex.close();
      return;
    }
    respond(
        ex,
        200,
        Json.object(
            "issuer",
            issuer,
            "authorization_endpoint",
            issuer + "/authorize",
            "token_endpoint",
            issuer + "/token",
            "userinfo_endpoint",
            issuer + "/userinfo"));
  }

  private void token(com.sun.net.httpserver.HttpExchange ex) throws java.io.IOException {
    Map<String, String> params =
        form(new String(ex.getRequestBody().readAllBytes(), StandardCharsets.UTF_8));
    String code = params.getOrDefault("code", "");
    String verifier = params.getOrDefault("code_verifier", "");
    if (!codes.contains(code) || !Secrets.same(verifier, verifiers.getOrDefault(code, "x"))) {
      respond(ex, 400, Json.object("error", "invalid_grant"));
      return;
    }
    codes.remove(code);
    try {
      respond(
          ex,
          200,
          Json.object(
              "access_token",
              "access-example",
              "token_type",
              "Bearer",
              "id_token",
              idToken(
                  issuer,
                  expectedNonce.get(),
                  "example-client",
                  "alice",
                  "Alice Example",
                  "alice@example.com")));
    } catch (JOSEException e) {
      throw new java.io.IOException(e);
    }
  }

  private void userinfo(com.sun.net.httpserver.HttpExchange ex) throws java.io.IOException {
    String auth = ex.getRequestHeaders().getFirst("Authorization");
    if (!"Bearer access-example".equals(auth)) {
      ex.sendResponseHeaders(401, -1);
      ex.close();
      return;
    }
    respond(
        ex,
        200,
        Json.object(
            "sub",
            "alice",
            "name",
            "Alice Example",
            "email",
            "alice@example.com",
            "email_verified",
            true));
  }

  private static void respond(com.sun.net.httpserver.HttpExchange ex, int status, JsonNode json)
      throws java.io.IOException {
    byte[] body = json.toString().getBytes(StandardCharsets.UTF_8);
    ex.getResponseHeaders().add("Content-Type", "application/json");
    ex.sendResponseHeaders(status, body.length);
    ex.getResponseBody().write(body);
    ex.close();
  }

  private OidcConnector connector() {
    return new OidcConnector(
        HttpClient.newBuilder()
            .connectTimeout(Duration.ofSeconds(5))
            .followRedirects(HttpClient.Redirect.NEVER)
            .build(),
        clock);
  }

  private ObjectNode config() {
    return Json.object(
        "issuer",
        issuer,
        "clientId",
        "example-client",
        "clientSecret",
        "example-secret",
        "scopes",
        List.of("openid", "profile", "email"));
  }

  private ObjectNode ctx(JsonNode state) {
    return Json.object(
        "txId",
        "01936f00-0000-7000-8000-000000000099",
        "tenantId",
        "01936f00-0000-7000-8000-000000000020",
        "providerId",
        "01936f00-0000-7000-8000-000000000002",
        "purpose",
        "login",
        "locale",
        "en",
        "callbackUrl",
        "https://portal.example.com/api/iam/v1/login/callback/01936f00-0000-7000-8000-000000000002",
        "state",
        state,
        "expiresAt",
        clock.instant().plusSeconds(300).toString());
  }

  @Test
  void descriptorAndConfigSchemaValidate() {
    var connector = connector();
    schemas.validate(
        "identity/connector.schema.json#/$defs/ConnectorDescriptor", connector.descriptor());
    assertEquals("oidc", connector.descriptor().path("type").asString());
    assertTrue(connector.descriptor().path("multipleInstances").asBoolean());
    assertTrue(
        connector
            .descriptor()
            .path("configSchema")
            .path("properties")
            .path("clientSecret")
            .path("x-secret")
            .asBoolean());
  }

  @Test
  void invalidConfigRejectedBySchema() {
    var registry = new ConnectorRegistry(schemas, List.of(connector()));
    assertThrows(
        RuntimeException.class, () -> registry.validate("oidc", Json.object("issuer", issuer)));
  }

  @Test
  void discoveryFailureRejectsValidateConfig() {
    downDiscovery = true;
    assertThrows(RuntimeException.class, () -> connector().validateConfig(config()));
  }

  @Test
  void beginRedirectIncludesPkceAndAdvanceReturnsProfile() throws Exception {
    var connector = connector();
    connector.validateConfig(config());
    JsonNode begin = connector.begin(ctx(null), config());
    schemas.validate("identity/connector.schema.json#/$defs/ConnectorResult", begin);
    assertEquals("redirect", begin.path("step").path("type").asString());
    Map<String, String> q = query(URI.create(begin.path("step").path("url").asString()));
    assertEquals("S256", q.get("code_challenge_method"));
    assertEquals(43, q.get("code_challenge").length());
    assertTrue(q.get("scope").contains("openid"));
    assertEquals(
        "https://portal.example.com/api/iam/v1/login/callback/01936f00-0000-7000-8000-000000000002",
        q.get("redirect_uri"));

    String code = "auth-code-example";
    codes.add(code);
    verifiers.put(code, begin.path("state").path("codeVerifier").asString());
    expectedNonce.set(begin.path("state").path("nonce").asString());

    JsonNode done =
        connector.advance(
            ctx(begin.path("state")),
            config(),
            Json.object(
                "type",
                "callback",
                "params",
                Json.object(
                    "code", code, "state", begin.path("state").path("oauthState").asString())));
    schemas.validate("identity/connector.schema.json#/$defs/ConnectorResult", done);
    assertEquals("done", done.path("step").path("type").asString());
    assertEquals("alice", done.path("step").path("profile").path("subject").asString());
    assertEquals("alice@example.com", done.path("step").path("profile").path("email").asString());
    assertTrue(done.path("step").path("profile").path("emailVerified").asBoolean());
  }

  @Test
  void badStateFailsWithExternalDenied() {
    JsonNode begin = connector().begin(ctx(null), config());
    JsonNode fail =
        connector()
            .advance(
                ctx(begin.path("state")),
                config(),
                Json.object(
                    "type",
                    "callback",
                    "params",
                    Json.object("code", "x", "state", "wrong-state")));
    assertEquals("iam.external_denied", fail.path("step").path("code").asString());
  }

  @Test
  void badPkceFailsWithExternalDenied() {
    JsonNode begin = connector().begin(ctx(null), config());
    ObjectNode state = ((ObjectNode) begin.path("state")).deepCopy();
    String code = "code-bad-pkce";
    codes.add(code);
    verifiers.put(code, begin.path("state").path("codeVerifier").asString());
    state.put("codeVerifier", "a".repeat(43));
    expectedNonce.set(state.path("nonce").asString());
    JsonNode fail =
        connector()
            .advance(
                ctx(state),
                config(),
                Json.object(
                    "type",
                    "callback",
                    "params",
                    Json.object("code", code, "state", state.path("oauthState").asString())));
    assertEquals("iam.external_denied", fail.path("step").path("code").asString());
  }

  @Test
  void idpErrorParamFailsWithExternalDenied() {
    JsonNode begin = connector().begin(ctx(null), config());
    JsonNode fail =
        connector()
            .advance(
                ctx(begin.path("state")),
                config(),
                Json.object(
                    "type",
                    "callback",
                    "params",
                    Json.object(
                        "error",
                        "access_denied",
                        "state",
                        begin.path("state").path("oauthState").asString())));
    assertEquals("iam.external_denied", fail.path("step").path("code").asString());
  }

  @Test
  void unreachableIssuerOnBeginFailsUnavailable() {
    JsonNode fail =
        connector()
            .begin(
                ctx(null),
                Json.object(
                    "issuer",
                    "http://127.0.0.1:1",
                    "clientId",
                    "example-client",
                    "clientSecret",
                    "example-secret"));
    assertEquals("iam.connector_unavailable", fail.path("step").path("code").asString());
  }

  private static String idToken(
      String issuer, String nonce, String clientId, String sub, String name, String email)
      throws JOSEException {
    SignedJWT jwt =
        new SignedJWT(
            new JWSHeader(JWSAlgorithm.HS256),
            new JWTClaimsSet.Builder()
                .issuer(issuer)
                .subject(sub)
                .audience(clientId)
                .claim("nonce", nonce)
                .claim("name", name)
                .claim("email", email)
                .claim("email_verified", true)
                .expirationTime(Date.from(Instant.parse("2026-06-01T01:00:00Z")))
                .issueTime(Date.from(Instant.parse("2026-06-01T00:00:00Z")))
                .build());
    jwt.sign(new MACSigner(HMAC_KEY));
    return jwt.serialize();
  }

  private static Map<String, String> query(URI uri) {
    Map<String, String> out = new LinkedHashMap<>();
    if (uri.getRawQuery() == null) return out;
    for (String pair : uri.getRawQuery().split("&")) {
      String[] parts = pair.split("=", 2);
      out.put(
          URLDecoder.decode(parts[0], StandardCharsets.UTF_8),
          parts.length > 1 ? URLDecoder.decode(parts[1], StandardCharsets.UTF_8) : "");
    }
    return out;
  }

  private static Map<String, String> form(String body) {
    Map<String, String> out = new LinkedHashMap<>();
    if (body == null || body.isBlank()) return out;
    for (String pair : body.split("&")) {
      String[] parts = pair.split("=", 2);
      out.put(
          URLDecoder.decode(parts[0], StandardCharsets.UTF_8),
          parts.length > 1 ? URLDecoder.decode(parts[1], StandardCharsets.UTF_8) : "");
    }
    return out;
  }
}
