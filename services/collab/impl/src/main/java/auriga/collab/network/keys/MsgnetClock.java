package auriga.collab.network.keys;

import auriga.common.error.AurigaException;
import auriga.common.error.ErrorCode;
import io.grpc.Status;
import java.time.Clock;
import java.time.Duration;
import java.time.Instant;
import java.util.Objects;
import java.util.function.LongSupplier;

/**
 * Process-local wall/monotonic comparison. Database samples must come from the committing
 * transaction.
 */
public final class MsgnetClock {
  private static final ErrorCode CLOCK_ERROR =
      ErrorCode.of("collab.msgnet_clock_error", 503, Status.Code.UNAVAILABLE);
  private final Clock wall;
  private final LongSupplier monotonic;
  private Instant baselineWall;
  private long baselineTick;
  private Instant previousWall;
  private long previousTick;
  private boolean failed;
  private int stableSamples;

  public MsgnetClock(Clock wall) {
    this(wall, System::nanoTime);
  }

  MsgnetClock(Clock wall, LongSupplier monotonic) {
    this.wall = Objects.requireNonNull(wall);
    this.monotonic = Objects.requireNonNull(monotonic);
    previousWall = wall.instant();
    previousTick = monotonic.getAsLong();
    baselineWall = previousWall;
    baselineTick = previousTick;
  }

  /**
   * Returns a checked whole-second timestamp or fails before any signing/first-publication write.
   */
  public synchronized Instant sample(Instant databaseTime) {
    Instant now = wall.instant();
    long tick = monotonic.getAsLong();
    long elapsed = tick - previousTick;
    Duration error = Duration.between(previousWall, now).minusNanos(elapsed);
    Duration baselineError = Duration.between(baselineWall, now).minusNanos(tick - baselineTick);
    Duration checkedError = failed ? error : baselineError;
    boolean aligned =
        databaseTime != null
            && Duration.between(databaseTime, now).abs().compareTo(Duration.ofSeconds(60)) <= 0;
    boolean jump =
        elapsed < 0
            || checkedError.compareTo(Duration.ofSeconds(300)) > 0
            || checkedError.compareTo(Duration.ofSeconds(-60)) < 0;
    if (!aligned || jump) {
      failed = true;
      stableSamples = 0;
      previousWall = now;
      previousTick = tick;
      throw new AurigaException(CLOCK_ERROR);
    }
    if (failed) {
      // Calls less than 30s apart cannot manufacture the two recovery observations.
      if (elapsed >= Duration.ofSeconds(30).toNanos()) {
        stableSamples = error.abs().compareTo(Duration.ofSeconds(5)) <= 0 ? stableSamples + 1 : 0;
        previousWall = now;
        previousTick = tick;
      }
      if (stableSamples < 2) throw new AurigaException(CLOCK_ERROR);
      failed = false;
      baselineWall = now;
      baselineTick = tick;
    }
    previousWall = now;
    previousTick = tick;
    return now.truncatedTo(java.time.temporal.ChronoUnit.SECONDS);
  }

  public synchronized boolean clockError() {
    return failed;
  }
}
