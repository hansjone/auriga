package auriga.iam;

import java.io.IOException;
import java.nio.file.*;
import java.security.KeyStore;
import java.security.cert.CertificateFactory;
import java.time.Duration;
import java.util.*;
import java.util.concurrent.TimeUnit;
import javax.net.ssl.SSLContext;
import javax.net.ssl.TrustManagerFactory;

/** One-day, per-host CA. Only the explicitly constructed child client trusts it. */
final class NetworkSourceJavaTls {
  final Path store, ca, edgeCert, edgeKey;
  final String password = Secrets.random();

  NetworkSourceJavaTls(Path directory) throws Exception {
    store = directory.resolve("https.p12");
    ca = directory.resolve("ca.pem");
    edgeCert = directory.resolve("edge-hop-cert.pem");
    edgeKey = directory.resolve("edge-hop-key.pem");
    var root = directory.resolve("ca.p12");
    var csr = directory.resolve("server.csr");
    var cert = directory.resolve("server.pem");
    keytool(
        "-genkeypair",
        "-alias",
        "ca",
        "-keyalg",
        "EC",
        "-groupname",
        "secp256r1",
        "-dname",
        "CN=Auriga isolated test CA",
        "-ext",
        "bc:c",
        "-validity",
        "1",
        "-keystore",
        root.toString());
    keytool(
        "-exportcert",
        "-rfc",
        "-alias",
        "ca",
        "-keystore",
        root.toString(),
        "-file",
        ca.toString());
    keytool(
        "-genkeypair",
        "-alias",
        "server",
        "-keyalg",
        "EC",
        "-groupname",
        "secp256r1",
        "-dname",
        "CN=localhost",
        "-validity",
        "1",
        "-keystore",
        store.toString());
    keytool("-certreq", "-alias", "server", "-keystore", store.toString(), "-file", csr.toString());
    keytool(
        "-gencert",
        "-rfc",
        "-alias",
        "ca",
        "-keystore",
        root.toString(),
        "-infile",
        csr.toString(),
        "-outfile",
        cert.toString(),
        "-validity",
        "1",
        "-ext",
        "SAN=ip:127.0.0.1,dns:localhost",
        "-ext",
        "EKU=serverAuth");
    keytool(
        "-importcert",
        "-alias",
        "ca",
        "-keystore",
        store.toString(),
        "-file",
        ca.toString(),
        "-noprompt");
    keytool(
        "-importcert",
        "-alias",
        "server",
        "-keystore",
        store.toString(),
        "-file",
        cert.toString(),
        "-noprompt");
    var clientStore = directory.resolve("edge-hop.p12");
    keytool(
        "-genkeypair",
        "-alias",
        "edge",
        "-keyalg",
        "EC",
        "-groupname",
        "secp256r1",
        "-dname",
        "CN=Auriga isolated edge hop",
        "-validity",
        "1",
        "-keystore",
        clientStore.toString());
    keytool(
        "-certreq", "-alias", "edge", "-keystore", clientStore.toString(), "-file", csr.toString());
    keytool(
        "-gencert",
        "-rfc",
        "-alias",
        "ca",
        "-keystore",
        root.toString(),
        "-infile",
        csr.toString(),
        "-outfile",
        cert.toString(),
        "-validity",
        "1",
        "-ext",
        "EKU=clientAuth");
    keytool(
        "-importcert",
        "-alias",
        "ca",
        "-keystore",
        clientStore.toString(),
        "-file",
        ca.toString(),
        "-noprompt");
    keytool(
        "-importcert",
        "-alias",
        "edge",
        "-keystore",
        clientStore.toString(),
        "-file",
        cert.toString(),
        "-noprompt");
    var client = KeyStore.getInstance("PKCS12");
    try (var input = Files.newInputStream(clientStore)) {
      client.load(input, password.toCharArray());
    }
    pem(edgeCert, "CERTIFICATE", client.getCertificate("edge").getEncoded());
    pem(edgeKey, "PRIVATE KEY", client.getKey("edge", password.toCharArray()).getEncoded());
    Files.delete(clientStore);
    Files.delete(root);
    Files.delete(csr);
    Files.delete(cert);
  }

  private static void pem(Path path, String kind, byte[] encoded) throws Exception {
    NetworkSourceJavaPrivateFiles.replace(
        path,
        "-----BEGIN "
            + kind
            + "-----\n"
            + Base64.getMimeEncoder(64, new byte[] {'\n'}).encodeToString(encoded)
            + "\n-----END "
            + kind
            + "-----\n");
    Arrays.fill(encoded, (byte) 0);
  }

  private void keytool(String... args) throws Exception {
    var executable =
        Path.of(
            System.getProperty("java.home"),
            "bin",
            System.getProperty("os.name").startsWith("Windows") ? "keytool.exe" : "keytool");
    var command = new ArrayList<String>();
    command.add(executable.toString());
    command.addAll(List.of(args));
    command.addAll(List.of("-storetype", "PKCS12", "-storepass:env", "AURIGA_TEST_TLS_PASSWORD"));
    var builder =
        new ProcessBuilder(command)
            .redirectErrorStream(true)
            .redirectOutput(ProcessBuilder.Redirect.DISCARD);
    builder.environment().put("AURIGA_TEST_TLS_PASSWORD", password);
    var child = builder.start();
    try {
      if (!child.waitFor(Duration.ofSeconds(30).toMillis(), TimeUnit.MILLISECONDS)
          || child.exitValue() != 0) throw new IOException("Isolated TLS keytool failed");
    } finally {
      if (child.isAlive()) child.destroyForcibly();
      if (!child.waitFor(5, TimeUnit.SECONDS))
        throw new IOException("TLS keytool did not terminate");
    }
  }

  SSLContext clientContext() throws Exception {
    var trust = KeyStore.getInstance(KeyStore.getDefaultType());
    trust.load(null, null);
    try (var input = Files.newInputStream(ca)) {
      trust.setCertificateEntry(
          "test-ca", CertificateFactory.getInstance("X.509").generateCertificate(input));
    }
    var factory = TrustManagerFactory.getInstance(TrustManagerFactory.getDefaultAlgorithm());
    factory.init(trust);
    var context = SSLContext.getInstance("TLS");
    context.init(null, factory.getTrustManagers(), null);
    return context;
  }
}
