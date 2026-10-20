package auriga.workspace.domain;

import java.time.Duration;
import java.time.Instant;

/** Conservative decisions: missing or stale activity never authorizes reclamation. */
public final class Lifecycle {
  public static final Duration WAKE_LEAD = Duration.ofSeconds(120);
  private static final Duration MAX_ALARM_IDLE_EXTENSION = WAKE_LEAD.multipliedBy(2);

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
    return nextWake != null && !nextWake.isAfter(now.plus(WAKE_LEAD));
  }

  public static boolean alarmBudgetAvailable(Instant now, Instant activity, Duration timeout) {
    return now.isBefore(activity.plus(timeout).plus(MAX_ALARM_IDLE_EXTENSION));
  }

  public static boolean alarmSoon(
      Instant now, Instant nextWake, Instant activity, Duration timeout) {
    return alarmBudgetAvailable(now, activity, timeout)
        && nextWake != null
        && nextWake.isAfter(now)
        && !nextWake.isAfter(now.plus(WAKE_LEAD).plus(WAKE_LEAD));
  }
}
