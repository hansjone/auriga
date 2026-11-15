package auriga.collab.network.keys;

import java.security.KeyPair;
import java.security.KeyPairGenerator;
import java.security.PrivateKey;
import java.security.SecureRandom;
import java.security.Signature;
import java.security.cert.CertificateFactory;
import java.security.cert.X509Certificate;
import java.time.Instant;
import java.util.Date;
import java.util.List;
import org.bouncycastle.asn1.ASN1Encodable;
import org.bouncycastle.asn1.ASN1Integer;
import org.bouncycastle.asn1.ASN1ObjectIdentifier;
import org.bouncycastle.asn1.DERBitString;
import org.bouncycastle.asn1.DERSequence;
import org.bouncycastle.asn1.x500.X500Name;
import org.bouncycastle.asn1.x500.X500NameBuilder;
import org.bouncycastle.asn1.x500.style.BCStyle;
import org.bouncycastle.asn1.x509.*;

/** Fixed Ed25519 internal CA/leaf profile. Caller chooses only already-authorized SANs and EKUs. */
final class MsgnetCertificates {
  record Issued(KeyPair pair, X509Certificate certificate) {}

  private MsgnetCertificates() {}

  static Issued issue(
      String name,
      Instant before,
      Instant after,
      List<String> sans,
      List<String> ekus,
      X509Certificate ca,
      PrivateKey caSecret) {
    try {
      var pair = KeyPairGenerator.getInstance("Ed25519").generateKeyPair();
      var algorithm = new AlgorithmIdentifier(new ASN1ObjectIdentifier("1.3.101.112"));
      var tbs = new V3TBSCertificateGenerator();
      tbs.setSerialNumber(
          new ASN1Integer(
              new java.math.BigInteger(159, new SecureRandom()).add(java.math.BigInteger.ONE)));
      tbs.setSignature(algorithm);
      var subject = new X500NameBuilder(BCStyle.INSTANCE).addRDN(BCStyle.CN, name).build();
      tbs.setSubject(subject);
      tbs.setIssuer(
          ca == null ? subject : X500Name.getInstance(ca.getSubjectX500Principal().getEncoded()));
      tbs.setStartDate(new Time(Date.from(before)));
      tbs.setEndDate(new Time(Date.from(after)));
      tbs.setSubjectPublicKeyInfo(SubjectPublicKeyInfo.getInstance(pair.getPublic().getEncoded()));
      var extensions = new ExtensionsGenerator();
      extensions.addExtension(
          Extension.basicConstraints,
          true,
          ca == null ? new BasicConstraints(0) : new BasicConstraints(false));
      extensions.addExtension(
          Extension.keyUsage,
          true,
          new KeyUsage(
              ca == null ? KeyUsage.keyCertSign | KeyUsage.cRLSign : KeyUsage.digitalSignature));
      if (ca != null) {
        if (sans.isEmpty() || ekus.isEmpty() || after.isAfter(ca.getNotAfter().toInstant()))
          throw new IllegalArgumentException("Invalid internal certificate scope");
        var names =
            sans.stream()
                .map(
                    s ->
                        new GeneralName(
                            s.contains(":") || s.matches("[0-9.]+")
                                ? GeneralName.iPAddress
                                : GeneralName.dNSName,
                            s))
                .toArray(GeneralName[]::new);
        extensions.addExtension(Extension.subjectAlternativeName, false, new GeneralNames(names));
        var usages =
            ekus.stream()
                .map(
                    s ->
                        switch (s) {
                          case "serverAuth" -> KeyPurposeId.id_kp_serverAuth;
                          case "clientAuth" -> KeyPurposeId.id_kp_clientAuth;
                          default -> throw new IllegalArgumentException("Invalid internal EKU");
                        })
                .toArray(KeyPurposeId[]::new);
        extensions.addExtension(Extension.extendedKeyUsage, false, new ExtendedKeyUsage(usages));
      }
      tbs.setExtensions(extensions.generate());
      var body = tbs.generateTBSCertificate();
      var signer = Signature.getInstance("Ed25519");
      signer.initSign(ca == null ? pair.getPrivate() : caSecret);
      signer.update(body.getEncoded("DER"));
      byte[] der =
          new DERSequence(new ASN1Encodable[] {body, algorithm, new DERBitString(signer.sign())})
              .getEncoded("DER");
      var cert =
          (X509Certificate)
              CertificateFactory.getInstance("X.509")
                  .generateCertificate(new java.io.ByteArrayInputStream(der));
      cert.verify(ca == null ? pair.getPublic() : ca.getPublicKey());
      return new Issued(pair, cert);
    } catch (java.security.GeneralSecurityException | java.io.IOException e) {
      throw new IllegalStateException("Internal certificate issuance failed", e);
    }
  }
}
