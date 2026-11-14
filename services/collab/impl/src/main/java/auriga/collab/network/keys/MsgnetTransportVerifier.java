package auriga.collab.network.keys;

import static auriga.collab.network.keys.MsgnetV2Verifier.*;

import java.time.Instant;
import java.util.HashSet;
import java.util.List;
import tools.jackson.databind.JsonNode;

/**
 * K3 transport proofs. Seen values and selector hashes must come from the caller's durable ledger.
 */
public final class MsgnetTransportVerifier {
  public record SeenTransport(String generation, String version, String digest) {}

  private final MsgnetV2Verifier trust;

  public MsgnetTransportVerifier(MsgnetV2Verifier trust) {
    this.trust = trust;
  }

  public void transition(
      JsonNode bundle,
      JsonNode authority,
      JsonNode control,
      JsonNode predecessor,
      SeenTransport seen,
      boolean isolationEnforced) {
    var c = control.path("payload");
    var a = authority.path("payload");
    var p = predecessor.path("bundle").path("payload");
    var t = bundle.path("payload");
    trust.verifyControl(control);
    trust.rootSignature(authority, "NetworkRuntimeAuthority");
    trust.verifyControl(predecessor.path("control"));
    trust.rootSignature(predecessor.path("authority"), "NetworkRuntimeAuthority");
    signedBundle(predecessor.path("bundle"), predecessor.path("authority"));
    signedBundle(bundle, authority);
    for (var value :
        List.of(
            c,
            a,
            p,
            t,
            predecessor.path("control").path("payload"),
            predecessor.path("authority").path("payload")))
      require(trust.scope(value), "scope_denied");
    authorityBinding(bundle, authority);
    require(
        p.path("authority_id")
                .equals(predecessor.path("authority").path("payload").path("authority_id"))
            && text(p, "authority_digest").equals(digest(predecessor.path("authority"))),
        "invalid_chain");
    require(
        allowed(predecessor.path("control").path("payload"), predecessor.path("authority")),
        "authority_unknown");
    require(
        !contains(
            predecessor.path("control").path("payload").path("revoked_authority_ids"),
            text(p, "authority_id")),
        "authority_revoked");
    require(allowed(c, authority), "authority_unknown");
    require(
        !contains(c.path("revoked_authority_ids"), text(t, "authority_id")), "authority_revoked");
    require(
        number(t, "control_revision").compareTo(number(c, "control_revision")) <= 0,
        "control_required");
    capability(a, "network_transport");
    require(!time(a, "not_before").isAfter(time(t, "issued_at")), "scope_denied");
    transportScope(t, a);
    require(
        number(t, "generation").compareTo(number(c, "minimum_generation")) >= 0,
        "version_rollback");
    require(
        digest(predecessor.path("bundle")).equals(text(t, "previous_bundle_digest")),
        "invalid_transition");
    if (!seen.digest().isEmpty()
        && seen.generation().equals(text(p, "generation"))
        && seen.version().equals(text(p, "bundle_version")))
      require(seen.digest().equals(digest(predecessor.path("bundle"))), "invalid_transition");
    if (!seen.version().isEmpty() && seen.generation().equals(text(p, "generation")))
      require(
          integer(seen.version()).compareTo(number(p, "bundle_version")) <= 0, "version_rollback");
    if (!seen.generation().isEmpty())
      require(
          number(t, "generation").compareTo(integer(seen.generation())) >= 0, "version_rollback");
    var anchor = c.path("transport_transition");
    require(anchor.isObject(), "control_required");
    require(
        text(anchor, "predecessor_digest").equals(digest(predecessor.path("bundle")))
            && anchor.path("from_generation").equals(p.path("generation"))
            && anchor.path("to_generation").equals(t.path("generation"))
            && anchor.path("authority_id").equals(t.path("authority_id"))
            && anchor.path("authority_digest").equals(t.path("authority_digest"))
            && anchor.path("first_bundle_version").equals(t.path("bundle_version")),
        "invalid_transition");
    require("dual_trust".equals(text(t, "phase")), "invalid_transition");
    if ("authority_transition".equals(text(anchor, "mode"))) {
      require(t.path("generation").equals(p.path("generation")), "invalid_transition");
      require(
          number(t, "bundle_version").compareTo(number(p, "bundle_version")) > 0,
          "version_rollback");
      require("retire_old".equals(text(p, "phase")), "invalid_transition");
      require(
          !contains(c.path("revoked_authority_ids"), text(p, "authority_id")), "authority_revoked");
      require(allowed(c, predecessor.path("authority")), "authority_unknown");
      require(t.path("scope").equals(p.path("scope")), "scope_denied");
      require(
          t.path("required_reader_ids").equals(p.path("required_reader_ids")),
          "invalid_transition");
      require(list(t.path("cas")).containsAll(list(p.path("cas"))), "invalid_transition");
      require(
          list(t.path("cas")).stream()
              .anyMatch(ca -> find(p.path("cas"), "ca_id", text(ca, "ca_id")) == null),
          "invalid_transition");
    } else {
      require(number(t, "generation").compareTo(number(p, "generation")) > 0, "version_rollback");
      require(
          contains(c.path("revoked_authority_ids"), text(p, "authority_id")), "authority_revoked");
      for (var ca : t.path("cas"))
        for (var old : p.path("cas"))
          require(
              !ca.path("ca_id").equals(old.path("ca_id"))
                  && !ca.path("certificate_der").equals(old.path("certificate_der")),
              "invalid_transition");
      require(isolationEnforced, "identity_quarantined");
    }
    schema("TransportPredecessor", predecessor, "invalid_chain");
    schema("SignedTransportTrustBundle", bundle, "invalid_chain");
  }

