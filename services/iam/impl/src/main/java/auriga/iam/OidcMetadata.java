package auriga.iam;

import java.net.URI;
import java.net.http.*;
import java.time.*;
import java.util.Objects;
import java.util.concurrent.ConcurrentHashMap;
import org.slf4j.LoggerFactory;
import tools.jackson.databind.JsonNode;

/** Lazily loads and caches OIDC discovery documents. Secrets are never logged. */
final class OidcMetadata {
  private static final Duration TTL = Duration.ofMinutes(10);
  private static final Duration TIMEOUT = Duration.ofSeconds(10);

  record Document(
      String issuer,
      URI authorizationEndpoint,
      URI tokenEndpoint,
      URI userinfoEndpoint,
      URI jwksUri,
      Instant fetchedAt) {}

  private final HttpClient http;
  private final Clock clock;
  private final ConcurrentHashMap<String, Document> cache = new ConcurrentHashMap<>();

  OidcMetadata(HttpClient http, Clock clock) {
    this.http = http;
    this.clock = clock;
  }

  Document get(String issuer) {
    String key = normalizeIssuer(issuer);
    Document cached = cache.get(key);
    if (cached != null && cached.fetchedAt().plus(TTL).isAfter(clock.instant())) return cached;
    Document fresh = fetch(key);
    cache.put(key, fresh);
    return fresh;
  }

  void invalidate(String issuer) {
    cache.remove(normalizeIssuer(issuer));
  }

  static String normalizeIssuer(String issuer) {
    String value = Objects.requireNonNull(issuer, "issuer").trim();
    while (value.endsWith("/")) value = value.substring(0, value.length() - 1);
    return value;
  }

  static void requireIssuerUri(String issuer) {
    URI uri;
    try {
      uri = URI.create(normalizeIssuer(issuer));
    } catch (IllegalArgumentException e) {
      throw IamError.error("invalid_config", 400);
    }
    IamError.require(
        ("https".equals(uri.getScheme()) || "http".equals(uri.getScheme()))
            && uri.getHost() != null
            && !uri.getHost().isBlank()
            && uri.getRawUserInfo() == null
            && uri.getRawQuery() == null
            && uri.getFragment() == null,
        "invalid_config",
        400);
  }

  private Document fetch(String issuer) {
    long started = System.nanoTime();
    String outcome = "failure";
    URI discovery = URI.create(issuer + "/.well-known/openid-configuration");
    try {
      HttpRequest request =
          HttpRequest.newBuilder(discovery)
              .timeout(TIMEOUT)
              .header("Accept", "application/json")
              .GET()
              .build();
      HttpResponse<String> response = http.send(request, HttpResponse.BodyHandlers.ofString());
      IamError.require(response.statusCode() == 200, "connector_unavailable", 502);
      JsonNode body = Json.MAPPER.readTree(response.body());
      String docIssuer = normalizeIssuer(body.path("issuer").asString(""));
      IamError.require(issuer.equals(docIssuer), "invalid_config", 400);
      URI authorization = absolute(issuer, body.path("authorization_endpoint").asString(""));
      URI token = absolute(issuer, body.path("token_endpoint").asString(""));
      IamError.require(authorization != null && token != null, "invalid_config", 400);
      URI userinfo = absolute(issuer, body.path("userinfo_endpoint").asString(""));
      URI jwks = absolute(issuer, body.path("jwks_uri").asString(""));
      outcome = "success";
      return new Document(issuer, authorization, token, userinfo, jwks, clock.instant());
    } catch (InterruptedException e) {
      Thread.currentThread().interrupt();
      throw IamError.error("connector_unavailable", 502);
    } catch (java.io.IOException e) {
      throw IamError.error("connector_unavailable", 502);
    } finally {
      LoggerFactory.getLogger(OidcMetadata.class)
          .info(
              "OIDC discovery issuerHost={} outcome={} elapsedMs={}",
              URI.create(issuer).getHost(),
              outcome,
              (System.nanoTime() - started) / 1_000_000);
    }
  }

  private static URI absolute(String issuer, String value) {
    if (value == null || value.isBlank()) return null;
    URI uri = URI.create(value);
    if (!uri.isAbsolute()) uri = URI.create(issuer + (value.startsWith("/") ? value : "/" + value));
    IamError.require(
        "https".equals(uri.getScheme()) || "http".equals(uri.getScheme()), "invalid_config", 400);
    return uri;
  }
}
