package auriga.collab.network.keys;

import static auriga.collab.network.keys.NetworkKeyConfigurationTest.protectedFile;
import static org.junit.jupiter.api.Assertions.*;

import auriga.collab.app.NetworkJson;
import auriga.common.context.TenantContext;
import auriga.common.persistence.CommonMigrations;
import auriga.common.persistence.TenantTransactionManager;
import auriga.testsupport.PostgresExtension;
import java.nio.channels.FileChannel;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.StandardOpenOption;
import java.security.KeyPair;
import java.time.Clock;
import java.time.Instant;
import java.time.ZoneOffset;
import java.util.HashMap;
import java.util.Map;
import java.util.UUID;
import javax.sql.DataSource;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.condition.EnabledIfSystemProperty;
import org.junit.jupiter.api.extension.ExtendWith;
import org.junit.jupiter.api.io.TempDir;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.ValueSource;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.test.util.ReflectionTestUtils;
import org.springframework.transaction.support.TransactionTemplate;
import tools.jackson.databind.node.ArrayNode;
import tools.jackson.databind.node.ObjectNode;

@ExtendWith(PostgresExtension.class)
class NetworkKeyOperatorPostgresTest {
  static DataSource source;

  @TempDir(factory = NetworkKeyConfigurationTest.WorkspaceTempDirectory.class)
  Path directory;

  final KeyTestMaterial m = new KeyTestMaterial();
  final UUID tenant = UUID.randomUUID();
  final Path unused = Path.of("unused");

