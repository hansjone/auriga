package auriga.common.observability;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.context.RequestContext;
import auriga.common.context.TraceContext;
import auriga.common.security.CorrelationFilter;
import ch.qos.logback.classic.Level;
import ch.qos.logback.classic.Logger;
import ch.qos.logback.classic.LoggerContext;
import ch.qos.logback.classic.spi.ILoggingEvent;
import ch.qos.logback.classic.spi.LoggingEvent;
import ch.qos.logback.core.read.ListAppender;
import java.util.Map;
import org.junit.jupiter.api.Test;
import org.slf4j.LoggerFactory;
import org.springframework.boot.SpringApplication;
import org.springframework.mock.env.MockEnvironment;
import org.springframework.mock.web.MockHttpServletRequest;
import org.springframework.mock.web.MockHttpServletResponse;
import org.springframework.web.servlet.HandlerMapping;
import tools.jackson.databind.json.JsonMapper;

class TraceLoggingTest {
  @Test
  void requestLogsUseTemplatesAndNeverCredentialPathValues() throws Exception {
    Logger logger = (Logger) LoggerFactory.getLogger(CorrelationFilter.class);
    var events = new ListAppender<ILoggingEvent>();
    events.start();
    logger.addAppender(events);
    try {
      for (String path :
          new String[] {
            "/api/org/v1/invitations/preview/secret-invite",
            "/api/iam/v1/device/secret-code/approve",
            "/api/iam/v1/device/secret-code/deny",
            "/unmatched/secret-unknown"
          }) {
        new CorrelationFilter()
            .doFilter(
                new MockHttpServletRequest("GET", path),
                new MockHttpServletResponse(),
                (q, r) -> {});
        assertThrows(
            IllegalStateException.class,
            () ->
                new CorrelationFilter()
                    .doFilter(
                        new MockHttpServletRequest("GET", path),
                        new MockHttpServletResponse(),
                        (q, r) -> {
                          throw new IllegalStateException("failure");
                        }));
      }
      assertFalse(events.list.isEmpty());
      for (var event : events.list)
        assertFalse(new JsonLogFormatter().format(event).contains("secret-"));
      events.list.clear();
      var request = new MockHttpServletRequest("GET", "/api/org/v1/members/alice");
      new CorrelationFilter()
          .doFilter(
              request,
              new MockHttpServletResponse(),
              (q, r) ->
                  q.setAttribute(
                      HandlerMapping.BEST_MATCHING_PATTERN_ATTRIBUTE, "/api/org/v1/members/{id}"));
      assertTrue(events.list.getFirst().getFormattedMessage().contains("/api/org/v1/members/{id}"));
    } finally {
      logger.detachAppender(events);
      events.stop();
    }
  }

  static final String PARENT = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01";

  @Test
  void responseAndScopedContextContinueTraceWithoutReplacingBusinessId() throws Exception {
    var request = new MockHttpServletRequest("POST", "/api/collab/v1/test");
    request.addHeader("traceparent", PARENT);
    request.addHeader("X-Request-Id", "business-ulid");
    var response = new MockHttpServletResponse();
    new CorrelationFilter()
        .doFilter(
            request,
            response,
            (q, r) -> {
              assertEquals(
                  PARENT.substring(3, 35),
                  RequestContext.current().orElseThrow().trace().traceId());
              assertEquals("business-ulid", RequestContext.current().orElseThrow().requestId());
            });
    assertEquals(PARENT.substring(3, 35), response.getHeader("X-Trace-Id"));
    assertEquals(
        PARENT.substring(3, 35),
        TraceContext.parse(response.getHeader("traceparent")).orElseThrow().traceId());
    assertTrue(RequestContext.current().isEmpty());
  }

  @Test
  void malformedTraceRegeneratesAndExceptionRestoresContext() {
    var request = new MockHttpServletRequest("GET", "/api/test");
    request.addHeader("traceparent", "x".repeat(10000));
    var response = new MockHttpServletResponse();
    var failure = new IllegalStateException("failure");
    assertSame(
        failure,
        assertThrows(
            IllegalStateException.class,
            () ->
                new CorrelationFilter()
                    .doFilter(
                        request,
                        response,
                        (q, r) -> {
                          throw failure;
                        })));
    assertTrue(TraceContext.parse(response.getHeader("traceparent")).isPresent());
    assertTrue(RequestContext.current().isEmpty());
  }

  @Test
  void stackAndBusinessFieldsSurviveWhileSecretsAreMasked() {
    var event =
        new LoggingEvent(
            "fqcn",
            new LoggerContext().getLogger("auriga.collab.app.Mailbox"),
            Level.ERROR,
            "account_id=alice password=hidden-password totp=hidden-totp AURIGA_SIGNING_TOKEN=hidden-env",
            new IllegalStateException(
                "failed\n-----BEGIN PRIVATE KEY-----\nhidden-private\n-----END PRIVATE KEY-----"),
            null);
    event.setMDCPropertyMap(Map.of("traceId", PARENT.substring(3, 35), "service", "workspace"));
    String line = new JsonLogFormatter().format(event);
    var json = JsonMapper.builder().build().readTree(line);
    assertEquals("collab", json.get("service").asString());
    assertEquals(PARENT.substring(3, 35), json.get("trace_id").asString());
    assertTrue(line.contains("account_id=alice"));
    assertTrue(line.contains("TraceLoggingTest.java"));
    for (String secret :
        new String[] {"hidden-password", "hidden-totp", "hidden-env", "hidden-private"})
      assertFalse(line.contains(secret));
  }

  @Test
  void testEnvironmentCanEnableDebug() {
    var env = new MockEnvironment().withProperty("AURIGA_LOG_LEVEL", "DEBUG");
    new JsonLoggingEnvironmentPostProcessor().postProcessEnvironment(env, new SpringApplication());
    assertEquals("DEBUG", env.getProperty("logging.level.auriga"));
    assertEquals(
        "input_tokens=42 max_tokens=2048 token_id=business-reference",
        SensitiveDataMasker.mask("input_tokens=42 max_tokens=2048 token_id=business-reference"));
  }
}
