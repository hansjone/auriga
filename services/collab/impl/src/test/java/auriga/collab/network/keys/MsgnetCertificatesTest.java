package auriga.collab.network.keys;

import static org.junit.jupiter.api.Assertions.*;

import java.security.KeyStore;
import java.time.Instant;
import java.util.List;
import javax.net.ssl.*;
import org.junit.jupiter.api.Test;

class MsgnetCertificatesTest {
  @Test
  void issuedServerAndClientPerformMutualTlsAndEnforceScope() throws Exception {
    Instant now = Instant.now();
    var serverCa =
        MsgnetCertificates.issue(
            "server",
            now.minusSeconds(300),
            now.plusSeconds(86400),
            List.of(),
            List.of(),
            null,
            null);
    var clientCa =
        MsgnetCertificates.issue(
            "client",
            now.minusSeconds(300),
            now.plusSeconds(86400),
            List.of(),
            List.of(),
            null,
            null);
    var server =
        MsgnetCertificates.issue(
            "submit",
            now.minusSeconds(300),
            now.plusSeconds(3600),
            List.of("localhost"),
            List.of("serverAuth"),
            serverCa.certificate(),
            serverCa.pair().getPrivate());
    var client =
        MsgnetCertificates.issue(
            "broker-client",
            now.minusSeconds(300),
            now.plusSeconds(3600),
            List.of("localhost"),
            List.of("clientAuth"),
            clientCa.certificate(),
            clientCa.pair().getPrivate());
    assertEquals(-1, server.certificate().getBasicConstraints());
    assertEquals(List.of("1.3.6.1.5.5.7.3.1"), server.certificate().getExtendedKeyUsage());
    assertThrows(
        IllegalArgumentException.class,
        () ->
            MsgnetCertificates.issue(
                "bad",
                now,
                now.plusSeconds(172800),
                List.of("localhost"),
                List.of("serverAuth"),
                serverCa.certificate(),
                serverCa.pair().getPrivate()));
    var serverContext = context(server, serverCa, clientCa);
    var clientContext = context(client, clientCa, serverCa);
    try (var listener =
            (SSLServerSocket)
                serverContext
                    .getServerSocketFactory()
                    .createServerSocket(0, 1, java.net.InetAddress.getLoopbackAddress());
        var workers = java.util.concurrent.Executors.newVirtualThreadPerTaskExecutor()) {
      listener.setNeedClientAuth(true);
      listener.setSoTimeout(5000);
      var accepted =
          workers.submit(
              () -> {
                try (var socket = (SSLSocket) listener.accept()) {
                  socket.setSoTimeout(5000);
                  socket.startHandshake();
                  return socket.getSession().getPeerPrincipal().getName();
                }
              });
      try (var socket =
          (SSLSocket)
              clientContext.getSocketFactory().createSocket("localhost", listener.getLocalPort())) {
        socket.setSoTimeout(5000);
        var parameters = socket.getSSLParameters();
        parameters.setEndpointIdentificationAlgorithm("HTTPS");
        socket.setSSLParameters(parameters);
        socket.startHandshake();
        assertTrue(socket.getSession().isValid());
      }
      assertTrue(accepted.get(10, java.util.concurrent.TimeUnit.SECONDS).contains("broker-client"));
    }
  }

  private static SSLContext context(
      MsgnetCertificates.Issued identity,
      MsgnetCertificates.Issued issuer,
      MsgnetCertificates.Issued trusted)
      throws Exception {
    var keys = KeyStore.getInstance("PKCS12");
    keys.load(null, null);
    keys.setKeyEntry(
        "identity",
        identity.pair().getPrivate(),
        new char[0],
        new java.security.cert.Certificate[] {identity.certificate(), issuer.certificate()});
    var managers = KeyManagerFactory.getInstance(KeyManagerFactory.getDefaultAlgorithm());
    managers.init(keys, new char[0]);
    var trust = KeyStore.getInstance("PKCS12");
    trust.load(null, null);
    trust.setCertificateEntry("ca", trusted.certificate());
    var trustManagers = TrustManagerFactory.getInstance(TrustManagerFactory.getDefaultAlgorithm());
    trustManagers.init(trust);
    var context = SSLContext.getInstance("TLS");
    context.init(managers.getKeyManagers(), trustManagers.getTrustManagers(), null);
    return context;
  }
}
