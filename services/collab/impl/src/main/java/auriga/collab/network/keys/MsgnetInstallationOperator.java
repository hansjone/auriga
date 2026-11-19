package auriga.collab.network.keys;

import static auriga.collab.network.keys.MsgnetInstallationFiles.*;

import auriga.collab.app.NetworkJson;
import java.nio.charset.StandardCharsets;
import java.nio.file.Path;
import java.security.KeyFactory;
import java.security.KeyPairGenerator;
import java.security.PublicKey;
import java.security.SecureRandom;
import java.security.spec.PKCS8EncodedKeySpec;
import java.security.spec.X509EncodedKeySpec;
import java.time.Clock;
import java.time.Instant;
import java.util.*;
import org.springframework.core.env.MapPropertySource;
import org.springframework.core.env.StandardEnvironment;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** Local installation preparation only. Does not start Spring, connect to a DB or project keys. */
public final class MsgnetInstallationOperator {
  private static final Set<String> ANCHORS =
      Set.of("tenantId", "realm", "rootKid", "rootFingerprint");
  private static final Set<String> PREPARE =
      Set.of(
          "version",
          "mode",
          "tenantId",
          "realm",
          "rootKid",
          "rootFingerprint",
          "rootPublic",
          "approvedSource",
          "identities",
          "authorityId",
          "authorityRevision",
          "runtimeKid",
          "kekId",
          "controlRevision",
          "minimumGeneration",
          "minimumKeysetVersion",
          "notBefore",
          "limits",
          "transportScope",
          "projectorIdentityId",
          "outputDirectory");
  private static final Set<String> PREPARED =
      Set.of(
          "version",
          "mode",
          "tenantId",
          "realm",
          "rootKid",
          "rootFingerprint",
          "kekId",
          "projectorIdentityId",
          "approvedSource",
          "rootPublic",
          "runtimePrivate",
          "kek",
          "identities",
          "request");
  private static final Set<String> REQUEST =
      Set.of(
          "version",
          "mode",
          "tenantId",
          "realm",
          "rootKid",
          "rootFingerprint",
          "rootSpki",
          "authority",
          "control");
  private static final List<String> CAPABILITIES =
      List.of(
          "keyset",
          "announcement",
          "business_rotation",
          "restart_recovery",
          "history_archive",
          "network_transport");

  private MsgnetInstallationOperator() {}

  /** prepare|sign|finalize|migration-plan protected-plan.json; argv never contains key bytes. */
  public static void main(String[] args) {
    try {
      check(args.length == 2);
      Path plan = path(args[1]);
      JsonNode receipt =
          switch (args[0]) {
            case "prepare" -> prepare(plan, Clock.systemUTC());
            case "sign" -> sign(plan, Clock.systemUTC());
            case "finalize" -> finalizeInstallation(plan, Clock.systemUTC());
            case "migration-plan" -> MsgnetInstallationMigration.prepare(plan, Clock.systemUTC());
            default -> throw invalid();
          };
      System.out.println(NetworkJson.canonical(receipt, 1048576));
    } catch (Exception failure) {
      // Parsers, JDBC settings and filesystem exceptions must not leak paths or key material.
      System.err.println(
          "Msgnet installation operation rejected; preserve partial output and review inputs");
      System.exit(1);
    }
  }

