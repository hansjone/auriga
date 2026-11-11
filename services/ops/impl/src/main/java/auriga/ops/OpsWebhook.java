package auriga.ops;

import java.io.*;
import java.net.*;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.util.concurrent.*;
import javax.net.ssl.*;

/** Resolves once, selects only public IPv4, pins the socket and verifies TLS hostname. */
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

  private static final Semaphore SLOTS = new Semaphore(8);

  static InetAddress selectAddress(InetAddress[] addresses) {
    for (var address : addresses) if (publicAddress(address)) return address;
    throw OpsError.error("invalid", 400);
  }

  /** Includes DNS, connect, TLS, write and response; close interrupts blocked socket I/O. */
  static void bounded(Duration timeout, Socket socket, Runnable work) {
    if (!SLOTS.tryAcquire()) {
      close(socket);
      throw OpsError.error("unavailable", 503);
    }
    var task =
        new FutureTask<Void>(
            () -> {
              work.run();
              return null;
            });
    Thread.ofVirtual()
        .name("ops-webhook")
        .start(
            () -> {
              try {
                task.run();
              } finally {
                SLOTS.release();
              }
            });
    try {
      task.get(timeout.toMillis(), TimeUnit.MILLISECONDS);
    } catch (ExecutionException e) {
      if (e.getCause() instanceof RuntimeException failure) throw failure;
      throw OpsError.error("delivery_failed", 502);
    } catch (TimeoutException e) {
      throw OpsError.error("delivery_failed", 502);
    } catch (InterruptedException e) {
      Thread.currentThread().interrupt();
      throw OpsError.error("delivery_failed", 502);
    } finally {
      close(socket);
      task.cancel(true);
    }
  }

  private static void close(Socket socket) {
    try {
      socket.close();
    } catch (IOException ignored) {
      /* already failed/closed */
    }
  }

  void send(String url, String bearer, String payload) {
    URI u = target(url);
    OpsError.require(bearer.length() <= 4096 && bearer.chars().allMatch(c -> c >= 33 && c <= 126));
    try {
      try (Socket raw = new Socket()) {
        bounded(Duration.ofSeconds(10), raw, () -> send(raw, u, bearer, payload));
      }
    } catch (IOException e) {
      throw OpsError.error("delivery_failed", 502);
    }
  }

  private void send(Socket raw, URI u, String bearer, String payload) {
    try {
      var address = selectAddress(InetAddress.getAllByName(u.getHost()));
      raw.connect(new InetSocketAddress(address, 443), 3000);
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
    } catch (IOException e) {
      throw OpsError.error("delivery_failed", 502);
    }
  }
}
