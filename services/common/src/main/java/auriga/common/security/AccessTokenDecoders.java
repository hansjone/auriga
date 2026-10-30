package auriga.common.security;

import com.nimbusds.jose.JOSEObjectType;
import com.nimbusds.jose.JWSAlgorithm;
import com.nimbusds.jose.jwk.source.JWKSource;
import com.nimbusds.jose.jwk.source.JWKSourceBuilder;
import com.nimbusds.jose.proc.DefaultJOSEObjectTypeVerifier;
import com.nimbusds.jose.proc.JWSVerificationKeySelector;
import com.nimbusds.jose.proc.SecurityContext;
import com.nimbusds.jose.util.DefaultResourceRetriever;
import com.nimbusds.jwt.proc.DefaultJWTProcessor;
import java.net.MalformedURLException;
import java.util.ArrayList;
import java.util.Collection;
import java.util.List;
import java.util.UUID;
import org.springframework.security.oauth2.core.DelegatingOAuth2TokenValidator;
import org.springframework.security.oauth2.core.OAuth2Error;
import org.springframework.security.oauth2.core.OAuth2TokenValidator;
import org.springframework.security.oauth2.core.OAuth2TokenValidatorResult;
import org.springframework.security.oauth2.jwt.Jwt;
import org.springframework.security.oauth2.jwt.JwtDecoder;
import org.springframework.security.oauth2.jwt.JwtTimestampValidator;
import org.springframework.security.oauth2.jwt.NimbusJwtDecoder;

/**
 * Builds the {@link JwtDecoder} for iam access tokens (RFC 9068, contracts/auth).
 *
 * <ul>
 *   <li>JOSE header {@code typ} must be {@code at+jwt}: ID tokens and other JWTs are refused.
 *   <li>Only ES256 signatures with keys from iam's JWKS; the key set is cached (TTL) and re-fetched
 *       on an unknown {@code kid}, rate limited so random kids can not hammer iam.
 *   <li>{@code iss} must match; {@code aud} must contain {@code auriga} (tokens for third party
 *       OIDC clients only carry the client id and are refused).
 *   <li>{@code exp} is required; exp/nbf allow 30 seconds of clock skew.
 *   <li>The claims the platform relies on ({@code sub}, {@code tid}, {@code ptyp}, {@code ctx},
 *       {@code jti}, {@code iat}) must be present and well formed.
 * </ul>
 */
public final class AccessTokenDecoders {
  private AccessTokenDecoders() {}

  /** Cached, rate limited JWKS source; share one between the platform and internal decoders. */
  public static JWKSource<SecurityContext> jwkSource(AccessTokenSettings settings) {
    DefaultResourceRetriever retriever = new DefaultResourceRetriever(2_000, 2_000, 256 * 1024);
    try {
      return JWKSourceBuilder.<SecurityContext>create(settings.jwkSetUri().toURL(), retriever)
          .cache(settings.jwksCacheTtl().toMillis(), JWKSourceBuilder.DEFAULT_CACHE_REFRESH_TIMEOUT)
          .refreshAheadCache(false)
          .rateLimited(Math.max(1, settings.jwksMinRefreshInterval().toMillis()))
          .retrying(true)
          .build();
    } catch (MalformedURLException e) {
      throw new IllegalArgumentException("Invalid JWKS URI " + settings.jwkSetUri(), e);
    }
  }

  /** Decoder for the HTTP API: platform tokens ({@code aud} contains {@code auriga}). */
  public static JwtDecoder create(AccessTokenSettings settings) {
    return create(settings, jwkSource(settings));
  }

  public static JwtDecoder create(AccessTokenSettings settings, JWKSource<SecurityContext> source) {
    return decoder(settings, source, false);
  }

  /**
   * Decoder for internal gRPC: only internal service tokens ({@code aud} contains {@code
   * auriga.internal}, {@code ptyp=internal_service}, {@code ctx=service}, {@code sub=svc:<name>}).
   * External client credentials tokens (service accounts) can never pass it.
   */
  public static JwtDecoder createInternal(AccessTokenSettings settings) {
    return createInternal(settings, jwkSource(settings));
  }

  public static JwtDecoder createInternal(
      AccessTokenSettings settings, JWKSource<SecurityContext> source) {
    return decoder(settings.withAudience(AccessTokenSettings.INTERNAL_AUDIENCE), source, true);
  }

