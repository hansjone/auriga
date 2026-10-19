package io.github.hansjone.auriga.common.events;

import java.time.Instant;
import java.util.UUID;
import tools.jackson.databind.DeserializationFeature;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.json.JsonMapper;

/**
 * An event as seen by a consumer. Parsing is lenient (contracts/README.md): unknown attributes and
 * unknown data fields are ignored.
 */
public record ReceivedEvent(
    UUID id,
    String type,
    String source,
    String subject,
    Instant time,
    UUID tenantId,
    String dataVersion,
    String traceparent,
    JsonNode data) {
  static final JsonMapper LENIENT =
      JsonMapper.builder()
          .disable(DeserializationFeature.FAIL_ON_UNKNOWN_PROPERTIES)
          .disable(DeserializationFeature.FAIL_ON_NULL_FOR_PRIMITIVES)
          .build();

  /** Binds {@link #data()} to a type, ignoring fields the type does not declare. */
  public <T> T dataAs(Class<T> type) {
    return LENIENT.treeToValue(data, type);
  }

  /**
   * @throws IllegalArgumentException when id, type, tenantid or data are missing or malformed
   */
  public static ReceivedEvent parse(byte[] payload) {
    EventEnvelope e;
    try {
      e = LENIENT.readValue(payload, EventEnvelope.class);
    } catch (RuntimeException ex) {
      throw new IllegalArgumentException("Event is not valid JSON", ex);
    }
    if (e == null || e.id() == null || e.type() == null || e.tenantid() == null) {
      throw new IllegalArgumentException("Event lacks id, type or tenantid");
    }
    if (e.data() == null || !e.data().isObject()) {
      throw new IllegalArgumentException("Event data must be an object");
    }
    return new ReceivedEvent(
        UUID.fromString(e.id()),
        e.type(),
        e.source(),
        e.subject(),
        e.time() == null ? null : Instant.parse(e.time()),
        UUID.fromString(e.tenantid()),
        e.dataversion(),
        e.traceparent(),
        e.data());
  }
}
