package auriga.common.audit;

/**
 * Records an audit entry in the caller's transaction (outbox type {@code audit.recorded}, NATS
 * subject {@code auriga.audit.<service>}). Must be called inside the business transaction, like
 * {@link auriga.common.events.EventPublisher}.
 */
public interface AuditLogger {
  void record(AuditEntry entry);
}
