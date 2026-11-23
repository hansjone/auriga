package auriga.iam;

import io.grpc.Context;
import java.io.IOException;
import java.net.*;
import java.util.*;
import org.apache.hc.client5.http.DnsResolver;
import org.apache.hc.client5.http.classic.methods.HttpUriRequestBase;
import org.apache.hc.client5.http.config.*;
import org.apache.hc.client5.http.impl.classic.HttpClients;
import org.apache.hc.client5.http.impl.io.PoolingHttpClientConnectionManagerBuilder;
import org.apache.hc.core5.http.ContentType;
import org.apache.hc.core5.http.io.entity.ByteArrayEntity;
import org.apache.hc.core5.util.Timeout;
import tools.jackson.databind.JsonNode;

/**
 * Destination and resolved addresses are checked at socket creation, including private networks.
 */
final class UacHttp {
  @FunctionalInterface
  interface DnsLookup {
    InetAddress[] resolve(String host) throws UnknownHostException;
  }

  record Response(int status, byte[] body, Map<String, List<String>> headers) {
    Response(int status, byte[] body) {
      this(status, body, Map.of());
    }

    @Override
    public String toString() {
      return "UacResponse[status=" + status + "]";
    }
  }

  private record DestinationPolicy(Set<String> addresses, boolean dynamicAddresses) {
    boolean allows(InetAddress address, boolean testLoopback) {
      if (!dynamicAddresses) return addresses.contains(address.getHostAddress());
      return isSafeDynamicAddress(address, testLoopback);
    }
  }

  private final Map<String, DestinationPolicy> destinations;

  /** Empty or missing allowedDestinations: any HTTPS origin, resolved address not checked. */
  private final boolean unrestricted;

  private final boolean testLoopback;
  private final DnsLookup dnsLookup;

  UacHttp(JsonNode settings, boolean testLoopback) {
    this(settings, testLoopback, InetAddress::getAllByName);
  }

  UacHttp(JsonNode settings, boolean testLoopback, DnsLookup dnsLookup) {
    this.testLoopback = testLoopback;
    this.dnsLookup = Objects.requireNonNull(dnsLookup, "dnsLookup");
    Map<String, DestinationPolicy> entries = new HashMap<>();
    JsonNode list = settings.path("allowedDestinations");
    if (!list.isMissingNode() && !list.isNull() && !list.isArray())
      throw new IllegalArgumentException("Invalid UAC destination allowlist");
    for (JsonNode entry : list) {
      if (!entry.isObject() || !entry.path("origin").isString())
        throw new IllegalArgumentException("Invalid UAC destination allowlist");
      URI origin = URI.create(entry.path("origin").asString());
      if (origin.getHost() == null || origin.getScheme() == null)
        throw new IllegalArgumentException("Invalid UAC destination allowlist");
      JsonNode dynamicNode = entry.path("allowDynamicAddresses");
      if (!dynamicNode.isMissingNode() && !dynamicNode.isBoolean())
        throw new IllegalArgumentException("Invalid UAC destination allowlist");
      boolean dynamicAddresses = dynamicNode.asBoolean(false);
      JsonNode addressesNode = entry.path("addresses");
      if (!addressesNode.isMissingNode() && !addressesNode.isNull() && !addressesNode.isArray())
        throw new IllegalArgumentException("Invalid UAC address allowlist");
      Set<String> addresses = new HashSet<>();
      for (JsonNode value : addressesNode) {
        if (!value.isString() || !value.asString().matches("[0-9a-fA-F:.]+"))
          throw new IllegalArgumentException("Invalid UAC address allowlist");
        try {
          addresses.add(InetAddress.ofLiteral(value.asString()).getHostAddress());
        } catch (IllegalArgumentException e) {
          throw new IllegalArgumentException("Invalid UAC address allowlist", e);
        }
      }
      if (dynamicAddresses) {
        if (!addresses.isEmpty() || parseLiteralAddress(origin.getHost()) != null)
          throw new IllegalArgumentException("Invalid UAC destination allowlist");
      } else if (addresses.isEmpty()) {
        throw new IllegalArgumentException("Invalid UAC destination allowlist");
      }
      String key = origin(origin);
      if (entries.putIfAbsent(key, new DestinationPolicy(Set.copyOf(addresses), dynamicAddresses))
          != null) throw new IllegalArgumentException("Invalid UAC destination allowlist");
    }
    destinations = Map.copyOf(entries);
    unrestricted = destinations.isEmpty();
  }

