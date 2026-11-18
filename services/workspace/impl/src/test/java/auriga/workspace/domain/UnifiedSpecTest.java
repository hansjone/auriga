package auriga.workspace.domain;

import static auriga.contracts.resource.v1.UnifiedResourceDesiredState.*;
import static org.junit.jupiter.api.Assertions.*;

import auriga.common.error.AurigaException;
import auriga.contracts.node.v1.*;
import auriga.contracts.resource.v1.*;
import java.util.*;
import org.junit.jupiter.api.Test;

class UnifiedSpecTest {
  @Test
  void defaultAllowsRemovalWhileRequiredRejectsIt() {
    var installed =
        empty.toBuilder()
            .addUnifiedResources(ref.toBuilder().setInstallationMode("default"))
            .build();
    var removed =
        ref.toBuilder()
            .setEnabled(false)
            .setDesiredState(UNIFIED_RESOURCE_DESIRED_STATE_REMOVED)
            .setInstallationMode("default")
            .setInstallationRevision(2)
            .build();
    var selection = UnifiedSelection.newBuilder().setIdentity(id).setAction("remove").build();
    assertEquals(
        removed,
        UnifiedSpec.apply(installed, 2, List.of(selection), resolution(2, removed), false)
            .getUnifiedResources(0));
    assertThrows(
        AurigaException.class,
        () ->
            UnifiedSpec.apply(
                installed,
                2,
                List.of(selection),
                resolution(2, removed.toBuilder().setInstallationMode("required").build()),
                false));
  }

  @Test
  void trustedAssignmentMaySupersedeAnUnappliedRevisionButCannotRebindIdentitySequence() {
    var installed = empty.toBuilder().addUnifiedResources(ref).build();
    var next = ref.toBuilder().setInstallationMode("required").setInstallationRevision(2).build();
    var selection = UnifiedSelection.newBuilder().setIdentity(id).setAction("install").build();
    assertThrows(
        AurigaException.class,
        () -> UnifiedSpec.apply(installed, 3, List.of(selection), resolution(3, next), false));
    assertEquals(
        next,
        UnifiedSpec.apply(installed, 3, List.of(selection), resolution(3, next), false, true)
            .getUnifiedResources(0));
    assertThrows(
        AurigaException.class,
        () ->
            UnifiedSpec.apply(
                installed,
                3,
                List.of(selection),
                resolution(3, next.toBuilder().setReleaseSequence(6).build()),
                false,
                true));
  }

  @Test
  void authoritativeAssignmentCanRestoreAnUnavailableMandatoryVersionOnly() {
    var installed = empty.toBuilder().addUnifiedResources(ref).build();
    var previousVersion =
        id.toBuilder().setVersionId("previous-version").setVersion("0.9.0").build();
    var restored =
        ref.toBuilder()
            .setIdentity(previousVersion)
            .setReleaseSequence(6)
            .setInstallationMode("required")
            .setInstallationRevision(2)
            .build();
    var selection =
        UnifiedSelection.newBuilder().setIdentity(previousVersion).setAction("install").build();
    assertThrows(
        AurigaException.class,
        () -> UnifiedSpec.apply(installed, 2, List.of(selection), resolution(2, restored), false));
    assertThrows(
        AurigaException.class,
        () ->
            UnifiedSpec.apply(
                installed,
                2,
                List.of(selection),
                resolution(2, restored.toBuilder().setInstallationMode("optional").build()),
                false,
                true));
    assertEquals(
        restored,
        UnifiedSpec.apply(installed, 2, List.of(selection), resolution(2, restored), false, true)
            .getUnifiedResources(0));
  }

  @Test
  void safetyDispatchCannotCarryNewBytesOrAnOrdinaryPolicyChange() {
    var installed = empty.toBuilder().addUnifiedResources(ref).build();
    var disabled =
        ref.toBuilder()
            .setEnabled(false)
            .setInstallationRevision(2)
            .setDesiredState(UNIFIED_RESOURCE_DESIRED_STATE_DISABLED)
            .build();
    var stopped = installed.toBuilder().setGeneration(10).setUnifiedResources(0, disabled).build();
    assertTrue(UnifiedSpec.safetyChange(installed, stopped));
    assertFalse(
        UnifiedSpec.safetyChange(
            installed,
            stopped.toBuilder()
                .setUnifiedResources(0, disabled.toBuilder().setReleaseSequence(8))
                .build()));
    assertFalse(
        UnifiedSpec.safetyChange(
            installed, stopped.toBuilder().setAllowPersonalPlugins(true).build()));
    assertFalse(
        UnifiedSpec.safetyChange(installed, stopped.toBuilder().addUnifiedResources(ref).build()));
    assertFalse(
        UnifiedSpec.safetyChange(
            installed,
            installed.toBuilder()
                .setUnifiedResources(0, ref.toBuilder().setInstallationMode("required"))
                .build()));
    assertTrue(
        UnifiedSpec.safetyChange(
            installed, installed.toBuilder().addResourceRevocations(list).build()));
  }

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
          .setInstallationRevision(1)
          .setDesiredState(UNIFIED_RESOURCE_DESIRED_STATE_ENABLED)
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
          .setResourceSelectionRevision(1)
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
  void pluginSelectionAndRenewalUseTheSameSpecWhileUnknownKindsAreDenied() {
    var pluginId = id.toBuilder().setKind("plugin").build();
    var plugin = ref.toBuilder().setIdentity(pluginId).build();
    var choice = UnifiedSelection.newBuilder().setIdentity(pluginId).setAction("install").build();
    var installed = UnifiedSpec.apply(empty, 2, List.of(choice), resolution(2, plugin), false);
    assertEquals(pluginId, installed.getUnifiedResources(0).getIdentity());
    assertEquals(
        installed, UnifiedSpec.apply(installed, 2, List.of(), resolution(2, plugin), true));
    for (String kind : List.of("tool", "unknown")) {
      var bad = pluginId.toBuilder().setKind(kind).build();
      assertThrows(
          AurigaException.class,
          () ->
              UnifiedSpec.apply(
                  empty,
                  2,
                  List.of(choice.toBuilder().setIdentity(bad).build()),
                  resolution(2, plugin.toBuilder().setIdentity(bad).build()),
                  false));
    }
  }

