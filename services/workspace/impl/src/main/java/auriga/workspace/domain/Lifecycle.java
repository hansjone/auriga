package auriga.workspace.domain;

import java.time.Duration;
import java.time.Instant;

/** Conservative decisions: missing or stale activity never authorizes reclamation. */
public final class Lifecycle {
  private Lifecycle() {}

  public static Instant latestActivity(
      Instant current, Instant now, boolean active, Instant interaction) {
    Instant candidate = active ? now : interaction;
    if (candidate == null) return current;
    if (candidate.isAfter(now)) candidate = now;
    return candidate.isAfter(current) ? candidate : current;
  }

  public static boolean idle(
      Instant now,
      Instant activity,
      Instant pluginSeen,
      boolean pluginActive,
      Instant cpuSeen,
      boolean cpuActive,
      boolean alwaysOn,
      Duration timeout) {
    return !alwaysOn
        && !pluginActive
        && !cpuActive
        && pluginSeen != null
        && cpuSeen != null
        && !pluginSeen.isBefore(now.minusSeconds(90))
        && !cpuSeen.isBefore(now.minusSeconds(60))
        && !activity.isAfter(now.minus(timeout));
  }

  public static boolean wake(Instant now, Instant nextWake) {
    return nextWake != null && !nextWake.isAfter(now.plusSeconds(120));
  }
}
