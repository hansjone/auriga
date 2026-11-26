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
  final java.util.List<ObjectNode> exported = new java.util.ArrayList<>();

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
            access,
            tenant,
            event -> exported.add(event.deepCopy()));
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
    body.set(
        "components",
        ResourceBody.JSON
            .createObjectNode()
            .put("dshVersion", "0.2.1-rc.3")
            .put("nodeVersion", "24.0.0")
            .put("pluginVersion", "1.0.0"));
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
    try (var stream = releases.download(2).stream()) {
      assertArrayEquals("zip".getBytes(StandardCharsets.UTF_8), stream.readAllBytes());
    }
    releases.withdraw(actor, 2);
    rejects(() -> releases.latest(), CommonErrors.NOT_FOUND);
    rejects(() -> releases.download(2), CommonErrors.NOT_FOUND);
    assertEquals(2, releases.list(actor).path("audit").size());
    assertEquals(2, releases.list(actor).path("highestSequence").asInt());
    assertEquals(2, exported.size());
    releases.flushAudit();
    assertEquals(2, exported.size());
  }

  @Test
  void withdrawalReturnsPreviousPublicReleaseWithoutPrivateFields() throws Exception {
    var first = envelope();
    releases.publish(actor, first, zip());
    var controller = new DesktopController(releases);
    assertEquals(200, controller.latest("stable", "win-x64").getStatusCode().value());
    var response = controller.download(2, null);
    assertEquals(200, response.getStatusCode().value());
    assertEquals(
        "attachment; filename=\"AurigaDSH-Setup-2.0.0.exe\"",
        response.getHeaders().getFirst("Content-Disposition"));
    assertEquals(
        "AurigaDSH-Setup-2.0.0.exe",
        controller.latest("stable", "win-x64").getHeaders().getFirst("X-Desktop-Filename"));
    response.getBody().getInputStream().close();
    rejects(controller::list, CommonErrors.UNAUTHENTICATED);
    var second = sign(envelope().put("version", "3.0.0").put("sequence", 3));
    releases.publish(actor, second, zip());
    assertEquals(3, releases.latest().path("sequence").asInt());
    releases.withdraw(actor, 3);
    assertEquals(first, releases.latest());
    assertFalse(releases.latest().has("actor"));
    assertFalse(releases.latest().has("audit"));
    assertFalse(releases.latest().has("path"));
    assertFalse(releases.latest().has("exported"));
  }

  @Test
  void auditQueueSurvivesExporterFailureAndRetriesAfterRestart() throws Exception {
    var pem = Base64.getEncoder().encodeToString(keys.getPublic().getEncoded());
    var failing =
        new DesktopReleases(
            root,
            pem,
            Clock.fixed(now, ZoneOffset.UTC),
            access,
            tenant,
            event -> {
              throw new IllegalStateException("unavailable");
            });
    failing.publish(actor, envelope(), zip());
    assertFalse(failing.list(actor).path("audit").get(0).path("exported").asBoolean());
    releases.flushAudit();
    assertEquals(1, exported.size());
    releases.flushAudit();
    assertEquals(1, exported.size());
  }

  @Test
  void renewalAndExpiredPublicDownloadRemainAvailable() throws Exception {
    var first = envelope();
    releases.publish(actor, first, zip());
    var later =
        new DesktopReleases(
            root,
            Base64.getEncoder().encodeToString(keys.getPublic().getEncoded()),
            Clock.fixed(Instant.parse("2026-12-02T00:00:00Z"), ZoneOffset.UTC),
            access,
            tenant,
            event -> exported.add(event.deepCopy()));
    rejects(() -> later.verify(first), CommonErrors.VALIDATION_FAILED);
    assertEquals(2, later.latest().path("sequence").asInt());
    try (var stream = later.download(2).stream()) {
      assertArrayEquals("zip".getBytes(StandardCharsets.UTF_8), stream.readAllBytes());
    }
    var renewed = sign(first.deepCopy().put("sequence", 3).put("notAfter", "2027-01-01T00:00:00Z"));
    later.publish(actor, renewed, zip());
    assertEquals(3, later.latest().path("sequence").asInt());
    var changedId =
        sign(
            renewed
                .deepCopy()
                .put("sequence", 4)
                .put("packageId", "different")
                .put("notAfter", "2027-02-01T00:00:00Z"));
    rejects(() -> later.publish(actor, changedId, zip()), CommonErrors.CONFLICT);
    var unchangedExpiry = sign(renewed.deepCopy().put("sequence", 4));
    assertEquals(4, later.publish(actor, unchangedExpiry, zip()).path("sequence").asInt());
    later.withdraw(actor, 4);
    later.withdraw(actor, 3);
    assertEquals(2, later.latest().path("sequence").asInt());
  }

  @Test
  void anonymousDownloadSupportsSingleOpenEndedRange() throws Exception {
    releases.publish(actor, envelope(), zip());
    var controller = new DesktopController(releases);
    var partial = controller.download(2, "bytes=1-");
    assertEquals(206, partial.getStatusCode().value());
    assertEquals("bytes 1-2/3", partial.getHeaders().getFirst("Content-Range"));
    assertEquals(2, partial.getHeaders().getContentLength());
    try (var stream = partial.getBody().getInputStream()) {
      assertEquals("ip", new String(stream.readAllBytes(), StandardCharsets.UTF_8));
    }
    for (String range :
        List.of("bytes=3-", "bytes=0-1,2-", "bytes=-1", "bytes=999999999999999999999-"))
      assertEquals(416, controller.download(2, range).getStatusCode().value());
    var mvc =
        org.springframework.test.web.servlet.setup.MockMvcBuilders.standaloneSetup(controller)
            .build();
    mvc.perform(
            org.springframework.test.web.servlet.request.MockMvcRequestBuilders.get(
                    "/api/resource/v1/desktop/packages/2")
                .header("Range", "bytes=1-"))
        .andExpect(
            org.springframework.test.web.servlet.result.MockMvcResultMatchers.status()
                .isPartialContent())
        .andExpect(
            org.springframework.test.web.servlet.result.MockMvcResultMatchers.header()
                .string("Content-Range", "bytes 1-2/3"))
        .andExpect(
            org.springframework.test.web.servlet.result.MockMvcResultMatchers.content()
                .bytes("ip".getBytes(StandardCharsets.UTF_8)));
  }

  @Test
  void requiresLivePermissionAndTenant() throws Exception {
    var valid = envelope();
    when(access.administrator(actor, DesktopReleases.MANAGE))
        .thenThrow(new AurigaException(CommonErrors.FORBIDDEN));
    rejects(() -> releases.list(actor), CommonErrors.FORBIDDEN);
    rejects(() -> releases.preflight(actor, valid), CommonErrors.FORBIDDEN);
    rejects(() -> releases.publish(actor, valid, zip()), CommonErrors.FORBIDDEN);
    rejects(() -> releases.withdraw(actor, 2), CommonErrors.FORBIDDEN);
    var other = mock(AurigaPrincipal.class);
    when(access.administrator(other, DesktopReleases.MANAGE))
        .thenReturn(new ResourceAccess.Reader(UUID.randomUUID(), account, false, Set.of(), false));
    rejects(() -> releases.list(other), CommonErrors.FORBIDDEN);
    rejects(() -> releases.preflight(other, valid), CommonErrors.FORBIDDEN);
    for (var method : DesktopController.class.getDeclaredMethods()) {
      assertTrue(
          method.isAnnotationPresent(RequiresPermission.class)
              || method.isAnnotationPresent(PublicEndpoint.class));
      if (Set.of("publish", "withdraw").contains(method.getName()))
        assertNotNull(method.getAnnotation(RequiresRecentStepUp.class));
      else assertNull(method.getAnnotation(RequiresRecentStepUp.class));
    }
  }

  @Test
  void consoleStatusUsesActualLatestAndRegisteredSigningKey() throws Exception {
    var empty = releases.list(actor);
    assertTrue(empty.path("channels").isEmpty());
    assertEquals(
        HexFormat.of()
            .formatHex(MessageDigest.getInstance("SHA-256").digest(keys.getPublic().getEncoded())),
        empty.path("publicKeyFingerprint").asString());
    assertEquals(
        Base64.getEncoder().encodeToString(keys.getPublic().getEncoded()),
        empty.path("publicKeySpki").asString());
    var valid = envelope();
    assertEquals(
        empty.path("publicKeyFingerprint"),
        releases.preflight(actor, valid).path("publicKeyFingerprint"));
    assertTrue(releases.list(actor).path("items").isEmpty());
    rejects(
        () -> releases.preflight(actor, valid.deepCopy().put("version", "9.0.0")),
        CommonErrors.VALIDATION_FAILED);
    releases.publish(actor, valid, zip());
    assertEquals(valid, releases.list(actor).path("channels").path("stable"));
    assertEquals(valid, releases.list(actor).path("platforms").path("win-x64").path("stable"));
    rejects(() -> releases.preflight(actor, valid), CommonErrors.CONFLICT);
    releases.withdraw(actor, 2);
    assertTrue(releases.list(actor).path("channels").isEmpty());
  }

  @Test
  void linuxPlatformPublishesIndependentlyOfWindows() throws Exception {
    var windows = envelope();
    releases.publish(actor, windows, zip());
    byte[] linuxBytes = "linux-zip".getBytes(StandardCharsets.UTF_8);
    var linux =
        sign(
            envelope()
                .put("platform", "linux-x64")
                .put("sequence", 3)
                .put("packageId", "package-linux-2")
                .put("sizeBytes", linuxBytes.length)
                .put(
                    "sha256",
                    HexFormat.of()
                        .formatHex(MessageDigest.getInstance("SHA-256").digest(linuxBytes))));
    releases.publish(actor, linux, new ByteArrayInputStream(linuxBytes));
    assertEquals("win-x64", releases.latest("stable", "win-x64").path("platform").asString());
    assertEquals("linux-x64", releases.latest("stable", "linux-x64").path("platform").asString());
    var controller = new DesktopController(releases);
    assertEquals(
        "auriga-desktop-2.0.0-linux-x64.tar.gz",
        controller.latest("stable", "linux-x64").getHeaders().getFirst("X-Desktop-Filename"));
    assertEquals(
        "attachment; filename=\"auriga-desktop-2.0.0-linux-x64.tar.gz\"",
        controller.download(3, null).getHeaders().getFirst("Content-Disposition"));
  }

  @Test
  void signedChannelsPolicyAndExplicitRollback() throws Exception {
    var stable =
        sign(envelope().put("schema", 2).put("rolloutPercentage", 25).putNull("rollbackFrom"));
    releases.publish(actor, stable, zip());
    var beta =
        sign(stable.deepCopy().put("channel", "beta").put("version", "3.0.0").put("sequence", 3));
    releases.publish(actor, beta, zip());
    assertEquals("2.0.0", releases.latest().path("version").asString());
    assertEquals("3.0.0", releases.latest("beta").path("version").asString());
    var mandatory =
        sign(
            stable
                .deepCopy()
                .put("minimumVersion", "2.0.0")
                .put("sequence", 4)
                .put("rolloutPercentage", 0));
    releases.publish(actor, mandatory, zip());
    assertEquals("2.0.0", releases.latest().path("minimumVersion").asString());
    var downgrade = sign(stable.deepCopy().put("version", "1.5.0").put("sequence", 5));
    rejects(() -> releases.publish(actor, downgrade, zip()), CommonErrors.CONFLICT);
    var rollback = sign(downgrade.deepCopy().put("rollbackFrom", "2.0.0"));
    releases.publish(actor, rollback, zip());
    assertEquals("1.5.0", releases.latest().path("version").asString());
    assertEquals("rollback", releases.list(actor).path("audit").get(3).path("action").asString());
    releases.withdraw(actor, 5);
    rejects(() -> releases.download(5), CommonErrors.NOT_FOUND);
    var wrongSource = sign(rollback.deepCopy().put("sequence", 6).put("rollbackFrom", "9.0.0"));
    rejects(() -> releases.publish(actor, wrongSource, zip()), CommonErrors.CONFLICT);
    var badMinimum = sign(stable.deepCopy().put("minimumVersion", "9.0.0"));
    rejects(() -> releases.verify(badMinimum), CommonErrors.VALIDATION_FAILED);
    var badRollout = sign(stable.deepCopy().put("rolloutPercentage", 4294967296L));
    rejects(() -> releases.verify(badRollout), CommonErrors.VALIDATION_FAILED);
  }

  @Test
  void httpPublishAcceptsSignedNullableRollbackPolicy() throws Exception {
    var policy =
        sign(
            envelope()
                .put("schema", 2)
                .put("channel", "beta")
                .put("minimumVersion", "2.0.0")
                .put("rolloutPercentage", 0)
                .putNull("rollbackFrom"));
    var mvc =
        org.springframework.test.web.servlet.setup.MockMvcBuilders.standaloneSetup(
                new DesktopController(releases))
            .build();
    org.springframework.security.core.context.SecurityContextHolder.getContext()
        .setAuthentication(new AurigaAuthentication(actor));
    try {
      mvc.perform(
              org.springframework.test.web.servlet.request.MockMvcRequestBuilders.post(
                      "/api/resource/v1/admin/desktop/releases/preflight")
                  .contentType("application/json")
                  .content(ResourceBody.JSON.writeValueAsBytes(policy)))
          .andExpect(
              org.springframework.test.web.servlet.result.MockMvcResultMatchers.status().isOk());
      assertTrue(releases.list(actor).path("items").isEmpty());
      mvc.perform(
              org.springframework.test.web.servlet.request.MockMvcRequestBuilders.post(
                      "/api/resource/v1/admin/desktop/releases")
                  .contentType("application/octet-stream")
                  .header(
                      "X-Desktop-Manifest",
                      Base64.getUrlEncoder()
                          .withoutPadding()
                          .encodeToString(ResourceBody.JSON.writeValueAsBytes(policy)))
                  .content("zip".getBytes(StandardCharsets.UTF_8)))
          .andExpect(
              org.springframework.test.web.servlet.result.MockMvcResultMatchers.status()
                  .isCreated());
      assertTrue(releases.latest("beta").path("rollbackFrom").isNull());
      assertEquals(0, releases.latest("beta").path("rolloutPercentage").asInt());
    } finally {
      org.springframework.security.core.context.SecurityContextHolder.clearContext();
    }
  }
}
