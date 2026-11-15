package auriga.iam.api;

import auriga.contracts.msgnet.model.InstallationPin;
import auriga.contracts.msgnet.model.ProjectionRequest;
import java.security.cert.X509Certificate;

/** Installation authentication, independent from expired service JWTs and message access. */
public interface NetworkInstallationAuthority {
  record VerifiedProjection(ProjectionRequest request, InstallationPin pin) {}

  /** peer must come from a successfully verified mTLS listener, never an HTTP header. */
  VerifiedProjection authenticateMaterial(byte[] request, X509Certificate peer);

  /** Only the fixed HTTPS sealed bootstrap endpoint may call this path. */
  VerifiedProjection authenticateSealedMaterial(byte[] request);

  /** C supplies a current, authoritative material grant, including root revocation/isolation. */
  @FunctionalInterface
  interface Access {
    boolean permitted(String tenant, String realm, String identity, String target);
  }

  /** C atomically persists nonce consumption before exporting or sealing material. */
  @FunctionalInterface
  interface Nonces {
    boolean claim(ProjectionRequest verifiedRequest);
  }
}
