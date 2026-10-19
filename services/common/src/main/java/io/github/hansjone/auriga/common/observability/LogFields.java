package io.github.hansjone.auriga.common.observability;

import io.github.hansjone.auriga.common.context.RequestContext;
import java.util.HashMap;
import java.util.LinkedHashMap;
import java.util.Map;
import java.util.UUID;
import org.slf4j.MDC;

/**
 * Puts correlation fields into the logging MDC for the duration of a block and restores the
 * previous values afterwards. MDC is thread-confined (never inherited), matching the explicit
 * propagation rule used for tenant and request context.
 */
public final class LogFields implements AutoCloseable {
  public static final String TRACE_ID = "traceId";
  public static final String SPAN_ID = "spanId";
  public static final String REQUEST_ID = "requestId";
  public static final String TENANT_ID = "tenantId";

  private final Map<String, String> previous = new HashMap<>();

  private LogFields(Map<String, String> fields) {
    fields.forEach(
        (k, v) -> {
          previous.put(k, MDC.get(k));
          if (v == null) MDC.remove(k);
          else MDC.put(k, v);
        });
  }

  public static LogFields of(Map<String, String> fields) {
    return new LogFields(fields);
  }

  public static LogFields of(RequestContext context) {
    Map<String, String> fields = new LinkedHashMap<>();
    fields.put(TRACE_ID, context.trace().traceId());
    fields.put(SPAN_ID, context.trace().spanId());
    fields.put(REQUEST_ID, context.requestId());
    return new LogFields(fields);
  }

  public static LogFields tenant(UUID tenantId) {
    Map<String, String> fields = new LinkedHashMap<>();
    fields.put(TENANT_ID, tenantId == null ? null : tenantId.toString());
    return new LogFields(fields);
  }

  @Override
  public void close() {
    previous.forEach(
        (k, v) -> {
          if (v == null) MDC.remove(k);
          else MDC.put(k, v);
        });
  }
}
