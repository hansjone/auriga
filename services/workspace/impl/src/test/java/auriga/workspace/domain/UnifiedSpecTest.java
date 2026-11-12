package auriga.workspace.domain;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.error.AurigaException;
import auriga.contracts.node.v1.*;
import auriga.contracts.resource.v1.*;
import java.util.*;
import org.junit.jupiter.api.Test;

class UnifiedSpecTest {
  final UnifiedResourceIdentity id =
      UnifiedResourceIdentity.newBuilder()
          .setTenantId("tenant")
          .setPackageId("package")
          .setVersionId("version")
          .setVersion("1.0.0")
          .setKind("skill")
          .setManifestDigest("sha256:manifest")
          .build();
  final UnifiedResourceRef ref =
      UnifiedResourceRef.newBuilder()
          .setIdentity(id)
          .setEnabled(true)
          .setReleaseSequence(7)
          .addArtifacts(
              UnifiedArtifactDownload.newBuilder()
                  .setArtifactId("payload")
                  .setDigest("sha256:payload"))
          .setEnvelope(
              UnifiedEnvelope.newBuilder()
                  .setSignature(ResourceSignature.newBuilder().setKid("release-key")))
          .build();
  final ResourceRevocationList list =
      ResourceRevocationList.newBuilder().setTenantId("tenant").setRevision(1).build();
  final InstanceSpec empty =
      InstanceSpec.newBuilder()
          .setTenantId("tenant")
          .setInstanceId("instance")
          .setGeneration(9)
          .setDesired(DesiredState.DESIRED_STATE_RUNNING)
          .build();

  ResolveUnifiedResourcesResponse resolution(long revision, UnifiedResourceRef... refs) {
    return ResolveUnifiedResourcesResponse.newBuilder()
        .setSelectionRevision(revision)
        .addAllResources(List.of(refs))
        .setRevocations(list)
        .build();
  }

  @Test
  void exactSelectionAndRenewalPreserveBytes() {
    var choice = UnifiedSelection.newBuilder().setIdentity(id).setAction("install").build();
    var installed = UnifiedSpec.apply(empty, 2, List.of(choice), resolution(2, ref), false, r -> 4);
    var renewed =
        ref.toBuilder()
            .setManifestDownloadPath("/internal/resource/v1/catalog/downloads/new/manifest")
            .setArtifacts(
                0,
                ref.getArtifacts(0).toBuilder()
                    .setDownloadPath(
                        "/internal/resource/v1/catalog/downloads/new/artifacts/payload"))
            .setLease(UnifiedLease.newBuilder().setExpiresAt("2026-11-14T00:00:00Z"))
            .build();
    var next = UnifiedSpec.apply(installed, 2, List.of(), resolution(2, renewed), true, r -> 4);
    assertEquals(installed.getResourceSelectionRevision(), next.getResourceSelectionRevision());
    assertEquals(ref.getIdentity(), next.getUnifiedResources(0).getIdentity());
    assertEquals(10, UnifiedSpec.bump(next).getGeneration());
    var changed =
        renewed.toBuilder()
            .setIdentity(id.toBuilder().setManifestDigest("sha256:replacement"))
            .build();
    assertThrows(
        AurigaException.class,
        () -> UnifiedSpec.apply(installed, 2, List.of(), resolution(2, changed), true, r -> 4));
    assertThrows(
        AurigaException.class,
        () -> UnifiedSpec.apply(installed, 1, List.of(choice), resolution(1, ref), false, r -> 4));
    assertThrows(
        AurigaException.class,
        () ->
            UnifiedSpec.apply(
                installed,
                2,
                List.of(),
                resolution(2, renewed),
                true,
                r -> r.equals(renewed) ? 5 : 4));
  }

  @Test
  void noCrossTenantOrUnrequestedResources() {
    var choice = UnifiedSelection.newBuilder().setIdentity(id).setAction("install").build();
    assertThrows(
        AurigaException.class,
        () -> UnifiedSpec.apply(empty, 2, List.of(), resolution(2, ref), false, r -> 1));
    assertThrows(
        AurigaException.class,
        () ->
            UnifiedSpec.apply(
                empty, 2, List.of(choice, choice), resolution(2, ref), false, r -> 1));
    assertThrows(
        AurigaException.class,
        () ->
            UnifiedSpec.apply(
                empty.toBuilder().setTenantId("foreign").build(),
                2,
                List.of(choice),
                resolution(2, ref),
                false,
                r -> 1));
  }

  @Test
  void revocationStopsOnEitherDigestOrVersionOrReusedArtifact() {
    var installed = empty.toBuilder().addUnifiedResources(ref).build();
    for (var entry :
        List.of(
            RevokedArtifact.newBuilder()
                .setPackageId("package")
                .setVersionId("version")
                .setDigest("sha256:legacy-alias")
                .build(),
            RevokedArtifact.newBuilder()
                .setPackageId("other")
                .setVersionId("other")
                .setDigest("sha256:manifest")
                .build(),
            RevokedArtifact.newBuilder()
                .setPackageId("other")
                .setVersionId("other")
                .setDigest("sha256:payload")
                .build())) {
      var revoked = list.toBuilder().addRevokedArtifacts(entry).build();
      var stopped = UnifiedSpec.revocations(installed, revoked, true);
      assertEquals(DesiredState.DESIRED_STATE_STOPPED, stopped.getDesired());
      assertFalse(stopped.getUnifiedResources(0).getEnabled());
      assertEquals(10, stopped.getGeneration());
      assertEquals(stopped, UnifiedSpec.revocations(stopped, revoked, true));
      assertThrows(AurigaException.class, () -> UnifiedSpec.revocations(stopped, list, true));
    }
  }
}
