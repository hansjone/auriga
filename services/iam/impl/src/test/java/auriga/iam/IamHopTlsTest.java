package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.error.AurigaException;
import jakarta.servlet.http.*;
import java.net.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.security.*;
import java.security.cert.*;
import java.time.Instant;
import java.util.*;
import javax.net.ssl.*;
import org.bouncycastle.asn1.*;
import org.bouncycastle.asn1.x500.X500Name;
import org.bouncycastle.asn1.x509.*;
import org.bouncycastle.asn1.x9.X9ObjectIdentifiers;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.io.TempDir;
import org.springframework.boot.tomcat.servlet.TomcatServletWebServerFactory;
import org.springframework.boot.web.server.Ssl;

/**
 * Real TLS handshake and real servlet certificate attributes; no synthetic TLS request attributes.
 */
class IamHopTlsTest {
  @TempDir Path dir;

  record Material(KeyPair key, X509Certificate cert) {}

  static KeyPair key() throws Exception {
    var gen = KeyPairGenerator.getInstance("EC");
    gen.initialize(new java.security.spec.ECGenParameterSpec("secp256r1"));
    return gen.generateKeyPair();
  }

  static Material certificate(String dns, Material ca, boolean root) throws Exception {
    var key = key();
    var now = Instant.now();
    var algorithm = new AlgorithmIdentifier(X9ObjectIdentifiers.ecdsa_with_SHA256);
    var tbs = new V3TBSCertificateGenerator();
    tbs.setSerialNumber(
        new ASN1Integer(
            new java.math.BigInteger(120, new SecureRandom()).add(java.math.BigInteger.ONE)));
    tbs.setSignature(algorithm);
    tbs.setIssuer(
        ca == null
            ? new X500Name("CN=test-ca")
            : X500Name.getInstance(ca.cert.getSubjectX500Principal().getEncoded()));
    tbs.setSubject(new X500Name("CN=" + dns));
    tbs.setStartDate(new Time(Date.from(now.minusSeconds(60))));
    tbs.setEndDate(new Time(Date.from(now.plusSeconds(3600))));
    tbs.setSubjectPublicKeyInfo(SubjectPublicKeyInfo.getInstance(key.getPublic().getEncoded()));
    var ex = new ExtensionsGenerator();
    ex.addExtension(
        org.bouncycastle.asn1.x509.Extension.basicConstraints, true, new BasicConstraints(root));
    ex.addExtension(
        org.bouncycastle.asn1.x509.Extension.keyUsage,
        true,
        new KeyUsage(root ? KeyUsage.keyCertSign | KeyUsage.cRLSign : KeyUsage.digitalSignature));
    if (!root) {
      ex.addExtension(
          org.bouncycastle.asn1.x509.Extension.extendedKeyUsage,
          false,
          new ExtendedKeyUsage(
              new KeyPurposeId[] {KeyPurposeId.id_kp_serverAuth, KeyPurposeId.id_kp_clientAuth}));
      ex.addExtension(
          org.bouncycastle.asn1.x509.Extension.subjectAlternativeName,
          false,
          new GeneralNames(
              dns.equals("localhost")
                  ? new GeneralName[] {
                    new GeneralName(GeneralName.dNSName, dns),
                    new GeneralName(GeneralName.iPAddress, "127.0.0.1")
                  }
                  : new GeneralName[] {new GeneralName(GeneralName.dNSName, dns)}));
    }
    tbs.setExtensions(ex.generate());
    var body = tbs.generateTBSCertificate();
    var sign = Signature.getInstance("SHA256withECDSA");
    sign.initSign(ca == null ? key.getPrivate() : ca.key.getPrivate());
    sign.update(body.getEncoded("DER"));
    byte[] der =
        new DERSequence(new ASN1Encodable[] {body, algorithm, new DERBitString(sign.sign())})
            .getEncoded("DER");
    var cert =
        (X509Certificate)
            CertificateFactory.getInstance("X.509")
                .generateCertificate(new java.io.ByteArrayInputStream(der));
    return new Material(key, cert);
  }

  Path pem(String name, String type, byte[] bytes) throws Exception {
    Path p = dir.resolve(name);
    Files.writeString(
        p,
        "-----BEGIN "
            + type
            + "-----\n"
            + Base64.getMimeEncoder(64, new byte[] {10}).encodeToString(bytes)
            + "\n-----END "
            + type
            + "-----\n");
    return p;
  }

  static SSLContext client(Material material, Material ca) throws Exception {
    var trust = KeyStore.getInstance("PKCS12");
    trust.load(null, null);
    trust.setCertificateEntry("ca", ca.cert);
    var tm = TrustManagerFactory.getInstance(TrustManagerFactory.getDefaultAlgorithm());
    tm.init(trust);
    KeyManager[] managers = null;
    if (material != null) {
      var store = KeyStore.getInstance("PKCS12");
      store.load(null, null);
      store.setKeyEntry(
          "client",
          material.key.getPrivate(),
          new char[0],
          new java.security.cert.Certificate[] {material.cert});
      var km = KeyManagerFactory.getInstance(KeyManagerFactory.getDefaultAlgorithm());
      km.init(store, new char[0]);
      managers = km.getKeyManagers();
    }
    SSLContext ctx = SSLContext.getInstance("TLS");
    ctx.init(managers, tm.getTrustManagers(), new SecureRandom());
    return ctx;
  }

