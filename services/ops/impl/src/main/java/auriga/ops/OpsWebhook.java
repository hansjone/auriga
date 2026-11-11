package auriga.ops;

import java.io.*;
import java.net.*;
import java.nio.charset.StandardCharsets;
import javax.net.ssl.*;

/**
 * Resolves once, verifies every answer, pins the socket, verifies TLS hostname; no redirects/proxy.
 */
class OpsWebhook {
  static URI target(String value) {
    try {
      URI u = URI.create(value);
      OpsError.require(
          value.length() <= 2048
              && "https".equals(u.getScheme())
              && u.getHost() != null
              && u.getUserInfo() == null
              && u.getFragment() == null
              && (u.getPort() == -1 || u.getPort() == 443));
      String host = u.getHost();
      OpsError.require(
          host.matches("(?i)[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?")
              && !host.matches("[0-9.]+")
              && host.contains("."));
      return u;
    } catch (IllegalArgumentException e) {
      throw OpsError.error("invalid", 400);
    }
  }

  static boolean publicAddress(InetAddress address) {
    byte[] b = address.getAddress();
    if (b.length != 4) return false;
    int a = b[0] & 255, c = b[1] & 255, d = b[2] & 255;
    return !(a == 0
        || a == 10
        || a == 127
        || a >= 224
        || (a == 100 && c >= 64 && c <= 127)
        || (a == 169 && c == 254)
        || (a == 172 && c >= 16 && c <= 31)
        || (a == 192 && (c == 168 || c == 0 || (c == 88 && d == 99)))
        || (a == 198 && (c == 18 || c == 19 || (c == 51 && d == 100)))
        || (a == 203 && c == 0 && d == 113)
        || address.isAnyLocalAddress()
        || address.isLoopbackAddress()
        || address.isLinkLocalAddress()
        || address.isSiteLocalAddress()
        || address.isMulticastAddress());
  }

  static void checkAddresses(InetAddress[] addresses) {
    OpsError.require(addresses.length > 0);
    for (var a : addresses) OpsError.require(publicAddress(a));
  }

  void send(String url, String bearer, String payload) {
    URI u = target(url);
    OpsError.require(bearer.length() <= 4096 && bearer.chars().allMatch(c -> c >= 33 && c <= 126));
    try {
      InetAddress[] answers = InetAddress.getAllByName(u.getHost());
      checkAddresses(answers);
      try (Socket raw = new Socket()) {
        raw.connect(new InetSocketAddress(answers[0], 443), 3000);
        raw.setSoTimeout(5000);
        try (SSLSocket tls =
            (SSLSocket)
                ((SSLSocketFactory) SSLSocketFactory.getDefault())
                    .createSocket(raw, u.getHost(), 443, true)) {
          SSLParameters params = tls.getSSLParameters();
          params.setEndpointIdentificationAlgorithm("HTTPS");
          tls.setSSLParameters(params);
          tls.startHandshake();
          String path = u.getRawPath().isEmpty() ? "/" : u.getRawPath();
          if (u.getRawQuery() != null) path += "?" + u.getRawQuery();
          byte[] bytes = payload.getBytes(StandardCharsets.UTF_8);
          String head =
              "POST "
                  + path
                  + " HTTP/1.1\r\nHost: "
                  + u.getHost()
                  + "\r\nContent-Type: application/json\r\nConnection: close\r\nContent-Length: "
                  + bytes.length
                  + "\r\n"
                  + (bearer.isEmpty() ? "" : "Authorization: Bearer " + bearer + "\r\n")
                  + "\r\n";
          tls.getOutputStream().write(head.getBytes(StandardCharsets.US_ASCII));
          tls.getOutputStream().write(bytes);
          tls.getOutputStream().flush();
          var input = tls.getInputStream();
          var line = new ByteArrayOutputStream();
          long deadline = System.nanoTime() + 5_000_000_000L;
          for (int c; (c = input.read()) != -1 && c != '\n'; ) {
            if (line.size() >= 1024 || System.nanoTime() > deadline)
              throw OpsError.error("delivery_failed", 502);
            line.write(c);
          }
          if (!line.toString(StandardCharsets.US_ASCII).matches("HTTP/1\\.[01] 2[0-9]{2} .*"))
            throw OpsError.error("delivery_failed", 502);
        }
      }
    } catch (IOException e) {
      throw OpsError.error("delivery_failed", 502);
    }
  }
}
