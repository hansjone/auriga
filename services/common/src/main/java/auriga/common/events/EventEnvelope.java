package auriga.common.events;

import com.fasterxml.jackson.annotation.JsonIgnoreProperties;
import com.fasterxml.jackson.annotation.JsonInclude;
import com.fasterxml.jackson.annotation.JsonPropertyOrder;
import tools.jackson.databind.JsonNode;

/**
 * CloudEvents 1.0 structured envelope with the Auriga extensions (events/envelope.schema.json).
 * Unknown attributes are ignored when reading (consumers must be lenient).
 */
@JsonInclude(JsonInclude.Include.NON_NULL)
@JsonIgnoreProperties(ignoreUnknown = true)
@JsonPropertyOrder({
  "specversion",
  "id",
  "source",
  "type",
  "time",
  "subject",
  "datacontenttype",
  "dataversion",
  "tenantid",
  "traceparent",
  "data"
})
public record EventEnvelope(
    String specversion,
    String id,
    String source,
    String type,
    String time,
    String subject,
    String datacontenttype,
    String dataversion,
    String tenantid,
    String traceparent,
    JsonNode data) {
  public static final String SPEC_VERSION = "1.0";
  public static final String CONTENT_TYPE = "application/json";
  public static final String AUDIT_TYPE = "audit.recorded";

  /**
   * NATS subject: {@code auriga.evt.<type>}, audit records go to {@code auriga.audit.<service>}.
   */
  public static String natsSubject(String type, String serviceName) {
    return AUDIT_TYPE.equals(type) ? "auriga.audit." + serviceName : "auriga.evt." + type;
  }
}
