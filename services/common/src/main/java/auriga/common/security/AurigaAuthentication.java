package auriga.common.security;

import java.util.Objects;
import org.springframework.core.convert.converter.Converter;
import org.springframework.security.authentication.AbstractAuthenticationToken;
import org.springframework.security.core.authority.SimpleGrantedAuthority;
import org.springframework.security.oauth2.jwt.Jwt;

/**
 * Spring Security authentication for a validated access token. The raw token is not retained:
 * services never forward user tokens (service to service calls use service tokens).
 */
public final class AurigaAuthentication extends AbstractAuthenticationToken {
  private final AurigaPrincipal principal;

  public AurigaAuthentication(AurigaPrincipal principal) {
    super(
        principal.permissions().stream()
            .map(p -> new SimpleGrantedAuthority("PERM_" + p))
            .toList());
    this.principal = Objects.requireNonNull(principal);
    setAuthenticated(true);
  }

  @Override
  public Object getCredentials() {
    return null;
  }

  @Override
  public AurigaPrincipal getPrincipal() {
    return principal;
  }

  @Override
  public String getName() {
    return principal.subject();
  }

  @Override
  public boolean equals(Object o) {
    return o instanceof AurigaAuthentication other && principal.equals(other.principal);
  }

  @Override
  public int hashCode() {
    return principal.hashCode();
  }

  /** Converter plugged into {@code oauth2ResourceServer().jwt()}. */
  public static Converter<Jwt, AurigaAuthentication> converter() {
    return jwt -> new AurigaAuthentication(AccessTokenVerifier.toPrincipal(jwt));
  }
}
