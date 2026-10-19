package io.github.hansjone.auriga.common.observability;

import static org.junit.jupiter.api.Assertions.*;

import ch.qos.logback.classic.Level;
import ch.qos.logback.classic.Logger;
import ch.qos.logback.classic.LoggerContext;
import ch.qos.logback.classic.spi.LoggingEvent;
import java.util.Map;
import org.junit.jupiter.api.Test;
import org.springframework.boot.SpringApplication;
import org.springframework.mock.env.MockEnvironment;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.json.JsonMapper;

class JsonLogFormatterTest {
  private static final JsonMapper JSON = JsonMapper.builder().build();
  private final Logger logger = new LoggerContext().getLogger("test.Logger");

  private JsonNode format(String message, Throwable error, Map<String, String> mdc) {
    LoggingEvent event = new LoggingEvent("fqcn", logger, Level.INFO, message, error, null);
    event.setMDCPropertyMap(mdc);
    String line = new JsonLogFormatter().format(event);
    assertTrue(line.endsWith("\n"));
    assertEquals(1, line.lines().count());
    return JSON.readTree(line);
  }

  @Test
  void includesCorrelationFields() {
    JsonNode json =
        format(
            "hello",
            null,
            Map.of(
                "traceId",
                "t1",
                "spanId",
                "s1",
                "requestId",
                "r1",
                "tenantId",
                "x1",
                "other",
                "o"));
    assertEquals("hello", json.get("message").asString());
    assertEquals("INFO", json.get("level").asString());
    assertEquals("t1", json.get("traceId").asString());
    assertEquals("r1", json.get("requestId").asString());
    assertEquals("x1", json.get("tenantId").asString());
    assertNull(json.get("other"));
    assertNotNull(json.get("@timestamp"));
  }

  @Test
  void masksTokensAndPasswords() {
    String jwt =
        "eyJhbGciOiJFUzI1NiJ9.eyJzdWIiOiJhbGljZSJ9.c2lnbmF0dXJlc2lnbmF0dXJl"; // gitleaks:allow
    // (fake token for
    // masking test)
    JsonNode json =
        format(
            "Authorization: Bearer abcdef123456 jwt="
                + jwt
                + " password=hunter2 {\"client_secret\":\"s3cr3t\"} ait_0123456789",
            new IllegalStateException("token=leaky-token-value"),
            Map.of());
    String all = json.toString();
    for (String secret :
        new String[] {
          "abcdef123456", jwt, "hunter2", "s3cr3t", "0123456789", "leaky-token-value"
        }) {
      assertFalse(all.contains(secret), secret + " leaked: " + all);
    }
    assertTrue(json.get("exception").asString().contains("IllegalStateException"));
  }

  @Test
  void jsonLoggingIsTheDefaultButCanBeDisabled() {
    var processor = new JsonLoggingEnvironmentPostProcessor();
    MockEnvironment env = new MockEnvironment();
    processor.postProcessEnvironment(env, new SpringApplication());
    assertEquals(
        JsonLogFormatter.class.getName(), env.getProperty("logging.structured.format.console"));
    MockEnvironment off = new MockEnvironment().withProperty("auriga.logging.json", "false");
    processor.postProcessEnvironment(off, new SpringApplication());
    assertNull(off.getProperty("logging.structured.format.console"));
    MockEnvironment explicit =
        new MockEnvironment().withProperty("logging.structured.format.console", "ecs");
    processor.postProcessEnvironment(explicit, new SpringApplication());
    assertEquals("ecs", explicit.getProperty("logging.structured.format.console"));
  }
}
