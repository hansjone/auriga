package auriga.iam;

import jakarta.servlet.*;
import jakarta.servlet.http.*;
import java.io.IOException;
import java.util.UUID;

/** AUTH-K2 header: server generated, replaced by the saved transaction id when bound. */
final class UacCorrelation extends org.springframework.web.filter.OncePerRequestFilter {
  static final String HEADER = "X-Auriga-Correlation-Id";

  static void set(HttpServletResponse response, String correlation) {
    response.setHeader(HEADER, correlation);
  }

  @Override
  protected void doFilterInternal(
      HttpServletRequest request, HttpServletResponse response, FilterChain chain)
      throws IOException, ServletException {
    if (request.getRequestURI().startsWith("/api/iam/v1/")
        && request.getRequestURI().contains("/uac")) set(response, UUID.randomUUID().toString());
    chain.doFilter(request, response);
  }
}
