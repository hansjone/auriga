package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;

import java.io.*;
import java.net.*;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.*;
import java.util.concurrent.atomic.AtomicReference;
import javax.net.ssl.*;
import org.junit.jupiter.api.Test;

class RemoteMessageNetworkLedgerTest {
  @Test
  void authenticatesTlsAndReadsOnlyTheFixedNoStoreLedger() throws Exception {
    var ca = IamHopTlsTest.certificate("test-ca", null, true);
    var serverIdentity = IamHopTlsTest.certificate("localhost", ca, false);
    var service = IamHopTlsTest.certificate("iam-service", ca, false);
    SSLContext serverTLS = IamHopTlsTest.client(serverIdentity, ca),
        clientTLS = IamHopTlsTest.client(service, ca);
    var requests = new CopyOnWriteArrayList<String>();
    var authorization = new CopyOnWriteArrayList<String>();
    var body =
        new AtomicReference<>(
            "{\"tenant_id\":\"tenant\",\"realm\":\"1\",\"target_id\":\"target\",\"minimum_control_revision\":\"1\",\"control_digest\":\"sha256:"
                + "0".repeat(64)
                + "\",\"state\":\"ready\",\"identity_compromised\":false,\"challenge\":\"challenge\",\"new_chain_ready\":true}");
    try (var listener =
            (SSLServerSocket)
                serverTLS
                    .getServerSocketFactory()
                    .createServerSocket(0, 10, InetAddress.getLoopbackAddress());
        var executor = Executors.newVirtualThreadPerTaskExecutor()) {
      listener.setNeedClientAuth(true);
      listener.setSoTimeout(10000);
      var server =
          executor.submit(
              () -> {
                try {
                  for (int count = 0; count < 4; count++)
                    try (var socket = (SSLSocket) listener.accept()) {
                      socket.setSoTimeout(5000);
                      socket.startHandshake();
                      assertNotNull(socket.getSession().getPeerPrincipal());
                      var reader =
                          new BufferedReader(
                              new InputStreamReader(
                                  socket.getInputStream(), StandardCharsets.US_ASCII));
                      String line = reader.readLine();
                      requests.add(line);
                      while (!(line = reader.readLine()).isEmpty())
                        if (line.toLowerCase(java.util.Locale.ROOT).startsWith("authorization:"))
                          authorization.add(line);
                      String payload = body.get();
                      int status = requests.getLast().contains("/controls/pending/") ? 404 : 200;
                      byte[] raw = payload.getBytes(StandardCharsets.UTF_8);
                      socket
                          .getOutputStream()
                          .write(
                              ("HTTP/1.1 "
                                      + status
                                      + " result\r\nCache-Control: no-store\r\nContent-Type: application/json\r\nContent-Length: "
                                      + raw.length
                                      + "\r\nConnection: close\r\n\r\n")
                                  .getBytes(StandardCharsets.US_ASCII));
                      socket.getOutputStream().write(raw);
                      socket.getOutputStream().flush();
                    }
                } catch (Exception e) {
                  throw new RuntimeException(e);
                }
              });
      var origin = URI.create("https://localhost:" + listener.getLocalPort());
      try (var ledger =
          new RemoteMessageNetworkLedger(origin, clientTLS, () -> "synthetic-service-token")) {
        assertEquals("target", ledger.isolation("tenant", "1", "target").getTargetId());
        assertTrue(ledger.pending("tenant", "1", "target").isEmpty());
        assertThrows(RuntimeException.class, () -> ledger.isolation("other-tenant", "1", "target"));
        body.set("{}");
        assertThrows(RuntimeException.class, () -> ledger.isolation("tenant", "1", "target"));
        assertThrows(
            IllegalArgumentException.class, () -> ledger.isolation("tenant", "1", "../untrusted"));
      }
      server.get(15, TimeUnit.SECONDS);
      assertEquals(4, authorization.size());
      assertTrue(requests.stream().allMatch(r -> r.startsWith("GET /api/collab/v2/network/")));
    }
  }
}
