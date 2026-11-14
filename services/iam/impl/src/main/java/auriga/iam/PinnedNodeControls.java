package auriga.iam;

import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.contracts.msgnet.model.*;
import auriga.iam.api.NodeControlAuthority;
import java.nio.charset.StandardCharsets;
import java.nio.file.Path;
import java.security.*;
import java.security.PublicKey;
import java.security.spec.X509EncodedKeySpec;
import java.util.*;

/**
 * Only D's protected NodeControlPins snapshot establishes trust. No upload/enroll endpoint exists.
 */
public final class PinnedNodeControls implements NodeControlAuthority {
  private final String tenant, realm;
  private final Map<String, NodeControlPin> nodes;
  private final Map<String, PublicKey> keys;
  private final ContractSchemas schemas = new ContractSchemas();

  public static PinnedNodeControls load(Path file) throws java.io.IOException {
    return new PinnedNodeControls(InstallationPinFile.read(file));
  }

  PinnedNodeControls(byte[] raw) {
    var json = InstallationJson.parse(raw);
    schemas.validate("network/auto-renewal-v2.schema.json#/$defs/NodeControlPins", json);
    var pins = Json.MAPPER.treeToValue(json, NodeControlPins.class);
    tenant = pins.getTenantId();
    realm = pins.getRealm();
    var byNode = new HashMap<String, NodeControlPin>();
    var byIdentity = new HashMap<String, PublicKey>();
    var targets = new HashSet<String>();
    var materials = new HashSet<String>();
    for (var pin : pins.getPins()) {
      if (byNode.putIfAbsent(pin.getNodeId(), pin) != null
          || !targets.add(pin.getTargetId())
          || byIdentity.containsKey(pin.getIdentityId())
          || !materials.add(pin.getSigningSpki()))
        throw new IllegalArgumentException("duplicate node control pin");
      var revision = new java.math.BigInteger(pin.getIdentityRevision());
      if (revision.signum() <= 0 || revision.bitLength() > 64)
        throw new IllegalArgumentException("invalid node identity revision");
      try {
        var der = Base64.getUrlDecoder().decode(pin.getSigningSpki());
        var key = KeyFactory.getInstance("Ed25519").generatePublic(new X509EncodedKeySpec(der));
        if (!Arrays.equals(der, key.getEncoded())
            || !Base64.getUrlEncoder()
                .withoutPadding()
                .encodeToString(der)
                .equals(pin.getSigningSpki()))
          throw new IllegalArgumentException("noncanonical node key");
        byIdentity.put(pin.getIdentityId(), key);
      } catch (GeneralSecurityException invalid) {
        throw new IllegalArgumentException("invalid node public key", invalid);
      }
    }
    nodes = Map.copyOf(byNode);
    keys = Map.copyOf(byIdentity);
  }

  @Override
  public Binding binding(UUID authenticatedTenant, UUID authenticatedNode) {
    if (!tenant.equals(authenticatedTenant.toString())) throw denied();
    var pin = nodes.get(authenticatedNode.toString());
    if (pin == null) throw denied();
    // Return a copy: consumers cannot rewrite the trusted snapshot through a generated setter.
    return new Binding(
        tenant, realm, Json.MAPPER.treeToValue(Json.MAPPER.valueToTree(pin), NodeControlPin.class));
  }

  @Override
  public InstallationReceipt verify(UUID tenant, UUID node, byte[] raw, ControlPackage pending) {
    var binding = binding(tenant, node);
    var pin = binding.pin();
    var json = InstallationJson.parse(raw);
    schemas.validate("network/auto-renewal-v2.schema.json#/$defs/InstallationReceipt", json);
    var receipt = Json.MAPPER.treeToValue(json, InstallationReceipt.class);
    if (pending == null
        || !binding.tenant().equals(pending.getTenantId())
        || !binding.realm().equals(pending.getRealm())
        || !pin.getTargetId().equals(pending.getTargetId())
        || !pin.getIdentityId().equals(receipt.getIdentityId())
        || !pending.getPackageId().equals(receipt.getPackageId())
        || !pending.getTenantId().equals(receipt.getTenantId())
        || !pending.getRealm().equals(receipt.getRealm())
        || !pending.getTargetId().equals(receipt.getTargetId())
        || !pending.getChallenge().equals(receipt.getChallenge())
        || !pending.getControlDigest().equals(receipt.getControlDigest())
        || !pending
            .getControl()
            .getPayload()
            .getControlRevision()
            .equals(receipt.getControlRevision())
        || pending
            .getControl()
            .getPayload()
            .getRevokedInstallationIds()
            .contains(pin.getIdentityId())
        || !"installed".equals(receipt.getResult().getValue())
        || !pin.getIdentityId().equals(receipt.getIdentityAuth().getKid())) throw denied();
    try {
      var body = (tools.jackson.databind.node.ObjectNode) json.deepCopy();
      body.remove("identity_auth");
      var signature = java.security.Signature.getInstance("Ed25519");
      signature.initVerify(keys.get(pin.getIdentityId()));
      signature.update("Auriga.NetworkControlReceipt.v2\0".getBytes(StandardCharsets.UTF_8));
      signature.update(InstallationJson.canonical(body).getBytes(StandardCharsets.UTF_8));
      byte[] encoded = Base64.getUrlDecoder().decode(receipt.getIdentityAuth().getSignature());
      if (!Base64.getUrlEncoder()
              .withoutPadding()
              .encodeToString(encoded)
              .equals(receipt.getIdentityAuth().getSignature())
          || !signature.verify(encoded)) throw denied();
      return receipt;
    } catch (GeneralSecurityException | IllegalArgumentException invalid) {
      throw denied();
    }
  }

  private static AurigaException denied() {
    return new AurigaException(CommonErrors.FORBIDDEN);
  }
}
