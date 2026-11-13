package auriga.resource.app;

import static auriga.resource.web.ResourceBody.JSON;

import com.networknt.schema.*;
import java.nio.charset.StandardCharsets;
import java.security.*;
import java.security.spec.X509EncodedKeySpec;
import java.time.Instant;
import java.util.*;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** Independent verification of frozen cross-protocol proofs. P1 never enables plugin v2. */
public final class CatalogTrust {
  private final JsonNode trust;
  private final SchemaRegistry schemas =
      SchemaRegistry.withDefaultDialect(
          SpecificationVersion.DRAFT_2020_12,
          b ->
              b.schemaIdResolvers(
                      r ->
                          r.mapPrefix(
                              "https://auriga.example.com/contracts/",
                              "classpath:auriga/contracts/"))
                  .schemaRegistryConfig(
                      SchemaRegistryConfig.builder().formatAssertionsEnabled(true).build()));

  public CatalogTrust(JsonNode trust) {
    this.trust = trust.deepCopy();
  }

  private static void require(boolean value, String reason) {
    if (!value) throw new IllegalArgumentException(reason);
  }

  private void schema(String file, String name, JsonNode n) {
    require(
        schemas
            .getSchema(
                SchemaLocation.of(
                    "classpath:auriga/contracts/resource/" + file + ".schema.json#/$defs/" + name))
            .validate(n)
            .isEmpty(),
        "schema");
  }

  private boolean signed(JsonNode n, String purpose) {
    try {
      var signature = n.path("signature");
      var root = trust.path(purpose);
      if (!signature.path("kid").equals(root.path("kid"))
          || !signature.path("alg").asString().equals("Ed25519")) return false;
      String encoded = signature.path("value").asString();
      byte[] raw = Base64.getUrlDecoder().decode(encoded);
      if (raw.length != 64
          || !Base64.getUrlEncoder().withoutPadding().encodeToString(raw).equals(encoded))
        return false;
      var key =
          KeyFactory.getInstance("Ed25519")
              .generatePublic(
                  new X509EncodedKeySpec(
                      Base64.getDecoder().decode(root.path("publicKeySpkiBase64").asString())));
      var body = (ObjectNode) n.deepCopy();
      body.remove("signature");
      var verifier = Signature.getInstance("Ed25519");
      verifier.initVerify(key);
      verifier.update(ResourceSigning.canonical(body).getBytes(StandardCharsets.UTF_8));
      return verifier.verify(raw);
    } catch (Exception e) {
      return false;
    }
  }

  private static long time(JsonNode n, String k) {
    return Instant.parse(n.path(k).asString()).toEpochMilli();
  }

  private static boolean window(long start, long end, long now, long max) {
    return start <= now && now < end && end > start && end - start <= max;
  }

  private static boolean same(JsonNode a, JsonNode b, String... names) {
    for (String n : names) if (!a.path(n).equals(b.path(n))) return false;
    return true;
  }

  private static boolean contains(JsonNode a, JsonNode n) {
    for (var value : a) if (value.equals(n)) return true;
    return false;
  }

  private void release(JsonNode input) {
    var e = input.path("envelope");
    boolean old = input.path("protocol").asInt() == 1;
    schema(old ? "signatures" : "package", old ? "ResourceEnvelope" : "Envelope", e);
    require(signed(e, "release"), "signature");
    var s = e.path("statement");
    require(
        window(time(s, "issuedAt"), time(e, "notAfter"), time(input, "now"), 86400000),
        "release_expired");
    var expected = input.path("expected");
    require(
        same(expected, e, "tenantId", "packageId", "versionId")
            && (old
                ? expected.path("kind").asString().equals("plugin")
                : expected.path("kind").equals(s.path("kind"))),
        "identity");
    if (old) {
      require(
          CatalogManifest.digest(
                  Base64.getDecoder().decode(input.path("rawArchiveBase64").asString()))
              .equals(s.path("digest").asString()),
          "manifest_binding");
      return;
    }
    String raw = input.path("rawManifest").asString();
    var m = JSON.readTree(raw);
    require(
        schemas
            .getSchema(
                SchemaLocation.of(
                    "classpath:auriga/contracts/resource/package.schema.json#/$defs/Manifest"))
            .validate(m)
            .isEmpty(),
        "manifest_schema");
    require(
        CatalogManifest.digest(raw.getBytes(StandardCharsets.UTF_8))
                .equals(s.path("manifestDigest").asString())
            && same(m, s, "kind", "name", "version"),
        "manifest_binding");
    var digests = JSON.createArrayNode();
    CatalogManifest.artifactDigests(m).forEach(digests::add);
    require(digests.equals(s.path("artifactDigests")), "manifest_binding");
    for (var artifact : m.path("artifacts")) {
      byte[] bytes =
          Base64.getDecoder()
              .decode(
                  input.path("artifactBytes").path(artifact.path("id").asString()).asString(""));
      require(
          bytes.length == artifact.path("sizeBytes").asLong()
              && CatalogManifest.digest(bytes).equals(artifact.path("digest").asString()),
          "artifact_binding");
    }
  }

