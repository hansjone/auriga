package auriga.collab.network.keys;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.*;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.*;

import auriga.collab.CollabModuleConfiguration;
import auriga.collab.api.NetworkLocalTargets;
import auriga.collab.app.NetworkJson;
import auriga.collab.app.NetworkMailboxService;
import auriga.collab.app.NetworkMailboxWireService;
import auriga.collab.app.NetworkSigningKeys;
import auriga.collab.config.CollabNetworkConfiguration;
import auriga.collab.config.CollabRuntimeConfiguration;
import auriga.collab.grpc.NetworkAuthorityClient;
import auriga.collab.grpc.NetworkMailboxGrpcService;
import auriga.collab.grpc.NetworkSourceGrpcAuthority;
import auriga.common.context.RequestContext;
import auriga.common.error.AurigaException;
import auriga.common.persistence.TenantTransactionManager;
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
import javax.sql.DataSource;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.AnnotatedElementContext;
import org.junit.jupiter.api.extension.ExtensionContext;
import org.junit.jupiter.api.io.TempDir;
import org.junit.jupiter.api.io.TempDirFactory;
import org.springframework.beans.factory.support.AbstractBeanDefinition;
import org.springframework.beans.factory.support.StaticListableBeanFactory;
import org.springframework.boot.test.context.runner.ApplicationContextRunner;
import org.springframework.core.env.MapPropertySource;
import org.springframework.test.util.ReflectionTestUtils;
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
  void uninstalledApprovedReleaseCannotPartiallyAdvanceStateAndCanBeInstalledAfterRejection()
      throws Exception {
    var old = m.payload(5, KeyTestMaterial.NOW);
    Path state = directory.resolve("state.json");
    SigningKeyLoader.installState(old, state);
    var configured = new java.util.HashMap<>(secrets());
    var businessNext = KeyTestMaterial.pair();
    var protocolNext = KeyTestMaterial.pair();
    var next = m.payload(6, KeyTestMaterial.NOW);
    var entries = (tools.jackson.databind.node.ArrayNode) next.path("keys");
    entries.add(m.key("business-next", "business_delivery", businessNext).put("state", "next"));
    entries.add(m.key("protocol-next", "protocol_response", protocolNext).put("state", "next"));
    var release = m.release(next);
    var approved =
        m.verifier(clock).verify(release.signedKeyset(), release.announcement()).payload();
    configured.put(
        "business-next",
        protectedFile(
                directory.resolve("business-next.der"), businessNext.getPrivate().getEncoded())
            .toString());

    assertRejectedLoadKeepsState(approved, state, configured);
    assertEquals("5", NetworkJson.parse(Files.readString(state)).path("version").asString());
    // The same already-root-approved v6 remains installable, without relaxing anti-rollback.
    SigningKeyLoader.installState(approved, state);
    assertEquals("6", NetworkJson.parse(Files.readString(state)).path("version").asString());
    // Even after installation, loading a valid earlier key then missing a later NEXT must be inert.
    assertRejectedLoadKeepsState(approved, state, configured);
    configured.put(
        "protocol-next",
        protectedFile(
                directory.resolve("protocol-next.der"), protocolNext.getPrivate().getEncoded())
            .toString());
    assertHealthyLoadKeepsState(approved, state, configured);
    byte[] installed = Files.readAllBytes(state);
    assertThrows(
        IllegalArgumentException.class, () -> SigningKeyLoader.installState(approved, state));
    assertArrayEquals(installed, Files.readAllBytes(state));
  }

  @Test
  void wrongExtraAndDuplicatePrivateMappingsLeaveInstalledStateAndLockRecoverable()
      throws Exception {
    var approved = m.payload(5, KeyTestMaterial.NOW);
    Path state = directory.resolve("state.json");
    SigningKeyLoader.installState(approved, state);
    var valid = secrets();
    var wrongPair = new java.util.HashMap<>(valid);
    wrongPair.put(
        "management",
        protectedFile(
                directory.resolve("wrong.der"), KeyTestMaterial.pair().getPrivate().getEncoded())
            .toString());
    assertRejectedLoadKeepsState(approved, state, wrongPair);
    var extra = new java.util.HashMap<>(valid);
    extra.put("unpublished", valid.get("business"));
    assertRejectedLoadKeepsState(approved, state, extra);
    var duplicate = new java.util.HashMap<>(valid);
    duplicate.put("management", valid.get("protocol"));
    assertRejectedLoadKeepsState(approved, state, duplicate);
    var malformed = new java.util.HashMap<>(valid);
    malformed.put(
        "management", protectedFile(directory.resolve("malformed.der"), new byte[] {0}).toString());
    assertRejectedLoadKeepsState(approved, state, malformed);
    assertHealthyLoadKeepsState(approved, state, valid);
  }

  @Test
  void sameVersionDifferentApprovedContentCannotMutateStateOrBypassInstallProtection()
      throws Exception {
    var installed = m.payload(5, KeyTestMaterial.NOW);
    Path state = directory.resolve("state.json");
    SigningKeyLoader.installState(installed, state);
    var valid = secrets();
    var changed = installed.deepCopy();
    ((tools.jackson.databind.node.ObjectNode) changed.path("keys").get(0)).put("state", "revoked");
    var release = m.release(changed);
    var approved =
        m.verifier(clock).verify(release.signedKeyset(), release.announcement()).payload();
    assertRejectedLoadKeepsState(approved, state, valid);
    byte[] before = Files.readAllBytes(state);
    assertThrows(
        IllegalArgumentException.class, () -> SigningKeyLoader.installState(approved, state));
    assertArrayEquals(before, Files.readAllBytes(state));
    assertHealthyLoadKeepsState(installed, state, valid);
  }

  void assertRejectedLoadKeepsState(
      tools.jackson.databind.JsonNode approved, Path state, Map<String, String> configured)
      throws Exception {
    byte[] before = Files.readAllBytes(state);
    var version = NetworkJson.parse(before).path("version");
    var rejected =
        assertThrows(
            IllegalStateException.class,
            () ->
                SigningKeyLoader.load(
                    approved, state, configured, clock, (registry, work) -> work.get()));
    assertEquals("Cannot initialize approved network signing keys", rejected.getMessage());
    assertArrayEquals(
        before, Files.readAllBytes(state), "Failed load must preserve exact installed bytes");
    assertEquals(version, NetworkJson.parse(Files.readAllBytes(state)).path("version"));
    try (var channel =
            java.nio.channels.FileChannel.open(
                state.resolveSibling(state.getFileName() + ".lock"),
                java.nio.file.StandardOpenOption.WRITE);
        var lock = channel.tryLock()) {
      assertNotNull(lock, "Rejected load must release the publisher lock");
    }
  }

  void assertHealthyLoadKeepsState(
      tools.jackson.databind.JsonNode approved, Path state, Map<String, String> configured)
      throws Exception {
    byte[] before = Files.readAllBytes(state);
    try (var keys =
        SigningKeyLoader.load(approved, state, configured, clock, (registry, work) -> work.get())) {
      assertEquals(NetworkJson.canonical(approved), keys.publicKeysJson("2"));
      assertArrayEquals(before, Files.readAllBytes(state));
    }
    assertArrayEquals(before, Files.readAllBytes(state));
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
  void runtimeKeepsMainFileSigningRequirementsAndWireBindingsWhenKeyOperationsAreOff()
      throws Exception {
    Path state = directory.resolve("legacy.json");
    var properties = new java.util.HashMap<String, Object>();
    properties.put("auriga.collab.network.key-operations.enabled", "false");
    runtimeRunner(false, properties).run(context -> assertNotNull(context.getStartupFailure()));
    properties.put("auriga.collab.network.key-state-file", state.toString());
    runtimeRunner(false, properties).run(context -> assertNotNull(context.getStartupFailure()));
    Path publicFile =
        protectedFile(directory.resolve("legacy.pub"), m.protocol.getPublic().getEncoded());
    Path privateFile =
        protectedFile(directory.resolve("legacy.der"), m.protocol.getPrivate().getEncoded());
    String prefix = "auriga.collab.network.signing-keys[0].";
    properties.put(prefix + "realm", "2");
    properties.put(prefix + "purpose", "protocol_response");
    properties.put(prefix + "kid", "legacy-protocol");
    properties.put(prefix + "public-file", publicFile.toString());
    properties.put(prefix + "private-file", privateFile.toString());
    properties.put(prefix + "not-before", java.time.Instant.now().minusSeconds(60).toString());
    properties.put(prefix + "not-after", java.time.Instant.now().plusSeconds(3600).toString());
    properties.put(prefix + "state", "ACTIVE");
    var missingState = new java.util.HashMap<>(properties);
    missingState.remove("auriga.collab.network.key-state-file");
    runtimeRunner(false, missingState).run(context -> assertNotNull(context.getStartupFailure()));
    for (boolean approvedFirst : new boolean[] {false, true}) {
      runtimeRunner(approvedFirst, properties)
          .run(
              context -> {
                assertNull(context.getStartupFailure());
                assertEquals(
                    Set.of("networkSigningKeys"),
                    context.getBeansOfType(NetworkSigningKeys.class).keySet());
                assertFalse(context.containsBean("approvedNetworkSigningKeys"));
                assertEquals(
                    m.protocol.getPublic(),
                    context.getBean(NetworkSigningKeys.class).publicKey("legacy-protocol"));
                assertRuntimeBindings(context);
              });
    }
    assertTrue(Files.exists(state));
  }

  @Test
  void approvedRuntimeUsesOnlyTheRealLoaderInEitherConfigurationOrder() throws Exception {
    var issued = java.time.Instant.now().truncatedTo(java.time.temporal.ChronoUnit.SECONDS);
    var payload = m.payload(6, issued);
    for (var entry : payload.path("keys")) {
      ((tools.jackson.databind.node.ObjectNode) entry)
          .put("not_before", issued.minusSeconds(60).toString())
          .put("not_after", issued.plusSeconds(86400).toString());
    }
    var signed = m.release(payload);
    var approved =
        m.verifier(Clock.fixed(issued, ZoneOffset.UTC))
            .verify(signed.signedKeyset(), signed.announcement());
    var operations = mock(NetworkKeyOperations.class);
    when(operations.current()).thenReturn(approved.release());
    var privateFiles = secrets();
    Path root =
        protectedFile(directory.resolve("runtime-root.der"), m.root.getPublic().getEncoded());
    Path signedFile =
        protectedFile(
            directory.resolve("runtime-signed.json"),
            signed.signedKeyset().getBytes(java.nio.charset.StandardCharsets.UTF_8));
    Path announcement =
        protectedFile(
            directory.resolve("runtime-announcement.json"),
            signed.announcement().getBytes(java.nio.charset.StandardCharsets.UTF_8));
    for (boolean approvedFirst : new boolean[] {false, true}) {
      Path state = directory.resolve("approved-" + approvedFirst + ".json");
      SigningKeyLoader.installState(payload, state);
      byte[] before = Files.readAllBytes(state);
      var properties = new java.util.HashMap<String, Object>();
      String prefix = "auriga.collab.network.key-operations.";
      properties.put(prefix + "enabled", "true");
      properties.put(prefix + "tenant-id", UUID.randomUUID().toString());
      properties.put(prefix + "realm", "2");
      properties.put(prefix + "root-kid", "root");
      properties.put(prefix + "root-public-file", root.toString());
      properties.put(
          prefix + "root-fingerprint", RootKeyPublication.fingerprint(m.root.getPublic()));
      properties.put(prefix + "signed-file", signedFile.toString());
      properties.put(prefix + "announcement-file", announcement.toString());
      properties.put(prefix + "minimum-version", "6");
      properties.put("auriga.collab.network.key-state-file", state.toString());
      privateFiles.forEach(
          (kid, file) -> properties.put(prefix + "private-files[" + kid + "]", file));
      // A stale legacy configuration must never bind or construct the other signer.
      properties.put("auriga.collab.network.signing-keys[0].state", "INVALID");
      approvedRunner(approvedFirst, properties, operations)
          .run(
              context -> {
                assertNull(context.getStartupFailure());
                assertEquals(
                    Set.of("approvedNetworkSigningKeys"),
                    context.getBeansOfType(NetworkSigningKeys.class).keySet());
                assertFalse(context.containsBean("networkSigningKeys"));
                assertEquals(
                    NetworkJson.canonical(payload),
                    context.getBean(NetworkSigningKeys.class).publicKeysJson("2"));
                assertRuntimeBindings(context);
              });
      assertArrayEquals(before, Files.readAllBytes(state));
      var missingPrivate = new java.util.HashMap<>(properties);
      missingPrivate.remove(prefix + "private-files[management]");
      approvedRunner(approvedFirst, missingPrivate, operations)
          .run(context -> assertNotNull(context.getStartupFailure()));
      assertArrayEquals(before, Files.readAllBytes(state));
      var missingState = new java.util.HashMap<>(properties);
      missingState.remove("auriga.collab.network.key-state-file");
      approvedRunner(approvedFirst, missingState, operations)
          .run(context -> assertNotNull(context.getStartupFailure()));
      // Failed contexts must also release the approved loader's publisher lock.
      assertHealthyLoadKeepsState(payload, state, privateFiles);
    }
  }

  ApplicationContextRunner runtimeRunner(boolean approvedFirst, Map<String, Object> properties) {
    var dataSource = mock(DataSource.class);
    var manager = new TenantTransactionManager(dataSource);
    var client = mock(NetworkAuthorityClient.class);
    var locals = mock(NetworkLocalTargets.class);
    var factory = new StaticListableBeanFactory(Map.of("locals", locals));
    NetworkSourceGrpcAuthority source =
        ReflectionTestUtils.invokeMethod(
            new CollabNetworkConfiguration(),
            "networkSourceGrpcAuthority",
            client,
            manager,
            factory.getBeanProvider(NetworkLocalTargets.class));
    var order =
        approvedFirst
            ? new Class<?>[] {
              NetworkKeyOperationsConfiguration.class, CollabRuntimeConfiguration.class
            }
            : new Class<?>[] {
              CollabRuntimeConfiguration.class, NetworkKeyOperationsConfiguration.class
            };
    return new ApplicationContextRunner()
        .withUserConfiguration(order)
        .withPropertyValues("auriga.service.schema=collab")
        .withInitializer(
            context ->
                context
                    .getEnvironment()
                    .getPropertySources()
                    .addFirst(
                        new MapPropertySource("runtime-test", new java.util.HashMap<>(properties))))
        .withBean(DataSource.class, () -> dataSource)
        .withBean(TenantTransactionManager.class, () -> manager)
        .withBean(NetworkAuthorityClient.class, () -> client)
        .withBean(NetworkLocalTargets.class, () -> locals)
        .withBean(NetworkSourceGrpcAuthority.class, () -> source);
  }

  ApplicationContextRunner approvedRunner(
      boolean first, Map<String, Object> properties, NetworkKeyOperations operations) {
    // Replace only the PG publication port. Root pin checks and the approved loader stay real.
    return runtimeRunner(first, properties)
        .withInitializer(
            context ->
                context.addBeanFactoryPostProcessor(
                    factory ->
                        ((AbstractBeanDefinition) factory.getBeanDefinition("networkKeyOperations"))
                            .setInstanceSupplier(() -> operations)));
  }

  void assertRuntimeBindings(org.springframework.context.ApplicationContext context) {
    var keys = context.getBean(NetworkSigningKeys.class);
    var mailbox = context.getBean(NetworkMailboxService.class);
    var wire = context.getBean(NetworkMailboxWireService.class);
    var source = context.getBean(NetworkSourceGrpcAuthority.class);
    var client = context.getBean(NetworkAuthorityClient.class);
    assertSame(source, ReflectionTestUtils.getField(mailbox, "authority"));
    assertSame(
        client, ReflectionTestUtils.getField(ReflectionTestUtils.getField(mailbox, "iam"), "iam"));
    assertSame(
        context.getBean(NetworkLocalTargets.class), ReflectionTestUtils.getField(source, "locals"));
    assertSame(client, ReflectionTestUtils.getField(source, "client"));
    assertSame(mailbox, ReflectionTestUtils.getField(wire, "mailbox"));
    assertSame(source, ReflectionTestUtils.getField(wire, "source"));
    assertSame(
        keys,
        ReflectionTestUtils.getField(ReflectionTestUtils.getField(mailbox, "signer"), "keys"));
    assertSame(
        keys, ReflectionTestUtils.getField(ReflectionTestUtils.getField(wire, "signer"), "keys"));
    assertSame(
        wire,
        ReflectionTestUtils.getField(context.getBean(NetworkMailboxGrpcService.class), "service"));
    verifyNoInteractions(client, context.getBean(DataSource.class));
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
