package auriga.collab.network.keys;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.*;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.*;

import auriga.collab.CollabModuleConfiguration;
import auriga.collab.app.NetworkJson;
import auriga.common.context.RequestContext;
import auriga.common.error.AurigaException;
import auriga.common.security.AurigaPrincipal;
import auriga.common.security.PrincipalType;
import auriga.common.security.TokenContext;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.attribute.AclEntry;
import java.nio.file.attribute.AclEntryPermission;
import java.nio.file.attribute.AclEntryType;
import java.nio.file.attribute.AclFileAttributeView;
import java.nio.file.attribute.PosixFileAttributeView;
import java.nio.file.attribute.PosixFilePermissions;
import java.time.Clock;
import java.time.ZoneOffset;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.UUID;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.AnnotatedElementContext;
import org.junit.jupiter.api.extension.ExtensionContext;
import org.junit.jupiter.api.io.TempDir;
import org.junit.jupiter.api.io.TempDirFactory;
import org.springframework.boot.test.context.runner.ApplicationContextRunner;
import org.springframework.test.web.servlet.setup.MockMvcBuilders;

class NetworkKeyConfigurationTest {
  @TempDir(factory = WorkspaceTempDirectory.class)
  Path directory;

  public static class WorkspaceTempDirectory implements TempDirFactory {
    @Override
    public Path createTempDirectory(AnnotatedElementContext element, ExtensionContext context)
        throws Exception {
      Path dir = Files.createTempDirectory(Path.of("target"), "keyops-").toAbsolutePath();
      restrictOwner(dir);
      return dir;
    }
  }

  final KeyTestMaterial m = new KeyTestMaterial();
  final Clock clock = Clock.fixed(KeyTestMaterial.NOW, ZoneOffset.UTC);

  static Path protectedFile(Path path, byte[] contents) throws Exception {
    Files.write(path, contents);
    restrictOwner(path);
    return path;
  }

  static void restrictOwner(Path path) throws Exception {
    var posix = Files.getFileAttributeView(path, PosixFileAttributeView.class);
    if (posix != null)
      Files.setPosixFilePermissions(
          path,
          PosixFilePermissions.fromString(Files.isDirectory(path) ? "rwx------" : "rw-------"));
    else {
      var acl = Files.getFileAttributeView(path, AclFileAttributeView.class);
      var entry =
          AclEntry.newBuilder()
              .setType(AclEntryType.ALLOW)
              .setPrincipal(acl.getOwner())
              .setPermissions(Set.of(AclEntryPermission.values()));
      if (Files.isDirectory(path))
        entry.setFlags(
            java.nio.file.attribute.AclEntryFlag.FILE_INHERIT,
            java.nio.file.attribute.AclEntryFlag.DIRECTORY_INHERIT);
      acl.setAcl(List.of(entry.build()));
    }
  }

  Map<String, String> secrets() throws Exception {
    return Map.of(
        "business",
        protectedFile(directory.resolve("business.der"), m.business.getPrivate().getEncoded())
            .toString(),
        "protocol",
        protectedFile(directory.resolve("protocol.der"), m.protocol.getPrivate().getEncoded())
            .toString(),
        "management",
        protectedFile(directory.resolve("management.der"), m.management.getPrivate().getEncoded())
            .toString());
  }

