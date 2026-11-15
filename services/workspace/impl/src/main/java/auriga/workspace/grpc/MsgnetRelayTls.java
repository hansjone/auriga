package auriga.workspace.grpc;

import auriga.workspace.domain.SecretFilePermissions;
import java.io.*;
import java.net.Socket;
import java.nio.file.*;
import java.security.*;
import java.security.cert.*;
import java.security.spec.PKCS8EncodedKeySpec;
import java.util.*;
import javax.net.ssl.*;

/**
 * Reloads the separate proof listener's leaf on every handshake, retaining native node CA trust.
 */
public final class MsgnetRelayTls extends X509ExtendedKeyManager {
  private final Path certificate, key;
  private final Map<String, Identity> identities = new LinkedHashMap<>();

  private record Identity(PrivateKey key, X509Certificate[] chain) {}

  private MsgnetRelayTls(Path certificate, Path key) {
    this.certificate = certificate;
    this.key = key;
  }

  public static SSLContext context(Path certificate, Path key, byte[] nodeCA) throws Exception {
    var manager = new MsgnetRelayTls(certificate, key);
    manager.load();
    var roots = KeyStore.getInstance(KeyStore.getDefaultType());
    roots.load(null);
    var factory = CertificateFactory.getInstance("X.509");
    int index = 0;
    for (var cert : factory.generateCertificates(new ByteArrayInputStream(nodeCA)))
      roots.setCertificateEntry("node-" + index++, cert);
    if (index == 0) throw new IllegalArgumentException("native node CA required");
    var trust = TrustManagerFactory.getInstance(TrustManagerFactory.getDefaultAlgorithm());
    trust.init(roots);
    var context = SSLContext.getInstance("TLS");
    context.init(new KeyManager[] {manager}, trust.getTrustManagers(), new SecureRandom());
    return context;
  }

  private synchronized String load() throws Exception {
    SecretFilePermissions.check(key);
    if (Files.size(key) > 16384 || Files.size(certificate) > 65536)
      throw new IOException("oversize relay TLS identity");
    var chain =
        CertificateFactory.getInstance("X.509")
            .generateCertificates(new ByteArrayInputStream(Files.readAllBytes(certificate)))
            .stream()
            .map(c -> (X509Certificate) c)
            .toArray(X509Certificate[]::new);
    if (chain.length == 0) throw new IOException("relay certificate required");
    chain[0].checkValidity();
    byte[] pem = Files.readAllBytes(key), der = null;
    try {
      String text = new String(pem, java.nio.charset.StandardCharsets.US_ASCII).strip();
      // Check PEM labels; private bytes are read from the protected file.
      if (!text.startsWith("-----BEGIN PRIVATE KEY-----") // gitleaks:allow
          || !text.endsWith("-----END PRIVATE KEY-----"))
        throw new IOException("PKCS8 relay key required");
      der =
          Base64.getMimeDecoder()
              .decode(
                  text.replace("-----BEGIN PRIVATE KEY-----", "")
                      .replace("-----END PRIVATE KEY-----", ""));
      String algorithm = chain[0].getPublicKey().getAlgorithm();
      var privateKey =
          KeyFactory.getInstance(algorithm).generatePrivate(new PKCS8EncodedKeySpec(der));
      String signature =
          switch (algorithm) {
            case "EC" -> "SHA256withECDSA";
            case "RSA" -> "SHA256withRSA";
            case "EdDSA", "Ed25519" -> "Ed25519";
            default -> throw new IOException("unsupported relay key");
          };
      byte[] challenge = new byte[32];
      new SecureRandom().nextBytes(challenge);
      var signer = Signature.getInstance(signature);
      signer.initSign(privateKey);
      signer.update(challenge);
      var verifier = Signature.getInstance(signature);
      verifier.initVerify(chain[0]);
      verifier.update(challenge);
      if (!verifier.verify(signer.sign())) throw new IOException("relay TLS key mismatch");
      String alias =
          HexFormat.of()
              .formatHex(MessageDigest.getInstance("SHA-256").digest(chain[0].getEncoded()));
      identities.put(alias, new Identity(privateKey, chain));
      while (identities.size() > 2) identities.remove(identities.keySet().iterator().next());
      return alias;
    } finally {
      Arrays.fill(pem, (byte) 0);
      if (der != null) Arrays.fill(der, (byte) 0);
    }
  }

  @Override
  public synchronized String chooseServerAlias(String type, Principal[] issuers, Socket socket) {
    try {
      String alias = load();
      return type.startsWith(identities.get(alias).key().getAlgorithm()) ? alias : null;
    } catch (Exception unavailable) {
      return null;
    }
  }

  @Override
  public String chooseEngineServerAlias(String type, Principal[] issuers, SSLEngine engine) {
    return chooseServerAlias(type, issuers, null);
  }

  @Override
  public synchronized X509Certificate[] getCertificateChain(String alias) {
    var value = identities.get(alias);
    return value == null ? null : value.chain().clone();
  }

  @Override
  public synchronized PrivateKey getPrivateKey(String alias) {
    var value = identities.get(alias);
    return value == null ? null : value.key();
  }

  @Override
  public String[] getServerAliases(String type, Principal[] issuers) {
    String alias = chooseServerAlias(type, issuers, null);
    return alias == null ? null : new String[] {alias};
  }

  @Override
  public String[] getClientAliases(String type, Principal[] issuers) {
    return null;
  }

  @Override
  public String chooseClientAlias(String[] type, Principal[] issuers, Socket socket) {
    return null;
  }
}
