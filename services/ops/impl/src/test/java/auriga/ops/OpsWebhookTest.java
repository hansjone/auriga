package auriga.ops;

import static org.junit.jupiter.api.Assertions.*;

import java.net.InetAddress;
import org.junit.jupiter.api.Test;

class OpsWebhookTest {
  @Test
  void blocksPrivateMetadataAndAmbiguousTargets() throws Exception {
    for (String host :
        new String[] {
          "127.0.0.1",
          "10." + "0.0.1",
          "169.254.169.254",
          "192." + "168.1.1",
          "100.64.0.1",
          "198.18.0.1",
          "::1",
          "::ffff:127.0.0.1",
          "2001:db8::1"
        }) { // gitleaks:allow
      assertFalse(OpsWebhook.publicAddress(InetAddress.getByName(host)), host);
    }
    for (String url :
        new String[] {
          "http://example.com",
          "https://user:pass@example.com",
          "https://example.com:8443",
          "https://example.com/#fragment",
          "https://127.1"
        }) {
      assertThrows(RuntimeException.class, () -> OpsWebhook.target(url));
    }
  }

  @Test
  void selectsOnlyPublicIpv4FromDualStackDns() throws Exception {
    var public4 = InetAddress.getByName("93.184.216.34");
    var public6 = InetAddress.getByName("2606:4700:4700::1111");
    var private4 = InetAddress.getLoopbackAddress();
    assertEquals(public4, OpsWebhook.selectAddress(new InetAddress[] {public6, public4}));
    assertEquals(public4, OpsWebhook.selectAddress(new InetAddress[] {private4, public6, public4}));
    assertThrows(
        RuntimeException.class, () -> OpsWebhook.selectAddress(new InetAddress[] {private4}));
    assertThrows(
        RuntimeException.class,
        () -> OpsWebhook.selectAddress(new InetAddress[] {private4, public6}));
    assertThrows(RuntimeException.class, () -> OpsWebhook.selectAddress(new InetAddress[] {}));
  }

  @Test
  void totalDeadlineClosesSocketAndBoundsCaller() throws Exception {
    var raw = new java.net.Socket();
    var exited = new java.util.concurrent.CountDownLatch(1);
    long started = System.nanoTime();
    assertThrows(
        RuntimeException.class,
        () ->
            OpsWebhook.bounded(
                java.time.Duration.ofMillis(100),
                raw,
                () -> {
                  try {
                    Thread.sleep(10000);
                  } catch (InterruptedException e) {
                    Thread.currentThread().interrupt();
                  } finally {
                    exited.countDown();
                  }
                }));
    assertTrue(raw.isClosed());
    assertTrue(exited.await(2, java.util.concurrent.TimeUnit.SECONDS));
    assertTrue(System.nanoTime() - started < java.time.Duration.ofSeconds(3).toNanos());
  }

  @Test
  void stalledTlsHandshakeIsClosedAtTotalDeadline() throws Exception {
    var factory = (javax.net.ssl.SSLSocketFactory) javax.net.ssl.SSLSocketFactory.getDefault();
    try (var server = new java.net.ServerSocket(0, 1, InetAddress.getLoopbackAddress());
        var raw = new java.net.Socket()) {
      raw.connect(new java.net.InetSocketAddress(server.getInetAddress(), server.getLocalPort()));
      var accepted = server.accept();
      try (accepted) {
        assertThrows(
            RuntimeException.class,
            () ->
                OpsWebhook.bounded(
                    java.time.Duration.ofMillis(200),
                    raw,
                    () -> {
                      try (var tls =
                          (javax.net.ssl.SSLSocket)
                              factory.createSocket(raw, "localhost", server.getLocalPort(), true)) {
                        tls.startHandshake();
                      } catch (java.io.IOException e) {
                        throw new java.io.UncheckedIOException(e);
                      }
                    }));
        assertTrue(raw.isClosed());
      }
    }
  }

  @Test
  void encryptsSecretsWithTenantBinding() {
    var vault = new OpsSecrets(java.util.Base64.getEncoder().encodeToString(new byte[32]));
    String encrypted = vault.seal("tenant-a", "sensitive");
    assertFalse(encrypted.contains("sensitive"));
    assertEquals("sensitive", vault.open("tenant-a", encrypted));
    assertThrows(RuntimeException.class, () -> vault.open("tenant-b", encrypted));
    assertThrows(RuntimeException.class, () -> new OpsSecrets("").seal("t", "value"));
  }
}
