package io.github.hansjone.auriga.common.events;

import io.nats.client.Connection;
import io.nats.client.JetStream;
import io.nats.client.JetStreamManagement;
import io.nats.client.JetStreamSubscription;
import io.nats.client.Message;
import io.nats.client.PullSubscribeOptions;
import io.nats.client.api.AckPolicy;
import io.nats.client.api.ConsumerConfiguration;
import io.nats.client.api.DeliverPolicy;
import java.time.Duration;
import java.util.List;
import java.util.Objects;
import java.util.regex.Pattern;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.context.SmartLifecycle;

/**
 * Runs one {@link EventHandler} as a JetStream durable pull consumer. Messages are processed one at
 * a time in delivery order; outcome RETRY naks with exponential delay, everything else acks.
 */
public final class NatsEventConsumer implements SmartLifecycle {
  private static final Logger log = LoggerFactory.getLogger(NatsEventConsumer.class);
  private static final Pattern NAME = Pattern.compile("^[a-z0-9][a-z0-9-]*$");

  private final Connection connection;
  private final EventHandler handler;
  private final InboxProcessor processor;
  private final String durable;
  private final Duration ackWait;
  private final Duration baseRedelivery;
  private volatile boolean running;
  private volatile Thread worker;

  public NatsEventConsumer(
      Connection connection,
      String serviceName,
      EventHandler handler,
      InboxProcessor processor,
      Duration ackWait,
      Duration baseRedelivery) {
    this.connection = Objects.requireNonNull(connection);
    this.handler = Objects.requireNonNull(handler);
    this.processor = Objects.requireNonNull(processor);
    if (!NAME.matcher(handler.name()).matches()) {
      throw new IllegalArgumentException("Invalid handler name: " + handler.name());
    }
    if (handler.eventTypes().isEmpty()) {
      throw new IllegalArgumentException("Handler " + handler.name() + " has no event types");
    }
    this.durable = serviceName + "-" + handler.name();
    this.ackWait = ackWait == null ? Duration.ofSeconds(30) : ackWait;
    this.baseRedelivery = baseRedelivery == null ? Duration.ofSeconds(1) : baseRedelivery;
  }

  public String durableName() {
    return durable;
  }

  static String streamFor(String type) {
    return EventEnvelope.AUDIT_TYPE.equals(type)
        ? NatsEvents.AUDIT_STREAM
        : NatsEvents.EVENTS_STREAM;
  }

  static String filterFor(String type) {
    return EventEnvelope.AUDIT_TYPE.equals(type) ? "auriga.audit.>" : "auriga.evt." + type;
  }

  private JetStreamSubscription subscribe() throws Exception {
    List<String> types = List.copyOf(handler.eventTypes());
    String stream = streamFor(types.getFirst());
    if (types.stream().anyMatch(t -> !streamFor(t).equals(stream))) {
      throw new IllegalArgumentException(
          "Handler " + handler.name() + " mixes audit and business events; split it");
    }
    JetStreamManagement jsm = connection.jetStreamManagement();
    jsm.addOrUpdateConsumer(
        stream,
        ConsumerConfiguration.builder()
            .durable(durable)
            .filterSubjects(types.stream().map(NatsEventConsumer::filterFor).distinct().toList())
            .ackPolicy(AckPolicy.Explicit)
            .deliverPolicy(DeliverPolicy.All)
            .ackWait(ackWait)
            // Dead-lettering is decided by the processor; keep JetStream from giving up first.
            .maxDeliver(processor.maxDeliveries() + 5L)
            .build());
    JetStream js = connection.jetStream();
    return js.subscribe(null, PullSubscribeOptions.bind(stream, durable));
  }

  private void loop() {
    JetStreamSubscription sub = null;
    while (running) {
      try {
        if (sub == null) sub = subscribe();
        for (Message msg : sub.fetch(10, Duration.ofSeconds(1))) {
          if (!running) break;
          long delivered = msg.metaData().deliveredCount();
          switch (processor.process(durable, handler, msg.getSubject(), msg.getData(), delivered)) {
            case RETRY -> msg.nakWithDelay(redeliveryDelay(delivered));
            default -> msg.ack();
          }
        }
      } catch (InterruptedException e) {
        Thread.currentThread().interrupt();
        return;
      } catch (Exception e) {
        if (!running) return;
        log.error("Consumer {} loop failed; resubscribing", durable, e);
        sub = null;
        try {
          Thread.sleep(baseRedelivery);
        } catch (InterruptedException ie) {
          Thread.currentThread().interrupt();
          return;
        }
      }
    }
    if (sub != null) {
      try {
        sub.unsubscribe();
      } catch (RuntimeException ignored) {
        // connection may already be closed
      }
    }
  }

  Duration redeliveryDelay(long delivered) {
    long factor = 1L << Math.min(10, Math.max(0, delivered - 1));
    Duration d = baseRedelivery.multipliedBy(factor);
    return d.compareTo(Duration.ofMinutes(5)) > 0 ? Duration.ofMinutes(5) : d;
  }

  @Override
  public synchronized void start() {
    if (running) return;
    running = true;
    worker = Thread.ofVirtual().name("auriga-consumer-" + durable).start(this::loop);
  }

  @Override
  public synchronized void stop() {
    running = false;
    Thread t = worker;
    if (t != null) {
      try {
        if (!t.join(Duration.ofSeconds(10))) t.interrupt();
      } catch (InterruptedException e) {
        Thread.currentThread().interrupt();
      }
    }
    worker = null;
  }

  @Override
  public boolean isRunning() {
    return running;
  }
}
