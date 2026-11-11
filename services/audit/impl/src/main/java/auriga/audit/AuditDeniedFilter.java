package auriga.audit;

import auriga.common.context.RequestContext;
import auriga.common.security.AurigaAuthentication;
import jakarta.servlet.*;
import jakarta.servlet.http.*;
import java.io.IOException;
import org.springframework.security.core.context.SecurityContextHolder;
import org.springframework.web.filter.OncePerRequestFilter;

/** Wraps authentication AND MVC authorization; never trusts claims from a rejected token. */
final class AuditDeniedFilter extends OncePerRequestFilter {
  private final AuditStore store;

  AuditDeniedFilter(AuditStore store) {
    this.store = store;
  }

  @Override
  protected void doFilterInternal(
      HttpServletRequest request, HttpServletResponse response, FilterChain chain)
      throws ServletException, IOException {
    try {
      chain.doFilter(request, response);
    } finally {
      if (response.getStatus() == 401 || response.getStatus() == 403) {
        var ctx = RequestContext.current().orElseGet(() -> RequestContext.start(null, null));
        var auth = SecurityContextHolder.getContext().getAuthentication();
        if (auth instanceof AurigaAuthentication a) ctx = ctx.withPrincipal(a.getPrincipal());
        ctx.run(store::recordDenied);
      }
    }
  }
}