  @Test
  void installsOnlyPublicStateAndLoadsIsolatedBusinessSecrets() throws Exception {
    var p = m.payload(5, KeyTestMaterial.NOW);
    Path state = directory.resolve("state.json");
    SigningKeyLoader.installState(p, state);
    var secretFiles = secrets();
    try (var keys =
        SigningKeyLoader.load(p, state, secretFiles, clock, (registry, work) -> work.get())) {
      assertEquals(NetworkJson.canonical(p), keys.publicKeysJson("2"));
      assertThrows(
          RuntimeException.class,
          () -> SigningKeyLoader.installState(p.deepCopy().put("keyset_version", "6"), state));
    }
    assertFalse(
        Files.readString(state)
            .contains(
                java.util.Base64.getEncoder()
                    .encodeToString(m.business.getPrivate().getEncoded())));
    assertThrows(RuntimeException.class, () -> SigningKeyLoader.installState(p, state));
    assertThrows(
        RuntimeException.class,
        () -> SigningKeyLoader.load(p, state, Map.of(), clock, (registry, work) -> work.get()));
    var wrong = new java.util.HashMap<>(secretFiles);
    wrong.put("business", secretFiles.get("protocol"));
    assertThrows(
        RuntimeException.class,
        () -> SigningKeyLoader.load(p, state, wrong, clock, (registry, work) -> work.get()));
    assertThrows(
        RuntimeException.class,
        () ->
            SigningKeyLoader.load(
                p.deepCopy().put("keyset_version", "6"),
                state,
                secretFiles,
                clock,
                (registry, work) -> work.get()));
  }

  @Test
  void offlineCommandProducesPinnedRootReleaseAndPublicState() throws Exception {
    var issued = java.time.Instant.now().truncatedTo(java.time.temporal.ChronoUnit.SECONDS);
    Path payload =
        protectedFile(
            directory.resolve("payload.json"),
            NetworkJson.canonical(m.payload(1, issued))
                .getBytes(java.nio.charset.StandardCharsets.UTF_8));
    Path rootPrivate =
        protectedFile(directory.resolve("root-private.der"), m.root.getPrivate().getEncoded());
    Path rootPublic =
        protectedFile(directory.resolve("root-public.der"), m.root.getPublic().getEncoded());
    Path signed = directory.resolve("signed.json");
    Path announcement = directory.resolve("announcement.json");
    OfflineKeySigning.main(
        new String[] {
          "sign",
          payload.toString(),
          "root",
          rootPrivate.toString(),
          signed.toString(),
          announcement.toString()
        });
    Path state = directory.resolve("state.json");
    OfflineKeySigning.main(
        new String[] {
          "install",
          signed.toString(),
          announcement.toString(),
          "root",
          rootPublic.toString(),
          RootKeyPublication.fingerprint(m.root.getPublic()),
          state.toString()
        });
    assertEquals("1", NetworkJson.parse(Files.readString(state)).path("version").asString());
    m.verifier(Clock.systemUTC()).verify(Files.readString(signed), Files.readString(announcement));
    assertThrows(
        Exception.class,
        () ->
            OfflineKeySigning.main(
                new String[] {
                  "sign",
                  payload.toString(),
                  "root",
                  rootPrivate.toString(),
                  signed.toString(),
                  announcement.toString()
                }));
  }

  @Test
  void shorterRootDeadlineAndFullyRevokedPurposesRemainPublishableWithoutSecrets()
      throws Exception {
    var p =
        m.payload(9, KeyTestMaterial.NOW)
            .put("valid_until", KeyTestMaterial.NOW.plusSeconds(3600).toString());
    for (var entry : p.path("keys"))
      ((tools.jackson.databind.node.ObjectNode) entry).put("state", "revoked");
    Path state = directory.resolve("state.json");
    SigningKeyLoader.installState(p, state);
    try (var keys =
        SigningKeyLoader.load(p, state, Map.of(), clock, (registry, work) -> work.get())) {
      assertEquals(NetworkJson.canonical(p), keys.publicKeysJson("2"));
    }
  }

