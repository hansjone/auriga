package auriga.collab.network.keys;

import auriga.collab.app.NetworkJson;
import auriga.common.error.AurigaException;
import auriga.common.error.ErrorCode;
import io.grpc.Status;
import java.math.BigInteger;
import java.nio.charset.StandardCharsets;
import java.security.PublicKey;
import java.security.Signature;
import java.time.Instant;
import java.util.ArrayList;
import java.util.Base64;
import java.util.HashSet;
import java.util.List;
import java.util.Set;
import tools.jackson.databind.JsonNode;

/** Installed-root verifier shared by publication, reload and material-export entry points. */
public final class MsgnetV2Verifier {
  static final List<String> PURPOSES =
      List.of("business_delivery", "protocol_response", "management_state");
  private final String tenant;
  private final String realm;
  private final String rootKid;
  private final PublicKey root;

  /** Values loaded from the local durable ledger before validating network input. */
  public record Seen(
      String controlRevision,
      String generation,
      String version,
      String digest,
      String predecessorDigest,
      List<JsonNode> firstPublications) {
    public Seen {
      firstPublications = List.copyOf(firstPublications);
    }

    public static Seen empty() {
      return new Seen("0", "0", "0", "", "", List.of());
    }
  }

  public MsgnetV2Verifier(String tenant, String realm, String rootKid, PublicKey root) {
    this.tenant = java.util.Objects.requireNonNull(tenant);
    this.realm = java.util.Objects.requireNonNull(realm);
    this.rootKid = java.util.Objects.requireNonNull(rootKid);
    this.root = java.util.Objects.requireNonNull(root);
  }

  public void verifyControl(JsonNode signed) {
    rootSignature(signed, "NetworkRootControl");
    require(scope(signed.path("payload")), "scope_denied");
    schema("SignedRootControl", signed, "invalid_chain");
  }

  public void verifyAuthority(JsonNode authority, JsonNode control, Instant now) {
    verifyControl(control);
    rootSignature(authority, "NetworkRuntimeAuthority");
    var a = authority.path("payload");
    var c = control.path("payload");
    require(
        !contains(c.path("revoked_authority_ids"), text(a, "authority_id")), "authority_revoked");
    require(allowed(c, authority), "authority_unknown");
    require(
        scope(a)
            && "no_hard_expiry".equals(text(a, "expiry_policy"))
            && !time(a, "not_before").isAfter(now),
        "scope_denied");
    schema("SignedRuntimeAuthority", authority, "invalid_chain");
  }

