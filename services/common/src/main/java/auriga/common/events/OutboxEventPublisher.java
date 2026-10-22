package auriga.common.events;

import auriga.common.context.RequestContext;
import auriga.common.context.TenantContext;
import auriga.common.persistence.CommonMigrations;
import auriga.common.persistence.TenantTransactionManager;
import auriga.common.persistence.UuidV7;
import java.sql.Timestamp;
import java.time.Clock;
import java.time.Instant;
import java.time.temporal.ChronoUnit;
import java.util.Objects;
import java.util.UUID;
import java.util.regex.Pattern;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.transaction.support.TransactionSynchronization;
import org.springframework.transaction.support.TransactionSynchronizationManager;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.json.JsonMapper;
import tools.jackson.databind.node.ObjectNode;

/** {@link EventPublisher} writing CloudEvents envelopes into the service's outbox table. */
public final class OutboxEventPublisher implements EventPublisher {
  private static final Pattern SERVICE = Pattern.compile("^[a-z][a-z0-9-]*$");

  /** First key of the two-key advisory lock, keeps outbox locks apart from other users. */
  static final int SUBJECT_LOCK_NAMESPACE = 0x41555249;

  private final JdbcTemplate jdbc;
  private final String serviceName;
  private final String insertSql;
  private final String subjectLockPrefix;
  private final EventSchemas schemas;
  private final JsonMapper json;
  private final Clock clock;
  private final Runnable afterCommit;

  /**
   * @param schemas contract validation; null disables it (production may turn it off)
   * @param afterCommit called after the surrounding transaction commits (wakes the forwarder)
   */
  public OutboxEventPublisher(
      JdbcTemplate jdbc,
      String schema,
      String serviceName,
      EventSchemas schemas,
      JsonMapper json,
      Clock clock,
      Runnable afterCommit) {
    this.jdbc = Objects.requireNonNull(jdbc);
    CommonMigrations.requireSchemaName(schema);
    if (serviceName == null || !SERVICE.matcher(serviceName).matches()) {
      throw new IllegalArgumentException("Invalid service name: " + serviceName);
    }
    this.serviceName = serviceName;
    this.subjectLockPrefix = schema + ":";
    this.schemas = schemas;
    this.json = Objects.requireNonNull(json);
    this.clock = Objects.requireNonNull(clock);
    this.afterCommit = afterCommit;
    this.insertSql =
        "INSERT INTO "
            + schema
            + ".outbox_event (id, tenant_id, event_type, subject_key, nats_subject, envelope,"
            + " created_at, next_attempt_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)";
  }

  @Override
  public UUID publish(String type, String subject, Object data, String dataVersion) {
    Objects.requireNonNull(type, "type");
    Objects.requireNonNull(data, "data");
    if (!TransactionSynchronizationManager.isActualTransactionActive()) {
      throw new IllegalStateException(
          "EventPublisher.publish must run inside the business transaction");
    }
    UUID tenant =
        TenantContext.isSystem() ? TenantContext.PLATFORM_TENANT : TenantContext.require();
    TenantTransactionManager.verifyTransactionTenant();
    Instant now = clock.instant().truncatedTo(ChronoUnit.MILLIS);
    UUID id = UuidV7.next(clock);
    JsonNode dataNode = json.valueToTree(data);
    if (!(dataNode instanceof ObjectNode)) {
      throw new InvalidEventException("Event data must be a JSON object");
    }
    EventEnvelope envelope =
        new EventEnvelope(
            EventEnvelope.SPEC_VERSION,
            id.toString(),
            "auriga/" + serviceName,
            type,
            now.toString(),
            subject,
            EventEnvelope.CONTENT_TYPE,
            dataVersion,
            tenant.toString(),
            RequestContext.current().map(c -> c.trace().child().toTraceparent()).orElse(null),
            dataNode);
    if (schemas != null) schemas.validate(json.valueToTree(envelope));
    String subjectKey = subject == null ? id.toString() : subject;
    // Serialize the same subject within its physical outbox, matching the forwarder's scope.
    // Separate service transactions may publish for the same subject on one call stack;
    // sharing their lock would make the inner transaction wait for its own caller to commit.
    jdbc.query(
        "SELECT pg_advisory_xact_lock(?, hashtext(?))",
        rs -> null,
        SUBJECT_LOCK_NAMESPACE,
        subjectLockPrefix + subjectKey);
    jdbc.update(
        insertSql,
        id,
        tenant,
        type,
        subjectKey,
        EventEnvelope.natsSubject(type, serviceName),
        json.writeValueAsString(envelope),
        Timestamp.from(now),
        Timestamp.from(now));
    if (afterCommit != null) {
      TransactionSynchronizationManager.registerSynchronization(
          new TransactionSynchronization() {
            @Override
            public void afterCommit() {
              afterCommit.run();
            }
          });
    }
    return id;
  }
}
