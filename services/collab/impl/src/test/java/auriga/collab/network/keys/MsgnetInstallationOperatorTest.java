package auriga.collab.network.keys;

import static auriga.collab.network.keys.MsgnetInstallationFiles.*;
import static org.junit.jupiter.api.Assertions.*;

import auriga.collab.app.NetworkJson;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.attribute.PosixFileAttributeView;
import java.nio.file.attribute.PosixFilePermissions;
import java.time.Clock;
import java.time.ZoneOffset;
import java.util.*;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

class MsgnetInstallationOperatorTest {
  @TempDir Path directory;
  final Clock clock = Clock.fixed(KeyTestMaterial.NOW, ZoneOffset.UTC);
  int sequence;

  final class Fixture {
    final KeyTestMaterial material = new KeyTestMaterial();
    final String tenant = UUID.randomUUID().toString();
    final ObjectNode plan;
    final Path rootPrivate;
    final Path prepared;
    Path signed;

    Fixture() throws Exception {
      ownerOnly(directory);
      rootPrivate = write(directory, "offline-root.der", material.root.getPrivate().getEncoded());
      Path root = write(directory, "root.der", material.root.getPublic().getEncoded());
      var pins =
          MsgnetRenewal.object()
              .put("schema_version", 2)
              .put("tenant_id", tenant)
              .put("realm", "2");
      var seal = java.security.KeyPairGenerator.getInstance("X25519").generateKeyPair();
      pins.putArray("pins")
          .addObject()
          .put("target_id", "network-target")
          .put("identity_id", "network-identity")
          .put("component", "network")
          .put("gateway_id", "gateway")
          .put("subject", "CN=projector")
          .put("signing_spki", encode(material.protocol.getPublic().getEncoded()))
          .put("sealing_spki", encode(seal.getPublic().getEncoded()));
      Path identities = writeJson(directory, "trusted-identities.json", pins);
      String fingerprint = RootKeyPublication.fingerprint(material.root.getPublic());
      var source =
          MsgnetRenewal.object()
              .put("version", 1)
              .put("mode", "fresh")
              .put("tenantId", tenant)
              .put("realm", "2")
              .put("rootKid", "root")
              .put("rootFingerprint", fingerprint)
              .put("identitiesSha256", reference(identities).path("sha256").asString())
              .put("approvedBy", "synthetic-test-operator")
              .put("approval", "ephemeral fixture only");
      plan =
          MsgnetRenewal.object()
              .put("version", 1)
              .put("mode", "fresh")
              .put("tenantId", tenant)
              .put("realm", "2")
              .put("rootKid", "root")
              .put("rootFingerprint", fingerprint)
              .put("authorityId", "runtime-authority")
              .put("authorityRevision", "1")
              .put("runtimeKid", "runtime-key")
              .put("kekId", "kek-v1")
              .put("controlRevision", "1")
              .put("minimumGeneration", "1")
              .put("minimumKeysetVersion", "100")
              .put("notBefore", clock.instant().toString())
              .put("projectorIdentityId", "network-identity")
              .put("outputDirectory", directory.resolve("prepared").toString());
      plan.set("rootPublic", reference(root));
      plan.set("identities", reference(identities));
      plan.set("approvedSource", reference(writeJson(directory, "source-approval.json", source)));
      plan.set(
          "limits",
          MsgnetRenewal.object()
              .put("keyset_seconds", 86400)
              .put("business_key_seconds", 691200)
              .put("restart_key_seconds", 604800)
              .put("next_maturity_seconds", 86400)
              .put("overlap_seconds", 360)
              .put("archive_grace_seconds", 360));
      var transport =
          plan.putArray("transportScope")
              .addObject()
              .put("component", "network")
              .put("identity_id", "network-identity");
      transport.putArray("sans").add("network");
      transport.putArray("ekus").add("serverAuth").add("clientAuth");
      prepared = directory.resolve("prepared/prepared.json");
    }

    void prepare() throws Exception {
      MsgnetInstallationOperator.prepare(writePlan(plan), clock);
    }

