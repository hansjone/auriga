package io.github.hansjone.auriga.common.events;

import java.util.UUID;

/**
 * Publishes a business event as part of the caller's database transaction (transactional outbox).
 * If the transaction rolls back, the event is never sent; once it commits, the forwarder delivers
 * it to NATS at least once.
 */
public interface EventPublisher {
  /**
   * @param type contract event type, e.g. {@code user.disabled}
   * @param subject id of the primary resource; events with the same subject are delivered in order
   * @param data payload matching the type's data schema (record, map or JsonNode)
   * @return the event id (UUIDv7)
   * @throws IllegalStateException when called outside a transaction
   */
  default UUID publish(String type, String subject, Object data) {
    return publish(type, subject, data, "1");
  }

  UUID publish(String type, String subject, Object data, String dataVersion);
}
