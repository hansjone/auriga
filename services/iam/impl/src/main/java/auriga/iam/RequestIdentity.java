package auriga.iam;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.contracts.deploymentconfig.DeploymentAccess;
import jakarta.servlet.http.*;
import java.net.*;
import java.security.cert.X509Certificate;
import java.util.*;
import java.util.function.Supplier;

/** Installation identity is rechecked only after authenticating the dedicated edge hop. */
public final class RequestIdentity {
  public record Host(UUID tenant, String audience, boolean secure) {}

  public record Hop(Set<String> peerAddresses, String dnsSan, boolean allowLoopbackHttp) {
    public Hop {
      peerAddresses = Set.copyOf(peerAddresses);
      if (peerAddresses.isEmpty())
        throw new IllegalArgumentException("Edge peer allowlist is required");
      for (String ip : peerAddresses) numericAddress(ip);
      if ((dnsSan == null || !dnsSan.matches("[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?"))
          && !allowLoopbackHttp) throw new IllegalArgumentException("Edge DNS SAN is required");
    }
  }

  private final DeploymentAccess access;
  private final Hop hop;

  public RequestIdentity(DeploymentAccess access, Hop hop) {
    this.access = Objects.requireNonNull(access);
    this.hop = Objects.requireNonNull(hop);
  }

  public boolean ipAdmin(HttpServletRequest request) {
    return access.mode().equals("ip") && host(request).audience().equals("admin");
  }

  public String origin(HttpServletRequest request) {
    authenticateHop(request);
    String role = single(request, "X-Auriga-Entry-Role", CommonErrors.FORBIDDEN);
    String expected =
        switch (role) {
          case "portal" -> access.portalUrl();
          case "admin" -> access.adminUrl();
          default -> throw new AurigaException(CommonErrors.FORBIDDEN);
        };
    String entry = single(request, "X-Auriga-Entry-Origin", CommonErrors.FORBIDDEN);
    if (!expected.equals(entry)) throw new AurigaException(CommonErrors.FORBIDDEN);
    URI uri = URI.create(expected);
    String authority = single(request, "Host", CommonErrors.VALIDATION_FAILED);
    if (!normalizedAuthority(authority, uri.getScheme()).equals(uri.getRawAuthority()))
      throw new AurigaException(CommonErrors.FORBIDDEN);
    return expected;
  }

  public Host host(HttpServletRequest request) {
    String origin = origin(request);
    return new Host(
        UUID.fromString(access.tenantId()),
        origin.equals(access.adminUrl()) ? "admin" : "portal",
        origin.startsWith("https://"));
  }

  public void sameOrigin(HttpServletRequest request) {
    String expected = origin(request);
    String supplied = single(request, "Origin", CommonErrors.FORBIDDEN);
    try {
      URI uri = URI.create(supplied);
      if (uri.getRawAuthority() == null
          || uri.getRawUserInfo() != null
          || !Set.of("http", "https").contains(uri.getScheme())
          || (uri.getRawPath() != null && !uri.getRawPath().isEmpty())
          || uri.getRawQuery() != null
          || uri.getRawFragment() != null
          || !expected.equals(
              uri.getScheme()
                  + "://"
                  + normalizedAuthority(uri.getRawAuthority(), uri.getScheme())))
        throw new IllegalArgumentException();
    } catch (IllegalArgumentException | AurigaException e) {
      throw new AurigaException(CommonErrors.FORBIDDEN);
    }
  }

  private void authenticateHop(HttpServletRequest request) {
    String peer = request.getRemoteAddr();
    if (!hop.peerAddresses().contains(peer)) throw new AurigaException(CommonErrors.FORBIDDEN);
    if (!request.isSecure()) {
      if (hop.allowLoopbackHttp() && numericAddress(peer).isLoopbackAddress()) return;
      throw new AurigaException(CommonErrors.FORBIDDEN);
    }
    Object attribute = request.getAttribute("jakarta.servlet.request.X509Certificate");
    if (!(attribute instanceof X509Certificate[] chain) || chain.length == 0)
      throw new AurigaException(CommonErrors.FORBIDDEN);
    try {
      chain[0].checkValidity();
      var sans = chain[0].getSubjectAlternativeNames();
      if (sans != null)
        for (var san : sans)
          if (san.size() >= 2
              && Integer.valueOf(2).equals(san.get(0))
              && Objects.equals(hop.dnsSan(), san.get(1))) return;
    } catch (java.security.cert.CertificateException e) {
      /* fail closed */
    }
    throw new AurigaException(CommonErrors.FORBIDDEN);
  }

  private static InetAddress numericAddress(String value) {
    if (value == null || !value.matches("[0-9a-fA-F:.]+"))
      throw new IllegalArgumentException("Numeric peer address required");
    try {
      return InetAddress.getByName(value);
    } catch (UnknownHostException e) {
      throw new IllegalArgumentException("Invalid peer address", e);
    }
  }

  static String single(
      HttpServletRequest request, String name, auriga.common.error.ErrorCode code) {
    var values = Collections.list(request.getHeaders(name));
    if (values.size() != 1 || values.getFirst().isEmpty()) throw new AurigaException(code);
    return values.getFirst();
  }