    void sign() throws Exception {
      var signing =
          MsgnetRenewal.object()
              .put("version", 1)
              .put("rootPrivateFile", rootPrivate.toString())
              .put("outputDirectory", directory.resolve("signed").toString());
      signing.set("request", reference(directory.resolve("prepared/request.json")));
      MsgnetInstallationOperator.sign(writePlan(signing), clock);
      signed = directory.resolve("signed");
    }

    ObjectNode finalPlan() throws Exception {
      var result =
          MsgnetRenewal.object()
              .put("version", 1)
              .put("outputDirectory", directory.resolve("installation").toString());
      result.set("prepared", reference(prepared));
      result.set("authority", reference(signed.resolve("authority.json")));
      result.set("control", reference(signed.resolve("control.json")));
      return result;
    }
  }

  Path writePlan(JsonNode value) throws Exception {
    return writeJson(directory, "plan-" + sequence++ + ".json", value);
  }

  @Test
  void freshRoundtripUsesExistingVerifierConfigPinsAndKekAndExportsOnlyFixedSpec()
      throws Exception {
    var f = new Fixture();
    f.prepare();
    f.sign();
    var receipt = MsgnetInstallationOperator.finalizeInstallation(writePlan(f.finalPlan()), clock);
    assertEquals("configured", receipt.path("state").asString());
    Path output = directory.resolve("installation");
    var spec = json(output.resolve("installation.json"), true);
    assertEquals(
        Set.of(
            "version",
            "tenantId",
            "realm",
            "rootKid",
            "rootFingerprint",
            "kekId",
            "projectorIdentityId",
            "authority",
            "control",
            "runtimePrivate",
            "kek",
            "identities"),
        new HashSet<>(spec.propertyNames()));
    assertEquals(
        hash(Files.readAllBytes(output.resolve("installation.json"))),
        receipt.path("sha256").asString());
    for (String name : List.of("authority", "control", "runtimePrivate", "kek", "identities")) {
      String leaf = spec.path(name).path("path").asString();
      assertEquals(Path.of(leaf).getFileName().toString(), leaf);
      assertEquals(
          hash(Files.readAllBytes(output.resolve(leaf))),
          spec.path(name).path("sha256").asString());
      ProtectedKeyFiles.check(output.resolve(leaf), true);
    }
    var authority = json(output.resolve("authority.json"), false);
    var control = json(output.resolve("control.json"), false);
    new MsgnetV2Verifier(f.tenant, "2", "root", f.material.root.getPublic())
        .verifyAuthority(authority, control, clock.instant());
    MsgnetPolicy.authorizedConfig(MsgnetInstallationOperator.config(spec), authority);
    assertEquals(
        1,
        MsgnetControlLedger.pins(
                json(output.resolve("identities.json"), false), UUID.fromString(f.tenant), "2")
            .size());
    byte[] runtime = Files.readAllBytes(output.resolve("runtime-private.der"));
    MsgnetPrivateKeyStore.match(
        authority.path("payload").path("public_key_spki").asString(), runtime, "Ed25519");
    try (var kek =
        new MsgnetKeyEncryption(
            "kek-v1", Map.of("kek-v1", Files.readAllBytes(output.resolve("kek.bin"))))) {
      var aad = MsgnetKeyEncryptionTest.aad();
      assertArrayEquals(runtime, kek.decrypt(kek.encrypt(aad, runtime), aad));
    }
    assertFalse(Files.exists(output.resolve("offline-root.der")));
    assertFalse(
        NetworkJson.canonical(spec).contains(encode(f.material.root.getPrivate().getEncoded())));
    assertFalse(
        Files.readString(directory.resolve("prepared/request.json")).contains("approvedBy"));
    if (Files.getFileAttributeView(output, PosixFileAttributeView.class) != null) {
      assertEquals(
          PosixFilePermissions.fromString("rwx------"), Files.getPosixFilePermissions(output));
      assertEquals(
          PosixFilePermissions.fromString("rw-------"),
          Files.getPosixFilePermissions(output.resolve("runtime-private.der")));
    }
  }