  @BeforeAll
  static void migrate() {
    var roles =
        PostgresExtension.serviceRoles(
            NetworkKeyOperatorPostgresTest.class, "operator_owner", "operator_app", "collab");
    CommonMigrations.migrate(roles.owner().dataSource(), "collab");
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas("collab")
                .defaultSchema("collab")
                .locations("classpath:db/collab")
                .load())
        .migrate();
    source = roles.runtime().dataSource();
  }

  Clock clock(Instant time) {
    return Clock.fixed(time, ZoneOffset.UTC);
  }

  NetworkKeySettings settings(String floor) throws Exception {
    Path root = directory.resolve("root.der");
    if (!Files.exists(root)) protectedFile(root, m.root.getPublic().getEncoded());
    return new NetworkKeySettings(
        tenant,
        "2",
        "root",
        root,
        RootKeyPublication.fingerprint(m.root.getPublic()),
        unused,
        unused,
        directory.resolve("state.json"),
        floor,
        Map.of());
  }

  NetworkKeyOperator operator(Instant time) throws Exception {
    return new NetworkKeyOperator(settings("1"), source, clock(time));
  }

  RootKeyPublication.Release initial(ObjectNode p) throws Exception {
    var c = clock(Instant.parse(p.path("issued_at").asString()));
    var release = m.release(p);
    new NetworkKeyOperations(
            new JdbcTemplate(source),
            new TenantTransactionManager(source),
            tenant,
            "2",
            m.verifier(c),
            "1",
            c)
        .publish(release);
    if (!Files.exists(settings("1").stateFile()))
      SigningKeyLoader.installState(p, settings("1").stateFile());
    NetworkKeyConfigurationTest.restrictOwner(settings("1").stateFile());
    return release;
  }

  Map<String, String> secrets(Map<String, KeyPair> pairs) throws Exception {
    Map<String, String> paths = new HashMap<>();
    for (var entry : pairs.entrySet()) {
      Path path = directory.resolve(entry.getKey() + ".der");
      if (!Files.exists(path)) protectedFile(path, entry.getValue().getPrivate().getEncoded());
      paths.put(entry.getKey(), path.toString());
    }
    return paths;
  }

  ObjectNode next(
      ObjectNode previous,
      int version,
      Instant issued,
      Map<String, KeyPair> added,
      boolean retire) {
    var p =
        previous
            .deepCopy()
            .put("keyset_version", "" + version)
            .put("issued_at", issued.toString())
            .put("valid_until", issued.plusSeconds(86400).toString());
    if (retire) for (var key : p.path("keys")) ((ObjectNode) key).put("state", "retired");
    for (var entry : added.entrySet()) {
      String purpose =
          switch (entry.getKey()) {
            case "new-business" -> "business_delivery";
            case "new-protocol" -> "protocol_response";
            default -> "management_state";
          };
      ((ArrayNode) p.path("keys"))
          .add(m.key(entry.getKey(), purpose, entry.getValue()).put("state", "next"));
    }
    return p;
  }

  ObjectNode activate(ObjectNode previous, int version, Instant issued) {
    var p =
        previous
            .deepCopy()
            .put("keyset_version", "" + version)
            .put("issued_at", issued.toString())
            .put("valid_until", issued.plusSeconds(86400).toString());
    var replacing = new java.util.HashSet<String>();
    for (var key : p.path("keys"))
      if ("next".equals(key.path("state").asString()))
        replacing.add(key.path("purpose").asString());
    for (var key : p.path("keys")) {
      var k = (ObjectNode) key;
      if ("active".equals(k.path("state").asString())
          && replacing.contains(k.path("purpose").asString())) k.put("state", "retired");
      else if ("next".equals(k.path("state").asString())) k.put("state", "active");
    }
    return p;
  }

  @Test
  void expiredOldRecoveryUsesRealPublicationHistoryCeilingAndLoaderWithoutChangingOldKeys()
      throws Exception {
    var old = m.payload(1, KeyTestMaterial.NOW);
    for (var key : old.path("keys"))
      ((ObjectNode) key).put("not_after", KeyTestMaterial.NOW.plusSeconds(30).toString());
    var first = initial(old);
    Map<String, KeyPair> pairs =
        Map.of(
            "new-business",
            KeyTestMaterial.pair(),
            "new-protocol",
            KeyTestMaterial.pair(),
            "new-management",
            KeyTestMaterial.pair());
    Instant issued = KeyTestMaterial.NOW.plusSeconds(7200);
    Instant pgFirst = issued.plusMillis(123);
    var pre = next(old, 2, issued, pairs, true);
    var published = m.release(pre);
    var stateBefore = Files.readAllBytes(settings("1").stateFile());
    var status = operator(pgFirst).publish(first, published);
    for (var kid : pairs.keySet())
      assertEquals(pgFirst, Instant.parse(status.path("firstPublished").path(kid).asString()));
    assertArrayEquals(
        stateBefore,
        Files.readAllBytes(settings("1").stateFile()),
        "publication does not forge a local state install");
    var early = activate(pre, 3, issued.plusSeconds(86400));
    assertThrows(
        RuntimeException.class,
        () -> operator(issued.plusSeconds(86400)).publish(published, m.release(early)));
    var active = activate(pre, 3, issued.plusSeconds(86401));
    var current = m.release(active);
    operator(issued.plusSeconds(86401)).publish(published, current);
    operator(issued.plusSeconds(86401)).install(current, settings("1").stateFile(), secrets(pairs));
    for (int i = 0; i < 3; i++) {
      for (String field :
          new String[] {"kid", "alg", "purpose", "public_key_spki", "not_before", "not_after"})
        assertEquals(old.path("keys").get(i).path(field), active.path("keys").get(i).path(field));
      assertEquals("retired", active.path("keys").get(i).path("state").asString());
    }
    try (var keys =
        SigningKeyLoader.load(
            active,
            settings("1").stateFile(),
            secrets(pairs),
            clock(issued.plusSeconds(86401)),
            (registry, signature) ->
                new NetworkKeyOperations(
                        new JdbcTemplate(source),
                        new TenantTransactionManager(source),
                        tenant,
                        "2",
                        m.verifier(clock(issued.plusSeconds(86401))),
                        "1",
                        clock(issued.plusSeconds(86401)))
                    .signApproved(registry.publicKeysJson("2"), signature))) {
      assertEquals(NetworkJson.canonical(active), keys.publicKeysJson("2"));
      // Exercise the actual structured envelope signer, rather than a test-made signature.
      Class<?> signerType = Class.forName("auriga.collab.app.NetworkEnvelopeSigner");
      var constructor = signerType.getDeclaredConstructors()[0];
      constructor.setAccessible(true);
      Object signer = constructor.newInstance(keys, clock(issued.plusSeconds(86401)));
      Class<?> contextType = Class.forName("auriga.collab.app.NetworkEnvelopeSigner$Context");
      var contextConstructor = contextType.getDeclaredConstructors()[0];
      contextConstructor.setAccessible(true);
      Object context =
          contextConstructor.newInstance("2", "3", "incarnation", 1L, "nonce", "request");
      ObjectNode signed =
          ReflectionTestUtils.invokeMethod(
              signer,
              "sign",
              KeyTestMaterial.JSON.createObjectNode().put("type", "head_result"),
              "protocol_response",
              context,
              issued.plusSeconds(86460));
      assertNotNull(signed);
      assertEquals("new-protocol", signed.path("server_auth").path("kid").asString());
      byte[] input = ReflectionTestUtils.invokeMethod(signerType, "signingInput", signed);
      var verify = java.security.Signature.getInstance("Ed25519");
      verify.initVerify(pairs.get("new-protocol").getPublic());
      verify.update(input);
      assertTrue(
          verify.verify(
              java.util.Base64.getUrlDecoder()
                  .decode(signed.path("server_auth").path("signature").asString())));
    }
  }

  @ParameterizedTest
  @ValueSource(ints = {359, 360})
  void oldActiveOverlapCannotBeFakedByRetiringInTheActivationVersion(int overlap) throws Exception {
    var old = m.payload(1, KeyTestMaterial.NOW);
    for (var key : old.path("keys"))
      ((ObjectNode) key)
          .put("not_after", KeyTestMaterial.NOW.plusSeconds(86400 + overlap).toString());
    var first = initial(old);
    var pairs = Map.of("new-business", KeyTestMaterial.pair());
    var pre = next(old, 2, KeyTestMaterial.NOW, pairs, false);
    operator(KeyTestMaterial.NOW).publish(first, m.release(pre));
    var active = activate(pre, 3, KeyTestMaterial.NOW.plusSeconds(86400));
    if (overlap == 359)
      assertThrows(
          RuntimeException.class,
          () ->
              operator(KeyTestMaterial.NOW.plusSeconds(86400))
                  .publish(m.release(pre), m.release(active)));
    else
      operator(KeyTestMaterial.NOW.plusSeconds(86400)).publish(m.release(pre), m.release(active));
    assertEquals(
        overlap == 359 ? "2" : "3",
        operator(KeyTestMaterial.NOW.plusSeconds(86400))
            .status(m.release(overlap == 359 ? pre : active))
            .path("version")
            .asString());
  }

  @Test
  void operatorTrustRejectsFutureExpiredUnknownFieldsAndCandidateChosenRealm() throws Exception {
    var op = operator(KeyTestMaterial.NOW);
    var valid = m.release(m.payload(1, KeyTestMaterial.NOW));
    assertThrows(
        RuntimeException.class,
        () -> op.verify(m.release(m.payload(1, KeyTestMaterial.NOW.plusSeconds(1))), false));
    assertThrows(
        RuntimeException.class,
        () -> op.verify(m.release(m.payload(1, KeyTestMaterial.NOW.minusSeconds(86400))), false));
    assertThrows(
        RuntimeException.class,
        () -> op.verify(m.release(m.payload(1, KeyTestMaterial.NOW).put("realm", "3")), false));
    var unknown = (ObjectNode) NetworkJson.parse(valid.signedKeyset());
    unknown.put("unrecognized", true);
    assertThrows(
        RuntimeException.class,
        () ->
            op.verify(
                new RootKeyPublication.Release(
                    NetworkJson.canonical(unknown), valid.announcement()),
                false));
    var changedRoot = (ObjectNode) NetworkJson.parse(valid.signedKeyset());
    ((ObjectNode) changedRoot.path("root_auth")).put("kid", "other");
    assertThrows(
        RuntimeException.class,
        () ->
            op.verify(
                new RootKeyPublication.Release(
                    NetworkJson.canonical(changedRoot), valid.announcement()),
                false));
    var tooLong =
        m.payload(1, KeyTestMaterial.NOW)
            .put("valid_until", KeyTestMaterial.NOW.plusSeconds(86401).toString());
    assertThrows(RuntimeException.class, () -> m.release(tooLong));
  }

  @Test
  void expiredActiveCannotBeRetiredInTheSameVersionAsNextActivation() throws Exception {
    var old = m.payload(1, KeyTestMaterial.NOW);
    for (var key : old.path("keys"))
      ((ObjectNode) key).put("not_after", KeyTestMaterial.NOW.plusSeconds(30).toString());
    var first = initial(old);
    Instant issued = KeyTestMaterial.NOW.plusSeconds(7200);
    var pre = next(old, 2, issued, Map.of("new-business", KeyTestMaterial.pair()), false);
    operator(issued).publish(first, m.release(pre));
    Instant mature = issued.plusSeconds(86400);
    var active = activate(pre, 3, mature);
    assertThrows(
        RuntimeException.class, () -> operator(mature).publish(m.release(pre), m.release(active)));
    var retired = next(pre, 3, mature, Map.of(), true);
    for (var key : retired.path("keys"))
      if (key.path("kid").asString().startsWith("new-")) ((ObjectNode) key).put("state", "next");
    operator(mature).publish(m.release(pre), m.release(retired));
    var recovered = activate(retired, 4, mature);
    operator(mature).publish(m.release(retired), m.release(recovered));
    assertEquals("4", operator(mature).status(m.release(recovered)).path("version").asString());
  }

  @Test
  void statusComparesArchivedFirstPublicationCheckpointToLiveDatabase() throws Exception {
    var first = initial(m.payload(1, KeyTestMaterial.NOW));
    var status = operator(KeyTestMaterial.NOW).status(first);
    var checkpoint = status.path("firstPublished").deepCopy();
    ((ObjectNode) checkpoint).put("business", KeyTestMaterial.NOW.minusSeconds(1).toString());
    var op = new NetworkKeyOperator(settings("1"), source, clock(KeyTestMaterial.NOW), checkpoint);
    assertThrows(RuntimeException.class, () -> op.status(first));
  }

  @Test
  void operatorRechecksTheSealedStateHashInsideThePublicationTransaction() throws Exception {
    var first = initial(m.payload(1, KeyTestMaterial.NOW));
    byte[] before = Files.readAllBytes(settings("1").stateFile());
    String hash =
        java.util.HexFormat.of()
            .formatHex(java.security.MessageDigest.getInstance("SHA-256").digest(before));
    var op = new NetworkKeyOperator(settings("1"), source, clock(KeyTestMaterial.NOW), null, hash);
    op.status(first);
    var corrupted = (ObjectNode) NetworkJson.parse(before);
    ((ObjectNode) corrupted.path("keys").get(0)).put("purpose", "protocol_response");
    protectedFile(
        settings("1").stateFile(),
        NetworkJson.canonical(corrupted).getBytes(java.nio.charset.StandardCharsets.UTF_8));
    assertThrows(
        RuntimeException.class,
        () -> op.publish(first, m.release(m.payload(2, KeyTestMaterial.NOW))));
    protectedFile(settings("1").stateFile(), before);
    assertEquals(
        "1",
        op.status(first).path("version").asString(),
        "state corruption cannot be repaired by a PG publication");
  }

  @Test
  void operatorRejectsSuperuserAndInheritedTableOwnerPrivileges() throws Exception {
    var first = initial(m.payload(1, KeyTestMaterial.NOW));
    var superSource = PostgresExtension.database(NetworkKeyOperatorPostgresTest.class).dataSource();
    assertThrows(
        RuntimeException.class,
        () ->
            new NetworkKeyOperator(settings("1"), superSource, clock(KeyTestMaterial.NOW))
                .status(first));
    var admin = new JdbcTemplate(superSource);
    admin.execute("GRANT operator_owner TO operator_app");
    try {
      assertThrows(RuntimeException.class, () -> operator(KeyTestMaterial.NOW).status(first));
    } finally {
      admin.execute("REVOKE operator_owner FROM operator_app");
    }
    assertEquals("1", operator(KeyTestMaterial.NOW).status(first).path("version").asString());
  }

  @Test
  @EnabledIfSystemProperty(named = "auriga.operator.jar", matches = ".+")
  void packagedCliReadsActualPgRollsBackPreflightAndInstallsWithOriginalStateHash()
      throws Exception {
    var first = initial(m.payload(1, KeyTestMaterial.NOW));
    var candidate =
        m.release(m.payload(2, Instant.now().truncatedTo(java.time.temporal.ChronoUnit.SECONDS)));
    Path firstSigned =
        protectedFile(
            directory.resolve("first-signed.json"),
            first.signedKeyset().getBytes(java.nio.charset.StandardCharsets.UTF_8));
    Path firstAnn =
        protectedFile(
            directory.resolve("first-announcement.json"),
            first.announcement().getBytes(java.nio.charset.StandardCharsets.UTF_8));
    Path signed =
        protectedFile(
            directory.resolve("signed.json"),
            candidate.signedKeyset().getBytes(java.nio.charset.StandardCharsets.UTF_8));
    Path ann =
        protectedFile(
            directory.resolve("announcement.json"),
            candidate.announcement().getBytes(java.nio.charset.StandardCharsets.UTF_8));
    var s = settings("1");
    var data = (org.postgresql.ds.PGSimpleDataSource) source;
    String prefix = "auriga.collab.network.key-operations.";
    String config =
        "spring.datasource.url="
            + data.getURL()
            + "\nspring.datasource.username=operator_app\nspring.datasource.password=\n"
            + prefix
            + "tenant-id="
            + tenant
            + "\n"
            + prefix
            + "realm=2\n"
            + prefix
            + "root-kid=root\n"
            + prefix
            + "root-fingerprint="
            + s.rootFingerprint()
            + "\n"
            + prefix
            + "minimum-version=1\n";
    Path configFile =
        protectedFile(
            directory.resolve("operator.properties"),
            config.getBytes(java.nio.charset.StandardCharsets.UTF_8));
    byte[] original = Files.readAllBytes(s.stateFile());
    String stateHash =
        java.util.HexFormat.of()
            .formatHex(java.security.MessageDigest.getInstance("SHA-256").digest(original));
    var plan =
        KeyTestMaterial.JSON
            .createObjectNode()
            .put("configFile", configFile.toString())
            .put("rootFile", s.rootPublicFile().toString())
            .put("expectedSignedFile", firstSigned.toString())
            .put("expectedAnnouncementFile", firstAnn.toString())
            .put("signedFile", signed.toString())
            .put("announcementFile", ann.toString())
            .put("stateFile", s.stateFile().toString())
            .put("expectedHistoryFile", "")
            .put("expectedStateSha256", stateHash);
    plan.set(
        "privateFiles",
        KeyTestMaterial.JSON.valueToTree(
            secrets(
                Map.of(
                    "business", m.business, "protocol", m.protocol, "management", m.management))));
    Path planFile =
        protectedFile(
            directory.resolve("packaged-plan.json"),
            NetworkJson.canonical(plan).getBytes(java.nio.charset.StandardCharsets.UTF_8));
    Path jar = Path.of(System.getProperty("auriga.operator.jar")).toAbsolutePath();
    assertEquals(
        0, NetworkKeyOperatorLauncherTest.launch(directory, jar, planFile, "pg-status", "status"));
    assertEquals(
        0,
        NetworkKeyOperatorLauncherTest.launch(
            directory, jar, planFile, "pg-preflight", "preflight"));
    assertEquals("1", operator(KeyTestMaterial.NOW).status(first).path("version").asString());
    assertEquals(
        0,
        NetworkKeyOperatorLauncherTest.launch(directory, jar, planFile, "pg-publish", "publish"));
    assertArrayEquals(original, Files.readAllBytes(s.stateFile()));
    assertEquals("2", operator(KeyTestMaterial.NOW).status(candidate).path("version").asString());
    assertEquals(
        0,
        NetworkKeyOperatorLauncherTest.launch(directory, jar, planFile, "pg-install", "install"));
    assertEquals(
        "2", NetworkJson.parse(Files.readAllBytes(s.stateFile())).path("version").asString());
    plan.put("expectedSignedFile", signed.toString())
        .put("expectedAnnouncementFile", ann.toString());
    protectedFile(
        planFile, NetworkJson.canonical(plan).getBytes(java.nio.charset.StandardCharsets.UTF_8));
    assertEquals(
        1,
        NetworkKeyOperatorLauncherTest.launch(
            directory, jar, planFile, "old-state-checkpoint", "status"));
    plan.put(
        "expectedStateSha256",
        java.util.HexFormat.of()
            .formatHex(
                java.security.MessageDigest.getInstance("SHA-256")
                    .digest(Files.readAllBytes(s.stateFile()))));
    protectedFile(
        planFile, NetworkJson.canonical(plan).getBytes(java.nio.charset.StandardCharsets.UTF_8));
    assertEquals(
        0,
        NetworkKeyOperatorLauncherTest.launch(
            directory, jar, planFile, "installed-pg-status", "status"));
  }

  @Test
  void normalRotationInstallsBothActiveAndNextAndRejectsStaleSigner() throws Exception {
    var old = m.payload(1, KeyTestMaterial.NOW);
    var first = initial(old);
    var all =
        new HashMap<>(
            Map.of("business", m.business, "protocol", m.protocol, "management", m.management));
    all.put("new-business", KeyTestMaterial.pair());
    var pre =
        next(old, 2, KeyTestMaterial.NOW, Map.of("new-business", all.get("new-business")), false);
    try (var stale =
        SigningKeyLoader.load(
            old,
            settings("1").stateFile(),
            secrets(
                Map.of("business", m.business, "protocol", m.protocol, "management", m.management)),
            clock(KeyTestMaterial.NOW),
            (registry, work) -> work.get())) {
      assertThrows(
          RuntimeException.class,
          () -> operator(KeyTestMaterial.NOW).publish(first, m.release(pre)));
      assertEquals(
          "1",
          operator(KeyTestMaterial.NOW).status(first).path("version").asString(),
          "busy original signer lock prevents operator publication before PG writes");
      new NetworkKeyOperations(
              new JdbcTemplate(source),
              new TenantTransactionManager(source),
              tenant,
              "2",
              m.verifier(clock(KeyTestMaterial.NOW)),
              "1",
              clock(KeyTestMaterial.NOW))
          .publish(m.release(pre));
      assertThrows(
          RuntimeException.class,
          () ->
              new NetworkKeyOperations(
                      new JdbcTemplate(source),
                      new TenantTransactionManager(source),
                      tenant,
                      "2",
                      m.verifier(clock(KeyTestMaterial.NOW)),
                      "1",
                      clock(KeyTestMaterial.NOW))
                  .assertApproved(stale.publicKeysJson("2")));
      assertThrows(
          RuntimeException.class,
          () ->
              operator(KeyTestMaterial.NOW)
                  .install(m.release(pre), settings("1").stateFile(), secrets(all)),
          "original state lock must block install");
    }
    operator(KeyTestMaterial.NOW).install(m.release(pre), settings("1").stateFile(), secrets(all));
    var active = activate(pre, 3, KeyTestMaterial.NOW.plusSeconds(86400));
    operator(KeyTestMaterial.NOW.plusSeconds(86400)).publish(m.release(pre), m.release(active));
    all.remove("business");
    operator(KeyTestMaterial.NOW.plusSeconds(86400))
        .install(m.release(active), settings("1").stateFile(), secrets(all));
  }

  @Test
  void missingDatabaseRowWrongTenantAndLocalHigherVersionCannotBecomeFirstInstall()
      throws Exception {
    var p = m.payload(1, KeyTestMaterial.NOW);
    SigningKeyLoader.installState(p, settings("1").stateFile());
    assertThrows(
        RuntimeException.class,
        () ->
            operator(KeyTestMaterial.NOW)
                .publish(m.release(p), m.release(m.payload(2, KeyTestMaterial.NOW))));
    var first = initial(p);
    var s = settings("1");
    var foreign =
        new NetworkKeySettings(
            UUID.randomUUID(),
            s.realm(),
            s.rootKid(),
            s.rootPublicFile(),
            s.rootFingerprint(),
            unused,
            unused,
            s.stateFile(),
            "1",
            Map.of());
    assertThrows(
        RuntimeException.class,
        () -> new NetworkKeyOperator(foreign, source, clock(KeyTestMaterial.NOW)).status(first));
    assertThrows(
        RuntimeException.class,
        () ->
            new NetworkKeyOperator(settings("2"), source, clock(KeyTestMaterial.NOW))
                .status(first));
  }

  @Test
  void alteredHistorySameVersionRollbackAndWrongPairAreRejectedWithoutStateWrites()
      throws Exception {
    var p = m.payload(1, KeyTestMaterial.NOW);
    var first = initial(p);
    var op = operator(KeyTestMaterial.NOW);
    var changed = p.deepCopy().put("keyset_version", "2");
    ((ObjectNode) changed.path("keys").get(0))
        .put("not_after", KeyTestMaterial.NOW.plusSeconds(999999).toString());
    assertThrows(RuntimeException.class, () -> op.publish(first, m.release(changed)));
    var omitted = p.deepCopy().put("keyset_version", "2");
    ((ArrayNode) omitted.path("keys")).remove(0);
    assertThrows(RuntimeException.class, () -> op.publish(first, m.release(omitted)));
    var next = m.release(p.deepCopy().put("keyset_version", "2"));
    op.preflight(first, next);
    assertEquals(
        "1",
        op.status(first).path("version").asString(),
        "preflight runs the production transition in a rolled back transaction");
    assertThrows(
        RuntimeException.class,
        () ->
            op.verify(
                new RootKeyPublication.Release(first.signedKeyset(), next.announcement()), false));
    op.publish(first, next);
    assertThrows(
        RuntimeException.class,
        () -> op.publish(first, first),
        "stale expected PG checkpoint rejected");
    assertThrows(RuntimeException.class, () -> op.publish(next, first), "rollback rejected");
    assertThrows(
        RuntimeException.class,
        () ->
            op.publish(
                next,
                m.release(
                    p.deepCopy()
                        .put("keyset_version", "2")
                        .put("valid_until", KeyTestMaterial.NOW.plusSeconds(1234).toString()))));
    assertEquals("2", op.status(next).path("version").asString());
  }

  @Test
  void malformedPgHistoryAndLowerPgThanRuntimeAreRejected() throws Exception {
    var p = m.payload(1, KeyTestMaterial.NOW);
    var first = initial(p);
    byte[] original = Files.readAllBytes(settings("1").stateFile());
    SigningKeyLoader.installState(m.payload(2, KeyTestMaterial.NOW), settings("1").stateFile());
    assertThrows(RuntimeException.class, () -> operator(KeyTestMaterial.NOW).status(first));
    protectedFile(settings("1").stateFile(), original);
    TenantContext.call(
        tenant,
        () ->
            new TransactionTemplate(new TenantTransactionManager(source))
                .execute(
                    tx -> {
                      new JdbcTemplate(source)
                          .update(
                              "UPDATE collab.network_key_publication SET first_published = '{}'::jsonb WHERE tenant_id = ?",
                              tenant);
                      return null;
                    }));
    assertThrows(RuntimeException.class, () -> operator(KeyTestMaterial.NOW).status(first));
  }

  @Test
  void wrongPrivateAndLiveOriginalLockNeverReplaceState() throws Exception {
    var p = m.payload(1, KeyTestMaterial.NOW);
    var first = initial(p);
    var next = m.release(p.deepCopy().put("keyset_version", "2"));
    operator(KeyTestMaterial.NOW).publish(first, next);
    var original = Files.readAllBytes(settings("1").stateFile());
    var wrong =
        secrets(Map.of("business", m.protocol, "protocol", m.protocol, "management", m.management));
    assertThrows(
        RuntimeException.class,
        () -> operator(KeyTestMaterial.NOW).install(next, settings("1").stateFile(), wrong));
    assertArrayEquals(original, Files.readAllBytes(settings("1").stateFile()));
    var right =
        secrets(
            Map.of(
                "business-right",
                m.business,
                "protocol-right",
                m.protocol,
                "management-right",
                m.management));
    var paths =
        Map.of(
            "business",
            right.get("business-right"),
            "protocol",
            right.get("protocol-right"),
            "management",
            right.get("management-right"));
    Path lockPath = settings("1").stateFile().resolveSibling("state.json.lock");
    try (var channel = FileChannel.open(lockPath, StandardOpenOption.WRITE);
        var lock = channel.lock()) {
      assertThrows(
          RuntimeException.class,
          () -> operator(KeyTestMaterial.NOW).install(next, settings("1").stateFile(), paths));
    }
    assertArrayEquals(original, Files.readAllBytes(settings("1").stateFile()));
  }
}
