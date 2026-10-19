package io.github.hansjone.auriga.iam;

import io.github.hansjone.auriga.common.context.TenantContext;
import jakarta.servlet.http.*;
import java.util.*;
import java.util.function.Supplier;

/**
 * Hosts are installation configuration, never query/header tenant ids or client-selected context.
 */
public final class RequestIdentity {
  public record Host(UUID tenant, String audience, boolean secure) {
    public Host {
      if (!Set.of("portal", "admin").contains(audience))
        throw new IllegalArgumentException("Invalid host audience");
    }
  }

  private final Map<String, Host> hosts;
  private final Set<String> trustedProxies;

  public RequestIdentity(Map<String, Host> hosts, Set<String> trustedProxies) {
    this.hosts = Map.copyOf(hosts);
    this.trustedProxies = Set.copyOf(trustedProxies);
  }

  public Host host(HttpServletRequest request) {
    Host h = hosts.get(request.getServerName().toLowerCase(Locale.ROOT));
    IamError.require(h != null, "unknown_host", 404);
    return h;
  }

  public <T> T call(HttpServletRequest request, Supplier<T> body) {
    Host h = host(request);
    IamError.require(
        TenantContext.current().isEmpty() || TenantContext.require().equals(h.tenant()),
        "tenant_mismatch",
        403);
    return TenantContext.call(h.tenant(), body::get);
  }

  public String clientIp(HttpServletRequest request) {
    if (!trustedProxies.contains(request.getRemoteAddr())) return request.getRemoteAddr();
    String ip = request.getHeader("X-Auriga-Client-IP");
    IamError.require(ip != null && ip.matches("[0-9a-fA-F:.]+"), "missing_client_ip", 400);
    return ip;
  }

  static String cookie(HttpServletRequest request, String name) {
    if (request.getCookies() != null)
      for (Cookie c : request.getCookies()) if (c.getName().equals(name)) return c.getValue();
    return null;
  }

  public String cookieName(HttpServletRequest request, String base) {
    return (host(request).secure() ? "__Host-" : "") + base;
  }

  public String sessionCookie(HttpServletRequest request) {
    return cookie(
        request,
        cookieName(
            request,
            host(request).audience().equals("admin") ? "auriga_admin_session" : "auriga_session"));
  }

  public String binding(HttpServletRequest request) {
    return cookie(request, cookieName(request, "auriga_login_tx"));
  }

  public void setCookie(
      HttpServletRequest request,
      HttpServletResponse response,
      String name,
      String value,
      boolean httpOnly,
      long maxAge) {
    // __Host- cookies MUST use Path=/, even though the current OpenAPI text specifies a subpath.
    response.addHeader(
        "Set-Cookie",
        org.springframework.http.ResponseCookie.from(cookieName(request, name), value)
            .path("/")
            .secure(host(request).secure())
            .httpOnly(httpOnly)
            .sameSite("Lax")
            .maxAge(maxAge)
            .build()
            .toString());
  }

  public void issued(
      HttpServletRequest request, HttpServletResponse response, LoginManager.Result result) {
    if (result.bindingCookie() != null)
      setCookie(request, response, "auriga_login_tx", result.bindingCookie(), true, 300);
    if (result.session() != null) {
      setCookie(
          request,
          response,
          host(request).audience().equals("admin") ? "auriga_admin_session" : "auriga_session",
          result.session().cookie(),
          true,
          -1);
      setCookie(request, response, "auriga_csrf", result.session().csrf(), false, -1);
    }
    response.setHeader("Cache-Control", "no-store");
    response.setHeader("Pragma", "no-cache");
  }
}
