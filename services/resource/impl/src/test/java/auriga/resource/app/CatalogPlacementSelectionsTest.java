package auriga.resource.app;

import static auriga.resource.web.ResourceBody.JSON;
import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import auriga.contracts.node.v1.DshInstanceMode;
import auriga.contracts.node.v1.SharedDshBinding;
import auriga.resource.persistence.ResourceWorkflowStore;
import java.util.*;
import org.junit.jupiter.api.Test;

class CatalogPlacementSelectionsTest {
  final UUID tenant = UUID.randomUUID(), member = UUID.randomUUID(), pack = UUID.randomUUID();
  final SharedDshBinding binding =
      SharedDshBinding.newBuilder()
          .setTenantId(tenant.toString())
          .setMemberId(member.toString())
          .setPlacementId(UUID.randomUUID().toString())
          .setPhysicalInstanceId(UUID.randomUUID().toString())
          .setEpoch(2)
          .setRevision(3)
          .setMode(DshInstanceMode.DSH_INSTANCE_MODE_DEDICATED)
          .build();

  CatalogAssignments.Effective policy(String mode, long sequence) {
    return new CatalogAssignments.Effective(
        JSON.createObjectNode()
            .put("tenantId", tenant.toString())
            .put("packageId", pack.toString())
            .put("versionId", UUID.randomUUID().toString())
            .put("kind", "plugin")
            .put("version", "1.0.0")
            .put("manifestDigest", "sha256:" + "a".repeat(64)),
        mode,
        sequence);
  }

  Map<String, Object> row(CatalogAssignments.Effective value, String state, long epoch) {
    return Map.of(
        "package_id",
        pack,
        "owner_id",
        member,
        "epoch",
        epoch,
        "revision",
        5L,
        "identity",
        value.identity().toString(),
        "release_sequence",
        value.sequence(),
        "desired_state",
        state);
  }

  @Test
  void dedicatedOptionalPluginKeepsItsRealIdentityAndMode() {
    var chosen = policy("optional", 3);
    var material =
        CatalogPlacementSelections.material(binding, Map.of(), List.of(row(chosen, "ENABLED", 2)));
    assertEquals(chosen, material.get(pack));
    assertEquals("plugin", material.get(pack).identity().path("kind").asString());
  }

  @Test
  void optionalAssignmentIsNotAnInstallationAndStaleEpochDoesNotRestoreFiles() {
    var optional = policy("optional", 3);
    assertTrue(
        CatalogPlacementSelections.material(binding, Map.of(pack, optional), List.of()).isEmpty());
    assertTrue(
        CatalogPlacementSelections.material(binding, Map.of(), List.of(row(optional, "ENABLED", 1)))
            .isEmpty());
  }

  @Test
  void defaultCanBeDisabledButRequiredPolicyRemainsActive() {
    var selected = policy("optional", 3);
    var disabled = List.of(row(selected, "DISABLED", 2));
    assertTrue(
        CatalogPlacementSelections.material(binding, Map.of(pack, policy("default", 3)), disabled)
            .isEmpty());
    var required = policy("required", 4);
    assertEquals(
        required,
        CatalogPlacementSelections.material(binding, Map.of(pack, required), disabled).get(pack));
  }

  @Test
  void newMandatoryReleaseOverridesAnOlderChoiceWithoutBorrowingItsLease() {
    var selected = policy("optional", 3);
    var required = policy("required", 4);
    assertEquals(
        required,
        CatalogPlacementSelections.material(
                binding, Map.of(pack, required), List.of(row(selected, "ENABLED", 2)))
            .get(pack));
    var newer = policy("optional", 5);
    var actual =
        CatalogPlacementSelections.material(
                binding, Map.of(pack, required), List.of(row(newer, "ENABLED", 2)))
            .get(pack);
    assertEquals(newer.identity(), actual.identity());
    assertEquals("required", actual.mode());
  }

  @Test
  void defaultPolicyCanBeDisabledBeforeAnyPersonalSelectionButRequiredCannot() {
    var db = mock(ResourceWorkflowStore.class);
    when(db.rows(anyString(), any())).thenReturn(List.of());
    var choices = new CatalogPlacementSelections(db);
    var policy = policy("default", 3);
    var selection = JSON.createObjectNode().put("action", "disable");
    selection.set("identity", policy.identity());
    assertEquals(
        "DISABLED", choices.write(binding, selection, 3, policy, "\"0\"").get("desired_state"));
    var required =
        new CatalogAssignments.Effective(policy.identity(), "required", policy.sequence());
    assertThrows(
        auriga.common.error.AurigaException.class,
        () -> choices.write(binding, selection, 3, required, "\"0\""));
  }

  @Test
  void anotherMemberCannotSupplyAChoiceAndSharedNeverLoadsPersonalChoices() {
    var selected = policy("optional", 3);
    var wrong = new HashMap<>(row(selected, "ENABLED", 2));
    wrong.put("owner_id", UUID.randomUUID());
    assertThrows(
        auriga.common.error.AurigaException.class,
        () -> CatalogPlacementSelections.material(binding, Map.of(), List.of(wrong)));
    assertTrue(
        CatalogPlacementSelections.material(
                binding.toBuilder().setMode(DshInstanceMode.DSH_INSTANCE_MODE_SHARED).build(),
                Map.of(),
                List.of(row(selected, "ENABLED", 2)))
            .isEmpty());
  }
}