  @Test
  void authoritativeWithdrawalOfPendingRequiredInstallCanAdvanceSafely() {
    var choice = UnifiedSelection.newBuilder().setIdentity(id).setAction("install").build();
    var disabled =
        ref.toBuilder()
            .setEnabled(false)
            .setInstallationMode("optional")
            .setInstallationRevision(2)
            .setDesiredState(UNIFIED_RESOURCE_DESIRED_STATE_DISABLED)
            .build();
    var next = UnifiedSpec.apply(empty, 2, List.of(choice), resolution(2, disabled), false);
    assertEquals(disabled, next.getUnifiedResources(0));
    assertThrows(
        AurigaException.class,
        () ->
            UnifiedSpec.apply(
                empty,
                2,
                List.of(choice),
                resolution(2, disabled.toBuilder().setInstallationMode("required").build()),
                false));
    var installed = empty.toBuilder().addUnifiedResources(ref).build();
    assertThrows(
        AurigaException.class,
        () ->
            UnifiedSpec.apply(
                installed,
                1,
                List.of(),
                resolution(1, ref.toBuilder().setInstallationMode("required").build()),
                true));
  }

  @Test
  void removalCarriesExactNewInstallationRevision() {
    var installed =
        empty.toBuilder().addUnifiedResources(ref).setResourceSelectionRevision(1).build();
    var tombstone =
        ref.toBuilder()
            .setEnabled(false)
            .setInstallationRevision(2)
            .setDesiredState(UNIFIED_RESOURCE_DESIRED_STATE_REMOVED)
            .build();
    var remove = UnifiedSelection.newBuilder().setIdentity(id).setAction("remove").build();
    var next = UnifiedSpec.apply(installed, 2, List.of(remove), resolution(2, tombstone), false);
    assertEquals(tombstone, next.getUnifiedResources(0));
    assertThrows(
        AurigaException.class,
        () -> UnifiedSpec.apply(installed, 2, List.of(remove), resolution(2), false));
    assertThrows(
        AurigaException.class,
        () ->
            UnifiedSpec.apply(
                installed,
                2,
                List.of(remove),
                resolution(2, tombstone.toBuilder().setInstallationRevision(1).build()),
                false));
  }

  @Test
  void exactSelectionAndRenewalPreserveBytes() {
    var choice = UnifiedSelection.newBuilder().setIdentity(id).setAction("install").build();
    var installed = UnifiedSpec.apply(empty, 2, List.of(choice), resolution(2, ref), false);
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
    var next = UnifiedSpec.apply(installed, 2, List.of(), resolution(2, renewed), true);
    assertEquals(installed.getResourceSelectionRevision(), next.getResourceSelectionRevision());
    assertEquals(ref.getIdentity(), next.getUnifiedResources(0).getIdentity());
    assertEquals(10, UnifiedSpec.bump(next).getGeneration());
    var changed =
        renewed.toBuilder()
            .setIdentity(id.toBuilder().setManifestDigest("sha256:replacement"))
            .build();
    assertThrows(
        AurigaException.class,
        () -> UnifiedSpec.apply(installed, 2, List.of(), resolution(2, changed), true));
    assertThrows(
        AurigaException.class,
        () -> UnifiedSpec.apply(installed, 1, List.of(choice), resolution(1, ref), false));
    assertThrows(
        AurigaException.class,
        () ->
            UnifiedSpec.apply(
                installed,
                2,
                List.of(),
                resolution(2, renewed.toBuilder().setInstallationRevision(2).build()),
                true));
  }