  public void confirmation(
      JsonNode evidence,
      JsonNode bundle,
      JsonNode authority,
      JsonNode control,
      String challenge,
      Instant now,
      String requiredPhase,
      JsonNode expectedSelectors) {
    var e = evidence.path("payload");
    var b = bundle.path("payload");
    var a = authority.path("payload");
    var c = control.path("payload");
    trust.verifyControl(control);
    trust.rootSignature(authority, "NetworkRuntimeAuthority");
    signedBundle(bundle, authority);
    signature(
        evidence,
        "Auriga.NetworkTransportConfirmation.v2",
        RootKeyPublication.publicKey(text(a, "public_key_spki")));
    require(allowed(c, authority), "authority_unknown");
    require(
        !contains(c.path("revoked_authority_ids"), text(a, "authority_id")), "authority_revoked");
    capability(a, "network_transport");
    for (var value : List.of(a, b, c)) require(trust.scope(value), "scope_denied");
    require(
        b.path("authority_id").equals(a.path("authority_id"))
            && text(b, "authority_digest").equals(digest(authority))
            && text(evidence.path("runtime_auth"), "kid").equals(text(a, "runtime_kid"))
            && text(e, "authority_digest").equals(digest(authority))
            && e.path("authority_id").equals(a.path("authority_id")),
        "invalid_chain");
    require(e.path("control_revision").equals(c.path("control_revision")), "control_required");
    require(text(e, "bundle_digest").equals(digest(bundle)), "invalid_chain");
    for (String field :
        List.of("tenant_id", "realm", "bundle_version", "phase", "required_reader_ids"))
      require(e.path(field).equals(b.path(field)), "invalid_chain");
    require(challenge.equals(text(e, "challenge")), "invalid_chain");
    require(
        !time(e, "issued_at").isAfter(now) && !time(e, "issued_at").isBefore(now.minusSeconds(60)),
        "projection_pending");
    var readers = new HashSet<String>();
    for (var row : e.path("readers")) require(readers.add(text(row, "reader_id")), "invalid_chain");
    require(readers.size() == b.path("required_reader_ids").size(), "projection_pending");
    for (var id : b.path("required_reader_ids")) {
      var row = find(e.path("readers"), "reader_id", id.asString());
      require(row != null, "projection_pending");
      if ("isolated".equals(text(row, "phase"))) {
        require(row.path("isolation_enforced").asBoolean(), "identity_quarantined");
      } else {
        var selector = row.path("selector");
        require(
            selector.path("installation_id").equals(row.path("identity_id"))
                && selector.path("bundle_digest").equals(e.path("bundle_digest"))
                && selector.path("bundle_version").equals(e.path("bundle_version"))
                && digest(selector).equals(expectedSelectors.path(id.asString()).asString("")),
            "invalid_chain");
        require(
            !"handshake_verified".equals(requiredPhase)
                || "handshake_verified".equals(text(row, "phase")),
            "projection_pending");
      }
    }
    schema("SignedTransportConfirmation", evidence, "invalid_chain");
  }

