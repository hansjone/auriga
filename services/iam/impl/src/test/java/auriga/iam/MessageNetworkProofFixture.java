package auriga.iam;

import auriga.contracts.msgnet.model.*;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.security.*;
import java.time.*;
import java.util.*;
import tools.jackson.databind.node.ObjectNode;

/** Frozen public shape with fresh runtime-generated root and issuer keys. */
final class MessageNetworkProofFixture {
  final UUID tenant = UUID.randomUUID(), node = UUID.randomUUID();
  final KeyPair root = KeyPairGenerator.getInstance("Ed25519").generateKeyPair();
  final KeyPair runtime = KeyPairGenerator.getInstance("Ed25519").generateKeyPair();
  final ObjectNode control, authority, bundle, evidence, materialSelector;
  final Clock clock;
  final String readerId, identityId;
  private final String installedRootKid;
  final Map<String, String> authorized = new HashMap<>();
  final Map<String, String> identities = new HashMap<>();

  MessageNetworkProofFixture() throws Exception {
    var fixture =
        Json.MAPPER.readTree(
            Files.readString(
                Path.of("../../../contracts/network/fixtures/auto-renewal-v2-vectors.json")));
    ObjectNode selected = null;
    for (var vector : fixture.path("boundary_cases"))
      if (vector.path("id").asText().equals("k3-confirmation-complete"))
        selected = (ObjectNode) vector.path("input");
    if (selected == null) throw new IllegalStateException("frozen confirmation fixture missing");
    var input =
        Json.MAPPER.readTree(selected.toString().replace("synthetic-tenant", tenant.toString()));
    control = (ObjectNode) input.path("control").deepCopy();
    installedRootKid = control.path("root_auth").path("kid").asText();
    authority = (ObjectNode) input.path("authority").deepCopy();
    bundle = (ObjectNode) input.path("bundle").deepCopy();
    evidence = (ObjectNode) input.path("evidence").deepCopy();
    materialSelector = (ObjectNode) row().path("selector").deepCopy();
    readerId = row().path("reader_id").asText();
    identityId = materialSelector.path("installation_id").asText();
    clock = Clock.fixed(Instant.parse(input.path("now").asText()), ZoneOffset.UTC);
    payload(authority).put("public_key_spki", spki(runtime));
    identities.put(readerId, identityId);
    refresh();
  }

  static ObjectNode payload(ObjectNode signed) {
    return (ObjectNode) signed.path("payload");
  }

  ObjectNode row() {
    return (ObjectNode) evidence.path("payload").path("readers").get(0);
  }

  String rootKid() {
    return installedRootKid;
  }

  String runtimeKid() {
    return authority.path("payload").path("runtime_kid").asText();
  }

  static String spki(KeyPair pair) {
    return Base64.getUrlEncoder().withoutPadding().encodeToString(pair.getPublic().getEncoded());
  }

  static String digest(ObjectNode document) {
    return MessageNetworkSelectors.digest(document);
  }

  static byte[] bytes(ObjectNode document) {
    return InstallationJson.canonical(document).getBytes(StandardCharsets.UTF_8);
  }

  void sign(ObjectNode document, String member, String domain, String kid, KeyPair key)
      throws Exception {
    var signature = java.security.Signature.getInstance("Ed25519");
    signature.initSign(key.getPrivate());
    signature.update((domain + "\0").getBytes(StandardCharsets.UTF_8));
    signature.update(bytes(payload(document)));
    document.set(
        member,
        Json.object(
            "alg",
            "Ed25519",
            "kid",
            kid,
            "signature",
            Base64.getUrlEncoder().withoutPadding().encodeToString(signature.sign())));
  }

  void signRoot() throws Exception {
    sign(control, "root_auth", "Auriga.NetworkRootControl.v2", rootKid(), root);
  }

  void signBundle() throws Exception {
    sign(bundle, "runtime_auth", "Auriga.NetworkTransportTrustBundle.v2", runtimeKid(), runtime);
  }

  void signEvidence() throws Exception {
    sign(evidence, "runtime_auth", "Auriga.NetworkTransportConfirmation.v2", runtimeKid(), runtime);
  }

  void refresh() throws Exception {
    sign(authority, "root_auth", "Auriga.NetworkRuntimeAuthority.v2", rootKid(), root);
    String ad = digest(authority);
    var reference =
        Json.object(
            "authority_id",
            authority.path("payload").path("authority_id").asText(),
            "authority_digest",
            ad);
    payload(control).set("allowed_authorities", Json.MAPPER.createArrayNode().add(reference));
    signRoot();
    payload(bundle).put("authority_digest", ad);
    signBundle();
    materialSelector.put("bundle_digest", digest(bundle));
    materialSelector.put("bundle_version", payload(bundle).path("bundle_version").asText());
    row().set("selector", materialSelector.deepCopy());
    authorized.put(readerId, digest(materialSelector));
    payload(evidence).put("authority_digest", ad);
    payload(evidence).put("bundle_digest", digest(bundle));
    payload(evidence).put("bundle_version", payload(bundle).path("bundle_version").asText());
    payload(evidence).put("phase", payload(bundle).path("phase").asText());
    payload(evidence).put("control_revision", payload(control).path("control_revision").asText());
    signEvidence();
  }

  SignedTransportTrustBundle transport() {
    return Json.MAPPER.treeToValue(bundle, SignedTransportTrustBundle.class);
  }

  byte[] history() {
    return bytes(Json.object("bundle", bundle, "authority", authority, "control", control));
  }

  IsolationRecord record() {
    return new IsolationRecord()
        .tenantId(tenant.toString())
        .realm("1")
        .targetId("node-target")
        .minimumControlRevision(payload(control).path("control_revision").asText())
        .controlDigest(digest(control))
        .challenge("control-challenge")
        .state(IsolationRecord.StateEnum.CONFIRMED)
        .identityCompromised(false)
        .newChainReady(false);
  }

  MessageNetworkProofVerifier verifier() {
    return new MessageNetworkProofVerifier(spki(root), rootKid(), clock);
  }

  SignedTransportConfirmation verify() {
    return verifier()
        .verify(
            transport(),
            history(),
            bytes(control),
            bytes(evidence),
            record(),
            tenant.toString(),
            "1");
  }

  ExpectedSelectors selectors(ExpectedSelectorsQuery q) {
    var b = payload(bundle);
    return new ExpectedSelectors()
        .tenantId(tenant.toString())
        .realm("1")
        .authorityId(b.path("authority_id").asText())
        .authorityDigest(b.path("authority_digest").asText())
        .generation(b.path("generation").asText())
        .bundleVersion(b.path("bundle_version").asText())
        .bundleDigest(digest(bundle))
        .phase(
            ExpectedSelectors.PhaseEnum.valueOf(b.path("phase").asText().toUpperCase(Locale.ROOT)))
        .controlRevision(payload(control).path("control_revision").asText())
        .controlDigest(digest(control))
        .challenge(q.getChallenge())
        .issuedAt(clock.instant().toString())
        .requiredReaderIds(Set.of(readerId))
        .expectedSelectors(new HashMap<>(authorized))
        .readerIdentities(new HashMap<>(identities));
  }
}
