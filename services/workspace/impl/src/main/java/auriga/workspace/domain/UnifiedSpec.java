package auriga.workspace.domain;

import auriga.common.error.*;
import auriga.contracts.node.v1.*;
import auriga.contracts.resource.v1.*;
import java.util.*;

/** Pure spec rules. Cryptographic verification belongs to the trusted node executor. */
public final class UnifiedSpec {
  private UnifiedSpec() {}

  public static boolean executionChanged(InstanceSpec before, InstanceSpec after) {
    if (before.getUnifiedResourcesCount() != after.getUnifiedResourcesCount()) return true;
    var prior = refs(before.getUnifiedResourcesList());
    return after.getUnifiedResourcesList().stream()
        .anyMatch(
            ref -> {
              var old = prior.get(ref.getIdentity().getPackageId());
              return old == null
                  || !old.getIdentity().equals(ref.getIdentity())
                  || UnifiedResourceIntent.revision(old) != UnifiedResourceIntent.revision(ref)
                  || !UnifiedResourceIntent.desired(old).equals(UnifiedResourceIntent.desired(ref));
            });
  }

  public static InstanceSpec apply(
      InstanceSpec old,
      long revision,
      List<UnifiedSelection> selections,
      ResolveUnifiedResourcesResponse resolved,
      boolean renewal) {
    require(revision > 0 && revision <= 9007199254740991L);
    require(resolved.getSelectionRevision() == revision);
    require(
        renewal
            ? old.getResourceSelectionRevision() == revision
            : old.getResourceSelectionRevision() + 1 == revision);
    Map<String, UnifiedResourceRef> before = refs(old.getUnifiedResourcesList());
    Map<String, UnifiedResourceRef> incoming = refs(resolved.getResourcesList());
    var result = new LinkedHashMap<>(before);
    Set<String> changed = new HashSet<>();
    if (renewal) {
      require(incoming.keySet().equals(before.keySet()));
      incoming.forEach(
          (key, next) -> {
            var previous = before.get(key);
            require(sameBytes(previous, next));
            require(sameIntentOrSafetyDisable(previous, next));
            result.put(key, next);
          });
    } else {
      for (var selection : selections) {
        var id = selection.getIdentity();
        require(id.getTenantId().equals(old.getTenantId()) && id.getKind().equals("skill"));
        String key = id.getPackageId();
        require(changed.add(key));
        var next = incoming.remove(key);
        var previous = before.get(key);
        switch (selection.getAction()) {
          case "install", "enable", "disable" -> {
            require(next != null && next.getIdentity().equals(id));
            require(next.getEnabled() == !selection.getAction().equals("disable"));
            require(
                UnifiedResourceIntent.desired(next)
                    .equals(selection.getAction().equals("disable") ? "DISABLED" : "ENABLED"));
            require(UnifiedResourceIntent.revision(next) > 0);
            if (previous != null) {
              require(
                  UnifiedResourceIntent.revision(next) > UnifiedResourceIntent.revision(previous));
              require(next.getReleaseSequence() >= previous.getReleaseSequence());
              if (next.getReleaseSequence() == previous.getReleaseSequence())
                require(sameBytes(previous, next));
            }
            result.put(key, next);
          }
          case "remove" -> {
            require(
                previous != null
                    && next != null
                    && previous.getIdentity().equals(id)
                    && next.getIdentity().equals(id));
            require(!next.getEnabled() && UnifiedResourceIntent.desired(next).equals("REMOVED"));
            require(
                UnifiedResourceIntent.revision(next) > UnifiedResourceIntent.revision(previous));
            require(sameBytes(previous, next));
            result.put(key, next);
          }
          default -> throw new AurigaException(CommonErrors.VALIDATION_FAILED);
        }
      }
      incoming.forEach(
          (key, next) -> {
            var previous = before.get(key);
            require(previous != null && sameBytes(previous, next));
            require(sameIntentOrSafetyDisable(previous, next));
            result.put(key, next);
          });
    }
    for (var ref : result.values()) {
      require(ref.getIdentity().getTenantId().equals(old.getTenantId()));
      require(ref.getIdentity().getKind().equals("skill"));
      require(UnifiedResourceIntent.revision(ref) > 0);
      UnifiedResourceIntent.desired(ref);
    }
    var next =
        old.toBuilder()
            .clearUnifiedResources()
            .addAllUnifiedResources(result.values())
            .setResourceSelectionRevision(revision)
            .build();
    return revocations(next, resolved.getRevocations(), false);
  }

