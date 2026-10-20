package auriga.workspace.grpc;

import auriga.common.error.*;
import io.micrometer.core.instrument.MeterRegistry;
import java.time.*;
import java.util.*;

/** Fixed memory, source-IP admission before decoding Join; metrics never use IP as a label. */
public final class JoinRateLimiter {
  private static final int MAX_SOURCES = 4096;

  private static final class Bucket {
    double tokens = 10;
    Instant updated, blockedUntil = Instant.MIN;
    int failures;

    Bucket(Instant now) {
      updated = now;
    }
  }

  private final Clock clock;
  private final MeterRegistry metrics;
  private final Map<String, Bucket> sources = new HashMap<>();
  private double global = 100;
  private Instant globalTime;

  public JoinRateLimiter(Clock clock, MeterRegistry metrics) {
    this.clock = clock;
    this.metrics = metrics;
    globalTime = clock.instant();
  }

  public synchronized void admit(String source) {
    Instant now = clock.instant();
    global =
        Math.min(
            100, global + Math.max(0, Duration.between(globalTime, now).toMillis()) / 1000.0 * 20);
    globalTime = now;
    if (global < 1) reject();
    global--;
    Bucket bucket = sources.get(source);
    if (bucket == null) {
      if (sources.size() >= MAX_SOURCES)
        sources
            .entrySet()
            .removeIf(
                e ->
                    e.getValue().updated.isBefore(now.minusSeconds(120))
                        && e.getValue().blockedUntil.isBefore(now));
      if (sources.size() >= MAX_SOURCES) reject();
      bucket = new Bucket(now);
      sources.put(source, bucket);
    }
    bucket.tokens =
        Math.min(
            10,
            bucket.tokens + Math.max(0, Duration.between(bucket.updated, now).toMillis()) / 1000.0);
    bucket.updated = now;
    if (bucket.blockedUntil.isAfter(now) || bucket.tokens < 1) reject();
    bucket.tokens--;
  }

  public synchronized void failed(String source) {
    var bucket = sources.get(source);
    if (bucket != null && ++bucket.failures >= 5) {
      bucket.blockedUntil = clock.instant().plusSeconds(30);
      bucket.failures = 0;
    }
    metrics.counter("auriga.workspace.join.failures").increment();
  }

  public synchronized void succeeded(String source) {
    var bucket = sources.get(source);
    if (bucket != null) bucket.failures = 0;
    metrics.counter("auriga.workspace.join.accepted").increment();
  }

  private void reject() {
    metrics.counter("auriga.workspace.join.rejected").increment();
    throw new AurigaException(CommonErrors.RATE_LIMITED);
  }
}
