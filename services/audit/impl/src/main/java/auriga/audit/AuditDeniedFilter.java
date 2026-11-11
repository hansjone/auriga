package auriga.audit;

import auriga.common.context.RequestContext;
import auriga.common.security.AurigaAuthentication;
import jakarta.servlet.*;
import jakarta.servlet.http.*;
import java.io.IOException;
import java.time.Duration;
import java.util.*;
import java.util.function.LongSupplier;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.security.core.context.SecurityContextHolder;
import org.springframework.web.filter.OncePerRequestFilter;

/** Wraps authentication AND MVC authorization; never trusts claims from a rejected token. */
final class AuditDeniedFilter extends OncePerRequestFilter {
  private static final Logger LOG = LoggerFactory.getLogger(AuditDeniedFilter.class);
  private final AuditStore store;
  private final DenialSampler sampler;

  AuditDeniedFilter(AuditStore store) {
    this(store, System::nanoTime);
  }

  AuditDeniedFilter(AuditStore store, LongSupplier clock) {
    this.store = store;
    this.sampler = new DenialSampler(clock);
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
        ctx =
            ctx.withPrincipal(
                auth instanceof AurigaAuthentication a && a.isAuthenticated()
                    ? a.getPrincipal()
                    : null);
        ctx =
            ctx.withClient(ctx.clientIp() == null ? request.getRemoteAddr() : ctx.clientIp(), null);
        var principal = ctx.principal();
        String subject =
            principal == null ? null : principal.tenantId() + ":" + principal.subject();
        if (sampler.allow(ctx.clientIp(), subject)) {
          try {
            ctx.run(store::recordDenied);
          } catch (RuntimeException ex) {
            // Storage availability must never replace an authentication/authorization response.
            LOG.warn("Denied-request audit unavailable; original response retained");
          }
        }
      }
    }
  }

  /**
   * Per-process, fixed-minute sampling: 5 per source AND subject, 100 in total. Only admitted keys
   * are retained, so each map is bounded by the global budget.
   */
  private static final class DenialSampler {
    private final LongSupplier clock;
    private long started;
    private int total;
    private final Map<String, Integer> sources = new HashMap<>();
    private final Map<String, Integer> subjects = new HashMap<>();

    DenialSampler(LongSupplier clock) {
      this.clock = clock;
      started = clock.getAsLong();
    }

    synchronized boolean allow(String source, String subject) {
      long now = clock.getAsLong();
      if (now - started >= Duration.ofMinutes(1).toNanos()) {
        started = now;
        total = 0;
        sources.clear();
        subjects.clear();
      }
      if (total >= 100
          || sources.getOrDefault(source, 0) >= 5
          || (subject != null && subjects.getOrDefault(subject, 0) >= 5)) return false;
      total++;
      sources.merge(source, 1, Integer::sum);
      if (subject != null) subjects.merge(subject, 1, Integer::sum);
      return true;
    }
  }
}
