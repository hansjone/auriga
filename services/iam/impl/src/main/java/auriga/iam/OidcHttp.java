package auriga.iam;

import java.io.IOException;
import java.net.*;
import java.nio.charset.StandardCharsets;
import java.util.Map;
import org.apache.hc.client5.http.DnsResolver;
import org.apache.hc.client5.http.classic.methods.HttpUriRequestBase;
import org.apache.hc.client5.http.config.*;
import org.apache.hc.client5.http.impl.classic.HttpClients;
import org.apache.hc.client5.http.impl.io.PoolingHttpClientConnectionManagerBuilder;
import org.apache.hc.core5.http.ContentType;
import org.apache.hc.core5.http.io.entity.StringEntity;
import org.apache.hc.core5.util.Timeout;

/** OIDC-only transport. The resolver returns the exact addresses checked at socket creation. */
final class OidcHttp {
  static final int MAX_BODY = 1_048_576;
  private final boolean allowLocalHttp;

  record Response(int statusCode, String body) {}

  OidcHttp(boolean allowLocalHttp) {
    this.allowLocalHttp = allowLocalHttp;
  }

  static boolean localHost(String host) {
    return host != null
        && java.util.Set.of("localhost", "127.0.0.1", "::1", "[::1]")
            .contains(host.toLowerCase(java.util.Locale.ROOT));
  }

  void validate(URI uri) {
    IamError.require(
        uri.getHost() != null
            && uri.getRawUserInfo() == null
            && uri.getFragment() == null
            && ("https".equals(uri.getScheme())
                || allowLocalHttp && localHost(uri.getHost()) && "http".equals(uri.getScheme())),
        "invalid_config",
        400);
  }

  static boolean publicAddress(InetAddress address) {
    if (address.isAnyLocalAddress()
        || address.isLoopbackAddress()
        || address.isLinkLocalAddress()
        || address.isSiteLocalAddress()
        || address.isMulticastAddress()) return false;
    byte[] b = address.getAddress();
    if (b.length == 4) {
      int a = b[0] & 255, c = b[1] & 255;
      return a != 0
          && a != 10
          && a != 127
          && a < 224
          && !(a == 100 && c >= 64 && c <= 127)
          && !(a == 169 && c == 254)
          && !(a == 172 && c >= 16 && c <= 31)
          && !(a == 192 && (c == 0 || c == 168))
          && !(a == 198 && (c == 18 || c == 19));
    }
    // Only global unicast IPv6, excluding documentation and transition mechanisms.
    return (b[0] & 0xe0) == 0x20
        && !((b[0] & 255) == 0x20 && (b[1] & 255) == 0x02)
        && !((b[0] & 255) == 0x20
            && (b[1] & 255) == 1
            && ((b[2] & 255) == 0 || (b[2] & 255) == 0x0d));
  }

  InetAddress[] resolve(String host) throws UnknownHostException {
    InetAddress[] addresses = InetAddress.getAllByName(host);
    for (InetAddress address : addresses)
      if (localHost(host)
          ? !allowLocalHttp || !address.isLoopbackAddress()
          : !publicAddress(address))
        throw new UnknownHostException("OIDC target address is prohibited");
    return addresses;
  }

  Response send(URI uri, String method, String body, Map<String, String> headers)
      throws IOException {
    validate(uri);
    DnsResolver resolver =
        new DnsResolver() {
          public InetAddress[] resolve(String host) throws UnknownHostException {
            return OidcHttp.this.resolve(host);
          }

          public String resolveCanonicalHostname(String host) {
            return host;
          }
        };
    // A short-lived client avoids a background pool and retains strict per-request DNS checks.
    try (var manager =
            PoolingHttpClientConnectionManagerBuilder.create()
                .setDnsResolver(resolver)
                .setDefaultConnectionConfig(
                    ConnectionConfig.custom()
                        .setConnectTimeout(Timeout.ofSeconds(10))
                        .setSocketTimeout(Timeout.ofSeconds(10))
                        .build())
                .build();
        var client =
            HttpClients.custom()
                .setConnectionManager(manager)
                .disableRedirectHandling()
                .disableAutomaticRetries()
                .disableCookieManagement()
                .setDefaultRequestConfig(
                    RequestConfig.custom()
                        .setResponseTimeout(Timeout.ofSeconds(10))
                        .setConnectionRequestTimeout(Timeout.ofSeconds(10))
                        .build())
                .build()) {
      var request = new HttpUriRequestBase(method, uri);
      headers.forEach(request::setHeader);
      request.setHeader("Accept", "application/json");
      if (body != null)
        request.setEntity(new StringEntity(body, ContentType.APPLICATION_FORM_URLENCODED));
      return client.execute(
          request,
          response -> {
            var entity = response.getEntity();
            if (entity == null) return new Response(response.getCode(), "");
            if (entity.getContentLength() > MAX_BODY) {
              request.cancel();
              throw new IOException("OIDC response too large");
            }
            try (var stream = entity.getContent()) {
              byte[] bytes = stream.readNBytes(MAX_BODY + 1);
              if (bytes.length > MAX_BODY) {
                request.cancel();
                throw new IOException("OIDC response too large");
              }
              return new Response(response.getCode(), new String(bytes, StandardCharsets.UTF_8));
            }
          });
    }
  }
}
