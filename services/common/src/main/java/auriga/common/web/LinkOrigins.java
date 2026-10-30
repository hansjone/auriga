package auriga.common.web;

import jakarta.servlet.*;
import jakarta.servlet.http.HttpServletRequest;
import java.io.IOException;
import java.net.URI;
import java.util.List;
import java.util.Set;
import org.springframework.security.web.util.matcher.IpAddressMatcher;

/**
 * Approved request origins for links and same-root issuers; never a principal or CSRF authority.
 */
public final class LinkOrigins implements Filter {
  private static final ScopedValue<String> CURRENT = ScopedValue.newInstance();
  private final Set<String> allowed;
  private final List<IpAddressMatcher> proxies;

  public LinkOrigins(List<String> allowedOrigins, List<String> trustedProxies) {
    allowedOrigins.forEach(LinkOrigins::canonicalOrigin);
    allowed = Set.copyOf(allowedOrigins);
    proxies = trustedProxies.stream().map(LinkOrigins::proxy).toList();
  }

  public static String currentOr(String defaultOrigin) {
    return CURRENT.isBound() && !CURRENT.get().isEmpty() ? CURRENT.get() : defaultOrigin;
  }

  /**
   * Installation configuration only. Issuer aliases never select another verification key source.
   */
  public static Set<String> issuerOrigins(String defaultIssuer, List<String> allowedOrigins) {
    allowedOrigins.forEach(LinkOrigins::canonicalOrigin);
    var issuers = new java.util.HashSet<>(allowedOrigins);
    issuers.add(java.util.Objects.requireNonNull(defaultIssuer));
    return Set.copyOf(issuers);
  }

  public <T, X extends Throwable> T call(
      HttpServletRequest request, ScopedValue.CallableOp<T, X> work) throws X {
    return ScopedValue.where(CURRENT, select(request)).call(work);
  }

  private String select(HttpServletRequest request) {
    if (allowed.isEmpty()) return "";
    boolean trusted = proxies.stream().anyMatch(proxy -> proxy.matches(request.getRemoteAddr()));
    String host = single(request, trusted ? "X-Forwarded-Host" : "Host");
    String scheme = trusted ? single(request, "X-Forwarded-Proto") : request.getScheme();
    String candidate = scheme + "://" + host;
    // Exact membership also rejects duplicates, comma chains and noncanonical authorities.
    return allowed.contains(candidate) ? candidate : "";
  }

  private static String single(HttpServletRequest request, String name) {
    var values = java.util.Collections.list(request.getHeaders(name));
    return values.size() == 1 ? values.getFirst() : "";
  }

  private static IpAddressMatcher proxy(String value) {
    String[] parts = value.split("/", -1);
    if (parts.length > 2
        || !parts[0].matches("[0-9a-fA-F:.]+")
        || parts.length == 2 && !parts[1].matches("[1-9][0-9]*"))
      throw new IllegalArgumentException("Link proxy must be a numeric IP or nonzero CIDR");
    return new IpAddressMatcher(value);
  }

  private static void canonicalOrigin(String value) {
    try {
      URI uri = URI.create(value);
      String scheme = uri.getScheme();
      String host = uri.getHost();
      int port = uri.getPort();
      if (!Set.of("http", "https").contains(scheme)
          || host == null
          || host.length() > 253
          || !host.matches("[a-z0-9.-]+")
          || port == 0
          || port > 65535
          || port == (scheme.equals("https") ? 443 : 80)) throw new IllegalArgumentException();
      for (String label : host.split("\\.", -1))
        if (label.length() > 63 || !label.matches("[a-z0-9](?:[a-z0-9-]*[a-z0-9])?"))
          throw new IllegalArgumentException();
      if (host.matches("[0-9.]+")) {
        String[] octets = host.split("\\.", -1);
        if (octets.length != 4) throw new IllegalArgumentException();
        for (String octet : octets)
          if (!octet.matches("0|[1-9][0-9]{0,2}") || Integer.parseInt(octet) > 255)
            throw new IllegalArgumentException();
      }
      String canonical = scheme + "://" + host + (port < 0 ? "" : ":" + port);
      if (!canonical.equals(value)) throw new IllegalArgumentException();
    } catch (RuntimeException error) {
      throw new IllegalArgumentException("Link origin must be a canonical HTTP(S) origin");
    }
  }

  @Override
  public void doFilter(ServletRequest request, ServletResponse response, FilterChain chain)
      throws IOException, ServletException {
    try {
      call(
          (HttpServletRequest) request,
          () -> {
            chain.doFilter(request, response);
            return null;
          });
    } catch (IOException | ServletException error) {
      throw error;
    } catch (Exception error) {
      throw new ServletException(error);
    }
  }
}