  @Test
  void prepareRejectsSourceScopeRootUnknownFieldsAndOldV1ModeBeforeOutput() throws Exception {
    var f = new Fixture();
    for (var mutation :
        List.of(
            Map.entry("tenantId", UUID.randomUUID().toString()),
            Map.entry("realm", "3"),
            Map.entry("rootFingerprint", "sha256:" + "0".repeat(64)),
            Map.entry("mode", "migration"),
            Map.entry("unexpected", "x"))) {
      var plan = f.plan.deepCopy().put(mutation.getKey(), mutation.getValue());
      assertThrows(
          Exception.class, () -> MsgnetInstallationOperator.prepare(writePlan(plan), clock));
      assertFalse(Files.exists(directory.resolve("prepared")));
    }
    var wrongType = f.plan.deepCopy().put("realm", 2);
    assertThrows(
        Exception.class, () -> MsgnetInstallationOperator.prepare(writePlan(wrongType), clock));
  }

  @Test
  void duplicatePlanKeysAndUnnormalizedOrRelativeReferencesReject() throws Exception {
    var f = new Fixture();
    String text = NetworkJson.canonical(f.plan);
    Path duplicate =
        write(
            directory,
            "duplicate.json",
            ("{\"version\":1," + text.substring(1))
                .getBytes(java.nio.charset.StandardCharsets.UTF_8));
    assertThrows(Exception.class, () -> MsgnetInstallationOperator.prepare(duplicate, clock));
    for (String path : List.of("root.der", directory.resolve("child/../root.der").toString())) {
      var changed = f.plan.deepCopy();
      ((ObjectNode) changed.path("rootPublic")).put("path", path);
      assertThrows(
          Exception.class, () -> MsgnetInstallationOperator.prepare(writePlan(changed), clock));
    }
  }

  @Test
  void pinsAlgorithmsDuplicatesAndTransportOutsideEnrolledIdentityReject() throws Exception {
    var f = new Fixture();
    var original = f.plan.path("identities").deepCopy();
    var pins = (ObjectNode) referencedJson(f.plan.path("identities"), false);
    ((tools.jackson.databind.node.ArrayNode) pins.path("pins"))
        .add(pins.path("pins").get(0).deepCopy());
    Path changed = writePlan(pins);
    f.plan.set("identities", reference(changed));
    assertThrows(
        Exception.class, () -> MsgnetInstallationOperator.prepare(writePlan(f.plan), clock));
    var g = f.plan.deepCopy();
    g.set("identities", original);
    ((ObjectNode) g.path("transportScope").get(0)).put("identity_id", "not-enrolled");
    assertThrows(Exception.class, () -> MsgnetInstallationOperator.prepare(writePlan(g), clock));
    var swapped = (ObjectNode) referencedJson(original, false);
    ((ObjectNode) swapped.path("pins").get(0))
        .put("signing_spki", swapped.path("pins").get(0).path("sealing_spki").asString());
    var swappedPlan = f.plan.deepCopy();
    swappedPlan.set("identities", reference(writePlan(swapped)));
    assertThrows(
        Exception.class, () -> MsgnetInstallationOperator.prepare(writePlan(swappedPlan), clock));
  }

  @Test
  void optionalNodePinsRemainSeparateAndAreIncludedWithTheirOwnExactFileHash() throws Exception {
    var f = new Fixture();
    var nodes =
        MsgnetRenewal.object()
            .put("schema_version", 2)
            .put("tenant_id", f.tenant)
            .put("realm", "2");
    nodes
        .putArray("pins")
        .addObject()
        .put("target_id", "node-target")
        .put("identity_id", "node-identity")
        .put("node_id", "node-id")
        .put("identity_revision", "1")
        .put("signing_spki", encode(KeyTestMaterial.pair().getPublic().getEncoded()));
    Path nodeFile = writePlan(nodes);
    f.plan.set("nodeControlPins", reference(nodeFile));
    var source = (ObjectNode) referencedJson(f.plan.path("approvedSource"), true);
    source.put("nodeControlPinsSha256", reference(nodeFile).path("sha256").asString());
    f.plan.set("approvedSource", reference(writePlan(source)));
    f.prepare();
    f.sign();
    MsgnetInstallationOperator.finalizeInstallation(writePlan(f.finalPlan()), clock);
    var spec = json(directory.resolve("installation/installation.json"), true);
    assertEquals("node-control-pins.json", spec.path("nodeControlPins").path("path").asString());
    assertEquals(
        hash(Files.readAllBytes(directory.resolve("installation/node-control-pins.json"))),
        spec.path("nodeControlPins").path("sha256").asString());
    var crossed = nodes.deepCopy();
    ((ObjectNode) crossed.path("pins").get(0)).put("identity_id", "network-identity");
    var changed =
        f.plan.deepCopy().put("outputDirectory", directory.resolve("other-output").toString());
    changed.set("nodeControlPins", reference(writePlan(crossed)));
    assertThrows(
        Exception.class, () -> MsgnetInstallationOperator.prepare(writePlan(changed), clock));
  }

