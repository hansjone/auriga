package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.error.AurigaException;
import com.nimbusds.jose.*;
import com.nimbusds.jose.crypto.RSASSASigner;
import com.nimbusds.jose.jwk.*;
import com.nimbusds.jose.jwk.gen.RSAKeyGenerator;
import com.nimbusds.jwt.JWTClaimsSet;
import com.nimbusds.jwt.SignedJWT;
import com.sun.net.httpserver.HttpServer;
import java.net.*;
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
  private RSAKey signingKey;
  private RSAKey advertisedKey;
  private java.util.concurrent.ExecutorService executor;
  private String infoSub = "alice";
  private boolean infoVerified = true;
  private boolean signedVerified = true;
  private java.util.function.UnaryOperator<JWTClaimsSet> tokenClaims =
      java.util.function.UnaryOperator.identity();
  private String tokenEndpoint;
  private String tokenOverride;
  private boolean largeDiscovery;
  private boolean largeToken;
  private boolean largeUserinfo;
  private boolean largeJwks;
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
    signingKey = new RSAKeyGenerator(2048).keyID("test-key").generate();
    advertisedKey = signingKey;
    downDiscovery = false;
    verifiers.clear();
    codes.clear();
    expectedNonce.set("");
    server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
    executor = Executors.newCachedThreadPool();
    server.setExecutor(executor);
    issuer = "http://127.0.0.1:" + server.getAddress().getPort();
    server.createContext("/.well-known/openid-configuration", this::discovery);
    server.createContext("/token", this::token);
    server.createContext("/userinfo", this::userinfo);
    server.createContext(
        "/jwks",
        ex ->
            respond(
                ex,
                200,
                largeJwks
                    ? Json.object("large", "x".repeat(OidcHttp.MAX_BODY))
                    : Json.read(new JWKSet(advertisedKey.toPublicJWK()).toString())));
    server.start();
  }

  @AfterEach
  void stopIdp() {
    if (server != null) server.stop(0);
    if (executor != null) executor.shutdownNow();
  }

  private void discovery(com.sun.net.httpserver.HttpExchange ex) throws java.io.IOException {
    if (downDiscovery) {
      ex.sendResponseHeaders(503, -1);
      ex.close();
      return;
    }
    if (largeDiscovery) {
      respond(ex, 200, Json.object("data", "x".repeat(OidcHttp.MAX_BODY)));
      return;
    }
    respond(
        ex,
        200,
        Json.object(
            "jwks_uri",
            issuer + "/jwks",
            "issuer",
            issuer,
            "authorization_endpoint",
            issuer + "/authorize",
            "token_endpoint",
            tokenEndpoint == null ? issuer + "/token" : tokenEndpoint,
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
    if (largeToken) {
      respond(ex, 200, Json.object("data", "x".repeat(OidcHttp.MAX_BODY)));
      return;
    }
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
              tokenOverride != null
                  ? tokenOverride
                  : idToken(
                      issuer,
                      expectedNonce.get(),
                      "example-client",
                      "alice",
                      "Alice Example",
                      "alice@example.com")));
    } catch (JOSEException | java.text.ParseException e) {
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
    if (largeUserinfo) {
      respond(ex, 200, Json.object("data", "x".repeat(OidcHttp.MAX_BODY)));
      return;
    }
    respond(
        ex,
        200,
        Json.object(
            "sub",
            infoSub,
            "name",
            "Alice Example",
            "email",
            "alice@example.com",
            "email_verified",
            infoVerified));
  }

  private static void respond(com.sun.net.httpserver.HttpExchange ex, int status, JsonNode json)
      throws java.io.IOException {
    byte[] body = json.toString().getBytes(StandardCharsets.UTF_8);
    ex.getResponseHeaders().add("Content-Type", "application/json");
    ex.sendResponseHeaders(status, body.length > OidcHttp.MAX_BODY ? 0 : body.length);
    ex.getResponseBody().write(body);
    ex.close();
  }

  private OidcConnector connector() {
    return new OidcConnector(true, clock);
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

  private JsonNode authenticate(OidcConnector connector) {
    JsonNode begin = connector.begin(ctx(null), config());
    String code = UUID.randomUUID().toString();
    codes.add(code);
    verifiers.put(code, begin.path("state").path("codeVerifier").asString());
    expectedNonce.set(begin.path("state").path("nonce").asString());
    return connector.advance(
        ctx(begin.path("state")),
        config(),
        Json.object(
            "type",
            "callback",
            "params",
            Json.object("code", code, "state", begin.path("state").path("oauthState").asString())));
  }

  @Test
  void jwksWrongSigningKeyIsRejected() throws Exception {
    advertisedKey = new RSAKeyGenerator(2048).keyID("test-key").generate();
    assertEquals(
        "iam.external_denied", authenticate(connector()).path("step").path("code").asString());
  }

  @Test
  void unsignedAndHmacTokensAreRejected() throws Exception {
    tokenOverride =
        new com.nimbusds.jwt.PlainJWT(new JWTClaimsSet.Builder().subject("alice").build())
            .serialize();
    assertEquals(
        "iam.external_denied", authenticate(connector()).path("step").path("code").asString());
    byte[] secret = new byte[32];
    new java.security.SecureRandom().nextBytes(secret);
    SignedJWT jwt =
        new SignedJWT(
            new JWSHeader(JWSAlgorithm.HS256), new JWTClaimsSet.Builder().subject("alice").build());
    jwt.sign(new com.nimbusds.jose.crypto.MACSigner(secret));
    tokenOverride = jwt.serialize();
    assertEquals(
        "iam.external_denied", authenticate(connector()).path("step").path("code").asString());
  }

  @Test
  void mismatchedUserinfoSubjectIsRejected() {
    infoSub = "mallory";
    assertEquals(
        "iam.external_denied", authenticate(connector()).path("step").path("code").asString());
  }

  @Test
  void signedEmailClaimsCannotBeOverwritten() {
    infoVerified = false;
    assertTrue(
        authenticate(connector()).path("step").path("profile").path("emailVerified").asBoolean());
  }

  @Test
  void insecureIssuerAndPrivateAddressesAreRejected() throws Exception {
    assertEquals(
        "common.validation_failed",
        assertThrows(AurigaException.class, () -> new OidcConnector().validateConfig(config()))
            .errorCode()
            .code());
    var transport = new OidcHttp(false);
    for (String ip :
        List.of(
            "127.0.0.1",
            "10.0.0.1", // gitleaks:allow
            "169.254.169.254",
            "172.16.0.1", // gitleaks:allow
            "192.168.0.1", // gitleaks:allow
            "100.64.0.1",
            "::1",
            "fc00::1",
            "fe80::1",
            "::ffff:127.0.0.1")) {
      assertFalse(OidcHttp.publicAddress(InetAddress.getByName(ip)), ip);
      assertThrows(UnknownHostException.class, () -> transport.resolve(ip), ip);
    }
    assertEquals(
        "common.validation_failed",
        assertThrows(
                AurigaException.class,
                () -> new OidcHttp(true).validate(URI.create("http://192.0.2.1")))
            .errorCode()
            .code());
  }

  @Test
  void crossOriginDiscoveryEndpointCanCompleteLogin() {
    tokenEndpoint = "http://localhost:" + server.getAddress().getPort() + "/token";
    assertEquals("done", authenticate(connector()).path("step").path("type").asString());
  }

  @Test
  void insecureDiscoveredEndpointIsStillRejected() {
    tokenEndpoint = "http://idp.example.com/token";
    assertEquals(
        "common.validation_failed",
        assertThrows(AurigaException.class, () -> connector().validateConfig(config()))
            .errorCode()
            .code());
  }

  @Test
  void manualEndpointOverridesCannotBypassDiscovery() {
    var config = config();
    config.put("token_endpoint", "https://other.example.com/token");
    var registry = new ConnectorRegistry(schemas, List.of(connector()));
    assertEquals(
        "common.validation_failed",
        assertThrows(AurigaException.class, () -> registry.validate("oidc", config))
            .errorCode()
            .code());
  }

  @Test
  void productionDiscoveryAcceptsHttpsEndpointsOnDifferentOrigins() throws Exception {
    var http = org.mockito.Mockito.mock(OidcHttp.class);
    org.mockito.Mockito.doCallRealMethod().when(http).validate(org.mockito.ArgumentMatchers.any());
    org.mockito.Mockito.when(
            http.send(
                org.mockito.ArgumentMatchers.any(),
                org.mockito.ArgumentMatchers.eq("GET"),
                org.mockito.ArgumentMatchers.isNull(),
                org.mockito.ArgumentMatchers.anyMap()))
        .thenReturn(
            new OidcHttp.Response(
                200,
                Json.object(
                        "issuer",
                        "https://idp.example.com",
                        "authorization_endpoint",
                        "https://authorize.example.com/auth",
                        "token_endpoint",
                        "https://tokens.example.com/token",
                        "userinfo_endpoint",
                        "https://profile.example.com/userinfo",
                        "jwks_uri",
                        "https://keys.example.com/jwks")
                    .toString()));
    var document = new OidcMetadata(http, clock).get("https://idp.example.com");
    assertEquals("tokens.example.com", document.tokenEndpoint().getHost());
    assertEquals("keys.example.com", document.jwksUri().getHost());
    assertEquals("profile.example.com", document.userinfoEndpoint().getHost());
  }

  @Test
  void oversizedDiscoveryIsRejected() {
    largeDiscovery = true;
    assertEquals(
        "iam.connector_unavailable",
        assertThrows(AurigaException.class, () -> connector().validateConfig(config()))
            .errorCode()
            .code());
  }

  @Test
  void oversizedTokenUserinfoAndJwksAreRejected() {
    largeToken = true;
    assertEquals(
        "iam.connector_unavailable",
        authenticate(connector()).path("step").path("code").asString());
    largeToken = false;
    largeUserinfo = true;
    assertEquals(
        "iam.connector_unavailable",
        authenticate(connector()).path("step").path("code").asString());
    largeUserinfo = false;
    largeJwks = true;
    assertEquals(
        "iam.connector_unavailable",
        authenticate(connector()).path("step").path("code").asString());
  }

  @Test
  void userinfoCannotElevateSignedEmailVerification() {
    signedVerified = false;
    infoVerified = true;
    assertFalse(
        authenticate(connector()).path("step").path("profile").path("emailVerified").asBoolean());
  }

  @Test
  void signedWrongIssuerAudienceNonceAndExpiredTokensAreRejected() {
    List<java.util.function.UnaryOperator<JWTClaimsSet>> invalid =
        List.of(
            c -> new JWTClaimsSet.Builder(c).issuer("https://other.example.com").build(),
            c -> new JWTClaimsSet.Builder(c).audience("other-client").build(),
            c -> new JWTClaimsSet.Builder(c).claim("nonce", "wrong-nonce").build(),
            c ->
                new JWTClaimsSet.Builder(c)
                    .expirationTime(Date.from(clock.instant().minusSeconds(31)))
                    .build(),
            c ->
                new JWTClaimsSet.Builder(c)
                    .audience(List.of("example-client", "other-client"))
                    .claim("azp", "other-client")
                    .build());
    for (var mutation : invalid) {
      tokenClaims = mutation;
      assertEquals(
          "iam.external_denied", authenticate(connector()).path("step").path("code").asString());
    }
  }

  @Test
  void transportRejectsPrivateLiteralAtSocketResolution() {
    assertThrows(
        UnknownHostException.class,
        () -> new OidcHttp(false).send(URI.create("https://127.0.0.1:1"), "GET", null, Map.of()));
  }

  private String idToken(
      String issuer, String nonce, String clientId, String sub, String name, String email)
      throws JOSEException, java.text.ParseException {
    SignedJWT jwt =
        new SignedJWT(
            new JWSHeader.Builder(JWSAlgorithm.RS256).keyID("test-key").build(),
            new JWTClaimsSet.Builder()
                .issuer(issuer)
                .subject(sub)
                .audience(clientId)
                .claim("nonce", nonce)
                .claim("name", name)
                .claim("email", email)
                .claim("email_verified", signedVerified)
                .expirationTime(Date.from(Instant.parse("2026-06-01T01:00:00Z")))
                .issueTime(Date.from(Instant.parse("2026-06-01T00:00:00Z")))
                .build());
    jwt = new SignedJWT(jwt.getHeader(), tokenClaims.apply(jwt.getJWTClaimsSet()));
    jwt.sign(new RSASSASigner(signingKey));
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
