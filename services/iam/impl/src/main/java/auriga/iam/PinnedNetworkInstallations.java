package auriga.iam;

import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.contracts.msgnet.model.InstallationPin;
import auriga.contracts.msgnet.model.InstallationPins;
import auriga.contracts.msgnet.model.ProjectionRequest;
import auriga.iam.api.NetworkInstallationAuthority;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.nio.file.attribute.*;
import java.security.*;
import java.security.cert.X509Certificate;
import java.security.spec.X509EncodedKeySpec;
import java.util.*;
import javax.security.auth.x500.X500Principal;

/** Immutable pin snapshot loaded only from D's protected installation configuration. */
public final class PinnedNetworkInstallations implements NetworkInstallationAuthority {
  private final String tenant;
  private final String realm;
  private final Map<String, InstallationPin> pins;
  private final Map<String, PublicKey> signing;
  private final Access access;
  private final Nonces nonces;
  private final ContractSchemas schemas = new ContractSchemas();

  public static PinnedNetworkInstallations load(Path file, Access access, Nonces nonces)
      throws java.io.IOException {
    return new PinnedNetworkInstallations(InstallationPinFile.read(file), access, nonces);
  }

  PinnedNetworkInstallations(byte[] raw, Access access, Nonces nonces) {
    var json = InstallationJson.parse(raw);
    schemas.validate("network/auto-renewal-v2.schema.json#/$defs/InstallationPins", json);
    InstallationPins model = Json.MAPPER.treeToValue(json, InstallationPins.class);
    tenant = model.getTenantId();
    realm = model.getRealm();
    this.access = Objects.requireNonNull(access);
    this.nonces = Objects.requireNonNull(nonces);
    var identities = new HashMap<String, InstallationPin>();
    var keys = new HashMap<String, PublicKey>();
    var targets = new HashSet<String>();
    for (var pin : model.getPins()) {
      if (identities.putIfAbsent(pin.getIdentityId(), pin) != null
          || !targets.add(pin.getTargetId()))
        throw new IllegalArgumentException("duplicate installation pin");
      keys.put(pin.getIdentityId(), key(pin.getSigningSpki(), "Ed25519"));
      key(pin.getSealingSpki(), "X25519");
      if (!new X500Principal(pin.getSubject())
          .getName(X500Principal.RFC2253)
          .equals(pin.getSubject()))
        throw new IllegalArgumentException("noncanonical certificate subject");
    }
    pins = Map.copyOf(identities);
    signing = Map.copyOf(keys);
  }

  private static PublicKey key(String encoded, String algorithm) {
    try {
      byte[] raw = Base64.getUrlDecoder().decode(encoded);
      if (!Base64.getUrlEncoder().withoutPadding().encodeToString(raw).equals(encoded))
        throw new IllegalArgumentException();
      PublicKey key = KeyFactory.getInstance(algorithm).generatePublic(new X509EncodedKeySpec(raw));
      if (!Arrays.equals(raw, key.getEncoded())) throw new IllegalArgumentException();
      return key;
    } catch (GeneralSecurityException | IllegalArgumentException e) {
      throw new IllegalArgumentException("invalid installation public key");
    }
  }

  @Override
  public VerifiedProjection authenticateMaterial(byte[] request, X509Certificate peer) {
    if (peer == null) throw denied();
    try {
      peer.checkValidity();
    } catch (GeneralSecurityException invalid) {
      throw denied();
    }
    return authenticate(
        parseRequest(request), peer.getSubjectX500Principal().getName(X500Principal.RFC2253));
  }

  @Override
  public VerifiedProjection authenticateSealedMaterial(byte[] request) {
    return authenticate(parseRequest(request), null);
  }

  private VerifiedProjection authenticate(ProjectionRequest request, String subject) {
    if (request == null) throw denied();
    var json = Json.MAPPER.valueToTree(request);
    schemas.validate("network/auto-renewal-v2.schema.json#/$defs/ProjectionRequest", json);
    var pin = pins.get(request.getInstallationId());
    var auth = request.getIdentityAuth();
    if (pin == null
        || !tenant.equals(request.getTenantId())
        || !realm.equals(request.getRealm())
        || !pin.getGatewayId().equals(request.getGatewayId())
        || !pin.getComponent().getValue().equals(request.getComponent().getValue())
        || !pin.getIdentityId().equals(auth.getKid())
        || subject != null && !pin.getSubject().equals(subject)) throw denied();
    // Signature verification precedes any nonce mutation. A request cannot grant itself scope.
    try {
      byte[] signature = Base64.getUrlDecoder().decode(auth.getSignature());
      if (!Base64.getUrlEncoder()
          .withoutPadding()
          .encodeToString(signature)
          .equals(auth.getSignature())) throw denied();
      var body = json.deepCopy();
      ((tools.jackson.databind.node.ObjectNode) body).remove("identity_auth");
      var verifier = java.security.Signature.getInstance("Ed25519");
      verifier.initVerify(signing.get(pin.getIdentityId()));
      verifier.update("Auriga.NetworkProjectionRequest.v2\0".getBytes(StandardCharsets.UTF_8));
      verifier.update(InstallationJson.canonical(body).getBytes(StandardCharsets.UTF_8));
      if (!verifier.verify(signature)) throw denied();
    } catch (GeneralSecurityException | IllegalArgumentException invalid) {
      throw denied();
    }
    if (!access.permitted(tenant, realm, pin.getIdentityId(), pin.getTargetId())
        || !nonces.claim(Json.MAPPER.treeToValue(json, ProjectionRequest.class))) throw denied();
    // Generated models are mutable: never expose the pinned snapshot itself.
    return new VerifiedProjection(
        request, Json.MAPPER.treeToValue(Json.MAPPER.valueToTree(pin), InstallationPin.class));
  }

  private ProjectionRequest parseRequest(byte[] raw) {
    if (raw == null || raw.length > 65536) throw denied();
    var json = InstallationJson.parse(raw);
    schemas.validate("network/auto-renewal-v2.schema.json#/$defs/ProjectionRequest", json);
    return Json.MAPPER.treeToValue(json, ProjectionRequest.class);
  }

  private static AurigaException denied() {
    return new AurigaException(CommonErrors.FORBIDDEN);
  }
}
