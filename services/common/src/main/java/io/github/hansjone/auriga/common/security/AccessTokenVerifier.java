package io.github.hansjone.auriga.common.security;

import io.github.hansjone.auriga.common.error.AurigaException;
import io.github.hansjone.auriga.common.error.CommonErrors;
import java.time.Instant;
import java.util.List;
import java.util.Locale;
import java.util.Objects;
import java.util.Set;
import java.util.UUID;
import org.springframework.security.oauth2.jwt.Jwt;
import org.springframework.security.oauth2.jwt.JwtDecoder;
import org.springframework.security.oauth2.jwt.JwtException;

/** Validates a raw access token and maps its claims to an {@link AurigaPrincipal}. */
public final class AccessTokenVerifier {
  private final JwtDecoder decoder;

  public AccessTokenVerifier(JwtDecoder decoder) {
    this.decoder = Objects.requireNonNull(decoder);
  }

  /**
   * @throws AurigaException {@code common.unauthenticated} for any invalid token
   */
  public AurigaPrincipal verify(String token) {
    if (token == null || token.isBlank()) throw new AurigaException(CommonErrors.UNAUTHENTICATED);
    try {
      return toPrincipal(decoder.decode(token));
    } catch (JwtException | IllegalArgumentException e) {
      throw new AurigaException(CommonErrors.UNAUTHENTICATED, null, null, e);
    }
  }

  /** Maps validated claims; the decoder from {@link AccessTokenDecoders} guarantees presence. */
  public static AurigaPrincipal toPrincipal(Jwt jwt) {
    String tid = jwt.getClaimAsString("tid");
    UUID tenantId = UUID.fromString(tid);
    if (!tenantId.toString().equals(tid.toLowerCase(Locale.ROOT))) {
      throw new IllegalArgumentException("tid is not a canonical UUID");
    }
    List<String> perms = jwt.getClaimAsStringList("perms");
    List<String> roles = jwt.getClaimAsStringList("roles");
    return new AurigaPrincipal(
        jwt.getSubject(),
        tenantId,
        PrincipalType.fromClaim(jwt.getClaimAsString("ptyp")),
        TokenContext.fromClaim(jwt.getClaimAsString("ctx")),
        jwt.getClaimAsString("sid"),
        jwt.getClaimAsString("did"),
        jwt.getClaimAsString("mtyp"),
        roles == null ? List.of() : roles,
        perms == null ? Set.of() : Set.copyOf(perms),
        epochSeconds(jwt, "auth_time"),
        epochSeconds(jwt, "stepup_time"),
        jwt.getId());
  }

  private static Instant epochSeconds(Jwt jwt, String claim) {
    Object value = jwt.getClaims().get(claim);
    if (value == null) return null;
    if (value instanceof Instant i) return i;
    if (value instanceof Number n) return Instant.ofEpochSecond(n.longValue());
    throw new IllegalArgumentException(claim + " must be a number");
  }
}
