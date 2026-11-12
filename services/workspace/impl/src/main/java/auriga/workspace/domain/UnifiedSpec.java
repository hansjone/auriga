package auriga.workspace.domain;

import auriga.common.error.*;
import auriga.contracts.node.v1.*;
import auriga.contracts.resource.v1.*;
import java.util.*;
import java.util.function.ToLongFunction;

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
                  || old.getEnabled() != ref.getEnabled();
            });
  }

  public static InstanceSpec apply(
      InstanceSpec old,
      long revision,
      List<UnifiedSelection> selections,
      ResolveUnifiedResourcesResponse resolved,
      boolean renewal,
      ToLongFunction<UnifiedResourceRef> installationRevision) {
    require(revision > 0 && revision <= 9007199254740991L);
    require(resolved.getSelectionRevision() == revision);
    require(
        renewal
            ? old.getResourceSelectionRevision() == revision
            : old.getResourceSelectionRevision() < revision);
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
            require(previous.getEnabled() == next.getEnabled());
            require(
                installationRevision.applyAsLong(previous)
                    == installationRevision.applyAsLong(next));
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
            require(installationRevision.applyAsLong(next) > 0);
            if (previous != null) {
              require(
                  installationRevision.applyAsLong(next)
                      > installationRevision.applyAsLong(previous));
              require(next.getReleaseSequence() >= previous.getReleaseSequence());
              if (next.getReleaseSequence() == previous.getReleaseSequence())
                require(sameBytes(previous, next));
            }
            result.put(key, next);
          }
          case "remove" -> {
            require(previous != null && previous.getIdentity().equals(id) && next == null);
            // K2 must carry the resource-assigned uninstall revision to the node.
            // An absent ref cannot transport it; never acknowledge a guessed revision.
            throw new AurigaException(CommonErrors.UNAVAILABLE);
          }
          default -> throw new AurigaException(CommonErrors.VALIDATION_FAILED);
        }
      }
      require(incoming.isEmpty());
    }
    for (var ref : result.values()) {
      require(ref.getIdentity().getTenantId().equals(old.getTenantId()));
      require(ref.getIdentity().getKind().equals("skill"));
      require(installationRevision.applyAsLong(ref) > 0);
    }
    var next =
        old.toBuilder()
            .clearUnifiedResources()
            .addAllUnifiedResources(result.values())
            .setResourceSelectionRevision(revision)
            .build();
    return revocations(next, resolved.getRevocations(), false);
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
    boolean affected = old.getUnifiedResourcesList().stream().anyMatch(r -> revoked(r, effective));
    var next =
        old.toBuilder()
            .clearResourceRevocations()
            .addResourceRevocations(effective)
            .clearUnifiedResources();
    old.getUnifiedResourcesList()
        .forEach(
            r ->
                next.addUnifiedResources(
                    revoked(r, effective) ? r.toBuilder().setEnabled(false).build() : r));
    if (affected) next.setDesired(DesiredState.DESIRED_STATE_STOPPED);
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