  static ObjectNode prepare(Path planFile, Clock clock) throws Exception {
    var plan = json(planFile, true);
    fields(plan, PREPARE, Set.of("nodeControlPins"));
    version(plan);
    // A fresh plan is never a migration permission or evidence that PostgreSQL has no v1 row.
    check("fresh".equals(string(plan, "mode")));
    anchor(plan);
    var rootBytes = referenced(plan.path("rootPublic"), false, 1024);
    PublicKey root;
    try {
      root = root(rootBytes, string(plan, "rootFingerprint"));
    } finally {
      Arrays.fill(rootBytes, (byte) 0);
    }
    var identities = referencedJson(plan.path("identities"), false);
    var nodes =
        plan.has("nodeControlPins") ? referencedJson(plan.path("nodeControlPins"), false) : null;
    validatePins(plan, identities, nodes);
    approval(plan, plan.path("identities"), plan.path("nodeControlPins"));
    String authorityId = identifier(plan, "authorityId");
    String runtimeKid = identifier(plan, "runtimeKid");
    identifier(plan, "kekId");
    for (String field :
        List.of(
            "authorityRevision", "controlRevision", "minimumGeneration", "minimumKeysetVersion"))
      positive(plan, field);
    Instant notBefore = Instant.parse(string(plan, "notBefore"));
    check(
        notBefore.toString().equals(string(plan, "notBefore"))
            && !notBefore.isAfter(clock.instant()));
    var pair = KeyPairGenerator.getInstance("Ed25519").generateKeyPair();
    var authority =
        scope(plan)
            .put("schema_version", 2)
            .put("authority_id", authorityId)
            .put("authority_revision", string(plan, "authorityRevision"))
            .put("runtime_kid", runtimeKid)
            .put("public_key_spki", encode(pair.getPublic().getEncoded()))
            .put("not_before", notBefore.toString())
            .put("expiry_policy", "no_hard_expiry")
            .put("review_period_days", 365);
    var capabilities = authority.putArray("capabilities");
    CAPABILITIES.forEach(capabilities::add);
    authority.set("limits", plan.path("limits").deepCopy());
    authority.set("transport_scope", plan.path("transportScope").deepCopy());
    MsgnetSchema.validate("RuntimeAuthority", authority);
    MsgnetPolicy.authorizedConfig(config(plan), MsgnetRenewal.object().set("payload", authority));
    validateTransport(authority, identities);
    var control =
        scope(plan)
            .put("schema_version", 2)
            .put("control_revision", string(plan, "controlRevision"))
            .put(
                "issued_at",
                clock.instant().truncatedTo(java.time.temporal.ChronoUnit.SECONDS).toString())
            .put("minimum_generation", string(plan, "minimumGeneration"))
            .put("minimum_keyset_version", string(plan, "minimumKeysetVersion"));
    control.putArray("allowed_authorities");
    control.putArray("revoked_authority_ids");
    control.putArray("revoked_installation_ids");
    MsgnetSchema.validate("RootControl", control);
    var request =
        copyAnchors(plan)
            .put("version", 1)
            .put("mode", "fresh")
            .put("rootSpki", encode(root.getEncoded()));
    request.set("authority", authority);
    request.set("control", control);
    validateRequest(request, clock);

    // Everything above is validated before making an output directory. Later failures keep
    // evidence.
    Path output = newDirectory(string(plan, "outputDirectory"));
    byte[] runtime = pair.getPrivate().getEncoded();
    byte[] kek = new byte[32];
    new SecureRandom().nextBytes(kek);
    try {
      var prepared =
          copyAnchors(plan)
              .put("version", 1)
              .put("mode", "fresh")
              .put("kekId", string(plan, "kekId"))
              .put("projectorIdentityId", string(plan, "projectorIdentityId"));
      prepared.set("approvedSource", plan.path("approvedSource").deepCopy());
      prepared.set("rootPublic", reference(write(output, "root-public.der", root.getEncoded())));
      prepared.set("runtimePrivate", reference(write(output, "runtime-private.der", runtime)));
      prepared.set("kek", reference(write(output, "kek.bin", kek)));
      prepared.set("identities", reference(writeJson(output, "identities.json", identities)));
      if (nodes != null)
        prepared.set(
            "nodeControlPins", reference(writeJson(output, "node-control-pins.json", nodes)));
      // Identity source hashes describe source bytes; output JSON is canonicalized separately.
      prepared.set("sourceIdentities", plan.path("identities").deepCopy());
      if (nodes != null)
        prepared.set("sourceNodeControlPins", plan.path("nodeControlPins").deepCopy());
      prepared.set("request", reference(writeJson(output, "request.json", request)));
      Path result = writeJson(output, "prepared.json", prepared);
      return receipt("prepared", result);
    } finally {
      Arrays.fill(runtime, (byte) 0);
      Arrays.fill(kek, (byte) 0);
    }
  }

