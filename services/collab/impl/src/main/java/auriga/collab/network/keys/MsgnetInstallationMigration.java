package auriga.collab.network.keys;

import static auriga.collab.network.keys.MsgnetInstallationFiles.*;

import auriga.collab.app.NetworkJson;
import java.io.StringReader;
import java.nio.charset.StandardCharsets;
import java.nio.file.Path;
import java.time.Clock;
import java.util.*;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** Packages an explicitly approved legacy plan; the existing PG operator owns the whole barrier. */
final class MsgnetInstallationMigration {
  private MsgnetInstallationMigration() {}

  static ObjectNode prepare(Path planFile, Clock clock) throws Exception {
    var plan = json(planFile, true);
    fields(
        plan,
        Set.of(
            "version",
            "tenantId",
            "realm",
            "rootKid",
            "rootFingerprint",
            "configFile",
            "rootPublic",
            "permit",
            "ancestor",
            "queueCommitment",
            "members",
            "outputDirectory"),
        Set.of());
    version(plan);
    MsgnetInstallationOperator.anchor(plan);
    byte[] rootBytes = referenced(plan.path("rootPublic"), false, 1024);
    java.security.PublicKey root;
    try {
      root = MsgnetInstallationOperator.root(rootBytes, string(plan, "rootFingerprint"));
    } finally {
      Arrays.fill(rootBytes, (byte) 0);
    }
    var verifier =
        new MsgnetV2Verifier(
            string(plan, "tenantId"), string(plan, "realm"), string(plan, "rootKid"), root);
    var permit = referencedJson(plan.path("permit"), false);
    verifier.verifyMigrationPermit(permit);
    var payload = permit.path("payload");
    check(
        payload.path("tenant_id").asString().equals(string(plan, "tenantId"))
            && payload.path("realm").asString().equals(string(plan, "realm")));
    var legacy =
        new RootKeyPublication(
            string(plan, "realm"),
            string(plan, "rootKid"),
            root,
            string(plan, "rootFingerprint"),
            clock);
    var ancestors = referencedJson(plan.path("ancestor"), false);
    check(ancestors.isArray() && !ancestors.isEmpty());
    for (var ancestor : ancestors) {
      fields(ancestor, Set.of("signed_keyset", "announcement"), Set.of());
      legacy.verifyArchived(
          NetworkJson.canonical(ancestor.path("signed_keyset"), 1048576),
          NetworkJson.canonical(ancestor.path("announcement"), 1048576));
    }
    check(MsgnetV2Verifier.digest(ancestors).equals(payload.path("ancestor_digest").asString()));
    var commitment = referencedJson(plan.path("queueCommitment"), false);
    check(commitment.isArray() && !commitment.isEmpty());
    check(
        MsgnetV2Verifier.digest(commitment)
            .equals(payload.path("queue_commitment_digest").asString()));
    var committed = new TreeMap<java.math.BigInteger, JsonNode>();
    java.math.BigInteger last = java.math.BigInteger.ZERO;
    for (var item : commitment) {
      fields(item, Set.of("version", "pair_digest", "material_digest"), Set.of());
      var version = MsgnetV2Verifier.integer(string(item, "version"));
      check(version.compareTo(last) > 0);
      for (String name : List.of("pair_digest", "material_digest"))
        check(string(item, name).matches("sha256:[0-9a-f]{64}"));
      committed.put(version, item);
      last = version;
    }
    check(last.equals(MsgnetV2Verifier.number(payload, "maximum_committed_v1_version")));
    check(MsgnetV2Verifier.number(payload, "minimum_v2_version").compareTo(last) > 0);
    check(plan.path("members").isArray() && plan.path("members").size() == committed.size());
    var members = MsgnetRenewal.object().putArray("members");
    var seen = new HashSet<java.math.BigInteger>();
    for (var member : plan.path("members")) {
      fields(member, Set.of("signed", "announcement", "privateFiles"), Set.of());
      var signed = referencedJson(member.path("signed"), false);
      var announcement = referencedJson(member.path("announcement"), false);
      var release =
          legacy.verifyArchived(
              NetworkJson.canonical(signed, 1048576), NetworkJson.canonical(announcement, 1048576));
      var version = MsgnetV2Verifier.number(release.payload(), "keyset_version");
      check(seen.add(version) && committed.containsKey(version));
      check(
          MsgnetV2Verifier.digest(signed)
              .equals(committed.get(version).path("pair_digest").asString()));
      check(member.path("privateFiles").isObject());
      var ids = new HashSet<String>();
      var files = MsgnetRenewal.object();
      for (var key : release.payload().path("keys")) {
        String kid = key.path("kid").asString();
        check(ids.add(kid) && member.path("privateFiles").has(kid));
        var ref = member.path("privateFiles").path(kid);
        byte[] bytes = referenced(ref, true, 4096);
        try {
          MsgnetPrivateKeyStore.match(key.path("public_key_spki").asString(), bytes, "Ed25519");
        } finally {
          Arrays.fill(bytes, (byte) 0);
        }
        files.put(kid, referencePath(ref).toString());
      }
      check(ids.equals(new HashSet<>(member.path("privateFiles").propertyNames())));
      members
          .addObject()
          .put("signed_file", referencePath(member.path("signed")).toString())
          .put("announcement_file", referencePath(member.path("announcement")).toString())
          .set("private_files", files);
    }
    // No attempt to fabricate current PG pair, remaining lifetime or a completed freeze receipt.
    // Material commitment recomputation and all live checks remain in MsgnetMigration.migrate().
    byte[] configBytes = referenced(plan.path("configFile"), true, 65536);
    var properties = new Properties();
    try {
      properties.load(new StringReader(new String(configBytes, StandardCharsets.UTF_8)));
    } finally {
      Arrays.fill(configBytes, (byte) 0);
    }
    String prefix = "auriga.collab.network.key-operations.";
    for (var pair :
        Map.of(
                "tenantId",
                "tenant-id",
                "realm",
                "realm",
                "rootKid",
                "root-kid",
                "rootFingerprint",
                "root-fingerprint")
            .entrySet())
      check(string(plan, pair.getKey()).equals(properties.getProperty(prefix + pair.getValue())));
    check(
        referencePath(plan.path("rootPublic"))
            .equals(path(properties.getProperty(prefix + "root-public-file", ""))));
    String renewal = "auriga.collab.network.auto-renew.";
    for (String name :
        List.of(
            "spring.datasource.url", "spring.datasource.username", "spring.datasource.password"))
      check(properties.getProperty(name) != null && !properties.getProperty(name).isBlank());
    var env = new org.springframework.core.env.StandardEnvironment();
    env.getPropertySources()
        .remove(
            org.springframework.core.env.StandardEnvironment
                .SYSTEM_ENVIRONMENT_PROPERTY_SOURCE_NAME);
    env.getPropertySources()
        .remove(
            org.springframework.core.env.StandardEnvironment
                .SYSTEM_PROPERTIES_PROPERTY_SOURCE_NAME);
    env.getPropertySources()
        .addFirst(
            new org.springframework.core.env.PropertiesPropertySource(
                "protected-plan", properties));
    var renewalConfig = MsgnetRenewalConfiguration.config(env);
    try (var ring = new MsgnetRenewalConfiguration().msgnetKeyEncryption(env)) {
      // Check the real ring/config consumer without opening a datasource or writing PG.
    }
    var authority = json(path(properties.getProperty(renewal + "authority-source", "")), false);
    var control = json(path(properties.getProperty(renewal + "control-source", "")), false);
    verifier.verifyAuthority(authority, control, clock.instant());
    MsgnetPolicy.authorizedConfig(renewalConfig, authority);
    check(
        MsgnetV2Verifier.number(control.path("payload"), "minimum_keyset_version")
                .compareTo(MsgnetV2Verifier.number(payload, "minimum_v2_version"))
            >= 0);
    check(
        MsgnetV2Verifier.number(control.path("payload"), "minimum_generation")
                .compareTo(MsgnetV2Verifier.number(payload, "minimum_generation"))
            >= 0);
    Path runtimePath = path(properties.getProperty(renewal + "runtime-private-file", ""));
    byte[] runtime = read(runtimePath, true, 4096);
    try {
      MsgnetPrivateKeyStore.match(
          authority.path("payload").path("public_key_spki").asString(), runtime, "Ed25519");
    } finally {
      Arrays.fill(runtime, (byte) 0);
    }
    Path output = newDirectory(string(plan, "outputDirectory"));
    var result =
        MsgnetRenewal.object()
            .put("config_file", referencePath(plan.path("configFile")).toString())
            .put("permit_file", referencePath(plan.path("permit")).toString())
            .put("runtime_private_file", runtimePath.toString())
            .put("ancestor_file", referencePath(plan.path("ancestor")).toString());
    result.set("members", members);
    return MsgnetInstallationOperator.receipt(
        "prepared", writeJson(output, "migration-plan.json", result));
  }
}
