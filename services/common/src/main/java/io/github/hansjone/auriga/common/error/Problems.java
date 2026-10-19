package io.github.hansjone.auriga.common.error;

import io.github.hansjone.auriga.common.context.RequestContext;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.servlet.http.HttpServletResponse;
import java.io.IOException;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Objects;
import org.slf4j.MDC;
import org.springframework.http.HttpHeaders;
import org.springframework.http.HttpStatus;
import org.springframework.http.MediaType;
import org.springframework.http.ResponseEntity;
import tools.jackson.databind.json.JsonMapper;

/** Builds RFC 9457 problem documents ({@code Problem} in openapi/common.yaml). */
public final class Problems {
  public static final MediaType PROBLEM_JSON = MediaType.APPLICATION_PROBLEM_JSON;
  private static final JsonMapper JSON = JsonMapper.builder().build();

  private final ErrorMessages messages;

  public Problems(ErrorMessages messages) {
    this.messages = Objects.requireNonNull(messages);
  }

  public ErrorMessages messages() {
    return messages;
  }

  public Map<String, Object> body(
      ErrorCode code,
      int status,
      Map<String, ?> params,
      List<FieldViolation> violations,
      String instance,
      Locale locale) {
    Map<String, Object> body = new LinkedHashMap<>();
    body.put("type", "about:blank");
    HttpStatus resolved = HttpStatus.resolve(status);
    body.put("title", resolved == null ? "Error" : resolved.getReasonPhrase());
    body.put("status", status);
    body.put("detail", messages.message(code, params, locale));
    if (instance != null) body.put("instance", instance);
    body.put("code", code.code());
    if (params != null && !params.isEmpty()) body.put("params", params);
    if (violations != null && !violations.isEmpty()) {
      body.put(
          "errors",
          violations.stream()
              .map(
                  v -> {
                    Map<String, Object> e = new LinkedHashMap<>();
                    e.put("field", v.field());
                    e.put("code", v.code());
                    if (v.message() != null) e.put("message", v.message());
                    return e;
                  })
              .toList());
    }
    String traceId = currentTraceId();
    if (traceId != null) body.put("traceId", traceId);
    return body;
  }

  public ResponseEntity<Object> response(
      AurigaException e, int status, HttpServletRequest request) {
    Locale locale = messages.resolve(request.getHeader(HttpHeaders.ACCEPT_LANGUAGE));
    return ResponseEntity.status(status)
        .contentType(PROBLEM_JSON)
        .header(HttpHeaders.CONTENT_LANGUAGE, locale.toLanguageTag())
        .body(
            body(
                e.errorCode(),
                status,
                e.params(),
                e.violations(),
                request.getRequestURI(),
                locale));
  }

  /** For servlet filters and security handlers that run outside Spring MVC. */
  public void write(HttpServletRequest request, HttpServletResponse response, AurigaException e)
      throws IOException {
    int status = e.errorCode().httpStatus();
    Locale locale = messages.resolve(request.getHeader(HttpHeaders.ACCEPT_LANGUAGE));
    Map<String, Object> body =
        body(e.errorCode(), status, e.params(), e.violations(), request.getRequestURI(), locale);
    response.setStatus(status);
    response.setContentType(PROBLEM_JSON.toString());
    response.setCharacterEncoding("UTF-8");
    response.setHeader(HttpHeaders.CONTENT_LANGUAGE, locale.toLanguageTag());
    response.getOutputStream().write(JSON.writeValueAsBytes(body));
  }

  static String currentTraceId() {
    return RequestContext.current()
        .map(c -> c.trace().traceId())
        .orElseGet(() -> MDC.get("traceId"));
  }
}