  static ObjectNode sign(Path planFile, Clock clock) throws Exception {
    var plan = json(planFile, true);
    fields(plan, Set.of("version", "request", "rootPrivateFile", "outputDirectory"), Set.of());
    version(plan);
    Path requestPath = referencePath(plan.path("request"));
    var request = referencedJson(plan.path("request"), false);
    validateRequest(request, clock);
    Path secretPath = path(string(plan, "rootPrivateFile"));
    Path outputPath = path(string(plan, "outputDirectory"));
    check(!secretPath.startsWith(requestPath.getParent()) && !secretPath.startsWith(outputPath));
    byte[] secret = read(secretPath, true, 4096);
    try {
      var key = KeyFactory.getInstance("Ed25519").generatePrivate(new PKCS8EncodedKeySpec(secret));
      var authority =
          MsgnetOfflineRoot.sign(
              "authority", request.path("authority"), string(request, "rootKid"), key);
      var control =
          MsgnetOfflineRoot.sign(
              "control", controlPayload(request, authority), string(request, "rootKid"), key);
      // Also proves the supplied private key belongs to the independent installed public root.
      verifier(request).verifyAuthority(authority, control, clock.instant());
      Path output = newDirectory(outputPath.toString());
      writeJson(output, "authority.json", authority);
      Path result = writeJson(output, "control.json", control);
      return receipt("prepared", result);
    } finally {
      Arrays.fill(secret, (byte) 0);
    }
  }

  static ObjectNode finalizeInstallation(Path planFile, Clock clock) throws Exception {
    var plan = json(planFile, true);
    fields(
        plan, Set.of("version", "prepared", "authority", "control", "outputDirectory"), Set.of());
    version(plan);
    Path preparedPath = referencePath(plan.path("prepared"));
    var prepared = referencedJson(plan.path("prepared"), true);
    var required = new HashSet<>(PREPARED);
    required.add("sourceIdentities");
    fields(prepared, required, Set.of("nodeControlPins", "sourceNodeControlPins"));
    version(prepared);
    check("fresh".equals(string(prepared, "mode")));
    anchor(prepared);
    identifier(prepared, "kekId");
    for (var entry :
        Map.of(
                "rootPublic",
                "root-public.der",
                "runtimePrivate",
                "runtime-private.der",
                "kek",
                "kek.bin",
                "identities",
                "identities.json",
                "request",
                "request.json")
            .entrySet())
      check(
          referencePath(prepared.path(entry.getKey()))
              .equals(preparedPath.getParent().resolve(entry.getValue())));
    check(prepared.has("nodeControlPins") == prepared.has("sourceNodeControlPins"));
    if (prepared.has("nodeControlPins"))
      check(
          referencePath(prepared.path("nodeControlPins"))
              .equals(preparedPath.getParent().resolve("node-control-pins.json")));
    var request = referencedJson(prepared.path("request"), false);
    validateRequest(request, clock);
    for (String name : ANCHORS) check(prepared.path(name).equals(request.path(name)));
    var identities = referencedJson(prepared.path("identities"), false);
    var sourceIdentities = referencedJson(prepared.path("sourceIdentities"), false);
    check(identities.equals(sourceIdentities));
    var nodes =
        prepared.has("nodeControlPins")
            ? referencedJson(prepared.path("nodeControlPins"), false)
            : null;
    if (nodes != null)
      check(nodes.equals(referencedJson(prepared.path("sourceNodeControlPins"), false)));
    approval(prepared, prepared.path("sourceIdentities"), prepared.path("sourceNodeControlPins"));
    validatePins(prepared, identities, nodes);
    validateTransport(request.path("authority"), identities);
    byte[] rootBytes = referenced(prepared.path("rootPublic"), false, 1024);
    try {
      check(
          encode(root(rootBytes, string(prepared, "rootFingerprint")).getEncoded())
              .equals(string(request, "rootSpki")));
    } finally {
      Arrays.fill(rootBytes, (byte) 0);
    }
    var authority = referencedJson(plan.path("authority"), false);
    var control = referencedJson(plan.path("control"), false);
    check(authority.path("payload").equals(request.path("authority")));
    check(control.path("payload").equals(controlPayload(request, authority)));
    verifier(request).verifyAuthority(authority, control, clock.instant());
    MsgnetPolicy.authorizedConfig(config(prepared), authority);
    byte[] runtime = referenced(prepared.path("runtimePrivate"), true, 4096);
    byte[] kek = null;
    try {
      kek = referenced(prepared.path("kek"), true, 32);
      check(kek.length == 32);
      MsgnetPrivateKeyStore.match(
          MsgnetV2Verifier.text(authority.path("payload"), "public_key_spki"), runtime, "Ed25519");
      try (var ring =
          new MsgnetKeyEncryption(
              string(prepared, "kekId"), Map.of(string(prepared, "kekId"), kek))) {
        // Construction checks the exact existing KEK contract; no parallel encryption
        // implementation.
      }
      Path output = newDirectory(string(plan, "outputDirectory"));
      var spec =
          copyAnchors(prepared)
              .put("version", 1)
              .put("kekId", string(prepared, "kekId"))
              .put("projectorIdentityId", string(prepared, "projectorIdentityId"));
      spec.set("authority", leaf(writeJson(output, "authority.json", authority)));
      spec.set("control", leaf(writeJson(output, "control.json", control)));
      spec.set("runtimePrivate", leaf(write(output, "runtime-private.der", runtime)));
      spec.set("kek", leaf(write(output, "kek.bin", kek)));
      spec.set("identities", leaf(writeJson(output, "identities.json", identities)));
      if (nodes != null)
        spec.set("nodeControlPins", leaf(writeJson(output, "node-control-pins.json", nodes)));
      Path result = writeJson(output, "installation.json", spec);
      write(
          output,
          "installation.sha256",
          (hash(read(result, false, 1048576)) + "  installation.json\n")
              .getBytes(StandardCharsets.US_ASCII));
      return receipt("configured", result);
    } finally {
      Arrays.fill(runtime, (byte) 0);
      if (kek != null) Arrays.fill(kek, (byte) 0);
    }
  }