  static String request(int port, SSLContext client, String origin) throws Exception {
    try (var plain = new Socket("127.0.0.1", port);
        var socket =
            (SSLSocket) client.getSocketFactory().createSocket(plain, "localhost", port, true)) {
      socket.setSoTimeout(5000);
      var parameters = socket.getSSLParameters();
      parameters.setEndpointIdentificationAlgorithm("HTTPS");
      socket.setSSLParameters(parameters);
      socket.startHandshake();
      String req =
          "GET /probe HTTP/1.1\r\nHost: portal.example.com\r\nX-Auriga-Entry-Role: portal\r\nX-Auriga-Entry-Origin: "
              + origin
              + "\r\nConnection: close\r\n\r\n";
      socket.getOutputStream().write(req.getBytes(StandardCharsets.US_ASCII));
      return new String(socket.getInputStream().readAllBytes(), StandardCharsets.UTF_8);
    }
  }

  @Test
  void actualServletContainerRequiresClientCaThenIamRequiresEdgeSanAndPeer() throws Exception {
    Material ca = certificate("test-ca", null, true),
        serverCert = certificate("localhost", ca, false),
        edge = certificate("edge.internal", ca, false),
        other = certificate("other.internal", ca, false);
    Ssl ssl = new Ssl();
    ssl.setEnabled(true);
    ssl.setClientAuth(Ssl.ClientAuth.NEED);
    ssl.setCertificate(pem("server.pem", "CERTIFICATE", serverCert.cert.getEncoded()).toString());
    ssl.setCertificatePrivateKey(
        pem("server-key.pem", "PRIVATE KEY", serverCert.key.getPrivate().getEncoded()).toString());
    ssl.setTrustCertificate(pem("ca.pem", "CERTIFICATE", ca.cert.getEncoded()).toString());
    var factory = new TomcatServletWebServerFactory(0);
    factory.setAddress(InetAddress.getByName("127.0.0.1"));
    factory.setSsl(ssl);
    new IamHopServerGuard(false).postProcessAfterInitialization(factory, "server");
    var config = HttpBoundaryTest.fixture("domain-proxy.json");
    var identity =
        new java.util.concurrent.atomic.AtomicReference<>(
            new RequestIdentity(
                config, new RequestIdentity.Hop(Set.of("127.0.0.1"), "edge.internal", false)));
    var server =
        factory.getWebServer(
            context ->
                context
                    .addServlet(
                        "probe",
                        new HttpServlet() {
                          @Override
                          protected void doGet(HttpServletRequest req, HttpServletResponse response)
                              throws java.io.IOException {
                            try {
                              response.getWriter().write(identity.get().host(req).audience());
                            } catch (AurigaException e) {
                              response.setStatus(e.errorCode().httpStatus());
                              response.getWriter().write(e.errorCode().code());
                            }
                          }
                        })
                    .addMapping("/probe"));
    server.start();
    try {
      assertTrue(
          request(server.getPort(), client(edge, ca), config.portalUrl())
              .startsWith("HTTP/1.1 200"));
      assertTrue(
          request(server.getPort(), client(other, ca), config.portalUrl())
              .startsWith("HTTP/1.1 403"));
      assertThrows(
          Exception.class, () -> request(server.getPort(), client(null, ca), config.portalUrl()));
      Material rogueCa = certificate("rogue", null, true),
          rogue = certificate("edge.internal", rogueCa, false);
      // Trust the real server but supply a client identity signed by an unrelated CA.
      SSLContext rogueClient = client(rogue, ca);
      assertThrows(
          Exception.class, () -> request(server.getPort(), rogueClient, config.portalUrl()));
      assertTrue(
          request(server.getPort(), client(edge, ca), config.adminUrl())
              .startsWith("HTTP/1.1 403"));
      identity.set(
          new RequestIdentity(
              config, new RequestIdentity.Hop(Set.of("192.0.2.11"), "edge.internal", false)));
      assertTrue(
          request(server.getPort(), client(edge, ca), config.portalUrl())
              .startsWith("HTTP/1.1 403"));
    } finally {
      server.stop();
      server.destroy();
    }
  }

  @Test
  void connectorConfigurationCannotUsePropertiesToPretendClientAuthentication() throws Exception {
    var factory = new TomcatServletWebServerFactory(0);
    assertThrows(
        IllegalStateException.class,
        () -> new IamHopServerGuard(false).postProcessAfterInitialization(factory, "server"));
    factory.setAddress(InetAddress.getByName("0.0.0.0"));
    assertThrows(
        IllegalStateException.class,
        () -> new IamHopServerGuard(true).postProcessAfterInitialization(factory, "server"));
  }
}
