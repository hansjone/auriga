package auriga.common.security;

import java.util.Locale;

/** Access token claim {@code ptyp}. */
public enum PrincipalType {
  USER,
  SERVICE_ACCOUNT,
  DEVICE,
  /** Built-in platform service; only in {@code aud=auriga.internal} service-to-service tokens. */
  INTERNAL_SERVICE;

  public String claimValue() {
    return name().toLowerCase(Locale.ROOT);
  }

  public static PrincipalType fromClaim(String value) {
    for (PrincipalType t : values()) if (t.claimValue().equals(value)) return t;
    throw new IllegalArgumentException("Unknown ptyp: " + value);
  }
}
