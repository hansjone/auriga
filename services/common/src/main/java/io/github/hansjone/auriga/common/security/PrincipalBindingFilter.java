package io.github.hansjone.auriga.common.security;

import io.github.hansjone.auriga.common.context.RequestContext;
import io.github.hansjone.auriga.common.context.TenantContext;
import io.github.hansjone.auriga.common.observability.LogFields;
import jakarta.servlet.FilterChain;
import jakarta.servlet.ServletException;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.servlet.http.HttpServletResponse;
import java.io.IOException;
import org.springframework.security.core.Authentication;
import org.springframework.security.core.context.SecurityContextHolder;
import org.springframework.web.filter.OncePerRequestFilter;

/**
 * Runs after bearer token authentication: binds the principal into {@link RequestContext} and its
 * {@code tid} into {@link TenantContext} for the rest of the request. The tenant comes only from
 * the validated token, never from a request header.
 */
public final class PrincipalBindingFilter extends OncePerRequestFilter {
  @Override
  protected void doFilterInternal(
      HttpServletRequest request, HttpServletResponse response, FilterChain chain)
      throws ServletException, IOException {
    Authentication auth = SecurityContextHolder.getContext().getAuthentication();
    if (!(auth instanceof AurigaAuthentication authentication)) {
      chain.doFilter(request, response);
      return;
    }
    AurigaPrincipal principal = authentication.getPrincipal();
    RequestContext context =
        RequestContext.current()
            .orElseGet(
                () ->
                    RequestContext.start(
                        request.getHeader(CorrelationFilter.REQUEST_ID_HEADER),
                        request.getHeader(CorrelationFilter.TRACEPARENT_HEADER)))
            .withPrincipal(principal);
    try (var ignored = LogFields.tenant(principal.tenantId())) {
      ServletScopes.run(
          op -> context.call(() -> TenantContext.call(principal.tenantId(), op)),
          () -> chain.doFilter(request, response));
    }
  }
}
