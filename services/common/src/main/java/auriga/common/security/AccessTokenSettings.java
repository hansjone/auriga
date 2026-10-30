package auriga.common.security;

import java.net.InetAddress;
import java.net.URI;
import java.net.UnknownHostException;
import java.time.Clock;
import java.time.Duration;
import java.util.List;
import java.util.Locale;
import java.util.Objects;
import java.util.Set;

/**
 * How access tokens issued by iam are validated.
 *
 * @param issuer expected {@code iss} (the OIDC issuer URL)
 * @param jwkSetUri iam's {@code /oauth2/jwks}; must be https unless it points at a loopback address
 *     (or {@code allowInsecureJwksUri} is set explicitly)
 * @param audience audience every platform token must contain ({@code auriga})
 * @param clockSkew tolerance for exp/nbf (30 seconds)
 * @param jwksCacheTtl how long a fetched key set is trusted before re-fetching
 * @param jwksMinRefreshInterval minimum time between re-fetches triggered by unknown key ids
 * @param clock time source
 * @param allowInsecureJwksUri accept a plain http JWKS URI on a non-loopback host (keys could be
 *     replaced by anyone on the network path; only for isolated networks)
 * @param acceptedIssuers exact installation-owned issuer aliases sharing the fixed JWKS source
 */
public record AccessTokenSettings(
    String issuer,
    URI jwkSetUri,
    String audience,
    Duration clockSkew,
    Duration jwksCacheTtl,
    Duration jwksMinRefreshInterval,
    Clock clock,
    boolean allowInsecureJwksUri,
    Set<String> acceptedIssuers) {
  public static final String PLATFORM_AUDIENCE = "auriga";
  public static final String INTERNAL_AUDIENCE = "auriga.internal";
  public static final String ACCESS_TOKEN_TYPE = "at+jwt";

  public AccessTokenSettings {
    Objects.requireNonNull(issuer, "issuer");
    Objects.requireNonNull(jwkSetUri, "jwkSetUri");
    acceptedIssuers =
        auriga.common.web.LinkOrigins.issuerOrigins(
            issuer,
            acceptedIssuers == null
                ? List.of()
                : acceptedIssuers.stream().filter(value -> !issuer.equals(value)).toList());
    audience = audience == null ? PLATFORM_AUDIENCE : audience;
    clockSkew = clockSkew == null ? Duration.ofSeconds(30) : clockSkew;
    jwksCacheTtl = jwksCacheTtl == null ? Duration.ofMinutes(5) : jwksCacheTtl;
    jwksMinRefreshInterval =
        jwksMinRefreshInterval == null ? Duration.ofSeconds(30) : jwksMinRefreshInterval;
    clock = clock == null ? Clock.systemUTC() : clock;
    requireSecureJwksUri(jwkSetUri, allowInsecureJwksUri);
  }

  public AccessTokenSettings(
      String issuer,
      URI jwkSetUri,
      String audience,
      Duration clockSkew,
      Duration jwksCacheTtl,
      Duration jwksMinRefreshInterval,
      Clock clock,
      boolean allowInsecureJwksUri) {
    this(
        issuer,
        jwkSetUri,
        audience,
        clockSkew,
        jwksCacheTtl,
        jwksMinRefreshInterval,
        clock,
        allowInsecureJwksUri,
        Set.of(issuer));
  }

  public AccessTokenSettings withIssuerOrigins(List<String> origins) {
    return new AccessTokenSettings(
        issuer,
        jwkSetUri,
        audience,
        clockSkew,
        jwksCacheTtl,
        jwksMinRefreshInterval,
        clock,
        allowInsecureJwksUri,
        auriga.common.web.LinkOrigins.issuerOrigins(issuer, origins));
  }

  public static AccessTokenSettings of(String issuer, URI jwkSetUri) {
    return new AccessTokenSettings(issuer, jwkSetUri, null, null, null, null, null, false);
  }

  /** Same settings, different required audience. */
  public AccessTokenSettings withAudience(String newAudience) {
    return new AccessTokenSettings(
        issuer,
        jwkSetUri,
        newAudience,
        clockSkew,
        jwksCacheTtl,
        jwksMinRefreshInterval,
        clock,
        allowInsecureJwksUri,
        acceptedIssuers);
  }

  static void requireSecureJwksUri(URI uri, boolean allowInsecure) {
    String scheme = uri.getScheme() == null ? "" : uri.getScheme().toLowerCase(Locale.ROOT);
    if (scheme.equals("https")) return;
    if (!scheme.equals("http")) {
      throw new IllegalArgumentException("JWKS URI must be http(s): " + uri);
    }
    if (allowInsecure || isLoopback(uri.getHost())) return;
    throw new IllegalArgumentException(
        "JWKS URI on a non-loopback host must use https (or set allow-insecure-jwks-uri): " + uri);
  }

  /** Literal loopback addresses and {@code localhost} only; no DNS lookups. */
  static boolean isLoopback(String host) {
    if (host == null || host.isBlank()) return false;
    String h = host.startsWith("[") ? host.substring(1, host.length() - 1) : host;
    if (h.equalsIgnoreCase("localhost")) return true;
    if (!h.matches("[0-9.]+") && !h.contains(":")) return false;
    try {
      return InetAddress.getByName(h).isLoopbackAddress();
    } catch (UnknownHostException e) {
      return false;
    }
  }
}