  private static boolean sameIntentOrSafetyDisable(
      UnifiedResourceRef previous, UnifiedResourceRef next) {
    String before = UnifiedResourceIntent.desired(previous),
        after = UnifiedResourceIntent.desired(next);
    long prior = UnifiedResourceIntent.revision(previous),
        revision = UnifiedResourceIntent.revision(next);
    // A resource-side withdrawal/revocation allocates a new CAS for a safety
    // disable. Ordinary signing/lease renewal still preserves intent and CAS.
    return before.equals(after) && prior == revision
        || before.equals("ENABLED") && after.equals("DISABLED") && revision > prior;
  }

  public static boolean sameBytes(UnifiedResourceRef a, UnifiedResourceRef b) {
    return a.getIdentity().equals(b.getIdentity())
        && a.getReleaseSequence() == b.getReleaseSequence()
        && a.getEnvelope().getSchemaVersion() == b.getEnvelope().getSchemaVersion()
        && a.getEnvelope().getTenantId().equals(b.getEnvelope().getTenantId())
        && a.getEnvelope().getPackageId().equals(b.getEnvelope().getPackageId())
        && a.getEnvelope().getVersionId().equals(b.getEnvelope().getVersionId())
        && a.getEnvelope().getReleaseSequence() == b.getEnvelope().getReleaseSequence()
        && a.getEnvelope()
            .getStatement()
            .getPurpose()
            .equals(b.getEnvelope().getStatement().getPurpose())
        && artifactBytes(a).equals(artifactBytes(b))
        && a.getEnvelope().getStatement().getKind().equals(b.getEnvelope().getStatement().getKind())
        && a.getEnvelope().getStatement().getName().equals(b.getEnvelope().getStatement().getName())
        && a.getEnvelope()
            .getStatement()
            .getVersion()
            .equals(b.getEnvelope().getStatement().getVersion())
        && a.getEnvelope()
            .getStatement()
            .getManifestDigest()
            .equals(b.getEnvelope().getStatement().getManifestDigest())
        && a.getEnvelope()
            .getStatement()
            .getArtifactDigestsList()
            .equals(b.getEnvelope().getStatement().getArtifactDigestsList());
  }

  private static Map<String, UnifiedArtifactDownload> artifactBytes(UnifiedResourceRef resource) {
    Map<String, UnifiedArtifactDownload> result = new HashMap<>();
    for (var artifact : resource.getArtifactsList())
      require(
          result.put(artifact.getArtifactId(), artifact.toBuilder().clearDownloadPath().build())
              == null);
    return result;
  }

  public static InstanceSpec revocations(
      InstanceSpec old, ResourceRevocationList incoming, boolean bump) {
    require(incoming.getTenantId().equals(old.getTenantId()));
    var effective =
        old.getResourceRevocationsList().stream()
            .filter(
                r ->
                    r.getTenantId().equals(incoming.getTenantId())
                        && r.getRevision() > incoming.getRevision())
            .max(Comparator.comparingLong(ResourceRevocationList::getRevision))
            .orElse(incoming);
    // Same-revision replacement must not erase previously observed revocations.
    for (var prior : old.getResourceRevocationsList())
      if (prior.getTenantId().equals(effective.getTenantId())
          && prior.getRevision() == effective.getRevision()) require(prior.equals(effective));
    var next =
        old.toBuilder()
            .clearResourceRevocations()
            .addResourceRevocations(effective)
            .clearUnifiedResources();
    next.addAllUnifiedResources(old.getUnifiedResourcesList());
    var built = next.build();
    if (bump && !built.equals(old)) return bump(built);
    return built;
  }

  public static boolean revoked(UnifiedResourceRef ref, ResourceRevocationList list) {
    var id = ref.getIdentity();
    if (list.getRevokedKidsList().contains(ref.getEnvelope().getSignature().getKid())) return true;
    return list.getRevokedArtifactsList().stream()
        .anyMatch(
            r ->
                r.getPackageId().equals(id.getPackageId())
                        && r.getVersionId().equals(id.getVersionId())
                    || r.getDigest().equals(id.getManifestDigest())
                    || ref.getArtifactsList().stream()
                        .anyMatch(a -> a.getDigest().equals(r.getDigest())));
  }

  public static InstanceSpec bump(InstanceSpec spec) {
    require(spec.getGeneration() < 9007199254740991L);
    return spec.toBuilder().setGeneration(spec.getGeneration() + 1).build();
  }

  private static Map<String, UnifiedResourceRef> refs(List<UnifiedResourceRef> values) {
    Map<String, UnifiedResourceRef> result = new LinkedHashMap<>();
    for (var value : values) require(result.put(value.getIdentity().getPackageId(), value) == null);
    return result;
  }

  private static void require(boolean valid) {
    if (!valid) throw new AurigaException(CommonErrors.CONFLICT);
  }
}
