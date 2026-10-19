package io.github.hansjone.auriga.testsupport;

import static org.junit.jupiter.api.Assertions.*;

import java.net.Socket;
import java.net.URI;
import java.nio.charset.StandardCharsets;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;

@ExtendWith(NatsExtension.class)
class NatsExtensionTest {
  @Test
  void startsJetStreamServerOnLoopbackRandomPort(NatsServer server) throws Exception {
    assertTrue(server.isAlive());
    URI uri = URI.create(server.url());
    assertEquals("127.0.0.1", uri.getHost());
    assertNotEquals(4222, uri.getPort());
    assertEquals(server.url(), NatsExtension.url());
    try (Socket socket = new Socket(uri.getHost(), uri.getPort())) {
      byte[] info = new byte[512];
      int n = socket.getInputStream().read(info);
      String greeting = new String(info, 0, n, StandardCharsets.UTF_8);
      assertTrue(greeting.startsWith("INFO "), greeting);
      assertTrue(greeting.contains("\"jetstream\":true"), greeting);
      assertTrue(greeting.contains("\"version\":\"" + NatsServer.VERSION + "\""), greeting);
    }
  }

  @Test
  void separateServerStopsItsProcess() {
    NatsServer extra = NatsServer.start();
    assertTrue(extra.isAlive());
    extra.close();
    assertFalse(extra.isAlive());
  }

  @Test
  void cachedBinaryIsCheckedAgainstThePinnedHash(
      @org.junit.jupiter.api.io.TempDir java.nio.file.Path dir) throws Exception {
    String platform = NatsServer.platform();
    if (System.getenv("AURIGA_NATS_SERVER") == null) {
      // the binary this run uses passed the check
      assertTrue(NatsServer.binaryMatches(NatsServer.binary(), platform));
    }
    java.nio.file.Path tampered = dir.resolve("nats-server");
    java.nio.file.Files.writeString(tampered, "not the nats-server binary");
    assertFalse(NatsServer.binaryMatches(tampered, platform));
    assertFalse(NatsServer.binaryMatches(dir.resolve("missing"), platform));
  }
}