  @Test
  void noCrossTenantOrUnrequestedResources() {
    var choice = UnifiedSelection.newBuilder().setIdentity(id).setAction("install").build();
    assertThrows(
        AurigaException.class,
        () -> UnifiedSpec.apply(empty, 2, List.of(), resolution(2, ref), false));
    assertThrows(
        AurigaException.class,
        () -> UnifiedSpec.apply(empty, 2, List.of(choice, choice), resolution(2, ref), false));
    assertThrows(
        AurigaException.class,
        () ->
            UnifiedSpec.apply(
                empty.toBuilder().setTenantId("foreign").build(),
                2,
                List.of(choice),
                resolution(2, ref),
                false));
  }

  @Test
  void generatedIntentRejectsMissingDefaultUnknownAndInvalidRevision() {
    for (var invalid :
        List.of(
            ref.toBuilder().clearInstallationRevision().build(),
            ref.toBuilder().setInstallationRevision(-1).build(),
            ref.toBuilder().setInstallationRevision(9007199254740992L).build()))
      assertThrows(AurigaException.class, () -> UnifiedResourceIntent.revision(invalid));
    for (var invalid :
        List.of(
            ref.toBuilder().clearDesiredState().build(),
            ref.toBuilder().setDesiredState(UNIFIED_RESOURCE_DESIRED_STATE_UNSPECIFIED).build(),
            ref.toBuilder().setDesiredStateValue(99).build(),
            ref.toBuilder().setDesiredState(UNIFIED_RESOURCE_DESIRED_STATE_REMOVED).build()))
      assertThrows(AurigaException.class, () -> UnifiedResourceIntent.desired(invalid));
    var choice = UnifiedSelection.newBuilder().setIdentity(id).setAction("install").build();
    assertThrows(
        AurigaException.class,
        () ->
            UnifiedSpec.apply(
                empty,
                2,
                List.of(choice),
                resolution(2, ref.toBuilder().clearDesiredState().build()),
                false));
  }

  @Test
  void successReportsMatchOnlyTheirDesiredState() {
    var states =
        List.of(
            UNIFIED_RESOURCE_DESIRED_STATE_ENABLED,
            UNIFIED_RESOURCE_DESIRED_STATE_DISABLED,
            UNIFIED_RESOURCE_DESIRED_STATE_REMOVED);
    var reports = List.of("applied", "disabled", "removed");
    for (int i = 0; i < states.size(); i++) {
      var expected = ref.toBuilder().setDesiredState(states.get(i)).setEnabled(i == 0).build();
      for (int j = 0; j < reports.size(); j++)
        assertEquals(i == j, UnifiedResourceIntent.reportMatches(expected, reports.get(j)));
    }
  }

  @Test
  void revocationPreservesPrivateInstanceAndResourceIntent() {
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
      assertEquals(DesiredState.DESIRED_STATE_RUNNING, stopped.getDesired());
      assertEquals(
          ref,
          stopped.getUnifiedResources(0),
          "revocation cannot allocate an installation CAS or rewrite desired state");
      assertEquals(10, stopped.getGeneration());
      assertEquals(stopped, UnifiedSpec.revocations(stopped, revoked, true));
      assertThrows(AurigaException.class, () -> UnifiedSpec.revocations(stopped, list, true));
    }
  }

  @Test
  void renewalAcceptsOnlySameBytesMonotonicSafetyDisable() {
    var current = empty.toBuilder().addUnifiedResources(ref).build();
    var disabled =
        ref.toBuilder()
            .setEnabled(false)
            .setInstallationRevision(2)
            .setDesiredState(UNIFIED_RESOURCE_DESIRED_STATE_DISABLED)
            .build();
    var next = UnifiedSpec.apply(current, 1, List.of(), resolution(1, disabled), true);
    assertEquals(disabled, next.getUnifiedResources(0));
    for (var invalid :
        List.of(
            disabled.toBuilder().setInstallationRevision(1).build(),
            disabled.toBuilder()
                .setIdentity(id.toBuilder().setManifestDigest("sha256:changed"))
                .build()))
      assertThrows(
          AurigaException.class,
          () -> UnifiedSpec.apply(current, 1, List.of(), resolution(1, invalid), true));
    assertThrows(
        AurigaException.class,
        () ->
            UnifiedSpec.apply(
                next,
                1,
                List.of(),
                resolution(1, ref.toBuilder().setInstallationRevision(3).build()),
                true));
  }

  @Test
  void incrementRequiresNextRevisionAndRetainsFullResolutionEntries() {
    var first = empty.toBuilder().addUnifiedResources(ref).build();
    var other = ref.toBuilder().setIdentity(id.toBuilder().setPackageId("other")).build();
    var choice =
        UnifiedSelection.newBuilder().setIdentity(other.getIdentity()).setAction("install").build();
    assertThrows(
        AurigaException.class,
        () -> UnifiedSpec.apply(first, 3, List.of(choice), resolution(3, ref, other), false));
    var next = UnifiedSpec.apply(first, 2, List.of(choice), resolution(2, ref, other), false);
    assertEquals(List.of(ref, other), next.getUnifiedResourcesList());
  }
}