  @Test
  void exposedSecretAndSymlinkAreRejectedWithoutRevealingPaths() throws Exception {
    Path file =
        protectedFile(directory.resolve("secret.der"), m.business.getPrivate().getEncoded());
    assertArrayEquals(m.business.getPrivate().getEncoded(), ProtectedKeyFiles.readSecret(file));
    var posix = Files.getFileAttributeView(file, PosixFileAttributeView.class);
    if (posix != null)
      Files.setPosixFilePermissions(file, PosixFilePermissions.fromString("rw-r--r--"));
    else {
      var acl = Files.getFileAttributeView(file, AclFileAttributeView.class);
      var everyone =
          file.getFileSystem()
              .getUserPrincipalLookupService()
              .lookupPrincipalByGroupName("Everyone");
      var entries = new java.util.ArrayList<>(acl.getAcl());
      entries.add(
          AclEntry.newBuilder()
              .setType(AclEntryType.ALLOW)
              .setPrincipal(everyone)
              .setPermissions(AclEntryPermission.READ_DATA)
              .build());
      acl.setAcl(entries);
    }
    var failure =
        assertThrows(IllegalArgumentException.class, () -> ProtectedKeyFiles.readSecret(file));
    assertFalse(failure.getMessage().contains(file.toString()));
    if (posix != null) {
      Path link = Files.createSymbolicLink(directory.resolve("link.der"), file);
      assertThrows(RuntimeException.class, () -> ProtectedKeyFiles.readSecret(link));
    }
    Path huge = protectedFile(directory.resolve("large.der"), new byte[4097]);
    assertThrows(RuntimeException.class, () -> ProtectedKeyFiles.readSecret(huge));
  }

  @Test
  void fixedHttpEndpointsReturnRootSignedBytesAndEnforceTenantAndAuthentication() throws Exception {
    UUID tenant = UUID.randomUUID();
    var operations = mock(NetworkKeyOperations.class);
    var release = m.release(m.payload(1, KeyTestMaterial.NOW));
    when(operations.current()).thenReturn(release);
    var settings =
        new NetworkKeySettings(
            tenant,
            "2",
            "root",
            directory.resolve("root.der"),
            "pin",
            directory.resolve("signed.json"),
            directory.resolve("announcement.json"),
            directory.resolve("state.json"),
            "1",
            Map.of());
    var controller = new NetworkKeysController(operations, settings);
    assertEquals(
        "common.unauthenticated",
        assertThrows(AurigaException.class, controller::keys).errorCode().code());
    var mvc = MockMvcBuilders.standaloneSetup(controller).build();
    var principal =
        new AurigaPrincipal(
            "alice",
            tenant,
            PrincipalType.DEVICE,
            TokenContext.DEVICE,
            null,
            "device",
            null,
            List.of(),
            Set.of(),
            null,
            null,
            "token-id");
    RequestContext.start("keys-test", null)
        .withPrincipal(principal)
        .call(
            () -> {
              mvc.perform(
                      get("/api/collab/v1/network/keys")
                          .param("realm", "3")
                          .param("url", "https://example.com/untrusted"))
                  .andExpect(status().isOk())
                  .andExpect(header().string("Cache-Control", "no-store"))
                  .andExpect(content().string(release.signedKeyset()));
              mvc.perform(get("/api/collab/v1/network/keys/announcement"))
                  .andExpect(status().isOk())
                  .andExpect(content().string(release.announcement()));
              mvc.perform(post("/api/collab/v1/network/keys"))
                  .andExpect(status().isMethodNotAllowed());
              return null;
            });
    var stranger =
        new AurigaPrincipal(
            "alice",
            UUID.randomUUID(),
            PrincipalType.DEVICE,
            TokenContext.DEVICE,
            null,
            "device",
            null,
            List.of(),
            Set.of(),
            null,
            null,
            "token-id");
    RequestContext.start("keys-other", null)
        .withPrincipal(stranger)
        .run(
            () ->
                assertEquals(
                    "common.forbidden",
                    assertThrows(AurigaException.class, controller::keys).errorCode().code()));
  }

  @Test
  void keyOperationsRemainDisabledByDefaultAndFailClosedWhenConfigurationIsMissing() {
    new ApplicationContextRunner()
        .withUserConfiguration(CollabModuleConfiguration.class)
        .run(
            context -> {
              assertNull(context.getStartupFailure());
              assertFalse(context.containsBean("networkKeyOperations"));
              assertFalse(context.containsBean("approvedNetworkSigningKeys"));
            });
    new ApplicationContextRunner()
        .withUserConfiguration(CollabModuleConfiguration.class)
        .withPropertyValues(
            "auriga.service.schema=collab", "auriga.collab.network.key-operations.enabled=true")
        .run(context -> assertNotNull(context.getStartupFailure()));
  }
}
