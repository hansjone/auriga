package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.error.AurigaException;
import auriga.contracts.msgnet.model.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.nio.file.attribute.*;
import java.security.*;
import java.security.cert.X509Certificate;
import java.util.*;
import javax.security.auth.x500.X500Principal;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

class PinnedNetworkInstallationsTest {
  @TempDir Path directory;
  final KeyPair signing = KeyPairGenerator.getInstance("Ed25519").generateKeyPair();
  final KeyPair sealing = KeyPairGenerator.getInstance("X25519").generateKeyPair();
  final Set<String> used = new HashSet<>();
  boolean allowed = true;

  PinnedNetworkInstallationsTest() throws Exception {}

  static String encoded(byte[] raw) {
    return Base64.getUrlEncoder().withoutPadding().encodeToString(raw);
  }

  InstallationPin pin() {
    return new InstallationPin(
        "target",
        "identity",
        encoded(signing.getPublic().getEncoded()),
        InstallationPin.ComponentEnum.EDGE,
        "edge-gateway",
        encoded(sealing.getPublic().getEncoded()),
        "CN=edge-projector");
  }

  byte[] pins(InstallationPin... pins) {
    return Json.MAPPER.writeValueAsBytes(
        new InstallationPins().schemaVersion(2).tenantId("tenant").realm("1").pins(List.of(pins)));
  }

  PinnedNetworkInstallations authority() {
    return new PinnedNetworkInstallations(
        pins(pin()),
        (t, r, i, target) -> allowed,
        r -> used.add(r.getTenantId() + ":" + r.getInstallationId() + ":" + r.getNonce()));
  }

  ProjectionRequest request() throws Exception {
    byte[] nonce = new byte[32];
    new SecureRandom().nextBytes(nonce);
    var request =
        new ProjectionRequest()
            .tenantId("tenant")
            .realm("1")
            .installationId("identity")
            .component(ProjectionRequest.ComponentEnum.EDGE)
            .gatewayId("edge-gateway")
            .nonce(encoded(nonce))
            .expectedGeneration("1")
            .expectedKeysetVersion("2");
    var auth = java.security.Signature.getInstance("Ed25519");
    auth.initSign(signing.getPrivate());
    auth.update("Auriga.NetworkProjectionRequest.v2\0".getBytes(StandardCharsets.UTF_8));
    var body = (tools.jackson.databind.node.ObjectNode) Json.MAPPER.valueToTree(request);
    body.remove("identity_auth");
    auth.update(InstallationJson.canonical(body).getBytes(StandardCharsets.UTF_8));
    request.setIdentityAuth(
        new auriga.contracts.msgnet.model.Signature()
            .alg("Ed25519")
            .kid("identity")
            .signature(encoded(auth.sign())));
    return request;
  }

  static byte[] wire(ProjectionRequest request) {
    return Json.MAPPER.writeValueAsBytes(request);
  }

  @Test
  void readsOnlyTheProcessOwnedPrivateSnapshot() throws Exception {
    Path file = Files.write(directory.resolve("pins.json"), pins(pin()));
    if (directory.getFileSystem().supportedFileAttributeViews().contains("posix")) {
      Files.setPosixFilePermissions(directory, PosixFilePermissions.fromString("rwx------"));
      Files.setPosixFilePermissions(file, PosixFilePermissions.fromString("rw-------"));
      assertEquals(pins(pin()).length, InstallationPinFile.read(file).length);
      Files.setPosixFilePermissions(file, PosixFilePermissions.fromString("rw-r-----"));
      assertThrows(java.io.IOException.class, () -> InstallationPinFile.read(file));
    } else {
      var helper = new ServiceTokenFilePublisherWindowsTest();
      helper.dir = directory;
      helper.restrict(directory);
      helper.restrict(file);
      assertArrayEquals(pins(pin()), InstallationPinFile.read(file));
      var acl = Files.getFileAttributeView(file, AclFileAttributeView.class);
      var entries = new ArrayList<>(acl.getAcl());
      entries.add(
          AclEntry.newBuilder()
              .setPrincipal(helper.everyone())
              .setType(AclEntryType.ALLOW)
              .setPermissions(AclEntryPermission.READ_DATA)
              .build());
      acl.setAcl(entries);
      assertThrows(java.io.IOException.class, () -> InstallationPinFile.read(file));
    }
  }

