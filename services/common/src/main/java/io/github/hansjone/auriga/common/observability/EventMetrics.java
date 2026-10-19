package io.github.hansjone.auriga.common.observability;

import io.micrometer.core.instrument.Counter;
import io.micrometer.core.instrument.Gauge;
import io.micrometer.core.instrument.MeterRegistry;
import io.micrometer.core.instrument.Timer;
import java.time.Duration;
import java.util.concurrent.atomic.AtomicLong;

/**
 * Micrometer meters for the event pipeline. Names (Prometheus form in brackets):
 *
 * <ul>
 *   <li>{@code auriga.outbox.backlog} [auriga_outbox_backlog]: unpublished outbox rows
 *   <li>{@code auriga.outbox.forward.latency} [auriga_outbox_forward_latency_seconds]: commit to
 *       broker ack
 *   <li>{@code auriga.outbox.publish.failures} [auriga_outbox_publish_failures_total]
 *   <li>{@code auriga.events.consume.failures} [auriga_events_consume_failures_total], tags
 *       consumer, type
 *   <li>{@code auriga.events.dead_lettered} [auriga_events_dead_lettered_total], tags consumer,
 *       type (alert on any increase)
 *   <li>{@code auriga.events.processed} [auriga_events_processed_total], tags consumer, outcome
 * </ul>
 */
public final class EventMetrics {
  private final MeterRegistry registry;
  private final String service;
  private final AtomicLong backlog = new AtomicLong();
  private final Timer forwardLatency;
  private final Counter publishFailures;

  public EventMetrics(MeterRegistry registry, String service) {
    this.registry = registry;
    this.service = service;
    Gauge.builder("auriga.outbox.backlog", backlog, AtomicLong::get)
        .tag("service", service)
        .description("Unpublished outbox events")
        .register(registry);
    forwardLatency =
        Timer.builder("auriga.outbox.forward.latency")
            .tag("service", service)
            .description("Time from outbox write to broker acknowledgement")
            .register(registry);
    publishFailures =
        Counter.builder("auriga.outbox.publish.failures")
            .tag("service", service)
            .register(registry);
  }

  public void backlog(long value) {
    backlog.set(value);
  }

  public long backlog() {
    return backlog.get();
  }

  public void forwarded(Duration latency) {
    forwardLatency.record(latency.isNegative() ? Duration.ZERO : latency);
  }

  public void publishFailed() {
    publishFailures.increment();
  }

  public void consumeFailed(String consumer, String type) {
    counter("auriga.events.consume.failures", consumer, type).increment();
  }

  public void deadLettered(String consumer, String type) {
    counter("auriga.events.dead_lettered", consumer, type).increment();
  }

  /** Malformed or forged messages (wrong subject, type or producer). Alert on any increase. */
  public void rejected(String consumer) {
    Counter.builder("auriga.events.rejected")
        .tag("service", service)
        .tag("consumer", consumer)
        .register(registry)
        .increment();
  }

  /** Redelivery after a failure; compare with {@code auriga.events.max.deliveries}. */
  public void redelivered(String consumer, long deliveryCount) {
    Counter.builder("auriga.events.redelivered")
        .tag("service", service)
        .tag("consumer", consumer)
        .register(registry)
        .increment();
  }

  /** Configured dead-letter threshold, exported so dashboards can relate retries to it. */
  public void maxDeliveries(int value) {
    Gauge.builder("auriga.events.max.deliveries", () -> value)
        .tag("service", service)
        .register(registry);
  }

  public void processed(String consumer, String outcome) {
    Counter.builder("auriga.events.processed")
        .tag("service", service)
        .tag("consumer", consumer)
        .tag("outcome", outcome)
        .register(registry)
        .increment();
  }

  private Counter counter(String name, String consumer, String type) {
    return Counter.builder(name)
        .tag("service", service)
        .tag("consumer", consumer)
        .tag("type", type == null ? "unknown" : type)
        .register(registry);
  }
}
