package auriga.iam.api;

/** Verified identity keys preserve leading zeroes and case; only outer whitespace is removed. */
public final class UacEmployee {
  private UacEmployee() {}

  public static String canonical(String value) {
    if (value == null) throw new IllegalArgumentException("Missing verified employee identifier");
    String normalized = value.strip();
    if (normalized.isEmpty()
        || normalized.length() > 128
        || normalized.chars().anyMatch(Character::isISOControl))
      throw new IllegalArgumentException("Invalid verified employee identifier");
    return normalized;
  }

  /** For collision protection only, never used as the persistent identity key. */
  public static String asciiFold(String value) {
    StringBuilder out = new StringBuilder();
    value.chars().forEach(c -> out.append((char) (c >= 'A' && c <= 'Z' ? c + 32 : c)));
    return out.toString();
  }
}