  public void verify(JsonNode publication, JsonNode latestControl, Seen seen, Instant now) {
    var keyset = publication.path("keyset");
    var k = keyset.path("payload");
    var authority = publication.path("authority");
    var a = authority.path("payload");
    var c = latestControl.path("payload");
    rootSignature(latestControl, "NetworkRootControl");
    rootSignature(publication.path("control"), "NetworkRootControl");
    rootSignature(authority, "NetworkRuntimeAuthority");
    require(
        !contains(c.path("revoked_authority_ids"), text(a, "authority_id")), "authority_revoked");
    require(allowed(c, authority), "authority_unknown");
    require(
        k.path("control_revision")
            .equals(publication.path("control").path("payload").path("control_revision")),
        "invalid_chain");
    require(
        number(k, "control_revision").compareTo(number(c, "control_revision")) <= 0,
        "control_required");
    require(
        number(c, "control_revision").compareTo(integer(seen.controlRevision())) >= 0,
        "control_rollback");
    require(
        "no_hard_expiry".equals(text(a, "expiry_policy"))
            && !a.has("not_after")
            && !a.has("delegate"),
        "scope_denied");
    capability(a, "keyset");
    if ("restart_recovery".equals(text(k, "mode"))) capability(a, "restart_recovery");
    require(
        scope(k)
            && scope(a)
            && scope(c)
            && k.path("authority_id").equals(a.path("authority_id"))
            && text(k, "authority_digest").equals(MsgnetV2Verifier.digest(authority))
            && !time(a, "not_before").isAfter(now),
        "scope_denied");
    require(
        k.path("generation").equals(c.path("minimum_generation"))
            && number(k, "keyset_version").compareTo(number(c, "minimum_keyset_version")) >= 0,
        "version_rollback");
    int generationOrder = number(k, "generation").compareTo(integer(seen.generation()));
    require(
        generationOrder >= 0
            && (generationOrder > 0
                || number(k, "keyset_version").compareTo(integer(seen.version())) >= 0),
        "version_rollback");
    if (generationOrder == 0 && text(k, "keyset_version").equals(seen.version()))
      require(MsgnetV2Verifier.digest(keyset).equals(seen.digest()), "same_version_conflict");
    require(
        text(keyset.path("runtime_auth"), "kid").equals(text(a, "runtime_kid")), "scope_denied");
    signature(
        keyset,
        "Auriga.NetworkKeyset.v2",
        RootKeyPublication.publicKey(text(a, "public_key_spki")));
    Instant issued = time(k, "issued_at");
    require(
        !issued.isAfter(now.plusSeconds(60))
            && time(k, "valid_until").isAfter(now)
            && validLifetime(k, a),
        "invalid_transition");
    String mode = text(k, "mode");
    if ("bootstrap".equals(mode))
      require(
          ("0".equals(seen.version())
                  || (text(k, "generation").equals(seen.generation())
                      && text(k, "keyset_version").equals(seen.version())
                      && MsgnetV2Verifier.digest(keyset).equals(seen.digest())))
              && !publication.has("predecessor"),
          "invalid_transition");
    require(
        Set.of("bootstrap", "migration").contains(mode) || publication.has("predecessor"),
        "predecessor_required");
    JsonNode previous = null;
    if (publication.has("predecessor")) previous = predecessor(publication, c, seen);
    transitions(k, previous, a, c, publication, seen);
    schema("Publication", publication, "invalid_chain");
    schema("SignedRootControl", latestControl, "invalid_chain");
  }

  private JsonNode predecessor(JsonNode p, JsonNode latest, Seen seen) {
    var q = p.path("predecessor");
    var old = q.path("keyset").path("payload");
    var next = p.path("keyset").path("payload");
    require(
        MsgnetV2Verifier.digest(q.path("keyset")).equals(text(q, "keyset_digest"))
            && (!old.path("generation").equals(next.path("generation"))
                || number(old, "keyset_version").compareTo(number(next, "keyset_version")) < 0),
        "invalid_transition");
    rootSignature(q.path("control"), "NetworkRootControl");
    rootSignature(q.path("authority"), "NetworkRuntimeAuthority");
    var a = q.path("authority").path("payload");
    var c = q.path("control").path("payload");
    require(
        text(old, "authority_digest").equals(MsgnetV2Verifier.digest(q.path("authority")))
            && old.path("authority_id").equals(a.path("authority_id"))
            && old.path("control_revision").equals(c.path("control_revision"))
            && scope(old)
            && scope(a)
            && scope(c)
            && allowed(c, q.path("authority"))
            && !contains(c.path("revoked_authority_ids"), text(a, "authority_id"))
            && text(q.path("keyset").path("runtime_auth"), "kid").equals(text(a, "runtime_kid")),
        "invalid_chain");
    signature(
        q.path("keyset"),
        "Auriga.NetworkKeyset.v2",
        RootKeyPublication.publicKey(text(a, "public_key_spki")));
    require(
        contains(a.path("capabilities"), "keyset")
            && !time(a, "not_before").isAfter(time(old, "issued_at"))
            && validLifetime(old, a)
            && number(old, "generation").compareTo(number(c, "minimum_generation")) >= 0
            && number(old, "keyset_version").compareTo(number(c, "minimum_keyset_version")) >= 0,
        "invalid_chain");
    boolean revoked = contains(latest.path("revoked_authority_ids"), text(a, "authority_id"));
    if (!old.path("generation").equals(next.path("generation")) || revoked) {
      var t = latest.path("generation_transition");
      require(
          "authority_transition".equals(text(next, "mode"))
              && t.isObject()
              && t.path("predecessor_digest").equals(q.path("keyset_digest"))
              && t.path("from_generation").equals(old.path("generation"))
              && t.path("to_generation").equals(next.path("generation"))
              && t.path("authority_id").equals(next.path("authority_id"))
              && t.path("authority_digest").equals(next.path("authority_digest"))
              && t.path("first_version").equals(next.path("keyset_version"))
              && t.path("first_version").equals(latest.path("minimum_keyset_version"))
              && number(next, "generation").compareTo(number(old, "generation")) > 0,
          "invalid_transition");
      if (revoked) {
        for (var key : old.path("keys")) {
          if (Set.of("active", "next").contains(text(key, "state"))) {
            var replacement = find(next.path("keys"), "kid", text(key, "kid"));
            require(
                replacement != null && "revoked".equals(text(replacement, "state")),
                "invalid_transition");
          }
        }
        for (var key : next.path("keys"))
          if ("active".equals(text(key, "state")))
            require(find(old.path("keys"), "kid", text(key, "kid")) == null, "invalid_transition");
      }
    } else if ("authority_transition".equals(text(next, "mode"))) {
      require(!old.path("authority_id").equals(next.path("authority_id")), "invalid_transition");
    }
    require(
        seen.predecessorDigest().isEmpty()
            || seen.predecessorDigest().equals(text(q, "keyset_digest")),
        "invalid_transition");
    return old;
  }

