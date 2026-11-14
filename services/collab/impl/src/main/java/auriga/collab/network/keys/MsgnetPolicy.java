package auriga.collab.network.keys;

import static auriga.collab.network.keys.MsgnetV2Verifier.*;

import java.security.PublicKey;
import java.time.Instant;
import java.util.ArrayList;
import java.util.List;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.json.JsonMapper;
import tools.jackson.databind.node.ObjectNode;

/**
 * Checks performed before committing control receipts, material requests and migration barriers.
 */
public final class MsgnetPolicy {
  private MsgnetPolicy() {}

  /** Installation facts are supplied by the OS adapter, never by a projection request. */
  public static void projectionAccess(
      String component,
      long projectorUid,
      long readerUid,
      long peerUid,
      long ownerUid,
      long directoryMode,
      long fileMode,
      boolean privateMount,
      boolean dropAll) {
    require(List.of("network", "edge", "broker").contains(component), "scope_denied");
    long expected = "edge".equals(component) ? 65532 : 10001;
    require(
        projectorUid == expected
            && readerUid == expected
            && peerUid == expected
            && ownerUid == expected
            && directoryMode == 0700
            && fileMode == 0600
            && privateMount
            && dropAll,
        "scope_denied");
  }

  /** Bind the independent confirmation identity to an authenticated NodeControl connection. */
  public static void nodeControlBinding(
      JsonNode pin,
      String authenticatedNodeId,
      String targetId,
      String identityId,
      String revision,
      boolean compromised,
      boolean privateMaterial,
      boolean independentlyEnrolled) {
    require(
        authenticatedNodeId.equals(text(pin, "node_id"))
            && targetId.equals(text(pin, "target_id"))
            && identityId.equals(text(pin, "identity_id")),
        "scope_denied");
    require(revision.equals(text(pin, "identity_revision")), "version_rollback");
    require(!compromised, "identity_quarantined");
    require(!privateMaterial, "scope_denied");
    require(independentlyEnrolled, "identity_quarantined");
  }

  public static void config(JsonNode config) {
    schema("RenewalConfig", config, "invalid_transition");
    var reminders = new ArrayList<Long>();
    config.path("review_reminder_days").forEach(n -> reminders.add(n.asLong()));
    reminders.sort(java.util.Comparator.reverseOrder());
    require(
        config.path("review_period_days").asLong() == 365
            && config.path("refresh_seconds").asLong() < config.path("keyset_seconds").asLong()
            && config.path("rotation_seconds").asLong()
                    + config.path("preannounce_seconds").asLong()
                <= config.path("business_key_seconds").asLong()
            && config.path("preannounce_seconds").asLong()
                >= config.path("maturity_seconds").asLong()
            && config.path("leaf_renew_before_days").asLong() < config.path("leaf_days").asLong()
            && config.path("business_key_seconds").asLong()
                    - config.path("rotation_seconds").asLong()
                >= config.path("overlap_seconds").asLong()
            && config.path("ca_renew_before_years").asLong() < config.path("ca_years").asLong()
            && reminders.size() >= 3
            && reminders.get(0) >= 30
            && reminders.get(1) >= 7
            && reminders.get(2) >= 1,
        "invalid_transition");
  }

  public static void identitySignature(JsonNode object, String domain, PublicKey identity) {
    var body = ((ObjectNode) object).deepCopy();
    body.remove("identity_auth");
    var signed = JsonMapper.builder().build().createObjectNode();
    signed.set("payload", body);
    signed.set("runtime_auth", object.path("identity_auth"));
    signature(signed, domain, identity);
  }

  public static void authorizedConfig(JsonNode config, JsonNode authority) {
    var limits = authority.path("payload").path("limits");
    long business =
        Math.min(
            config.path("business_key_seconds").asLong(),
            limits.path("business_key_seconds").asLong());
    require(
        config.path("refresh_seconds").asLong()
                < Math.min(
                    config.path("keyset_seconds").asLong(), limits.path("keyset_seconds").asLong())
            && config.path("rotation_seconds").asLong()
                    + config.path("preannounce_seconds").asLong()
                <= business
            && config.path("preannounce_seconds").asLong()
                >= Math.max(
                    config.path("maturity_seconds").asLong(),
                    limits.path("next_maturity_seconds").asLong())
            && business - config.path("rotation_seconds").asLong()
                >= Math.max(
                    config.path("overlap_seconds").asLong(),
                    limits.path("overlap_seconds").asLong()),
        "scope_denied");
  }

