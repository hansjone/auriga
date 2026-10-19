package auriga.common.events;

/** Where the outbox forwarder sends events. Returning normally means the broker stored it. */
@FunctionalInterface
public interface EventTransport {
  /**
   * @param subject NATS subject
   * @param messageId event id, used as {@code Nats-Msg-Id} for broker-side de-duplication
   * @param payload the CloudEvents JSON
   */
  void send(String subject, String messageId, byte[] payload) throws Exception;
}