  private static JwtDecoder decoder(
      AccessTokenSettings settings, JWKSource<SecurityContext> source, boolean internal) {
    DefaultJWTProcessor<SecurityContext> processor = new DefaultJWTProcessor<>();
    processor.setJWSTypeVerifier(
        new DefaultJOSEObjectTypeVerifier<>(
            new JOSEObjectType(AccessTokenSettings.ACCESS_TOKEN_TYPE),
            new JOSEObjectType("application/" + AccessTokenSettings.ACCESS_TOKEN_TYPE)));
    processor.setJWSKeySelector(new JWSVerificationKeySelector<>(JWSAlgorithm.ES256, source));
    // Claims are checked by the Spring validators below (with our clock and skew).
    processor.setJWTClaimsSetVerifier((claims, context) -> {});
    NimbusJwtDecoder decoder = new NimbusJwtDecoder(processor);
    decoder.setJwtValidator(validator(settings, internal));
    return decoder;
  }

  static OAuth2TokenValidator<Jwt> validator(AccessTokenSettings settings, boolean internal) {
    JwtTimestampValidator timestamps = new JwtTimestampValidator(settings.clockSkew());
    timestamps.setClock(settings.clock());
    List<OAuth2TokenValidator<Jwt>> validators = new ArrayList<>();
    validators.add(AccessTokenDecoders::requireType);
    validators.add(AccessTokenDecoders::requireExpiry);
    validators.add(timestamps);
    validators.add(
        jwt ->
            settings.acceptedIssuers().contains(jwt.getClaimAsString("iss"))
                ? success()
                : failure("iss must match an approved installation origin"));
    validators.add(jwt -> audience(jwt, settings.audience()));
    validators.add(AccessTokenDecoders::platformClaims);
    validators.add(
        internal ? AccessTokenDecoders::internalClaims : AccessTokenDecoders::notInternal);
    return new DelegatingOAuth2TokenValidator<>(validators);
  }

  /** Defense in depth: the Nimbus type verifier already ran, check the parsed header again. */
  private static OAuth2TokenValidatorResult requireType(Jwt jwt) {
    Object typ = jwt.getHeaders().get("typ");
    if (typ instanceof String s
        && (s.equalsIgnoreCase(AccessTokenSettings.ACCESS_TOKEN_TYPE)
            || s.equalsIgnoreCase("application/" + AccessTokenSettings.ACCESS_TOKEN_TYPE))) {
      return OAuth2TokenValidatorResult.success();
    }
    return failure("typ must be at+jwt");
  }

  private static OAuth2TokenValidatorResult requireExpiry(Jwt jwt) {
    return jwt.getExpiresAt() == null ? failure("exp is required") : success();
  }

  private static OAuth2TokenValidatorResult audience(Jwt jwt, String required) {
    Collection<String> aud = jwt.getAudience();
    return aud != null && aud.contains(required)
        ? success()
        : failure("aud must contain " + required);
  }

  private static OAuth2TokenValidatorResult platformClaims(Jwt jwt) {
    if (isBlank(jwt.getSubject())) return failure("sub is required");
    if (isBlank(jwt.getId())) return failure("jti is required");
    if (jwt.getIssuedAt() == null) return failure("iat is required");
    try {
      UUID.fromString(jwt.getClaimAsString("tid"));
      PrincipalType.fromClaim(jwt.getClaimAsString("ptyp"));
      TokenContext.fromClaim(jwt.getClaimAsString("ctx"));
    } catch (RuntimeException e) {
      return failure("tid, ptyp and ctx must be present and valid");
    }
    return success();
  }

  /** Platform APIs never accept internal service tokens, whatever else their aud contains. */
  private static OAuth2TokenValidatorResult notInternal(Jwt jwt) {
    Collection<String> aud = jwt.getAudience();
    if (PrincipalType.INTERNAL_SERVICE.claimValue().equals(jwt.getClaimAsString("ptyp"))
        || (aud != null && aud.contains(AccessTokenSettings.INTERNAL_AUDIENCE))) {
      return failure("internal service tokens are not accepted here");
    }
    return success();
  }

  private static OAuth2TokenValidatorResult internalClaims(Jwt jwt) {
    Collection<String> aud = jwt.getAudience();
    boolean ok =
        PrincipalType.INTERNAL_SERVICE.claimValue().equals(jwt.getClaimAsString("ptyp"))
            && TokenContext.SERVICE.claimValue().equals(jwt.getClaimAsString("ctx"))
            && jwt.getSubject() != null
            && jwt.getSubject().matches("^svc:[a-z][a-z0-9-]*$")
            && aud != null
            && aud.size() == 1
            && aud.contains(AccessTokenSettings.INTERNAL_AUDIENCE)
            && "00000000-0000-0000-0000-000000000000".equals(jwt.getClaimAsString("tid"));
    return ok ? success() : failure("not an internal service token");
  }

  private static boolean isBlank(String s) {
    return s == null || s.isBlank();
  }

  private static OAuth2TokenValidatorResult success() {
    return OAuth2TokenValidatorResult.success();
  }

  private static OAuth2TokenValidatorResult failure(String description) {
    return OAuth2TokenValidatorResult.failure(new OAuth2Error("invalid_token", description, null));
  }
}