  private void migration(JsonNode input) {
    var p = input.path("proof");
    var a = p.path("legacyEnvelope");
    var e = p.path("unifiedEnvelope");
    var marker = p.path("marker");
    schema("unified", "UnifiedMigrationProof", p);
    require(signed(marker, "release"), "migration_signature");
    require(
        same(a, e, "tenantId", "packageId", "versionId", "releaseSequence")
            && same(marker, e, "tenantId", "packageId")
            && p.path("activatedAt").equals(marker.path("activatedAt"))
            && time(marker, "activatedAt") <= time(input, "now"),
        "migration_binding");
    require(
        marker.path("firstReleaseSequence").asLong() <= e.path("releaseSequence").asLong()
            && (marker.path("firstReleaseSequence").asLong() != e.path("releaseSequence").asLong()
                || marker.path("firstVersionId").equals(e.path("versionId"))),
        "migration_binding");
    var expected = JSON.createObjectNode();
    for (String k : List.of("tenantId", "packageId", "versionId")) expected.set(k, e.path(k));
    expected.put("kind", "plugin");
    var old = (ObjectNode) input.deepCopy();
    old.put("protocol", 1);
    old.set("envelope", a);
    old.set("expected", expected);
    release(old);
    var next = (ObjectNode) input.deepCopy();
    next.put("protocol", 2);
    next.set("envelope", e);
    next.set("expected", expected);
    release(next);
    var m = JSON.readTree(input.path("rawManifest").asString());
    boolean artifact = false;
    for (var x : m.path("artifacts"))
      if (x.path("id").equals(p.path("legacyArtifactId"))
          && x.path("role").asString().equals("payload")
          && x.path("digest").equals(a.path("statement").path("digest"))) artifact = true;
    require(
        e.path("statement").path("kind").asString().equals("plugin")
            && m.path("plugin").path("packageName").equals(a.path("statement").path("name"))
            && a.path("statement").path("version").equals(e.path("statement").path("version"))
            && input
                .path("previousManifestDigest")
                .equals(e.path("statement").path("manifestDigest"))
            && artifact,
        "migration_binding");
  }

  public void verify(String operation, JsonNode input) {
    switch (operation) {
      case "release" -> release(input);
      case "migration" -> migration(input);
      case "revocation" -> {
        var r = input.path("release");
        release(r);
        var list = input.path("list");
        var e = r.path("envelope");
        schema("signatures", "RevocationList", list);
        require(signed(list, "revocation"), "revocation_signature");
        require(list.path("tenantId").equals(e.path("tenantId")), "revocation_tenant");
        require(
            window(time(list, "issuedAt"), time(list, "expiresAt"), time(r, "now"), 86400000),
            "revocation_expired");
        var s = e.path("statement");
        boolean old = e.path("schemaVersion").asInt() == 1;
        boolean revoked = contains(list.path("revokedKids"), e.path("signature").path("kid"));
        for (var a : list.path("revokedArtifacts"))
          revoked |=
              old
                  ? same(a, e, "packageId", "versionId")
                      && a.path("digest").equals(s.path("digest"))
                  : same(a, e, "packageId", "versionId")
                      || a.path("digest").equals(s.path("manifestDigest"))
                      || contains(s.path("artifactDigests"), a.path("digest"));
        require(!revoked, "revoked");
      }
      case "watermark" -> {
        release(input.path("release"));
        var w = input.path("watermark");
        var e = input.path("release").path("envelope");
        require(same(w, e, "tenantId", "packageId"), "watermark_tenant");
        require(
            e.path("releaseSequence").asLong() >= w.path("releaseSequence").asLong(), "rollback");
        if (e.path("releaseSequence").asLong() > w.path("releaseSequence").asLong()) return;
        require(w.path("versionId").equals(e.path("versionId")), "watermark_alias");
        var digest =
            e.path("statement")
                .path(e.path("schemaVersion").asInt() == 1 ? "digest" : "manifestDigest");
        if (contains(w.path("verifiedDigestAliases"), digest)) return;
        require(input.has("migration"), "watermark_alias");
        migration(input.path("migration"));
        var proof = input.path("migration").path("proof");
        require(
            e.equals(proof.path("unifiedEnvelope"))
                && contains(
                    w.path("verifiedDigestAliases"),
                    proof.path("legacyEnvelope").path("statement").path("digest")),
            "watermark_alias");
      }
      case "renewal" -> {
        var m = input.path("marker");
        schema("unified", "UnifiedMigrationMarker", m);
        require(signed(m, "release"), "migration_signature");
        require(
            same(m, input.path("envelope"), "tenantId", "packageId")
                && m.path("activatedAt").equals(input.path("persistedActivatedAt")),
            "migration_binding");
        long cutoff = time(m, "activatedAt") + 86400000;
        require(
            time(input, "now") < cutoff && time(input.path("envelope"), "notAfter") <= cutoff,
            "renewal_cutoff");
        release(input);
      }
      case "downgrade" -> {
        var a = input.path("authorization");
        schema("unified", "UnifiedDowngradeAuthorization", a);
        require(signed(a, "downgrade"), "downgrade_signature");
        require(
            same(a, input, "identity", "instanceId", "fromReleaseSequence", "toReleaseSequence")
                && a.path("toReleaseSequence").asLong() < a.path("fromReleaseSequence").asLong(),
            "downgrade_scope");
        require(
            window(time(a, "issuedAt"), time(a, "expiresAt"), time(input, "now"), 600000),
            "downgrade_expired");
        require(
            !contains(input.path("usedAuthorizationIds"), a.path("authorizationId")),
            "downgrade_replay");
      }
      case "clock" -> {
        var c = input.path("clock");
        require(input.path("bootId").equals(c.path("bootId")), "clock_reverify");
        long mono = input.path("monotonicNowMillis").asLong(),
            last = c.path("lastMonotonicMillis").asLong(),
            wall = time(input, "wallNow"),
            max = time(c, "maxObservedTime");
        require(mono >= last && wall >= max - 120000, "clock_rollback");
        long effective = Math.max(wall, Math.max(max + mono - last, time(c, "lastTrustedTime")));
        require(
            effective < time(input, "leaseExpiresAt")
                && mono < c.path("leaseDeadlineMonotonicMillis").asLong(),
            "lease_expired");
      }
      default -> throw new IllegalArgumentException("unknown_operation");
    }
  }
}
