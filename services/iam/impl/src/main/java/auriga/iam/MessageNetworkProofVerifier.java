package auriga.iam;

import auriga.contracts.msgnet.model.*;
import java.math.BigInteger;
import java.nio.charset.StandardCharsets;
import java.security.*;
import java.security.PublicKey;
import java.security.spec.X509EncodedKeySpec;
import java.time.Clock;
import java.time.Instant;
import java.util.*;
import tools.jackson.databind.JsonNode;

/** IAM's independent installation root authenticates the original C documents. */
final class MessageNetworkProofVerifier {
  private final PublicKey root;
  private final String rootSpki, rootKid;
  private final Clock clock;
  private final ContractSchemas schemas = new ContractSchemas();

  MessageNetworkProofVerifier(String rootSpki, String rootKid, Clock clock) {
    this.root = publicKey(rootSpki);
    this.rootSpki = rootSpki;
    if (rootKid == null || rootKid.isBlank() || rootKid.length() > 128)
      throw new IllegalArgumentException("installation root kid required");
    this.rootKid = rootKid;
    this.clock = Objects.requireNonNull(clock);
  }

  SignedTransportConfirmation verify(
      SignedTransportTrustBundle expectedBundle,
      byte[] historyDocument,
      byte[] controlDocument,
      byte[] evidenceDocument,
      IsolationRecord isolation,
      String tenant,
      String realm) {
    var history = document("TransportPredecessor", historyDocument);
    var control = document("SignedRootControl", controlDocument);
    var evidence = document("SignedTransportConfirmation", evidenceDocument);
    var authority = history.path("authority");
    var bundle = history.path("bundle");
    var c = control.path("payload");
    var a = authority.path("payload");
    var b = bundle.path("payload");
    var e = evidence.path("payload");
    signature(control, "root_auth", "Auriga.NetworkRootControl.v2", rootKid, root);
    signature(authority, "root_auth", "Auriga.NetworkRuntimeAuthority.v2", rootKid, root);
    require(
        digest(bundle).equals(MessageNetworkSelectors.digest(expectedBundle)), "bundle mismatch");
    require(
        c.path("control_revision").asText().equals(isolation.getMinimumControlRevision())
            && digest(control).equals(isolation.getControlDigest()),
        "current root control required");
    for (var payload : List.of(c, a, b, e))
      require(
          payload.path("tenant_id").asText().equals(tenant)
              && payload.path("realm").asText().equals(realm),
          "proof scope denied");
    String authorityId = a.path("authority_id").asText(), authorityDigest = digest(authority);
    require(
        containsReference(c.path("allowed_authorities"), authorityId, authorityDigest),
        "authority not allowed");
    require(!contains(c.path("revoked_authority_ids"), authorityId), "authority revoked");
    require(
        contains(a.path("capabilities"), "network_transport"),
        "network_transport capability required");
    require(
        b.path("authority_id").asText().equals(authorityId)
            && b.path("authority_digest").asText().equals(authorityDigest)
            && e.path("authority_id").asText().equals(authorityId)
            && e.path("authority_digest").asText().equals(authorityDigest),
        "authority binding mismatch");
    require(!a.path("public_key_spki").asText().equals(rootSpki), "runtime key reuses root");
    var runtime = publicKey(a.path("public_key_spki").asText());
    String kid = a.path("runtime_kid").asText();
    signature(bundle, "runtime_auth", "Auriga.NetworkTransportTrustBundle.v2", kid, runtime);
    signature(evidence, "runtime_auth", "Auriga.NetworkTransportConfirmation.v2", kid, runtime);
    Instant notBefore = Instant.parse(a.path("not_before").asText());
    require(
        !notBefore.isAfter(clock.instant())
            && !Instant.parse(b.path("issued_at").asText()).isBefore(notBefore)
            && !Instant.parse(e.path("issued_at").asText()).isBefore(notBefore),
        "authority not active");
    require(
        new BigInteger(b.path("generation").asText())
                    .compareTo(new BigInteger(c.path("minimum_generation").asText()))
                >= 0
            && new BigInteger(b.path("control_revision").asText())
                    .compareTo(new BigInteger(c.path("control_revision").asText()))
                <= 0,
        "transport control rollback");
    for (var scope : b.path("scope"))
      require(containsObject(a.path("transport_scope"), scope), "transport scope denied");
    for (var row : e.path("readers"))
      if (!row.path("phase").asText().equals("isolated"))
        require(
            !contains(c.path("revoked_installation_ids"), row.path("identity_id").asText()),
            "reader identity revoked");
    return Json.MAPPER.treeToValue(evidence, SignedTransportConfirmation.class);
  }

  private JsonNode document(String definition, byte[] raw) {
    var json = InstallationJson.parse(raw);
    schemas.validate("network/auto-renewal-v2.schema.json#/$defs/" + definition, json);
    return json;
  }

  private static String digest(JsonNode document) {
    return "sha256:" + Secrets.hash(InstallationJson.canonical(document));
  }

  private static boolean contains(JsonNode array, String value) {
    for (var item : array) if (item.asText().equals(value)) return true;
    return false;
  }

  private static boolean containsReference(JsonNode array, String id, String digest) {
    for (var item : array)
      if (item.path("authority_id").asText().equals(id)
          && item.path("authority_digest").asText().equals(digest)) return true;
    return false;
  }

  private static boolean containsObject(JsonNode array, JsonNode value) {
    for (var item : array) if (item.equals(value)) return true;
    return false;
  }

  private static PublicKey publicKey(String encoded) {
    try {
      byte[] raw = decode(encoded);
      var key = KeyFactory.getInstance("Ed25519").generatePublic(new X509EncodedKeySpec(raw));
      require(Arrays.equals(raw, key.getEncoded()), "noncanonical public key");
      return key;
    } catch (GeneralSecurityException invalid) {
      throw new IllegalArgumentException("independent Ed25519 installation root required", invalid);
    }
  }

  private static byte[] decode(String text) {
    byte[] bytes = Base64.getUrlDecoder().decode(text);
    require(
        Base64.getUrlEncoder().withoutPadding().encodeToString(bytes).equals(text),
        "noncanonical signature encoding");
    return bytes;
  }

  private static void signature(
      JsonNode document, String member, String domain, String kid, PublicKey key) {
    var auth = document.path(member);
    require(
        auth.path("alg").asText().equals("Ed25519") && auth.path("kid").asText().equals(kid),
        "signature role mismatch");
    try {
      var verifier = java.security.Signature.getInstance("Ed25519");
      verifier.initVerify(key);
      verifier.update((domain + "\0").getBytes(StandardCharsets.UTF_8));
      verifier.update(
          InstallationJson.canonical(document.path("payload")).getBytes(StandardCharsets.UTF_8));
      byte[] signature = decode(auth.path("signature").asText());
      require(
          signature.length == 64 && verifier.verify(signature),
          "invalid message network signature");
    } catch (GeneralSecurityException invalid) {
      throw new IllegalStateException("invalid message network signature", invalid);
    }
  }

  private static void require(boolean condition, String message) {
    if (!condition) throw new IllegalStateException(message);
  }
}
