package auriga.common.events;

import auriga.common.context.RequestContext;
import auriga.common.context.TenantContext;
import auriga.common.observability.EventMetrics;
import auriga.common.observability.LogFields;
import auriga.common.persistence.CommonMigrations;
import auriga.common.persistence.UuidV7;
import java.nio.charset.StandardCharsets;
import java.sql.Timestamp;
import java.time.Clock;
import java.util.Objects;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.transaction.PlatformTransactionManager;
import org.springframework.transaction.support.TransactionTemplate;

/**
 * Consumer-side processing independent of the broker: de-duplicate by event id in the handler's
 * transaction, bind tenant/trace context, decide retry vs dead letter.
 */
public final class InboxProcessor {
  private static final Logger log = LoggerFactory.getLogger(InboxProcessor.class);

  public enum Outcome {
    PROCESSED,
    DUPLICATE,
    RETRY,
    DEAD_LETTERED
  }

  private final TransactionTemplate tx;
  private final JdbcTemplate jdbc;
  private final EventMetrics metrics;
  private final Clock clock;
  private final int maxDeliveries;
  private final EventProducers producers;
  private final String markSql;
  private final String deadLetterSql;

  /**
   * @param transactionManager must manage {@code jdbc}'s DataSource; use the tenant-aware manager
   *     so row level security sees the event's tenant
   * @param maxDeliveries deliveries before an event is dead-lettered
   */
  public InboxProcessor(
      PlatformTransactionManager transactionManager,
      JdbcTemplate jdbc,
      String schema,
      EventMetrics metrics,
      Clock clock,
      int maxDeliveries,
      EventProducers producers) {
    CommonMigrations.requireSchemaName(schema);
    this.producers = Objects.requireNonNull(producers);
    if (maxDeliveries < 1) throw new IllegalArgumentException("maxDeliveries must be >= 1");
    this.tx = new TransactionTemplate(transactionManager);
    this.jdbc = Objects.requireNonNull(jdbc);
    this.metrics = Objects.requireNonNull(metrics);
    this.clock = Objects.requireNonNull(clock);
    this.maxDeliveries = maxDeliveries;
    metrics.maxDeliveries(maxDeliveries);
    this.markSql =
        "INSERT INTO "
            + schema
            + ".processed_event (consumer, event_id, tenant_id, event_type, processed_at)"
            + " VALUES (?, ?, ?, ?, ?) ON CONFLICT (consumer, event_id) DO NOTHING";
    this.deadLetterSql =
        "INSERT INTO "
            + schema
            + ".dead_letter_event (id, consumer, event_id, tenant_id, event_type, payload, error,"
            + " attempts, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)";
  }

  public int maxDeliveries() {
    return maxDeliveries;
  }

  /**
   * @param natsSubject the subject the message arrived on
   * @param deliveryCount 1 for the first delivery
   */
  public Outcome process(
      String consumer,
      EventHandler handler,
      String natsSubject,
      byte[] payload,
      long deliveryCount) {
    ReceivedEvent event;
    try {
      event = ReceivedEvent.parse(payload);
      verifyOrigin(handler, natsSubject, event);
    } catch (RuntimeException e) {
      // Malformed or forged messages never become valid: dead-letter immediately.
      metrics.rejected(consumer);
      deadLetter(consumer, null, payload, e, deliveryCount);
      return Outcome.DEAD_LETTERED;
    }
    if (deliveryCount > 1) metrics.redelivered(consumer, deliveryCount);
    RequestContext context = RequestContext.start("evt-" + event.id(), event.traceparent());
    try (var a = LogFields.of(context);
        var b = LogFields.tenant(event.tenantId())) {
      try {
        Boolean fresh =
            context.call(
                () ->
                    TenantContext.call(
                        event.tenantId(),
                        () ->
                            tx.execute(
                                status -> {
                                  int inserted =
                                      jdbc.update(
                                          markSql,
                                          consumer,
                                          event.id(),
                                          event.tenantId(),
                                          event.type(),
                                          Timestamp.from(clock.instant()));
                                  if (inserted == 0) return false;
                                  try {
                                    handler.handle(event);
                                  } catch (RuntimeException e) {
                                    throw e;
                                  } catch (Exception e) {
                                    throw new HandlerFailure(e);
                                  }
                                  return true;
                                })));
        Outcome outcome = Boolean.TRUE.equals(fresh) ? Outcome.PROCESSED : Outcome.DUPLICATE;
        metrics.processed(consumer, outcome.name().toLowerCase(java.util.Locale.ROOT));
        return outcome;
      } catch (RuntimeException e) {
        Throwable cause = e instanceof HandlerFailure ? e.getCause() : e;
        metrics.consumeFailed(consumer, event.type());
        if (deliveryCount >= maxDeliveries) {
          deadLetter(consumer, event, payload, cause, deliveryCount);
          return Outcome.DEAD_LETTERED;
        }
        log.warn(
            "Consumer {} failed on event {} ({}), delivery {}/{}",
            consumer,
            event.id(),
            event.type(),
            deliveryCount,
            maxDeliveries,
            cause);
        metrics.processed(consumer, "retry");
        return Outcome.RETRY;
      }
    }
  }

  /**
   * The subject must match the type, the handler must have asked for the type, and the source must
   * be a legitimate producer (audit: the subject's service must be the source).
   */
  void verifyOrigin(EventHandler handler, String natsSubject, ReceivedEvent event) {
    if (!handler.eventTypes().contains(event.type())) {
      throw new IllegalArgumentException("Event type " + event.type() + " not subscribed");
    }
    String source = event.source() == null ? "" : event.source();
    if (EventEnvelope.AUDIT_TYPE.equals(event.type())) {
      String service = source.startsWith("auriga/") ? source.substring(7) : "";
      if (service.isEmpty() || !("auriga.audit." + service).equals(natsSubject)) {
        throw new IllegalArgumentException(
            "Audit record from " + source + " arrived on " + natsSubject);
      }
      return;
    }
    if (!("auriga.evt." + event.type()).equals(natsSubject)) {
      throw new IllegalArgumentException(
          "Event " + event.type() + " arrived on subject " + natsSubject);
    }
    if (!producers.allows(event.type(), source)) {
      throw new IllegalArgumentException(
          "Source " + source + " is not a producer of " + event.type());
    }
  }

  private void deadLetter(
      String consumer, ReceivedEvent event, byte[] payload, Throwable error, long attempts) {
    String type = event == null ? null : event.type();
    log.error(
        "ALERT dead-lettered event {} ({}) for consumer {} after {} deliveries",
        event == null ? "<unparseable>" : event.id(),
        type,
        consumer,
        attempts,
        error);
    metrics.deadLettered(consumer, type);
    metrics.processed(consumer, "dead_lettered");
    TenantContext.runAsSystem(
        () ->
            tx.executeWithoutResult(
                status ->
                    jdbc.update(
                        deadLetterSql,
                        UuidV7.next(clock),
                        consumer,
                        event == null ? null : event.id(),
                        event == null ? null : event.tenantId(),
                        type,
                        new String(payload, StandardCharsets.UTF_8),
                        String.valueOf(error),
                        (int) Math.min(Integer.MAX_VALUE, attempts),
                        Timestamp.from(clock.instant()))));
  }

  private static final class HandlerFailure extends RuntimeException {
    HandlerFailure(Exception cause) {
      super(cause);
    }
  }
}
