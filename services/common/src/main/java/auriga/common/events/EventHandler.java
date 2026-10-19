package auriga.common.events;

import java.util.Set;

/**
 * A durable event subscription. Declare it as a Spring bean; common creates a JetStream durable
 * consumer {@code <service>-<name>} filtered to {@link #eventTypes()}.
 *
 * <p>{@link #handle} runs inside a database transaction with the event's tenant bound; the
 * de-duplication record ({@code processed_event}) is written in the same transaction, so the
 * handler's changes and "processed" commit or roll back together. Throwing makes the event
 * redeliver with backoff; after the configured number of deliveries it goes to {@code
 * dead_letter_event}, is logged at ERROR and counted ({@code auriga.events.dead_lettered}).
 */
public interface EventHandler {
  /** Stable, unique per service: {@code [a-z0-9-]+}. Renaming creates a new consumer. */
  String name();

  /** Event types to receive, e.g. {@code user.disabled}. */
  Set<String> eventTypes();

  void handle(ReceivedEvent event) throws Exception;
}