  static String normalizedAuthority(String value, String scheme) {
    try {
      if (!value.matches("[A-Za-z0-9.-]+(?::[0-9]{1,5})?")) throw new IllegalArgumentException();
      String[] parts = value.split(":", -1);
      String host = parts[0].toLowerCase(Locale.ROOT);
      if (host.endsWith(".") || host.contains("..") || host.startsWith("."))
        throw new IllegalArgumentException();
      if (host.matches("[0-9.]+")) {
        String[] octets = host.split("\\.", -1);
        if (octets.length != 4) throw new IllegalArgumentException();
        for (String octet : octets)
          if (!octet.matches("0|[1-9][0-9]{0,2}") || Integer.parseInt(octet) > 255)
            throw new IllegalArgumentException();
      } else {
        for (String label : host.split("\\."))
          if (!label.matches("[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?"))
            throw new IllegalArgumentException();
      }
      int port = parts.length == 2 ? Integer.parseInt(parts[1]) : scheme.equals("https") ? 443 : 80;
      if (port < 1 || port > 65535) throw new IllegalArgumentException();
      return host
          + ((scheme.equals("https") && port == 443 || scheme.equals("http") && port == 80)
              ? ""
              : ":" + port);
    } catch (IllegalArgumentException e) {
      throw new AurigaException(CommonErrors.VALIDATION_FAILED);
    }
  }

  public <T> T call(HttpServletRequest request, Supplier<T> body) {
    Host h = host(request);
    if (TenantContext.current().isPresent() && !TenantContext.require().equals(h.tenant()))
      throw new AurigaException(CommonErrors.FORBIDDEN);
    return TenantContext.call(h.tenant(), body::get);
  }

  public String clientIp(HttpServletRequest request) {
    authenticateHop(request);
    String ip = single(request, "X-Auriga-Client-IP", CommonErrors.VALIDATION_FAILED);
    try {
      numericAddress(ip);
    } catch (IllegalArgumentException e) {
      throw new AurigaException(CommonErrors.VALIDATION_FAILED);
    }
    return ip;
  }

  static String cookie(HttpServletRequest request, String name) {
    String value = null;
    if (request.getCookies() != null)
      for (Cookie c : request.getCookies())
        if (c.getName().equals(name)) {
          if (value != null) throw new AurigaException(CommonErrors.UNAUTHENTICATED);
          value = c.getValue();
        }
    return value;
  }

  public String cookieName(HttpServletRequest request, String base) {
    return (host(request).secure() ? "__Host-" : "") + base;
  }

  public String transactionCookie(HttpServletRequest request) {
    return host(request).audience().equals("admin")
        ? "auriga_admin_login_tx"
        : "auriga_portal_login_tx";
  }

  public String sessionCookie(HttpServletRequest request) {
    return cookie(
        request,
        cookieName(
            request,
            host(request).audience().equals("admin") ? "auriga_admin_session" : "auriga_session"));
  }

  public String binding(HttpServletRequest request) {
    try {
      return cookie(request, cookieName(request, transactionCookie(request)));
    } catch (AurigaException e) {
      if (e.errorCode().equals(CommonErrors.UNAUTHENTICATED))
        throw new AurigaException(CommonErrors.NOT_FOUND);
      throw e;
    }
  }

  public tools.jackson.databind.node.ObjectNode browserSession(
      HttpServletRequest request, SessionManager sessions, boolean csrf) {
    var p =
        auriga.common.security.AuthorizationRules.currentPrincipal()
            .orElseThrow(() -> new AurigaException(CommonErrors.UNAUTHENTICATED));
    var h = host(request);
    var row = sessions.activeByCookie(sessionCookie(request), h.audience());
    if (row == null
        || p.type() != auriga.common.security.PrincipalType.USER
        || !p.tenantId().equals(h.tenant())
        || !row.path("sessionId").asString().equals(p.sessionId())
        || !row.path("accountId").asString().equals(p.subject())
        || !p.context().name().equalsIgnoreCase(h.audience()))
      throw new AurigaException(CommonErrors.UNAUTHENTICATED);
    if (csrf) {
      sameOrigin(request);
      String token = single(request, "X-Auriga-CSRF", CommonErrors.FORBIDDEN);
      boolean valid;
      if (ipAdmin(request))
        valid =
            sessions
                .verifyAdminCsrf(
                    auriga.contracts.iam.v1.VerifyAdminSessionCsrfRequest.newBuilder()
                        .setSessionToken(sessionCookie(request))
                        .setCsrfToken(token)
                        .build())
                .getActive();
      else
        valid =
            Secrets.same(token, cookie(request, cookieName(request, "auriga_csrf")))
                && Secrets.same(Secrets.hash(token), row.path("csrfHash").asString());
      if (!valid) throw new AurigaException(CommonErrors.FORBIDDEN);
    }
    return row;
  }

  public void setCookie(
      HttpServletRequest request,
      HttpServletResponse response,
      String name,
      String value,
      boolean httpOnly,
      long maxAge) {
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
      setCookie(request, response, transactionCookie(request), result.bindingCookie(), true, 300);
    if (result.session() != null) {
      setCookie(
          request,
          response,
          host(request).audience().equals("admin") ? "auriga_admin_session" : "auriga_session",
          result.session().cookie(),
          true,
          -1);
      if (!ipAdmin(request))
        setCookie(request, response, "auriga_csrf", result.session().csrf(), false, -1);
    }
    if (!result.transaction().path("status").asString().equals("pending"))
      setCookie(request, response, transactionCookie(request), "", true, 0);
    response.setHeader("Cache-Control", "no-store");
    response.setHeader("Pragma", "no-cache");
  }
}
