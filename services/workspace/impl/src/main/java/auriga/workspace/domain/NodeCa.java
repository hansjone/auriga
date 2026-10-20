package auriga.workspace.domain;

import static auriga.workspace.domain.WorkspaceErrors.valid;

import java.io.ByteArrayInputStream;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.security.KeyFactory;
import java.security.PrivateKey;
import java.security.PublicKey;
import java.security.Signature;
import java.security.cert.CertificateFactory;
import java.security.cert.X509Certificate;
import java.security.interfaces.ECPublicKey;
import java.security.spec.PKCS8EncodedKeySpec;
import java.security.spec.X509EncodedKeySpec;
import java.time.Clock;
import java.time.Instant;
import java.util.Base64;
import java.util.Date;
import java.util.HexFormat;
import java.util.UUID;
import org.bouncycastle.asn1.*;
import org.bouncycastle.asn1.pkcs.CertificationRequest;
import org.bouncycastle.asn1.x500.X500Name;
import org.bouncycastle.asn1.x509.*;
import org.bouncycastle.asn1.x9.X9ObjectIdentifiers;

/** Issues only P-256 client certificates with a server-selected, single node URI SAN. */
public final class NodeCa {
  private final X509Certificate ca;
  private final PrivateKey key;
  private final Clock clock;

  public NodeCa(X509Certificate ca, PrivateKey key, Clock clock) {
    this.ca = ca;
    this.key = key;
    this.clock = clock;
    try {
      ca.checkValidity(Date.from(clock.instant()));
      if (ca.getBasicConstraints() < 0)
        throw new IllegalArgumentException("Certificate is not a CA");
      byte[] challenge = WorkspaceCrypto.random(32);
      var signer = Signature.getInstance("SHA256withECDSA");
      signer.initSign(key);
      signer.update(challenge);
      var verifier = Signature.getInstance("SHA256withECDSA");
      verifier.initVerify(ca.getPublicKey());
      verifier.update(challenge);
      if (!verifier.verify(signer.sign()))
        throw new IllegalArgumentException("CA key does not match certificate");
    } catch (Exception e) {
      throw new IllegalArgumentException("Invalid node CA material", e);
    }
  }

  public static NodeCa load(Path certificate, Path privateKey, Clock clock) {
    try {
      SecretFilePermissions.check(privateKey);
      var ca = parse(Files.readAllBytes(certificate));
      String pem = Files.readString(privateKey, StandardCharsets.US_ASCII);
      byte[] der =
          Base64.getMimeDecoder()
              .decode(
                  pem.replace("-----BEGIN PRIVATE KEY-----", "")
                      .replace("-----END PRIVATE KEY-----", ""));
      return new NodeCa(
          ca, KeyFactory.getInstance("EC").generatePrivate(new PKCS8EncodedKeySpec(der)), clock);
    } catch (Exception e) {
      throw new IllegalStateException("Cannot load workspace CA files", e);
    }
  }

  public static X509Certificate parse(byte[] encoded) {
    try {
      return (X509Certificate)
          CertificateFactory.getInstance("X.509")
              .generateCertificate(new ByteArrayInputStream(encoded));
    } catch (Exception e) {
      throw new IllegalArgumentException("Invalid certificate", e);
    }
  }

  public byte[] bundle() {
    try {
      return pem(ca.getEncoded());
    } catch (Exception e) {
      throw new IllegalStateException(e);
    }
  }

  public X509Certificate certificate() {
    return ca;
  }

  public String fingerprint() {
    return HexFormat.of().formatHex(WorkspaceCrypto.hash(ca.getPublicKey().getEncoded()));
  }