  @Test
  void nonceStoreReceivesTheExactVerifiedRequestAndCannotMutateIt() throws Exception {
    var authority =
        new PinnedNetworkInstallations(
            pins(pin()),
            (t, r, i, target) -> true,
            verified -> {
              assertEquals("2", verified.getExpectedKeysetVersion());
              assertEquals("edge-gateway", verified.getGatewayId());
              verified.setGatewayId("changed-by-adapter");
              return true;
            });
    assertEquals(
        "edge-gateway",
        authority.authenticateSealedMaterial(wire(request())).request().getGatewayId());
  }

  @Test
  void rawUnknownAndDuplicateFieldsNeverReachNonceLedger() throws Exception {
    var authority = authority();
    String raw = Json.MAPPER.writeValueAsString(request());
    byte[] unknown =
        raw.replaceFirst("\\{", "{\"unexpected\":true,").getBytes(StandardCharsets.UTF_8);
    assertThrows(AurigaException.class, () -> authority.authenticateSealedMaterial(unknown));
    byte[] duplicate =
        raw.replaceFirst("\\{", "{\"tenant_id\":\"tenant\",").getBytes(StandardCharsets.UTF_8);
    assertThrows(AurigaException.class, () -> authority.authenticateSealedMaterial(duplicate));
    assertTrue(used.isEmpty());
  }

  @Test
  void sealedBootstrapHasIndependentSignatureAndDurableNonceGate() throws Exception {
    var authority = authority();
    var request = request();
    assertEquals(
        "identity", authority.authenticateSealedMaterial(wire(request)).pin().getIdentityId());
    assertEquals(
        "common.forbidden",
        assertThrows(
                AurigaException.class, () -> authority.authenticateSealedMaterial(wire(request)))
            .errorCode()
            .code());
    allowed = false;
    var revoked = request();
    assertThrows(AurigaException.class, () -> authority.authenticateSealedMaterial(wire(revoked)));
    assertEquals(1, used.size());
  }

  @Test
  void componentGatewayAndSignatureCannotSelfGrantScope() throws Exception {
    var authority = authority();
    var wrongComponent = request();
    wrongComponent.setComponent(ProjectionRequest.ComponentEnum.NETWORK);
    assertThrows(
        AurigaException.class, () -> authority.authenticateSealedMaterial(wire(wrongComponent)));
    var gateway = request();
    gateway.setGatewayId("another-gateway");
    assertThrows(AurigaException.class, () -> authority.authenticateSealedMaterial(wire(gateway)));
    var tampered = request();
    tampered.setExpectedKeysetVersion("3");
    assertThrows(AurigaException.class, () -> authority.authenticateSealedMaterial(wire(tampered)));
    assertTrue(used.isEmpty());
  }

  @Test
  void mtlsSubjectIsAdditionalToInstallationSignature() throws Exception {
    var authority = authority();
    var peer = mock(X509Certificate.class);
    when(peer.getSubjectX500Principal()).thenReturn(new X500Principal("CN=other"));
    var request = request();
    assertThrows(AurigaException.class, () -> authority.authenticateMaterial(wire(request), peer));
    when(peer.getSubjectX500Principal()).thenReturn(new X500Principal("CN=edge-projector"));
    assertNotNull(authority.authenticateMaterial(wire(request), peer));
    assertThrows(
        AurigaException.class, () -> authority.authenticateMaterial(wire(request()), null));
  }

  @Test
  void unknownDuplicateAndWrongAlgorithmPinsAreRejected() {
    var pin = pin();
    assertThrows(
        IllegalArgumentException.class,
        () ->
            new PinnedNetworkInstallations(pins(pin, pin), (t, r, i, x) -> true, verified -> true));
    pin.setSigningSpki(encoded(sealing.getPublic().getEncoded()));
    assertThrows(
        IllegalArgumentException.class,
        () -> new PinnedNetworkInstallations(pins(pin), (t, r, i, x) -> true, verified -> true));
    assertThrows(
        AurigaException.class,
        () ->
            new PinnedNetworkInstallations(
                "{\"schema_version\":2,\"schema_version\":2}".getBytes(StandardCharsets.UTF_8),
                (t, r, i, x) -> true,
                verified -> true));
  }
}
