package auriga.resource;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.error.*;
import auriga.common.security.*;
import auriga.resource.app.*;
import auriga.resource.web.DesktopController;
import auriga.resource.web.ResourceBody;
import java.io.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.Path;
import java.security.*;
import java.time.*;
import java.util.*;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.io.TempDir;
import tools.jackson.databind.node.ObjectNode;

class DesktopReleasesTest {
  @TempDir Path root;
  final ResourceAccess access = mock(ResourceAccess.class);
  final Instant now = Instant.parse("2026-11-12T00:00:00Z");
  final UUID tenant = UUID.randomUUID(), account = UUID.randomUUID();
  final AurigaPrincipal actor =
      new AurigaPrincipal(
          account.toString(),
          tenant,
          PrincipalType.USER,
          TokenContext.ADMIN,
          "session",
          null,
          "member",
          List.of(),
          Set.of(DesktopReleases.MANAGE),
          now,
          now,
          "jti");
  KeyPair keys;
  DesktopReleases releases;

  @BeforeEach
  void setup() throws Exception {
    keys = KeyPairGenerator.getInstance("Ed25519").generateKeyPair();
    var reader = new ResourceAccess.Reader(tenant, account, false, Set.of(), false);
    when(access.administrator(actor, DesktopReleases.MANAGE)).thenReturn(reader);
    when(access.member(actor)).thenReturn(reader);
    releases =
        new DesktopReleases(
            root,
            Base64.getEncoder().encodeToString(keys.getPublic().getEncoded()),
            Clock.fixed(now, ZoneOffset.UTC),
            access);
  }

  ObjectNode envelope() throws Exception {
    var body =
        ResourceBody.JSON
            .createObjectNode()
            .put("schema", 1)
            .put("product", "Auriga Desktop")
            .put("version", "2.0.0")
            .put("packageId", "package-2")
            .put("channel", "stable")
            .put("platform", "win-x64")
            .put("sizeBytes", 3)
            .put(
                "sha256",
                HexFormat.of()
                    .formatHex(
                        MessageDigest.getInstance("SHA-256")
                            .digest("zip".getBytes(StandardCharsets.UTF_8))))
            .put("minimumVersion", "1.0.0")
            .put("publishedAt", "2026-11-11T00:00:00Z")
            .put("notAfter", "2026-12-01T00:00:00Z")
            .put("sequence", 2)
            .put("notes", "");
    return sign(body);
  }

  ObjectNode sign(ObjectNode body) throws Exception {
    body.remove("signature");
    var signer = Signature.getInstance("Ed25519");
    signer.initSign(keys.getPrivate());
    signer.update(ResourceSigning.canonical(body).getBytes(StandardCharsets.UTF_8));
    body.set(
        "signature",
        ResourceBody.JSON
            .createObjectNode()
            .put("alg", "Ed25519")
            .put("kid", "auriga-desktop-release-1")
            .put("value", Base64.getUrlEncoder().withoutPadding().encodeToString(signer.sign())));
    return body;
  }

  void rejects(Runnable action, ErrorCode code) {
    assertEquals(code, assertThrows(AurigaException.class, action::run).errorCode());
  }

  InputStream zip() {
    return new ByteArrayInputStream("zip".getBytes(StandardCharsets.UTF_8));
  }

  @Test
  void validatesSignatureDigestAndSequence() throws Exception {
    var valid = envelope();
    var tampered = valid.deepCopy().put("notes", "tampered");
    rejects(() -> releases.publish(actor, tampered, zip()), CommonErrors.VALIDATION_FAILED);
    rejects(
        () -> releases.publish(actor, valid, new ByteArrayInputStream(new byte[] {1, 2, 3})),
        CommonErrors.VALIDATION_FAILED);
    assertEquals(2, releases.publish(actor, valid, zip()).path("sequence").asInt());
    rejects(() -> releases.publish(actor, valid, zip()), CommonErrors.CONFLICT);
    assertEquals(1, releases.list(actor).path("audit").size());
  }

  @Test
  void rejectsExpiredAndWrongKeys() throws Exception {
    var expired = envelope().put("notAfter", "2026-11-11T00:01:00Z");
    sign(expired);
    rejects(() -> releases.verify(expired), CommonErrors.VALIDATION_FAILED);
    var valid = envelope();
    keys = KeyPairGenerator.getInstance("Ed25519").generateKeyPair();
    sign(valid);
    rejects(() -> releases.verify(valid), CommonErrors.VALIDATION_FAILED);
  }

  @Test
  void withdrawalHidesDownloadAndRetainsAuditAndWatermark() throws Exception {
    releases.publish(actor, envelope(), zip());
    try (var stream = releases.download(actor, 2).stream()) {
      assertArrayEquals("zip".getBytes(StandardCharsets.UTF_8), stream.readAllBytes());
    }
    releases.withdraw(actor, 2);
    rejects(() -> releases.latest(actor), CommonErrors.NOT_FOUND);
    rejects(() -> releases.download(actor, 2), CommonErrors.NOT_FOUND);
    assertEquals(2, releases.list(actor).path("audit").size());
    assertEquals(2, releases.list(actor).path("highestSequence").asInt());
  }

  @Test
  void requiresLivePermissionAndTenant() throws Exception {
    var valid = envelope();
    when(access.administrator(actor, DesktopReleases.MANAGE))
        .thenThrow(new AurigaException(CommonErrors.FORBIDDEN));
    rejects(() -> releases.publish(actor, valid, zip()), CommonErrors.FORBIDDEN);
    rejects(() -> releases.withdraw(actor, 2), CommonErrors.FORBIDDEN);
    var other = mock(AurigaPrincipal.class);
    when(access.member(other))
        .thenReturn(new ResourceAccess.Reader(UUID.randomUUID(), account, false, Set.of(), false));
    rejects(() -> releases.latest(other), CommonErrors.NOT_FOUND);
    for (var method : DesktopController.class.getDeclaredMethods()) {
      assertNotNull(method.getAnnotation(RequiresPermission.class));
      if (Set.of("publish", "withdraw", "list").contains(method.getName()))
        assertNotNull(method.getAnnotation(RequiresRecentStepUp.class));
    }
  }
}