  private void transitions(
      JsonNode k, JsonNode previous, JsonNode a, JsonNode c, JsonNode p, Seen seen) {
    Instant issued = time(k, "issued_at");
    String mode = text(k, "mode");
    List<JsonNode> oldKeys = previous == null ? List.of() : list(previous.path("keys"));
    var keys = k.path("keys");
    for (var key : keys) {
      if (find(oldKeys, "kid", text(key, "kid")) == null) {
        capability(a, "business_rotation");
        var proof = find(k.path("first_publications"), "kid", text(key, "kid"));
        require(
            proof != null
                && proof.path("committed_version").equals(k.path("keyset_version"))
                && proof.path("committed_at").equals(k.path("issued_at")),
            "invalid_transition");
      }
    }
    for (var key : oldKeys) {
      if (find(keys, "kid", text(key, "kid")) == null) {
        capability(a, "history_archive");
        require(
            !time(k.path("archive"), "before")
                    .isAfter(
                        issued.minusSeconds(
                            a.path("limits").path("archive_grace_seconds").asLong()))
                && !time(key, "not_after").isAfter(time(k.path("archive"), "before")),
            "invalid_transition");
      }
    }
    List<JsonNode> snapshots = new ArrayList<>();
    if (previous != null) snapshots.add(previous);
    snapshots.add(k);
    for (var snapshot : snapshots) {
      for (var proof : snapshot.path("first_publications")) {
        var known = find(seen.firstPublications(), "kid", text(proof, "kid"));
        require(known == null || known.equals(proof), "invalid_transition");
        require(
            number(proof, "committed_version").compareTo(number(snapshot, "keyset_version")) <= 0
                && !time(proof, "committed_at").isAfter(time(snapshot, "issued_at")),
            "invalid_transition");
      }
    }
    var ids = new HashSet<String>();
    var material = new HashSet<String>();
    for (var key : keys) {
      require(
          !text(key, "public_key_spki").equals(text(a, "public_key_spki"))
              && !text(key, "public_key_spki")
                  .equals(
                      Base64.getUrlEncoder().withoutPadding().encodeToString(root.getEncoded())),
          "invalid_transition");
      require(
          ids.add(text(key, "kid")) && material.add(text(key, "public_key_spki")),
          "invalid_transition");
      require(
          time(key, "not_after").isAfter(time(key, "not_before"))
              && !time(key, "not_after")
                  .isAfter(
                      time(key, "not_before")
                          .plusSeconds(a.path("limits").path("business_key_seconds").asLong())),
          "invalid_transition");
      var old = find(oldKeys, "kid", text(key, "kid"));
      if (old != null) {
        for (String field : List.of("alg", "public_key_spki", "purpose", "not_before", "not_after"))
          require(old.path(field).equals(key.path(field)), "invalid_transition");
        if (Set.of("retired", "revoked").contains(text(old, "state")))
          require(old.path("state").equals(key.path("state")), "invalid_transition");
      }
    }
    boolean recoveryNeeded = false;
    for (String purpose : PURPOSES) {
      List<JsonNode> active = new ArrayList<>();
      for (var key : keys)
        if (purpose.equals(text(key, "purpose")) && "active".equals(text(key, "state")))
          active.add(key);
      require(active.size() == 1, "invalid_transition");
      var next = active.getFirst();
      require(valid(next, issued), "invalid_transition");
      JsonNode old = null;
      JsonNode mature = null;
      for (var key : oldKeys) {
        if (!purpose.equals(text(key, "purpose"))) continue;
        if ("active".equals(text(key, "state"))) old = key;
        if ("next".equals(text(key, "state")) && valid(key, issued)) {
          var proof = find(previous.path("first_publications"), "kid", text(key, "kid"));
          if (proof != null
              && !time(proof, "committed_at")
                  .isAfter(
                      issued.minusSeconds(a.path("limits").path("next_maturity_seconds").asLong())))
            mature = key;
        }
      }
      boolean same = old != null && old.path("kid").equals(next.path("kid"));
      if ("refresh".equals(mode)) require(same, "invalid_transition");
      if ("authority_transition".equals(mode)
          && previous.path("generation").equals(k.path("generation"))
          && !contains(
              c.path("revoked_authority_ids"),
              text(p.path("predecessor").path("authority").path("payload"), "authority_id")))
        require(same, "invalid_transition");
      if ("activate".equals(mode) && !same) {
        require(
            mature != null
                && mature.path("kid").equals(next.path("kid"))
                && (old == null
                    || !valid(old, issued)
                    || !time(old, "not_after")
                        .isBefore(
                            issued.plusSeconds(a.path("limits").path("overlap_seconds").asLong()))),
            "invalid_transition");
      }
      if ("restart_recovery".equals(mode)) {
        if (old != null && valid(old, issued)) require(same, "invalid_transition");
        else {
          recoveryNeeded = true;
          require(
              mature == null
                  && find(oldKeys, "kid", text(next, "kid")) == null
                  && time(next, "not_before").equals(issued)
                  && !time(next, "not_after")
                      .isAfter(
                          issued.plusSeconds(
                              a.path("limits").path("restart_key_seconds").asLong())),
              "invalid_transition");
        }
      }
    }
    if ("restart_recovery".equals(mode)) require(recoveryNeeded, "invalid_transition");
  }

