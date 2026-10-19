package io.github.hansjone.auriga.common.error;

import jakarta.servlet.http.HttpServletRequest;
import jakarta.validation.ConstraintViolationException;
import java.util.ArrayList;
import java.util.List;
import java.util.Objects;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.core.Ordered;
import org.springframework.core.annotation.Order;
import org.springframework.http.ResponseEntity;
import org.springframework.http.converter.HttpMessageNotReadableException;
import org.springframework.security.access.AccessDeniedException;
import org.springframework.security.core.AuthenticationException;
import org.springframework.validation.FieldError;
import org.springframework.web.ErrorResponse;
import org.springframework.web.bind.MethodArgumentNotValidException;
import org.springframework.web.bind.MissingRequestHeaderException;
import org.springframework.web.bind.MissingServletRequestParameterException;
import org.springframework.web.bind.annotation.ExceptionHandler;
import org.springframework.web.bind.annotation.RestControllerAdvice;
import org.springframework.web.method.annotation.HandlerMethodValidationException;
import org.springframework.web.method.annotation.MethodArgumentTypeMismatchException;

/** Maps every exception leaving a controller to {@code application/problem+json}. */
@RestControllerAdvice
@Order(Ordered.HIGHEST_PRECEDENCE)
public class ProblemExceptionHandler {
  private static final Logger log = LoggerFactory.getLogger(ProblemExceptionHandler.class);
  private final Problems problems;

  public ProblemExceptionHandler(Problems problems) {
    this.problems = Objects.requireNonNull(problems);
  }

  @ExceptionHandler(AurigaException.class)
  public ResponseEntity<Object> business(AurigaException e, HttpServletRequest request) {
    if (e.errorCode().httpStatus() >= 500) {
      log.error("Request failed with {}", e.errorCode().code(), e);
    }
    return problems.response(e, e.errorCode().httpStatus(), request);
  }

  @ExceptionHandler(MethodArgumentNotValidException.class)
  public ResponseEntity<Object> invalidBody(
      MethodArgumentNotValidException e, HttpServletRequest request) {
    List<FieldViolation> violations = new ArrayList<>();
    for (FieldError fe : e.getBindingResult().getFieldErrors()) {
      violations.add(
          new FieldViolation(
              FieldViolation.pointer(fe.getField()), code(fe.getCode()), fe.getDefaultMessage()));
    }
    e.getBindingResult()
        .getGlobalErrors()
        .forEach(g -> violations.add(new FieldViolation("", code(g.getCode()), null)));
    return problems.response(AurigaException.validation(violations), 400, request);
  }

  @ExceptionHandler(HandlerMethodValidationException.class)
  public ResponseEntity<Object> invalidParameters(
      HandlerMethodValidationException e, HttpServletRequest request) {
    List<FieldViolation> violations = new ArrayList<>();
    e.getParameterValidationResults()
        .forEach(
            r ->
                r.getResolvableErrors()
                    .forEach(
                        err ->
                            violations.add(
                                new FieldViolation(
                                    "/" + r.getMethodParameter().getParameterName(),
                                    code(err.getCodes() == null ? null : err.getCodes()[0]),
                                    err.getDefaultMessage()))));
    return problems.response(AurigaException.validation(violations), 400, request);
  }

  @ExceptionHandler(ConstraintViolationException.class)
  public ResponseEntity<Object> constraintViolation(
      ConstraintViolationException e, HttpServletRequest request) {
    List<FieldViolation> violations =
        e.getConstraintViolations().stream()
            .map(
                v ->
                    new FieldViolation(
                        FieldViolation.pointer(v.getPropertyPath().toString()),
                        code(
                            v.getConstraintDescriptor()
                                .getAnnotation()
                                .annotationType()
                                .getSimpleName()),
                        v.getMessage()))
            .toList();
    return problems.response(AurigaException.validation(violations), 400, request);
  }

  @ExceptionHandler({
    HttpMessageNotReadableException.class,
    MissingServletRequestParameterException.class,
    MissingRequestHeaderException.class,
    MethodArgumentTypeMismatchException.class
  })
  public ResponseEntity<Object> unreadable(Exception e, HttpServletRequest request) {
    List<FieldViolation> violations = new ArrayList<>();
    if (e instanceof MissingServletRequestParameterException m) {
      violations.add(new FieldViolation("/" + m.getParameterName(), "required", null));
    } else if (e instanceof MethodArgumentTypeMismatchException m) {
      violations.add(new FieldViolation("/" + m.getName(), "type_mismatch", null));
    } else if (e instanceof MissingRequestHeaderException m) {
      violations.add(new FieldViolation("/" + m.getHeaderName(), "required", null));
    }
    return problems.response(AurigaException.validation(violations), 400, request);
  }

  @ExceptionHandler(AuthenticationException.class)
  public ResponseEntity<Object> unauthenticated(Exception e, HttpServletRequest request) {
    return problems.response(new AurigaException(CommonErrors.UNAUTHENTICATED), 401, request);
  }

  @ExceptionHandler(AccessDeniedException.class)
  public ResponseEntity<Object> denied(Exception e, HttpServletRequest request) {
    return problems.response(new AurigaException(CommonErrors.FORBIDDEN), 403, request);
  }

  /** Anything else; Spring MVC errors (404 no handler, 405, 415, ...) keep their status. */
  @ExceptionHandler(Exception.class)
  public ResponseEntity<Object> unexpected(Exception e, HttpServletRequest request) {
    if (!(e instanceof ErrorResponse framework)) {
      log.error("Unhandled exception", e);
      return problems.response(new AurigaException(CommonErrors.INTERNAL), 500, request);
    }
    int status = framework.getStatusCode().value();
    ErrorCode code =
        switch (status) {
          case 401 -> CommonErrors.UNAUTHENTICATED;
          case 403 -> CommonErrors.FORBIDDEN;
          case 404 -> CommonErrors.NOT_FOUND;
          case 409 -> CommonErrors.CONFLICT;
          case 412 -> CommonErrors.PRECONDITION_FAILED;
          case 429 -> CommonErrors.RATE_LIMITED;
          case 503 -> CommonErrors.UNAVAILABLE;
          default -> status >= 500 ? CommonErrors.INTERNAL : CommonErrors.VALIDATION_FAILED;
        };
    if (status >= 500) log.error("Request failed", e);
    return problems.response(new AurigaException(code), status, request);
  }

  private static String code(String raw) {
    if (raw == null) return "invalid";
    // NotBlank -> not_blank
    return raw.replaceAll("([a-z0-9])([A-Z])", "$1_$2").toLowerCase(java.util.Locale.ROOT);
  }
}
