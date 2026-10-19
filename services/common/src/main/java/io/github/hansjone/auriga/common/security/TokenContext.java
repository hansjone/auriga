package io.github.hansjone.auriga.common.security;

import java.util.Locale;

/** Access token claim {@code ctx}: which kind of session or credential minted the token. */
public enum TokenContext {
  PORTAL,
  ADMIN,
  DEVICE,
  SERVICE;

  public String claimValue() {
    return name().toLowerCase(Locale.ROOT);
  }

  public static TokenContext fromClaim(String value) {
    for (TokenContext t : values()) if (t.claimValue().equals(value)) return t;
    throw new IllegalArgumentException("Unknown ctx: " + value);
  }
}
