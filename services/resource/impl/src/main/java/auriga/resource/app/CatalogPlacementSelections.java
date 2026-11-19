package auriga.resource.app;

import static auriga.resource.persistence.ResourceWorkflowStore.json;
import static auriga.resource.persistence.ResourceWorkflowStore.number;
import static auriga.resource.web.ResourceBody.JSON;

import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.contracts.node.v1.DshInstanceMode;
import auriga.contracts.node.v1.SharedDshBinding;
import auriga.resource.persistence.ResourceWorkflowStore;
import java.util.*;
import tools.jackson.databind.node.ObjectNode;

/** Resource-owned choices. A new placement epoch never restores an earlier mode's selections. */
final class CatalogPlacementSelections {
  private final ResourceWorkflowStore db;

  CatalogPlacementSelections(ResourceWorkflowStore db) {
    this.db = db;
  }

  List<Map<String, Object>> rows(SharedDshBinding binding) {
    return db.rows(
        "SELECT * FROM resource.catalog_placement_selection WHERE tenant_id=:tenant AND placement_id=:placement ORDER BY package_id FOR UPDATE",
        db.args("placement", UUID.fromString(binding.getPlacementId())));
  }

  static Map<UUID, CatalogAssignments.Effective> material(
      SharedDshBinding binding,
      Map<UUID, CatalogAssignments.Effective> policies,
      List<Map<String, Object>> choices) {
    var result = new HashMap<UUID, CatalogAssignments.Effective>();
    policies.forEach(
        (id, policy) -> {
          if (!policy.mode().equals("optional")) result.put(id, policy);
        });
    if (binding.getMode() != DshInstanceMode.DSH_INSTANCE_MODE_DEDICATED) return Map.copyOf(result);
    for (var row : choices) {
      requireOwner(binding, row);
      if (number(row, "epoch") != binding.getEpoch()) continue;
      UUID pack = (UUID) row.get("package_id");
      var policy = policies.get(pack);
      if (!row.get("desired_state").equals("ENABLED")) {
        if (policy == null || !policy.mode().equals("required")) result.remove(pack);
        continue;
      }
      var identity = (ObjectNode) json(row, "identity");
      long sequence = number(row, "release_sequence");
      if (policy != null
          && !policy.mode().equals("optional")
          && (!policy.identity().equals(identity) && sequence <= policy.sequence())) continue;
      result.put(
          pack,
          new CatalogAssignments.Effective(
              identity, policy == null ? "optional" : policy.mode(), sequence));
    }
    return Map.copyOf(result);
  }

  static void requireOwner(SharedDshBinding binding, Map<String, Object> row) {
    if (!row.get("owner_id").toString().equals(binding.getMemberId()))
      throw new AurigaException(CommonErrors.NOT_FOUND);
  }

  static String fingerprint(SharedDshBinding binding, List<Map<String, Object>> rows) {
    var state = JSON.createArrayNode();
    rows.stream()
        .filter(r -> number(r, "epoch") == binding.getEpoch())
        .sorted(Comparator.comparing(r -> r.get("package_id").toString()))
        .forEach(
            r -> {
              requireOwner(binding, r);
              state.add(
                  JSON.createObjectNode()
                      .put("packageId", r.get("package_id").toString())
                      .put("revision", number(r, "revision"))
                      .put("desiredState", r.get("desired_state").toString()));
            });
    return ResourceOperations.hash(ResourceSigning.canonical(state));
  }

  Map<String, Object> write(
      SharedDshBinding binding,
      ObjectNode selection,
      long sequence,
      CatalogAssignments.Effective policy,
      String etag) {
    if (binding.getMode() != DshInstanceMode.DSH_INSTANCE_MODE_DEDICATED)
      throw new AurigaException(CommonErrors.PRECONDITION_FAILED);
    var identity = selection.path("identity");
    UUID pack = UUID.fromString(identity.path("packageId").asString());
    var existing =
        rows(binding).stream()
            .filter(r -> r.get("package_id").equals(pack))
            .findFirst()
            .orElse(null);
    if (existing != null) requireOwner(binding, existing);
    boolean current = existing != null && number(existing, "epoch") == binding.getEpoch();
    ResourceOperations.match(etag, current ? number(existing, "revision") : 0);
    String action = selection.path("action").asString();
    if (!Set.of("install", "enable", "disable", "remove").contains(action))
      throw new AurigaException(CommonErrors.VALIDATION_FAILED);
    boolean baseline =
        !current
            && policy != null
            && !policy.mode().equals("optional")
            && policy.identity().equals(identity);
    if (!action.equals("install")
        && !baseline
        && (!current || !json(existing, "identity").equals(identity)))
      throw new AurigaException(CommonErrors.NOT_FOUND);
    CatalogAssignments.check(policy, identity, sequence, action);
    if (existing != null
        && (sequence < number(existing, "release_sequence")
            || sequence == number(existing, "release_sequence")
                && !json(existing, "identity").equals(identity)))
      throw CatalogStatePolicy.conflict();
    long revision =
        CatalogStatePolicy.advance(
            existing == null ? 0 : number(existing, "revision"),
            existing == null ? 0 : number(existing, "revision"));
    String state = CatalogStatePolicy.stateForAction(action);
    db.update(
        "INSERT INTO resource.catalog_placement_selection(tenant_id,placement_id,package_id,owner_id,epoch,revision,release_sequence,desired_state,identity) VALUES(:tenant,:placement,:package,:owner,:epoch,:revision,:sequence,:state,CAST(:identity AS jsonb)) ON CONFLICT(tenant_id,placement_id,package_id) DO UPDATE SET owner_id=excluded.owner_id,epoch=excluded.epoch,revision=excluded.revision,release_sequence=excluded.release_sequence,desired_state=excluded.desired_state,identity=excluded.identity",
        db.args(
            "placement",
            UUID.fromString(binding.getPlacementId()),
            "package",
            pack,
            "owner",
            UUID.fromString(binding.getMemberId()),
            "epoch",
            binding.getEpoch(),
            "revision",
            revision,
            "sequence",
            sequence,
            "state",
            state,
            "identity",
            identity.toString()));
    return Map.of(
        "package_id",
        pack,
        "owner_id",
        UUID.fromString(binding.getMemberId()),
        "epoch",
        binding.getEpoch(),
        "revision",
        revision,
        "release_sequence",
        sequence,
        "desired_state",
        state,
        "identity",
        identity.toString());
  }
}
