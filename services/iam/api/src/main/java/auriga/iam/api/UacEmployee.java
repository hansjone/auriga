package auriga.iam.api;

/** Employee identifiers preserve leading zeroes and fold ASCII case only. */
public final class UacEmployee {
  private UacEmployee() {}

  public static String canonical(String value) {
    if (value == null) throw new IllegalArgumentException("Missing verified employee identifier");
    String normalized = value.strip();
    if (normalized.isEmpty()
        || normalized.length() > 128
        || normalized.chars().anyMatch(Character::isISOControl))
      throw new IllegalArgumentException("Invalid verified employee identifier");
    StringBuilder result = new StringBuilder();
    normalized.chars().forEach(c -> result.append((char) (c >= 'a' && c <= 'z' ? c - 32 : c)));
    return result.toString();
  }
}
