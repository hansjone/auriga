package io.github.hansjone.auriga.common.security;

import io.github.hansjone.auriga.common.context.RequestContext;
import io.github.hansjone.auriga.common.observability.LogFields;
import jakarta.servlet.FilterChain;
import jakarta.servlet.ServletException;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.servlet.http.HttpServletResponse;
import java.io.IOException;
import org.springframework.web.filter.OncePerRequestFilter;

/**
 * First filter of every request: request id ({@code X-Request-Id}), W3C {@code traceparent}, log
 * fields, and the {@link RequestContext} scope that the rest of the request runs in.
 */
public final class CorrelationFilter extends OncePerRequestFilter {
  public static final String REQUEST_ID_HEADER = "X-Request-Id";
  public static final String TRACEPARENT_HEADER = "traceparent";

  @Override
  protected void doFilterInternal(
      HttpServletRequest request, HttpServletResponse response, FilterChain chain)
      throws ServletException, IOException {
    RequestContext context =
        RequestContext.start(
                request.getHeader(REQUEST_ID_HEADER), request.getHeader(TRACEPARENT_HEADER))
            .withClient(request.getRemoteAddr(), request.getHeader("User-Agent"));
    response.setHeader(REQUEST_ID_HEADER, context.requestId());
    try (var ignored = LogFields.of(context)) {
      ServletScopes.run(context::call, () -> chain.doFilter(request, response));
    }
  }
}