  static void anchor(JsonNode value) {
    String tenant = string(value, "tenantId");
    check(UUID.fromString(tenant).toString().equals(tenant));
    positive(value, "realm");
    identifier(value, "rootKid");
    check(string(value, "rootFingerprint").matches("sha256:[0-9a-f]{64}"));
  }

  private static void positive(JsonNode value, String field) {
    String number = string(value, field);
    check(MsgnetV2Verifier.integer(number).signum() > 0);
  }

  static PublicKey root(byte[] encoded, String fingerprint) throws Exception {
    check(encoded.length == 44);
    var key = KeyFactory.getInstance("Ed25519").generatePublic(new X509EncodedKeySpec(encoded));
    check(
        Arrays.equals(key.getEncoded(), encoded)
            && RootKeyPublication.fingerprint(key).equals(fingerprint));
    return key;
  }

  private static ObjectNode copyAnchors(JsonNode input) {
    var result = MsgnetRenewal.object();
    ANCHORS.forEach(name -> result.put(name, string(input, name)));
    return result;
  }

  private static ObjectNode scope(JsonNode input) {
    return MsgnetRenewal.object()
        .put("tenant_id", string(input, "tenantId"))
        .put("realm", string(input, "realm"));
  }

  private static String encode(byte[] bytes) {
    return Base64.getUrlEncoder().withoutPadding().encodeToString(bytes);
  }

  private static ObjectNode controlPayload(JsonNode request, JsonNode authority) {
    ObjectNode result = (ObjectNode) request.path("control").deepCopy();
    result
        .putArray("allowed_authorities")
        .addObject()
        .put("authority_id", MsgnetV2Verifier.text(authority.path("payload"), "authority_id"))
        .put("authority_digest", MsgnetV2Verifier.digest(authority));
    return result;
  }

  private static MsgnetV2Verifier verifier(JsonNode request) {
    return new MsgnetV2Verifier(
        string(request, "tenantId"),
        string(request, "realm"),
        string(request, "rootKid"),
        RootKeyPublication.publicKey(string(request, "rootSpki")));
  }

  private static void validateRequest(JsonNode request, Clock clock) throws Exception {
    fields(request, REQUEST, Set.of());
    version(request);
    anchor(request);
    check("fresh".equals(string(request, "mode")));
    byte[] spki = Base64.getUrlDecoder().decode(string(request, "rootSpki"));
    check(encode(spki).equals(string(request, "rootSpki")));
    root(spki, string(request, "rootFingerprint"));
    var authority = request.path("authority");
    var control = request.path("control");
    MsgnetSchema.validate("RuntimeAuthority", authority);
    MsgnetSchema.validate("RootControl", control);
    for (var payload : List.of(authority, control)) {
      check(payload.path("tenant_id").asString().equals(string(request, "tenantId")));
      check(payload.path("realm").asString().equals(string(request, "realm")));
    }
    check(!Instant.parse(authority.path("not_before").asString()).isAfter(clock.instant()));
    check(
        control.path("allowed_authorities").isEmpty()
            && control.path("revoked_authority_ids").isEmpty()
            && control.path("revoked_installation_ids").isEmpty()
            && !control.has("generation_transition")
            && !control.has("transport_transition"));
    check(
        new HashSet<>(
                MsgnetV2Verifier.list(authority.path("capabilities")).stream()
                    .map(JsonNode::asString)
                    .toList())
            .equals(new HashSet<>(CAPABILITIES)));
  }