  /**
   * Verify a root-anchored start, then a later snapshot against the current control and local
   * floor.
   */
  public void catchup(
      JsonNode proof,
      JsonNode current,
      JsonNode control,
      SeenTransport startSeen,
      String seenVersion,
      String seenDigest,
      boolean isolationEnforced,
      JsonNode confirmationContext) {
    transition(
        proof.path("first_bundle"),
        proof.path("authority"),
        proof.path("control"),
        proof.path("predecessor"),
        startSeen,
        isolationEnforced);
    var start = proof.path("first_bundle").path("payload");
    var t = current.path("payload");
    var c = control.path("payload");
    var authority = proof.path("authority");
    trust.verifyControl(control);
    signedBundle(current, authority);
    require(trust.scope(c) && trust.scope(t), "scope_denied");
    require(
        number(c, "control_revision")
                .compareTo(number(proof.path("control").path("payload"), "control_revision"))
            >= 0,
        "control_rollback");
    require(
        !contains(c.path("revoked_authority_ids"), text(authority.path("payload"), "authority_id")),
        "authority_revoked");
    require(allowed(c, authority), "authority_unknown");
    for (String field : List.of("authority_id", "authority_digest", "generation"))
      require(t.path(field).equals(start.path(field)), "invalid_chain");
    require(
        number(t, "control_revision").compareTo(number(c, "control_revision")) <= 0,
        "control_required");
    require(
        number(t, "generation").compareTo(number(c, "minimum_generation")) >= 0
            && number(t, "bundle_version").compareTo(number(start, "bundle_version")) >= 0
            && (seenVersion.isEmpty()
                || number(t, "bundle_version").compareTo(integer(seenVersion)) >= 0),
        "version_rollback");
    if (text(t, "bundle_version").equals(seenVersion))
      require(digest(current).equals(seenDigest), "same_version_conflict");
    transportScope(t, authority.path("payload"));
    if (!"dual_trust".equals(text(t, "phase"))) {
      require(confirmationContext.isObject(), "projection_pending");
      try {
        var i = confirmationContext;
        confirmation(
            i.path("evidence"),
            i.path("bundle"),
            i.path("authority"),
            i.path("control"),
            text(i, "challenge"),
            time(i, "now"),
            text(i, "required_phase"),
            i.path("expected_selectors"));
      } catch (auriga.common.error.AurigaException failure) {
        throw failure("projection_pending");
      }
      require(
          digest(confirmationContext.path("bundle")).equals(text(t, "previous_bundle_digest")),
          "invalid_chain");
      require(
          ("new_leaves".equals(text(t, "phase")) ? "dual_trust" : "new_leaves")
              .equals(text(confirmationContext.path("bundle").path("payload"), "phase")),
          "invalid_transition");
      require(
          ("new_leaves".equals(text(t, "phase")) ? "loaded" : "handshake_verified")
              .equals(text(confirmationContext, "required_phase")),
          "projection_pending");
    }
  }

  private static void signedBundle(JsonNode bundle, JsonNode authority) {
    signature(
        bundle,
        "Auriga.NetworkTransportTrustBundle.v2",
        RootKeyPublication.publicKey(text(authority.path("payload"), "public_key_spki")));
  }

  private static void authorityBinding(JsonNode bundle, JsonNode authority) {
    require(
        bundle
                .path("payload")
                .path("authority_id")
                .equals(authority.path("payload").path("authority_id"))
            && text(bundle.path("payload"), "authority_digest").equals(digest(authority))
            && text(bundle.path("runtime_auth"), "kid")
                .equals(text(authority.path("payload"), "runtime_kid")),
        "invalid_chain");
  }

  private static void transportScope(JsonNode bundle, JsonNode authority) {
    for (var scope : bundle.path("scope"))
      require(list(authority.path("transport_scope")).contains(scope), "scope_denied");
  }
}
