package auriga.iam;

import org.springframework.core.env.Environment;
import tools.jackson.databind.node.ObjectNode;

/** Installation policy, validated before any UAC worker may start. */
final class UacRevalidationConfiguration {
  private static final String PREFIX = "auriga.iam.uac.revalidation.";
  private final ObjectNode policy;

  UacRevalidationConfiguration(Environment env) {
    policy = read(env);
    // Stable across replicas and restarts; never derive the revision from process start time.
    policy.put(
        "configRevision", Long.parseLong(Secrets.hash(policy.toString()).substring(0, 12), 16));
  }

  ObjectNode policy() {
    return policy.deepCopy();
  }

  static ObjectNode read(Environment env) {
    String strategy = env.getProperty(PREFIX + "strategy", "token-verify");
    if (!strategy.equals("token-verify") && !strategy.equals("department"))
      throw invalid("strategy");
    return Json.object(
        "strategy", strategy,
        "intervalSeconds", integer(env, "interval-seconds", 7200, 60, 86400),
        "retryIntervalSeconds", integer(env, "retry-interval-seconds", 30, 5, 300),
        "maxRetries", integer(env, "max-retries", 3, 0, 10));
  }

  private static int integer(Environment env, String name, int fallback, int min, int max) {
    String raw = env.getProperty(PREFIX + name);
    if (raw == null) return fallback;
    if (!raw.matches("[0-9]+")) throw invalid(name);
    try {
      int value = Integer.parseInt(raw);
      if (value < min || value > max) throw invalid(name);
      return value;
    } catch (NumberFormatException e) {
      throw invalid(name);
    }
  }

  private static IllegalArgumentException invalid(String name) {
    return new IllegalArgumentException("Invalid configuration: " + PREFIX + name);
  }
}
