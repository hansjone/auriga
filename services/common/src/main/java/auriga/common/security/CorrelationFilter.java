package auriga.common.security;

import auriga.common.context.RequestContext;
import auriga.common.observability.LogFields;
import jakarta.servlet.FilterChain;
import jakarta.servlet.ServletException;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.servlet.http.HttpServletResponse;
import java.io.IOException;
import java.util.Collections;
import java.util.Map;
import java.util.Set;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.web.filter.OncePerRequestFilter;

/**
 * First filter of every request: request id ({@code X-Request-Id}), W3C {@code traceparent}, log
 * fields, and the {@link RequestContext} scope that the rest of the request runs in.
 */
public final class CorrelationFilter extends OncePerRequestFilter {
  private static final Logger LOG = LoggerFactory.getLogger(CorrelationFilter.class);
  public static final String REQUEST_ID_HEADER = "X-Request-Id";
  public static final String TRACEPARENT_HEADER = "traceparent";

  @Override
  protected void doFilterInternal(
      HttpServletRequest request, HttpServletResponse response, FilterChain chain)
      throws ServletException, IOException {
    RequestContext context =
        RequestContext.start(
                request.getHeader(REQUEST_ID_HEADER),
                Collections.list(request.getHeaders(TRACEPARENT_HEADER)).size() == 1
                    ? request.getHeader(TRACEPARENT_HEADER)
                    : null)
            .withClient(request.getRemoteAddr(), request.getHeader("User-Agent"));
    response.setHeader(REQUEST_ID_HEADER, context.requestId());
    response.setHeader(TRACEPARENT_HEADER, context.trace().toTraceparent());
    response.setHeader("X-Trace-Id", context.trace().traceId());
    String[] path = request.getRequestURI().split("/", 5);
    String service =
        path.length > 2
                && Set.of(
                        "iam",
                        "workspace",
                        "collab",
                        "org",
                        "ops",
                        "audit",
                        "resource",
                        "knowledge")
                    .contains(path[2])
            ? path[2]
            : "platform";
    try (var ignored = LogFields.of(context);
        var component = LogFields.of(Map.of("service", service))) {
      try {
        ServletScopes.run(context::call, () -> chain.doFilter(request, response));
      } catch (ServletException | IOException | RuntimeException error) {
        LOG.error(
            "HTTP request failed method={} path={}",
            request.getMethod(),
            request.getRequestURI(),
            error);
        throw error;
      } finally {
        LOG.info(
            "HTTP request completed method={} path={} status={}",
            request.getMethod(),
            request.getRequestURI(),
            response.getStatus());
      }
    }
  }
}
