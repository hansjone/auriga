package auriga.common.security;

import java.util.Objects;

/**
 * Verifier for internal service tokens only (gRPC), kept as its own type so it can never be mixed
 * up with the platform {@link AccessTokenVerifier} used by the HTTP API.
 */
public record InternalTokenVerifier(AccessTokenVerifier verifier) {
  public InternalTokenVerifier {
    Objects.requireNonNull(verifier);
  }
}
