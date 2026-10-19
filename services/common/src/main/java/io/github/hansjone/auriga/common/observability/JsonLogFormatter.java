package io.github.hansjone.auriga.common.observability;

import ch.qos.logback.classic.spi.ILoggingEvent;
import ch.qos.logback.classic.spi.IThrowableProxy;
import ch.qos.logback.classic.spi.ThrowableProxyUtil;
import java.time.Instant;
import java.util.LinkedHashMap;
import java.util.Map;
import org.springframework.boot.logging.structured.StructuredLogFormatter;
import tools.jackson.databind.json.JsonMapper;

/**
 * One JSON object per line: {@code @timestamp, level, logger, thread, message, traceId, spanId,
 * requestId, tenantId, exception}. Message and stack trace pass through {@link
 * SensitiveDataMasker}. Activated by {@code logging.structured.format.console} (set by default by
 * {@link JsonLoggingEnvironmentPostProcessor}).
 */
public final class JsonLogFormatter implements StructuredLogFormatter<ILoggingEvent> {
  private static final JsonMapper JSON = JsonMapper.builder().build();
  private static final String[] MDC_FIELDS = {
    LogFields.TRACE_ID, LogFields.SPAN_ID, LogFields.REQUEST_ID, LogFields.TENANT_ID
  };

  @Override
  public String format(ILoggingEvent event) {
    Map<String, Object> out = new LinkedHashMap<>();
    out.put("@timestamp", Instant.ofEpochMilli(event.getTimeStamp()).toString());
    out.put("level", event.getLevel().toString());
    out.put("logger", event.getLoggerName());
    out.put("thread", event.getThreadName());
    out.put("message", SensitiveDataMasker.mask(event.getFormattedMessage()));
    Map<String, String> mdc = event.getMDCPropertyMap();
    if (mdc != null) {
      for (String key : MDC_FIELDS) {
        String value = mdc.get(key);
        if (value != null) out.put(key, value);
      }
    }
    IThrowableProxy error = event.getThrowableProxy();
    if (error != null) {
      out.put("exception", SensitiveDataMasker.mask(ThrowableProxyUtil.asString(error)));
    }
    return JSON.writeValueAsString(out) + "\n";
  }
}