  public void verifyTransport(
      JsonNode bundle,
      JsonNode authority,
      String seenVersion,
      String previousPhase,
      boolean readersPersisted,
      boolean handshakesVerified) {
    var a = authority.path("payload");
    capability(a, "network_transport");
    signature(
        bundle,
        "Auriga.NetworkTransportTrustBundle.v2",
        RootKeyPublication.publicKey(text(a, "public_key_spki")));
    var t = bundle.path("payload");
    require(text(t, "authority_digest").equals(MsgnetV2Verifier.digest(authority)), "scope_denied");
    for (var scope : t.path("scope"))
      require(list(a.path("transport_scope")).contains(scope), "scope_denied");
    require(number(t, "bundle_version").compareTo(integer(seenVersion)) > 0, "version_rollback");
    var phases = List.of("dual_trust", "new_leaves", "retire_old");
    if (previousPhase != null && !previousPhase.isEmpty())
      require(
          phases.indexOf(text(t, "phase")) == phases.indexOf(previousPhase) + 1,
          "invalid_transition");
    if ("new_leaves".equals(text(t, "phase"))) require(readersPersisted, "projection_pending");
    if ("retire_old".equals(text(t, "phase"))) require(handshakesVerified, "projection_pending");
    schema("SignedTransportTrustBundle", bundle, "invalid_chain");
  }

  private void rootSignature(JsonNode signed, String domain) {
    require(rootKid.equals(text(signed.path("root_auth"), "kid")), "invalid_chain");
    signature(signed, "Auriga." + domain + ".v2", root);
  }