  public X509Certificate issue(byte[] csr, UUID node) {
    try {
      valid(csr.length > 0 && csr.length <= 16384);
      CertificationRequest request =
          CertificationRequest.getInstance(ASN1Primitive.fromByteArray(csr));
      valid(
          request
              .getSignatureAlgorithm()
              .getAlgorithm()
              .equals(X9ObjectIdentifiers.ecdsa_with_SHA256));
      var info = request.getCertificationRequestInfo();
      PublicKey publicKey =
          KeyFactory.getInstance("EC")
              .generatePublic(
                  new X509EncodedKeySpec(info.getSubjectPublicKeyInfo().getEncoded("DER")));
      valid(
          publicKey instanceof ECPublicKey
              && ((ECPublicKey) publicKey).getParams().getCurve().getField().getFieldSize() == 256);
      var signature = Signature.getInstance("SHA256withECDSA");
      signature.initVerify(publicKey);
      signature.update(info.getEncoded("DER"));
      valid(signature.verify(request.getSignature().getBytes()));
      Instant now = clock.instant();
      Instant expires = now.plusSeconds(30L * 86400);
      if (expires.isAfter(ca.getNotAfter().toInstant())) expires = ca.getNotAfter().toInstant();
      valid(expires.isAfter(now.plusSeconds(86400)));
      var algorithm = new AlgorithmIdentifier(X9ObjectIdentifiers.ecdsa_with_SHA256);
      var tbs = new V3TBSCertificateGenerator();
      tbs.setSerialNumber(
          new ASN1Integer(new java.math.BigInteger(1, WorkspaceCrypto.random(19)).setBit(0)));
      tbs.setSignature(algorithm);
      tbs.setIssuer(X500Name.getInstance(ca.getSubjectX500Principal().getEncoded()));
      tbs.setSubject(new X500Name("CN=auriga-node"));
      tbs.setStartDate(new Time(Date.from(now.minusSeconds(30))));
      tbs.setEndDate(new Time(Date.from(expires)));
      tbs.setSubjectPublicKeyInfo(info.getSubjectPublicKeyInfo());
      var extensions = new ExtensionsGenerator();
      extensions.addExtension(Extension.basicConstraints, true, new BasicConstraints(false));
      extensions.addExtension(Extension.keyUsage, true, new KeyUsage(KeyUsage.digitalSignature));
      extensions.addExtension(
          Extension.extendedKeyUsage, false, new ExtendedKeyUsage(KeyPurposeId.id_kp_clientAuth));
      extensions.addExtension(
          Extension.subjectAlternativeName,
          false,
          new GeneralNames(
              new GeneralName(GeneralName.uniformResourceIdentifier, "auriga://node/" + node)));
      tbs.setExtensions(extensions.generate());
      var body = tbs.generateTBSCertificate();
      signature.initSign(key);
      signature.update(body.getEncoded("DER"));
      return parse(
          new DERSequence(new ASN1Encodable[] {body, algorithm, new DERBitString(signature.sign())})
              .getEncoded("DER"));
    } catch (auriga.common.error.AurigaException e) {
      throw e;
    } catch (Exception e) {
      throw new auriga.common.error.AurigaException(
          auriga.common.error.CommonErrors.VALIDATION_FAILED);
    }
  }

  public void verify(X509Certificate leaf) {
    try {
      leaf.checkValidity(Date.from(clock.instant()));
      ca.checkValidity(Date.from(clock.instant()));
      leaf.verify(ca.getPublicKey());
      valid(
          leaf.getBasicConstraints() < 0
              && leaf.getExtendedKeyUsage() != null
              && leaf.getExtendedKeyUsage().contains("1.3.6.1.5.5.7.3.2"));
    } catch (Exception e) {
      throw new auriga.common.error.AurigaException(
          auriga.common.error.CommonErrors.UNAUTHENTICATED);
    }
  }

  public static byte[] pem(byte[] der) {
    return ("-----BEGIN CERTIFICATE-----\n"
            + Base64.getMimeEncoder(64, new byte[] {10}).encodeToString(der)
            + "\n-----END CERTIFICATE-----\n")
        .getBytes(StandardCharsets.US_ASCII);
  }

  public byte[] chain(X509Certificate leaf) {
    try {
      byte[] first = pem(leaf.getEncoded()), root = bundle();
      byte[] result = java.util.Arrays.copyOf(first, first.length + root.length);
      System.arraycopy(root, 0, result, first.length, root.length);
      return result;
    } catch (Exception e) {
      throw new IllegalStateException(e);
    }
  }
}
