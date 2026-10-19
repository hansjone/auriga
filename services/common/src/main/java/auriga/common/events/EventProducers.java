package auriga.common.events;

import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;

/**
 * Which service may produce which event type (contracts/events/README.md, "发布方" column). Consumers
 * drop events whose {@code source} is not a legitimate producer, so a compromised or buggy service
 * can not forge another service's events (NATS publish ACLs are the first line).
 *
 * <p>Keys are exact types ({@code user.disabled}) or prefixes ({@code user.*}); values are service
 * names ({@code org}). {@code audit.recorded} is not listed: every service may audit, and the
 * consumer instead checks that the subject suffix equals the source service.
 */
public final class EventProducers {
  /** The contract's event table at the time of writing. */
  public static final Map<String, List<String>> CONTRACT_DEFAULTS =
      Map.of(
          "user.*", List.of("org"),
          "identity.*", List.of("iam"),
          "instance.*", List.of("workspace"));

  private final Map<String, Set<String>> producers;

  private EventProducers(Map<String, Set<String>> producers) {
    this.producers = Map.copyOf(producers);
  }

  /** Contract defaults plus/overridden by {@code overrides} (same key replaces the default). */
  public static EventProducers of(Map<String, List<String>> overrides) {
    Map<String, Set<String>> map = new HashMap<>();
    CONTRACT_DEFAULTS.forEach((k, v) -> map.put(k, Set.copyOf(v)));
    if (overrides != null) overrides.forEach((k, v) -> map.put(k, Set.copyOf(v)));
    return new EventProducers(map);
  }

  /** True when {@code source} ({@code auriga/<service>}) may publish {@code type}. */
  public boolean allows(String type, String source) {
    if (type == null || source == null || !source.startsWith("auriga/")) return false;
    String service = source.substring("auriga/".length());
    Set<String> exact = producers.get(type);
    if (exact != null) return exact.contains(service);
    int dot = type.indexOf('.');
    if (dot < 0) return false;
    Set<String> prefix = producers.get(type.substring(0, dot) + ".*");
    return prefix != null && prefix.contains(service);
  }
}
