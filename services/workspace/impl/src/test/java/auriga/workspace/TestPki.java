package auriga.workspace;

import auriga.workspace.domain.NodeCa;
import java.security.*;
import java.security.cert.X509Certificate;
import java.time.Instant;
import java.util.Date;
import org.bouncycastle.asn1.*;
import org.bouncycastle.asn1.pkcs.*;
import org.bouncycastle.asn1.x500.X500Name;
import org.bouncycastle.asn1.x509.*;
import org.bouncycastle.asn1.x9.X9ObjectIdentifiers;

final class TestPki {
  static KeyPair key() throws Exception {
    var generator = KeyPairGenerator.getInstance("EC");
    generator.initialize(new java.security.spec.ECGenParameterSpec("secp256r1"));
    return generator.generateKeyPair();
  }

  static byte[] csr(KeyPair key) throws Exception {
    var info =
        new CertificationRequestInfo(
            new X500Name("CN=alice"),
            SubjectPublicKeyInfo.getInstance(key.getPublic().getEncoded()),
            null);
    return new CertificationRequest(
            info,
            new AlgorithmIdentifier(X9ObjectIdentifiers.ecdsa_with_SHA256),
            new DERBitString(sign(key.getPrivate(), info.getEncoded("DER"))))
        .getEncoded("DER");
  }

  static byte[] sign(PrivateKey key, byte[] bytes) throws Exception {
    var s = Signature.getInstance("SHA256withECDSA");
    s.initSign(key);
    s.update(bytes);
    return s.sign();
  }

  static X509Certificate ca(KeyPair key, Instant now) throws Exception {
    return certificate(key, key, null, now, true);
  }

  static X509Certificate certificate(
      KeyPair leaf, KeyPair issuer, X509Certificate ca, Instant now, boolean isCa)
      throws Exception {
    var algorithm = new AlgorithmIdentifier(X9ObjectIdentifiers.ecdsa_with_SHA256);
    var tbs = new V3TBSCertificateGenerator();
    tbs.setSerialNumber(
        new ASN1Integer(
            new java.math.BigInteger(120, new SecureRandom()).add(java.math.BigInteger.ONE)));
    tbs.setSignature(algorithm);
    tbs.setIssuer(
        ca == null
            ? new X500Name("CN=test-ca")
            : X500Name.getInstance(ca.getSubjectX500Principal().getEncoded()));
    tbs.setSubject(new X500Name(isCa ? "CN=test-ca" : "CN=localhost"));
    tbs.setStartDate(new Time(Date.from(now.minusSeconds(60))));
    tbs.setEndDate(new Time(Date.from(now.plusSeconds(365L * 86400))));
    tbs.setSubjectPublicKeyInfo(SubjectPublicKeyInfo.getInstance(leaf.getPublic().getEncoded()));
    var ex = new ExtensionsGenerator();
    ex.addExtension(Extension.basicConstraints, true, new BasicConstraints(isCa));
    ex.addExtension(
        Extension.keyUsage,
        true,
        new KeyUsage(isCa ? KeyUsage.keyCertSign | KeyUsage.cRLSign : KeyUsage.digitalSignature));
    if (!isCa) {
      ex.addExtension(
          Extension.extendedKeyUsage, false, new ExtendedKeyUsage(KeyPurposeId.id_kp_serverAuth));
      ex.addExtension(
          Extension.subjectAlternativeName,
          false,
          new GeneralNames(
              new GeneralName[] {
                new GeneralName(GeneralName.iPAddress, "127.0.0.1"),
                new GeneralName(GeneralName.dNSName, "localhost"),
                new GeneralName(GeneralName.uniformResourceIdentifier, "auriga://workspace")
              }));
    }
    tbs.setExtensions(ex.generate());
    var body = tbs.generateTBSCertificate();
    return NodeCa.parse(
        new DERSequence(
                new ASN1Encodable[] {
                  body,
                  algorithm,
                  new DERBitString(sign(issuer.getPrivate(), body.getEncoded("DER")))
                })
            .getEncoded("DER"));
  }
}
