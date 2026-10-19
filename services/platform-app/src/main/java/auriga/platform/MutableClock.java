package auriga.platform;

import java.time.Clock;
import java.time.Duration;
import java.time.Instant;
import java.time.ZoneId;
import java.util.concurrent.atomic.AtomicReference;

/** Dev/bootstrap clock so JWKS activation can advance past the propagation window. */
public final class MutableClock extends Clock {
  private final ZoneId zone;
  private final AtomicReference<Instant> instant;

  public MutableClock() {
    this(ZoneId.of("UTC"), Instant.now());
  }

  public MutableClock(ZoneId zone, Instant start) {
    this.zone = zone;
    this.instant = new AtomicReference<>(start);
  }

  public void advance(Duration amount) {
    instant.updateAndGet(i -> i.plus(amount));
  }

  @Override
  public ZoneId getZone() {
    return zone;
  }

  @Override
  public Clock withZone(ZoneId zone) {
    return new MutableClock(zone, instant.get());
  }

  @Override
  public Instant instant() {
    return instant.get();
  }
}