  public static void receipt(
      JsonNode request,
      JsonNode receipt,
      PublicKey identity,
      String identityId,
      boolean persisted,
      boolean usedChallenge,
      boolean exactReplay,
      boolean compromised,
      boolean newChainReady) {
    identitySignature(receipt, "Auriga.NetworkControlReceipt.v2", identity);
    for (String f :
        List.of("package_id", "tenant_id", "realm", "target_id", "challenge", "control_digest"))
      require(receipt.path(f).equals(request.path(f)), "invalid_transition");
    require(identityId.equals(text(receipt, "identity_id")), "scope_denied");
    require(
        receipt
                .path("control_revision")
                .equals(request.path("control").path("payload").path("control_revision"))
            && "installed".equals(text(receipt, "result"))
            && persisted,
        "invalid_transition");
    require(!usedChallenge || exactReplay, "invalid_transition");
    require(!compromised && newChainReady, "identity_quarantined");
    schema("InstallationReceipt", receipt, "invalid_chain");
  }

  public static void projection(
      JsonNode request,
      JsonNode response,
      PublicKey identity,
      boolean usedNonce,
      List<String> exportedPurposes) {
    identitySignature(request, "Auriga.NetworkProjectionRequest.v2", identity);
    for (String f : List.of("tenant_id", "realm", "installation_id", "component", "nonce"))
      require(request.path(f).equals(response.path(f)), "invalid_transition");
    require(!usedNonce, "invalid_transition");
    require(
        request.path("expected_generation").equals(response.path("generation"))
            && request.path("expected_keyset_version").equals(response.path("keyset_version")),
        "version_rollback");
    require(
        "network".equals(text(request, "component"))
            && exportedPurposes.stream().allMatch("protocol_response"::equals),
        "scope_denied");
    schema("ProjectionRequest", request, "invalid_chain");
    schema("ProjectionMaterial", response, "invalid_chain");
  }

  public static void announcement(JsonNode announcement, JsonNode publication, boolean persisted) {
    var a = publication.path("authority").path("payload");
    var k = publication.path("keyset").path("payload");
    var n = announcement.path("payload");
    signature(
        announcement,
        "Auriga.NetworkKeysetAnnouncement.v2",
        RootKeyPublication.publicKey(text(a, "public_key_spki")));
    capability(a, "announcement");
    require(
        text(n, "authority_digest").equals(MsgnetV2Verifier.digest(publication.path("authority")))
            && n.path("realm").equals(k.path("realm"))
            && n.path("tenant_id").equals(k.path("tenant_id")),
        "scope_denied");
    require(persisted, "projection_pending");
    int generation = number(n, "generation").compareTo(number(k, "generation"));
    require(
        generation <= 0
            && (generation != 0
                || number(n, "keyset_version").compareTo(number(k, "keyset_version")) <= 0),
        "projection_pending");
    require(
        n.path("generation").equals(k.path("generation"))
            && n.path("authority_id").equals(k.path("authority_id"))
            && n.path("control_revision").equals(k.path("control_revision"))
            && text(announcement.path("runtime_auth"), "kid").equals(text(a, "runtime_kid"))
            && n.path("keyset_version").equals(k.path("keyset_version"))
            && text(n, "keyset_digest").equals(MsgnetV2Verifier.digest(publication.path("keyset")))
            && n.path("valid_until").equals(k.path("valid_until")),
        "invalid_chain");
    schema("SignedKeysetAnnouncementV2", announcement, "invalid_chain");
  }

  public static void migration(
      JsonNode permit,
      PublicKey root,
      JsonNode members,
      String currentVersion,
      String currentDigest,
      long frozenSeconds,
      long remainingSeconds,
      String firstV2Version,
      boolean committed,
      boolean legacyDisabled) {
    signature(permit, "Auriga.NetworkMigrationPermit.v2", root);
    var p = permit.path("payload");
    boolean member = false;
    for (var m : members)
      if (currentVersion.equals(text(m, "version")) && currentDigest.equals(text(m, "pair_digest")))
        member = true;
    require(
        MsgnetV2Verifier.digest(members).equals(text(p, "queue_commitment_digest"))
            && member
            && integer(currentVersion).compareTo(number(p, "maximum_committed_v1_version")) <= 0,
        "migration_outside_commitment");
    require(frozenSeconds <= 600 && remainingSeconds >= 21600, "migration_expired");
    require(
        integer(firstV2Version).compareTo(number(p, "minimum_v2_version")) >= 0
            && (!committed || legacyDisabled),
        "invalid_transition");
    schema("SignedMigrationPermit", permit, "invalid_chain");
  }

  public static void archive(
      Instant issued,
      Instant before,
      long grace,
      JsonNode removed,
      JsonNode tombstones,
      JsonNode reappeared) {
    require(!before.isAfter(issued.minusSeconds(grace)), "invalid_transition");
    for (var key : removed) require(!time(key, "not_after").isAfter(before), "invalid_transition");
    for (var key : reappeared) {
      var known = find(tombstones, "kid", text(key, "kid"));
      require(
          known == null || (known.equals(key) && !"active".equals(text(key, "terminal_state"))),
          "invalid_transition");
    }
  }
}