  static boolean isSafeDynamicAddress(InetAddress address, boolean testLoopback) {
    if (testLoopback && address.isLoopbackAddress()) return true;
    if (address.isAnyLocalAddress()
        || address.isLoopbackAddress()
        || address.isLinkLocalAddress()
        || address.isMulticastAddress()) return false;
    byte[] bytes = address.getAddress();
    if (bytes.length == 4) {
      int first = bytes[0] & 0xff;
      return first != 0 && first < 224;
    }
    return true;
  }

  private static InetAddress parseLiteralAddress(String host) {
    if (host == null || host.isEmpty()) return null;
    String candidate =
        host.startsWith("[") && host.endsWith("]") ? host.substring(1, host.length() - 1) : host;
    try {
      return InetAddress.ofLiteral(candidate);
    } catch (IllegalArgumentException ignored) {
      return null;
    }
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
            && (unrestricted || destinations.containsKey(origin(uri))));
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
    var supplied = new HashMap<>(headers);
    supplied.put("Content-Type", "application/json;charset=UTF-8");
    return execute(
        uri,
        "POST",
        body.toString().getBytes(java.nio.charset.StandardCharsets.UTF_8),
        supplied,
        seconds * 1000,
        UacTokenVerification.MAX_BODY,
        Context.ROOT);
  }

  InetAddress[] resolve(URI uri) throws UnknownHostException {
    validate(uri, false);
    DestinationPolicy policy = destinations.get(origin(uri));
    InetAddress literal = parseLiteralAddress(uri.getHost());
    if (literal != null) {
      if (policy != null && !policy.allows(literal, testLoopback))
        throw new UnknownHostException("UAC destination rejected");
      return new InetAddress[] {literal};
    }
    return resolveHost(policy, uri.getHost());
  }

  private InetAddress[] resolveHost(DestinationPolicy policy, String host)
      throws UnknownHostException {
    InetAddress[] resolved = dnsLookup.resolve(host);
    if (resolved == null || resolved.length == 0)
      throw new UnknownHostException("UAC destination rejected");
    if (policy != null)
      for (InetAddress address : resolved)
        if (!policy.allows(address, testLoopback))
          throw new UnknownHostException("UAC destination rejected");
    return resolved;
  }

  Response execute(
      URI uri,
      String method,
      byte[] body,
      Map<String, String> headers,
      int timeoutMillis,
      int maximum,
      Context context)
      throws IOException {
    validate(uri, false);
    if (context.isCancelled()) throw new java.io.InterruptedIOException("UAC request cancelled");
    DestinationPolicy policy = destinations.get(origin(uri));
    // A numeric HttpHost may bypass the DNS resolver in an HTTP transport.
    // Apply the same policy before connecting in that case as well.
    InetAddress literal = parseLiteralAddress(uri.getHost());
    if (literal != null && policy != null && !policy.allows(literal, testLoopback))
      throw new UnknownHostException("UAC destination rejected");
    DnsResolver resolver =
        new DnsResolver() {
          public InetAddress[] resolve(String host) throws UnknownHostException {
            return resolveHost(policy, host);
          }

          public String resolveCanonicalHostname(String host) {
            return host;
          }
        };
    Timeout timeout = Timeout.ofMilliseconds(timeoutMillis);
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
      var request = new HttpUriRequestBase(method, uri);
      if (!Set.of("GET", "HEAD").contains(method))
        request.setEntity(new ByteArrayEntity(body, ContentType.APPLICATION_OCTET_STREAM));
      headers.forEach(request::setHeader);
      Context.CancellationListener cancellation = ignored -> request.cancel();
      context.addListener(cancellation, Runnable::run);
      Thread deadline =
          Thread.ofVirtual()
              .start(
                  () -> {
                    try {
                      Thread.sleep(timeoutMillis);
                      request.cancel();
                    } catch (InterruptedException ignored) {
                      Thread.currentThread().interrupt();
                    }
                  });
      try {
        return client.execute(
            request,
            response -> {
              Map<String, List<String>> received = new HashMap<>();
              for (var header : response.getHeaders())
                received
                    .computeIfAbsent(
                        header.getName().toLowerCase(Locale.ROOT), ignored -> new ArrayList<>())
                    .add(header.getValue());
              var entity = response.getEntity();
              if (entity == null) return new Response(response.getCode(), new byte[0], received);
              if (entity.getContentLength() > maximum) {
                request.cancel();
                throw new IOException("UAC response limit exceeded");
              }
              try (var input = entity.getContent()) {
                byte[] bytes = input.readNBytes(maximum + 1);
                if (bytes.length > maximum) {
                  request.cancel();
                  throw new IOException("UAC response limit exceeded");
                }
                return new Response(response.getCode(), bytes, received);
              }
            });
      } finally {
        deadline.interrupt();
        context.removeListener(cancellation);
      }
    }
  }
}