  static void signature(JsonNode signed, String domain, PublicKey key) {
    var auth = signed.has("root_auth") ? signed.path("root_auth") : signed.path("runtime_auth");
    require("Ed25519".equals(text(auth, "alg")), "invalid_chain");
    try {
      String encoded = text(auth, "signature");
      byte[] bytes = Base64.getUrlDecoder().decode(encoded);
      require(
          bytes.length == 64
              && Base64.getUrlEncoder().withoutPadding().encodeToString(bytes).equals(encoded),
          "invalid_chain");
      var verifier = Signature.getInstance("Ed25519");
      verifier.initVerify(key);
      verifier.update(
          (domain + "\0" + NetworkJson.canonical(signed.path("payload"), 1048576))
              .getBytes(StandardCharsets.UTF_8));
      require(verifier.verify(bytes), "invalid_chain");
    } catch (java.security.GeneralSecurityException | IllegalArgumentException e) {
      throw failure("invalid_chain");
    }
  }

  private boolean scope(JsonNode node) {
    return tenant.equals(text(node, "tenant_id")) && realm.equals(text(node, "realm"));
  }

  private static boolean validLifetime(JsonNode k, JsonNode a) {
    return time(k, "valid_until").isAfter(time(k, "issued_at"))
        && !time(k, "valid_until")
            .isAfter(
                time(k, "issued_at").plusSeconds(a.path("limits").path("keyset_seconds").asLong()));
  }

  private static boolean allowed(JsonNode control, JsonNode authority) {
    for (var entry : control.path("allowed_authorities"))
      if (entry.path("authority_id").equals(authority.path("payload").path("authority_id"))
          && text(entry, "authority_digest").equals(MsgnetV2Verifier.digest(authority)))
        return true;
    return false;
  }

  static void capability(JsonNode authority, String name) {
    require(contains(authority.path("capabilities"), name), "scope_denied");
  }

  static boolean valid(JsonNode key, Instant at) {
    return !time(key, "not_before").isAfter(at) && time(key, "not_after").isAfter(at);
  }

  static String text(JsonNode node, String field) {
    return node.path(field).asString("");
  }

  static String digest(JsonNode node) {
    return NetworkJson.digest(node, 1048576);
  }

  static Instant time(JsonNode node, String field) {
    return Instant.parse(text(node, field));
  }

  static BigInteger number(JsonNode node, String field) {
    return integer(text(node, field));
  }

  static BigInteger integer(String value) {
    require(value.matches("0|[1-9][0-9]{0,19}"), "invalid_chain");
    var result = new BigInteger(value);
    require(result.bitLength() <= 64, "invalid_chain");
    return result;
  }

  static List<JsonNode> list(JsonNode node) {
    var result = new ArrayList<JsonNode>();
    node.forEach(result::add);
    return result;
  }

  static JsonNode find(Iterable<JsonNode> nodes, String field, String value) {
    for (var n : nodes) if (value.equals(text(n, field))) return n;
    return null;
  }

  static boolean contains(JsonNode array, String value) {
    for (var item : array) if (value.equals(item.asString())) return true;
    return false;
  }

  static void schema(String definition, JsonNode value, String error) {
    try {
      MsgnetSchema.validate(definition, value);
    } catch (IllegalArgumentException e) {
      throw failure(error);
    }
  }

  static void require(boolean condition, String error) {
    if (!condition) throw failure(error);
  }

  static AurigaException failure(String error) {
    int httpStatus =
        switch (error) {
          case "scope_denied", "authority_revoked", "authority_unknown", "identity_quarantined" ->
              403;
          case "clock_error", "projection_pending" -> 503;
          case "invalid_chain", "invalid_transition", "predecessor_required" -> 400;
          default -> 409;
        };
    var grpc =
        switch (httpStatus) {
          case 403 -> Status.Code.PERMISSION_DENIED;
          case 503 -> Status.Code.UNAVAILABLE;
          case 400 -> Status.Code.INVALID_ARGUMENT;
          default -> Status.Code.FAILED_PRECONDITION;
        };
    return new AurigaException(ErrorCode.of("collab.msgnet_" + error, httpStatus, grpc));
  }
}