  ObjectNode migrationInput(Fixture f) throws Exception {
    var release = f.material.release(f.material.payload(51, clock.instant()));
    var signed = NetworkJson.parse(release.signedKeyset());
    var announcement = NetworkJson.parse(release.announcement());
    var ancestors = MsgnetRenewal.object().putArray("ancestor");
    ancestors.addObject().set("signed_keyset", signed).set("announcement", announcement);
    var materials = MsgnetRenewal.object().put("schema_version", 2);
    var keys = materials.putArray("keys");
    MsgnetV2Verifier.list(signed.path("keyset").path("keys")).stream()
        .sorted(Comparator.comparing(k -> k.path("kid").asString()))
        .forEach(
            k -> {
              var entry = keys.addObject();
              for (String field :
                  List.of("kid", "purpose", "public_key_spki", "not_before", "not_after"))
                entry.set(field, k.path(field));
            });
    MsgnetSchema.validate("MigrationMaterialCommitment", materials);
    var commitment = MsgnetRenewal.object().putArray("members");
    commitment
        .addObject()
        .put("version", "51")
        .put("pair_digest", MsgnetV2Verifier.digest(signed))
        .put("material_digest", MsgnetV2Verifier.digest(materials));
    var permitPayload =
        MsgnetRenewal.object()
            .put("schema_version", 2)
            .put("tenant_id", f.tenant)
            .put("realm", "2")
            .put("permit_id", "migration-test")
            .put("ancestor_digest", MsgnetV2Verifier.digest(ancestors))
            .put("queue_commitment_digest", MsgnetV2Verifier.digest(commitment))
            .put("maximum_committed_v1_version", "51")
            .put("minimum_v2_version", "100")
            .put("minimum_generation", "1");
    var permit =
        MsgnetOfflineRoot.sign("migration", permitPayload, "root", f.material.root.getPrivate());
    var plan =
        MsgnetRenewal.object()
            .put("version", 1)
            .put("tenantId", f.tenant)
            .put("realm", "2")
            .put("rootKid", "root")
            .put("rootFingerprint", f.plan.path("rootFingerprint").asString())
            .put("outputDirectory", directory.resolve("migration-output").toString());
    plan.set("rootPublic", f.plan.path("rootPublic"));
    plan.set("permit", reference(writePlan(permit)));
    plan.set("ancestor", reference(writePlan(ancestors)));
    plan.set("queueCommitment", reference(writePlan(commitment)));
    var member = plan.putArray("members").addObject();
    member.set("signed", reference(writePlan(signed)));
    member.set("announcement", reference(writePlan(announcement)));
    var privateFiles = member.putObject("privateFiles");
    privateFiles.set(
        "business",
        reference(
            write(
                directory, "legacy-business.der", f.material.business.getPrivate().getEncoded())));
    privateFiles.set(
        "protocol",
        reference(
            write(
                directory, "legacy-protocol.der", f.material.protocol.getPrivate().getEncoded())));
    privateFiles.set(
        "management",
        reference(
            write(
                directory,
                "legacy-management.der",
                f.material.management.getPrivate().getEncoded())));
    String props =
        "auriga.collab.network.key-operations.tenant-id="
            + f.tenant
            + "\n"
            + "auriga.collab.network.key-operations.realm=2\n"
            + "auriga.collab.network.key-operations.root-kid=root\n"
            + "auriga.collab.network.key-operations.root-fingerprint="
            + f.plan.path("rootFingerprint").asString()
            + "\n"
            + "auriga.collab.network.key-operations.root-public-file="
            + referencePath(f.plan.path("rootPublic"))
            + "\n"
            + "auriga.collab.network.auto-renew.authority-source="
            + f.signed.resolve("authority.json")
            + "\n"
            + "auriga.collab.network.auto-renew.control-source="
            + f.signed.resolve("control.json")
            + "\n"
            + "auriga.collab.network.auto-renew.runtime-private-file="
            + directory.resolve("prepared/runtime-private.der")
            + "\n"
            + "auriga.collab.network.auto-renew.kek-secret-file="
            + directory.resolve("prepared/kek.bin")
            + "\n"
            + "auriga.collab.network.auto-renew.kek-id=kek-v1\n"
            + "auriga.collab.network.auto-renew.projector-identity-id=network-identity\n"
            + "auriga.collab.network.auto-renew.projection-directory=/run/auriga-network\n"
            + "spring.datasource.url=jdbc:postgresql://fixture.invalid/unused\n"
            + "spring.datasource.username=synthetic-fixture\n"
            + "spring.datasource.password=synthetic-fixture-value\n";
    // java.util.Properties requires escaped Windows separators, exactly as an operator config does.
    props = props.replace("\\", "\\\\");
    plan.set(
        "configFile",
        reference(
            write(
                directory,
                "migration-config.properties",
                props.getBytes(java.nio.charset.StandardCharsets.UTF_8))));
    return plan;
  }

