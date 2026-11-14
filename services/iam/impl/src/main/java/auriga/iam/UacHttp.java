package auriga.iam;

import java.io.IOException;
import java.net.*;
import java.util.*;
import org.apache.hc.client5.http.DnsResolver;
import org.apache.hc.client5.http.classic.methods.HttpUriRequestBase;
import org.apache.hc.client5.http.config.*;
import org.apache.hc.client5.http.impl.classic.HttpClients;
import org.apache.hc.client5.http.impl.io.PoolingHttpClientConnectionManagerBuilder;
import org.apache.hc.core5.http.ContentType;
import org.apache.hc.core5.http.io.entity.StringEntity;
import org.apache.hc.core5.util.Timeout;
import tools.jackson.databind.JsonNode;

/**
 * Destination and resolved addresses are checked at socket creation, including private networks.
 */
final class UacHttp {
  record Response(int status, byte[] body) {
    @Override
    public String toString() {
      return "UacResponse[status=" + status + "]";
    }
  }

  private final Map<String, Set<String>> destinations;
  private final boolean testLoopback;

  UacHttp(JsonNode settings, boolean testLoopback) {
    this.testLoopback = testLoopback;
    Map<String, Set<String>> entries = new HashMap<>();
    for (JsonNode entry : settings.path("allowedDestinations")) {
      URI origin = URI.create(entry.path("origin").asString());
      Set<String> addresses = new HashSet<>();
      for (JsonNode value : entry.path("addresses")) {
        try {
          if (!value.asString().matches("[0-9a-fA-F:.]+")) throw new IllegalArgumentException();
          addresses.add(InetAddress.getByName(value.asString()).getHostAddress());
        } catch (UnknownHostException e) {
          throw new IllegalArgumentException("Invalid UAC address allowlist");
        }
      }
      if (addresses.isEmpty() || origin.getHost() == null)
        throw new IllegalArgumentException("Invalid UAC destination allowlist");
      entries.put(origin(origin), Set.copyOf(addresses));
    }
    destinations = Map.copyOf(entries);
  }

  void validate(URI uri, boolean base) {
    boolean local =
        testLoopback && "http".equals(uri.getScheme()) && OidcHttp.localHost(uri.getHost());
    IamError.validate(
        uri.getHost() != null
            && ("https".equals(uri.getScheme()) || local)
            && uri.getRawUserInfo() == null
            && uri.getRawFragment() == null
            && (!base || uri.getRawQuery() == null)
            && destinations.containsKey(origin(uri)));
  }

  private static String origin(URI uri) {
    return uri.getScheme()
        + "://"
        + (uri.getHost() == null ? "" : uri.getHost().toLowerCase(Locale.ROOT))
        + ":"
        + (uri.getPort() == -1 ? ("https".equals(uri.getScheme()) ? 443 : 80) : uri.getPort());
  }

  Response post(URI uri, JsonNode body, Map<String, String> headers, int seconds)
      throws IOException {
    validate(uri, false);
    Set<String> allowed = destinations.get(origin(uri));
    DnsResolver resolver =
        new DnsResolver() {
          public InetAddress[] resolve(String host) throws UnknownHostException {
            InetAddress[] resolved = InetAddress.getAllByName(host);
            for (InetAddress address : resolved)
              if (!allowed.contains(address.getHostAddress()))
                throw new UnknownHostException("UAC destination rejected");
            return resolved;
          }

          public String resolveCanonicalHostname(String host) {
            return host;
          }
        };
    Timeout timeout = Timeout.ofSeconds(seconds);
    try (var manager =
            PoolingHttpClientConnectionManagerBuilder.create()
                .setDnsResolver(resolver)
                .setDefaultConnectionConfig(
                    ConnectionConfig.custom()
                        .setConnectTimeout(timeout)
                        .setSocketTimeout(timeout)
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
                        .setResponseTimeout(timeout)
                        .setConnectionRequestTimeout(timeout)
                        .build())
                .build()) {
      var request = new HttpUriRequestBase("POST", uri);
      request.setEntity(new StringEntity(body.toString(), ContentType.APPLICATION_JSON));
      headers.forEach(request::setHeader);
      request.setHeader("Content-Type", "application/json;charset=UTF-8");
      Thread deadline =
          Thread.ofVirtual()
              .start(
                  () -> {
                    try {
                      Thread.sleep(seconds * 1000L);
                      request.cancel();
                    } catch (InterruptedException ignored) {
                      Thread.currentThread().interrupt();
                    }
                  });
      try {
        return client.execute(
            request,
            response -> {
              var entity = response.getEntity();
              if (entity == null) return new Response(response.getCode(), new byte[0]);
              if (entity.getContentLength() > UacTokenVerification.MAX_BODY) {
                request.cancel();
                throw new IOException("UAC response limit exceeded");
              }
              try (var input = entity.getContent()) {
                byte[] bytes = input.readNBytes(UacTokenVerification.MAX_BODY + 1);
                if (bytes.length > UacTokenVerification.MAX_BODY) {
                  request.cancel();
                  throw new IOException("UAC response limit exceeded");
                }
                return new Response(response.getCode(), bytes);
              }
            });
      } finally {
        deadline.interrupt();
      }
    }
  }
}