  private static void approval(JsonNode input, JsonNode identities, JsonNode nodes) {
    var source = referencedJson(input.path("approvedSource"), true);
    fields(
        source,
        Set.of(
            "version",
            "mode",
            "tenantId",
            "realm",
            "rootKid",
            "rootFingerprint",
            "identitiesSha256",
            "approvedBy",
            "approval"),
        Set.of("nodeControlPinsSha256"));
    version(source);
    anchor(source);
    for (String field : ANCHORS) check(source.path(field).equals(input.path(field)));
    check("fresh".equals(string(source, "mode")));
    string(source, "approvedBy");
    string(source, "approval");
    check(string(source, "identitiesSha256").equals(string(identities, "sha256")));
    check(source.has("nodeControlPinsSha256") == !nodes.isMissingNode());
    if (!nodes.isMissingNode())
      check(string(source, "nodeControlPinsSha256").equals(string(nodes, "sha256")));
  }

  private static void validatePins(JsonNode input, JsonNode identities, JsonNode nodes) {
    var tenant = UUID.fromString(string(input, "tenantId"));
    var pins = MsgnetControlLedger.pins(identities, tenant, string(input, "realm"));
    String projector = identifier(input, "projectorIdentityId");
    check(
        pins.values().stream()
            .anyMatch(
                pin -> pin.identityId().equals(projector) && pin.component().equals("network")));
    if (nodes != null) {
      var nodePins = MsgnetNodePins.read(nodes, tenant, string(input, "realm"));
      // Existing ledger also rejects cross-table target/identity reuse; construction does no I/O.
      new MsgnetControlLedger(null, null, tenant, string(input, "realm"), pins, nodePins);
    }
  }

  private static void validateTransport(JsonNode authority, JsonNode identities) {
    var covered = new HashSet<String>();
    for (var scope : authority.path("transport_scope")) {
      String id = scope.path("identity_id").asString();
      String component = scope.path("component").asString();
      check(covered.add(id));
      check(
          MsgnetV2Verifier.list(identities.path("pins")).stream()
              .anyMatch(
                  pin ->
                      pin.path("identity_id").asString().equals(id)
                          && pin.path("component").asString().equals(component)));
    }
    for (var pin : identities.path("pins"))
      check(covered.contains(pin.path("identity_id").asString()));
  }

  static JsonNode config(JsonNode input) {
    var env = new StandardEnvironment();
    env.getPropertySources().remove(StandardEnvironment.SYSTEM_ENVIRONMENT_PROPERTY_SOURCE_NAME);
    env.getPropertySources().remove(StandardEnvironment.SYSTEM_PROPERTIES_PROPERTY_SOURCE_NAME);
    var properties = new HashMap<String, Object>();
    String prefix = "auriga.collab.network.auto-renew.";
    properties.put(prefix + "kek-secret-file", "/etc/auriga/collab/kek.bin");
    properties.put(prefix + "kek-id", string(input, "kekId"));
    properties.put(prefix + "authority-source", "/etc/auriga/collab/authority.json");
    properties.put(prefix + "control-source", "/etc/auriga/collab/control.json");
    properties.put(prefix + "projector-identity-id", string(input, "projectorIdentityId"));
    properties.put(prefix + "projection-directory", "/run/auriga-network");
    env.getPropertySources().addFirst(new MapPropertySource("fixed-installation", properties));
    return MsgnetRenewalConfiguration.config(env);
  }

  private static ObjectNode leaf(Path file) {
    var result = reference(file);
    result.put("path", file.getFileName().toString());
    return result;
  }

  static ObjectNode receipt(String state, Path file) {
    return MsgnetRenewal.object()
        .put("state", state)
        .put("file", file.toString())
        .put("sha256", hash(read(file, false, 1048576)));
  }
}
