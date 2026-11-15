package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;

import auriga.contracts.msgnet.model.*;
import java.nio.charset.StandardCharsets;
import java.security.*;
import java.time.Instant;
import java.util.*;
import org.junit.jupiter.api.Test;

class PinnedNodeControlsTest {
  final UUID tenant = UUID.randomUUID(), node = UUID.randomUUID();
  final KeyPair key = KeyPairGenerator.getInstance("Ed25519").generateKeyPair();

  PinnedNodeControlsTest() throws Exception {}

  String encoded(byte[] value) {
    return Base64.getUrlEncoder().withoutPadding().encodeToString(value);
  }

  String challenge() {
    byte[] value = new byte[32];
    new SecureRandom().nextBytes(value);
    return encoded(value);
  }

  NodeControlPin pin() {
    return new NodeControlPin(
        "target", node.toString(), "node-confirmation", encoded(key.getPublic().getEncoded()), "1");
  }

  byte[] pins(NodeControlPin... values) {
    return Json.MAPPER.writeValueAsBytes(
        new NodeControlPins()
            .schemaVersion(2)
            .tenantId(tenant.toString())
            .realm("1")
            .pins(List.of(values)));
  }

  ControlPackage pending() {
    var control =
        new SignedRootControl()
            .payload(
                new RootControl()
                    .schemaVersion(2)
                    .tenantId(tenant.toString())
                    .realm("1")
                    .controlRevision("3")
                    .issuedAt("2026-11-15T00:00:00Z")
                    .minimumGeneration("1")
                    .minimumKeysetVersion("1")
                    .allowedAuthorities(List.of())
                    .revokedAuthorityIds(List.of())
                    .revokedInstallationIds(new ArrayList<>()));
    return new ControlPackage()
        .packageId("package")
        .tenantId(tenant.toString())
        .realm("1")
        .targetId("target")
        .challenge(challenge())
        .control(control)
        .controlDigest(InstallationJson.digest(Json.MAPPER.valueToTree(control)));
  }

  InstallationReceipt receipt(ControlPackage pending, KeyPair signer) throws Exception {
    var value =
        new InstallationReceipt()
            .packageId(pending.getPackageId())
            .tenantId(tenant.toString())
            .realm("1")
            .targetId("target")
            .identityId("node-confirmation")
            .challenge(pending.getChallenge())
            .controlRevision("3")
            .controlDigest(pending.getControlDigest())
            .result(InstallationReceipt.ResultEnum.INSTALLED)
            .persistedAt(
                Instant.now().truncatedTo(java.time.temporal.ChronoUnit.SECONDS).toString());
    var body = (tools.jackson.databind.node.ObjectNode) Json.MAPPER.valueToTree(value);
    body.remove("identity_auth");
    var signature = java.security.Signature.getInstance("Ed25519");
    signature.initSign(signer.getPrivate());
    signature.update("Auriga.NetworkControlReceipt.v2\0".getBytes(StandardCharsets.UTF_8));
    signature.update(InstallationJson.canonical(body).getBytes(StandardCharsets.UTF_8));
    return value.identityAuth(
        new auriga.contracts.msgnet.model.Signature()
            .alg("Ed25519")
            .kid("node-confirmation")
            .signature(encoded(signature.sign())));
  }

  byte[] wire(InstallationReceipt value) {
    return Json.MAPPER.writeValueAsBytes(value);
  }

  @Test
  void authenticatesOnlyTheRegisteredNodeAndOutstandingChallenge() throws Exception {
    var authority = new PinnedNodeControls(pins(pin()));
    var pending = pending();
    var receipt = receipt(pending, key);
    assertEquals(
        "node-confirmation",
        authority.verify(tenant, node, wire(receipt), pending).getIdentityId());
    assertThrows(
        RuntimeException.class,
        () -> authority.verify(tenant, UUID.randomUUID(), wire(receipt), pending));
    assertThrows(
        RuntimeException.class,
        () -> authority.verify(UUID.randomUUID(), node, wire(receipt), pending));
    assertThrows(
        RuntimeException.class, () -> authority.verify(tenant, node, wire(receipt), pending()));
    var rogue = KeyPairGenerator.getInstance("Ed25519").generateKeyPair();
    var forged = receipt(pending, rogue);
    assertThrows(
        RuntimeException.class, () -> authority.verify(tenant, node, wire(forged), pending));
    pending.getControl().getPayload().getRevokedInstallationIds().add("node-confirmation");
    assertThrows(
        RuntimeException.class, () -> authority.verify(tenant, node, wire(receipt), pending));
  }

  @Test
  void rejectsSharedPinsNativeEcdsaAndUntrustedReplacement() throws Exception {
    assertThrows(RuntimeException.class, () -> new PinnedNodeControls(pins(pin(), pin())));
    var ec = KeyPairGenerator.getInstance("EC").generateKeyPair();
    var wrong = pin().signingSpki(encoded(ec.getPublic().getEncoded()));
    assertThrows(RuntimeException.class, () -> new PinnedNodeControls(pins(wrong)));
    assertThrows(
        RuntimeException.class,
        () -> new PinnedNodeControls(pins(pin().identityRevision("18446744073709551616"))));
    var authority = new PinnedNodeControls(pins(pin()));
    authority.binding(tenant, node).pin().setIdentityId("mutated");
    assertEquals("node-confirmation", authority.binding(tenant, node).pin().getIdentityId());
    var pending = pending();
    var old = receipt(pending, key);
    var replacement =
        new PinnedNodeControls(
            pins(pin().identityId("new-node-confirmation").identityRevision("2")));
    assertThrows(
        RuntimeException.class, () -> replacement.verify(tenant, node, wire(old), pending));
  }
}