  @Test
  void explicitMigrationPackagesExactOriginalPlanWithoutClaimingLiveBarrier() throws Exception {
    var f = new Fixture();
    f.prepare();
    f.sign();
    var plan = migrationInput(f);
    var receipt = MsgnetInstallationMigration.prepare(writePlan(plan), clock);
    assertEquals("prepared", receipt.path("state").asString());
    var output = json(directory.resolve("migration-output/migration-plan.json"), true);
    assertEquals(
        Set.of("config_file", "permit_file", "runtime_private_file", "ancestor_file", "members"),
        new HashSet<>(output.propertyNames()));
    var member = output.path("members").get(0);
    assertEquals(
        Set.of("signed_file", "announcement_file", "private_files"),
        new HashSet<>(member.propertyNames()));
    assertEquals(
        Set.of("business", "protocol", "management"),
        new HashSet<>(member.path("private_files").propertyNames()));
    assertFalse(output.has("barrier_committed"));
  }

  @Test
  void explicitMigrationRejectsMissingExtraKidPairChangesAndWrongMaximumVersion() throws Exception {
    var f = new Fixture();
    f.prepare();
    f.sign();
    var plan = migrationInput(f);
    var missing = plan.deepCopy();
    ((ObjectNode) missing.path("members").get(0).path("privateFiles")).remove("management");
    assertThrows(
        Exception.class, () -> MsgnetInstallationMigration.prepare(writePlan(missing), clock));
    var extra = plan.deepCopy();
    ((ObjectNode) extra.path("members").get(0).path("privateFiles"))
        .set("extra", extra.path("members").get(0).path("privateFiles").path("management"));
    assertThrows(
        Exception.class, () -> MsgnetInstallationMigration.prepare(writePlan(extra), clock));
    var changed = plan.deepCopy();
    var otherRelease = f.material.release(f.material.payload(51, clock.instant().plusSeconds(1)));
    ((ObjectNode) changed.path("members").get(0))
        .set("signed", reference(writePlan(NetworkJson.parse(otherRelease.signedKeyset()))));
    ((ObjectNode) changed.path("members").get(0))
        .set("announcement", reference(writePlan(NetworkJson.parse(otherRelease.announcement()))));
    assertThrows(
        Exception.class, () -> MsgnetInstallationMigration.prepare(writePlan(changed), clock));
    var permitPayload =
        (ObjectNode) referencedJson(plan.path("permit"), false).path("payload").deepCopy();
    permitPayload.put("maximum_committed_v1_version", "52");
    var wrongMaximum = plan.deepCopy();
    wrongMaximum.set(
        "permit",
        reference(
            writePlan(
                MsgnetOfflineRoot.sign(
                    "migration", permitPayload, "root", f.material.root.getPrivate()))));
    assertThrows(
        Exception.class, () -> MsgnetInstallationMigration.prepare(writePlan(wrongMaximum), clock));
    assertFalse(Files.exists(directory.resolve("migration-output")));
  }

