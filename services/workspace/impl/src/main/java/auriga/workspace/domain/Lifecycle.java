package auriga.workspace.domain;

import java.time.Duration;
import java.time.Instant;

/** Conservative decisions: missing or stale activity never authorizes reclamation. */
public final class Lifecycle {
  public static final Duration WAKE_LEAD = Duration.ofSeconds(120);
  private static final Duration MAX_ALARM_IDLE_EXTENSION = WAKE_LEAD.multipliedBy(2);
  public static final Duration MIN_STOPPED = Duration.ofSeconds(30);
  public static final Duration MAX_WAKE_HORIZON = Duration.ofDays(400);

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

  /**
   * Whether an imminent alarm keeps a running instance up. Within one idle period any alarm counts
   * until {@code activity + timeout + 4 min}; after that only an alarm already reported before that
   * deadline, or too soon for a stop and pre-warm, still counts. Both must be due at most 4 minutes
   * past the deadline, so a moving alarm can never extend the period (absolute cap: {@code activity
   * + timeout + 8 min}).
   */
  public static boolean alarmSoon(
      Instant now, Instant nextWake, Instant reportedAt, Instant activity, Duration timeout) {
    if (nextWake == null
        || !nextWake.isAfter(now)
        || nextWake.isAfter(now.plus(MAX_ALARM_IDLE_EXTENSION))) return false;
    if (alarmBudgetAvailable(now, activity, timeout)) return true;
    Instant deadline = activity.plus(timeout).plus(MAX_ALARM_IDLE_EXTENSION);
    return reportedAt != null
        && !nextWake.isAfter(deadline.plus(MAX_ALARM_IDLE_EXTENSION))
        && (reportedAt.isBefore(deadline)
            || !nextWake.isAfter(now.plus(MIN_STOPPED).plus(WAKE_LEAD)));
  }

  /**
   * Due time of the single wake job kept when the idle deadline stops an instance whose alarm is
   * still in the future: its pre-warm job may already have been consumed while running. Never
   * earlier than {@link #MIN_STOPPED} from now, so the stop is not undone in the same decision
   * loop. Returns null when no future alarm remains.
   */
  public static Instant rewakeDue(Instant now, Instant nextWake) {
    if (nextWake == null || !nextWake.isAfter(now)) return null;
    Instant due = nextWake.minus(WAKE_LEAD), earliest = now.plus(MIN_STOPPED);
    return due.isBefore(earliest) ? earliest : due;
  }

  /**
   * Accepted alarm reports: at most {@link #WAKE_LEAD} in the past (clock skew, report delay) and
   * at most {@link #MAX_WAKE_HORIZON} ahead (yearly schedules). Null means no alarm.
   */
  public static boolean wakeInRange(Instant now, Instant nextWake) {
    return nextWake == null
        || !nextWake.isBefore(now.minus(WAKE_LEAD))
            && !nextWake.isAfter(now.plus(MAX_WAKE_HORIZON));
  }
}