  @Test
  void wrongOfflineRootAndRootPrivateInsidePreparationRejectWithoutSigningOutput()
      throws Exception {
    var f = new Fixture();
    f.prepare();
    Path other =
        write(directory, "wrong-root.der", KeyTestMaterial.pair().getPrivate().getEncoded());
    var signing =
        MsgnetRenewal.object()
            .put("version", 1)
            .put("rootPrivateFile", other.toString())
            .put("outputDirectory", directory.resolve("signed").toString());
    signing.set("request", reference(directory.resolve("prepared/request.json")));
    assertThrows(Exception.class, () -> MsgnetInstallationOperator.sign(writePlan(signing), clock));
    assertFalse(Files.exists(directory.resolve("signed")));
    signing.put("rootPrivateFile", directory.resolve("prepared/runtime-private.der").toString());
    assertThrows(Exception.class, () -> MsgnetInstallationOperator.sign(writePlan(signing), clock));
  }

  @Test
  void finalizeRejectsRuntimePairSwapAndWrongLengthKekEvenWithUpdatedLocalHashes()
      throws Exception {
    var f = new Fixture();
    f.prepare();
    f.sign();
    var manifest = (ObjectNode) json(f.prepared, true);
    Path runtime = directory.resolve("prepared/runtime-private.der");
    byte[] saved = Files.readAllBytes(runtime);
    Files.write(runtime, KeyTestMaterial.pair().getPrivate().getEncoded());
    manifest.set("runtimePrivate", reference(runtime));
    Files.writeString(f.prepared, NetworkJson.canonical(manifest));
    assertThrows(
        Exception.class,
        () -> MsgnetInstallationOperator.finalizeInstallation(writePlan(f.finalPlan()), clock));
    Files.write(runtime, saved);
    manifest.set("runtimePrivate", reference(runtime));
    Path kek = directory.resolve("prepared/kek.bin");
    Files.write(kek, new byte[31]);
    manifest.set("kek", reference(kek));
    Files.writeString(f.prepared, NetworkJson.canonical(manifest));
    assertThrows(
        Exception.class,
        () -> MsgnetInstallationOperator.finalizeInstallation(writePlan(f.finalPlan()), clock));
  }

  @Test
  void authorityOrControlChangedAfterSigningAndChangedFileBytesReject() throws Exception {
    var f = new Fixture();
    f.prepare();
    f.sign();
    var control = (ObjectNode) json(f.signed.resolve("control.json"), false);
    ((ObjectNode) control.path("payload")).put("minimum_generation", "2");
    Files.writeString(f.signed.resolve("control.json"), NetworkJson.canonical(control));
    assertThrows(
        Exception.class,
        () -> MsgnetInstallationOperator.finalizeInstallation(writePlan(f.finalPlan()), clock));
    var before = f.finalPlan();
    Files.writeString(f.signed.resolve("control.json"), "{}");
    assertThrows(
        Exception.class,
        () -> MsgnetInstallationOperator.finalizeInstallation(writePlan(before), clock));
  }

  @Test
  void existingOutputIsNeverOverwrittenAndPublicFilesCannotBeSecretInputs() throws Exception {
    var f = new Fixture();
    f.prepare();
    byte[] before = Files.readAllBytes(f.prepared);
    assertThrows(Exception.class, f::prepare);
    assertArrayEquals(before, Files.readAllBytes(f.prepared));
    Path input = writePlan(f.plan);
    if (Files.getFileAttributeView(input, PosixFileAttributeView.class) != null) {
      Files.setPosixFilePermissions(input, PosixFilePermissions.fromString("rw-r--r--"));
      assertThrows(Exception.class, () -> MsgnetInstallationOperator.prepare(input, clock));
      Path link = directory.resolve("plan-link.json");
      Files.createSymbolicLink(link, input);
      assertThrows(Exception.class, () -> MsgnetInstallationOperator.prepare(link, clock));
    } else {
      // Windows exercises the production ACL checker rather than silently accepting POSIX modes.
      ProtectedKeyFiles.check(input, true);
      assertThrows(
          Exception.class,
          () -> MsgnetInstallationOperator.prepare(directory.resolve("missing.json"), clock));
    }
  }

  private static String encode(byte[] bytes) {
    return Base64.getUrlEncoder().withoutPadding().encodeToString(bytes);
  }
}
