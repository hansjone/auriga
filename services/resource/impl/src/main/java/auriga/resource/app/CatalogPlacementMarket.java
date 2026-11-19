package auriga.resource.app;

import static auriga.resource.persistence.ResourceWorkflowStore.*;
import static auriga.resource.web.ResourceBody.invalid;
import static auriga.resource.web.ResourceBody.uuid;

import auriga.common.error.*;
import auriga.common.security.*;
import auriga.contracts.node.v1.*;
import auriga.resource.api.SharedResourcePlacements;
import auriga.resource.persistence.*;
import java.util.*;
import java.util.function.Supplier;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** Browser market for a live member placement; no owner/device credential is synthesized. */
public final class CatalogPlacementMarket {
  private final ResourceStore tx;
  private final ResourceWorkflowStore db;
  private final ResourceAccess access;
  private final CatalogWorkflow catalog;
  private final CatalogDelivery delivery;
  private final CatalogSharedResources resources;
  private final Supplier<SharedResourcePlacements> placements;
  private final CatalogPlacementSelections choices;
  private final ResourceCursor cursors;

  public CatalogPlacementMarket(
      ResourceStore tx,
      ResourceWorkflowStore db,
      ResourceAccess access,
      CatalogWorkflow catalog,
      CatalogDelivery delivery,
      CatalogSharedResources resources,
      Supplier<SharedResourcePlacements> placements,
      ResourceCursor cursors) {
    this.tx = tx;
    this.db = db;
    this.access = access;
    this.catalog = catalog;
    this.delivery = delivery;
    this.resources = resources;
    this.placements = placements;
    this.choices = new CatalogPlacementSelections(db);
    this.cursors = cursors;
  }

  static AurigaException requiresDedicated() {
    return new AurigaException(
        ErrorCode.of(
            "resource.shared_requires_dedicated", 409, io.grpc.Status.Code.FAILED_PRECONDITION));
  }

  private SharedResourcePlacements authority() {
    var value = placements.get();
    if (value == null) throw new AurigaException(CommonErrors.UNAVAILABLE);
    return value;
  }

  private SharedDshBinding resolve(AurigaPrincipal actor, UUID placement) {
    if (actor == null
        || actor.type() != PrincipalType.USER
        || actor.context() != TokenContext.PORTAL
        || actor.sessionId() == null) throw new AurigaException(CommonErrors.FORBIDDEN);
    var reader = access.member(actor);
    UUID session;
    try {
      session = UUID.fromString(actor.sessionId());
    } catch (IllegalArgumentException error) {
      throw new AurigaException(CommonErrors.UNAUTHENTICATED);
    }
    var binding = authority().resolve(reader.tenant(), reader.account(), placement, session);
    if (!binding.getTenantId().equals(reader.tenant().toString())
        || !binding.getMemberId().equals(reader.account().toString())
        || !binding.getPlacementId().equals(placement.toString())
        || !binding.getUnknownFields().asMap().isEmpty())
      throw new AurigaException(CommonErrors.NOT_FOUND);
    return binding;
  }

  private void fence(AurigaPrincipal actor, UUID placement, SharedDshBinding binding) {
    if (!resolve(actor, placement).equals(binding))
      throw new AurigaException(CommonErrors.NOT_FOUND);
  }

  public JsonNode select(
      AurigaPrincipal actor, UUID placement, ObjectNode input, String key, String etag) {
    if (input.size() != 1
        || !input.has("selection")
        || !(input.get("selection") instanceof ObjectNode selection)) throw invalid();
    delivery.placementSelection(selection);
    var binding = resolve(actor, placement);
    if (binding.getMode() != DshInstanceMode.DSH_INSTANCE_MODE_DEDICATED) throw requiresDedicated();
    String action = selection.path("action").asString();
    boolean active = Set.of("install", "enable").contains(action);
    String operation =
        "placement/" + placement + "/" + binding.getEpoch() + "/" + binding.getRevision();
    var result =
        catalog.mutate(
            actor,
            ResourceAccess.READ,
            false,
            operation,
            key,
            etag,
            input,
            reader -> {
              db.lock("catalog-assignments");
              db.lock("shared-view/" + placement);
              delivery.placementVersion(
                  reader, selection.path("identity"), active, action.equals("install"));
            },
            reader -> {
              var version =
                  delivery.placementVersion(
                      reader, selection.path("identity"), active, action.equals("install"));
              UUID pack = uuid(selection.path("identity"), "packageId");
              var policies =
                  delivery.sharedSelections(
                      reader, UUID.fromString(binding.getPhysicalInstanceId()));
              var chosen =
                  choices.write(
                      binding,
                      selection,
                      number(version, "release_sequence"),
                      policies.get(pack),
                      etag);
              // Reconcile the real signed intent in the same Resource transaction, including rapid
              // disable/enable pairs which must not collapse their installation high-water marks.
              var current = resources.snapshot(binding);
              var material =
                  current.resources().stream()
                      .filter(r -> r.getIdentity().getPackageId().equals(pack.toString()))
                      .findFirst();
              long installation = installationRevision(placement, pack);
              return JSON.createObjectNode()
                  .put("placementId", placement.toString())
                  .put("selectionRevision", number(chosen, "revision"))
                  .put("installationRevision", installation)
                  .put("desiredState", chosen.get("desired_state").toString())
                  .put("policyOverride", false)
                  .put("state", "applying")
                  .put("viewId", current.view().getViewId())
                  .put(
                      "installationMode",
                      material.map(r -> r.getInstallationMode()).orElse("optional"))
                  .set("identity", selection.path("identity").deepCopy());
            });
    fence(actor, placement, binding);
    return result;
  }

  private long installationRevision(UUID placement, UUID pack) {
    var rows =
        db.rows(
            "SELECT revision FROM resource.catalog_shared_installation WHERE tenant_id=:tenant AND placement_id=:placement AND package_id=:package",
            db.args("placement", placement, "package", pack));
    return rows.isEmpty() ? 0 : number(rows.getFirst(), "revision");
  }

  private record State(
      auriga.resource.api.SharedResourceResolution.Snapshot snapshot,
      NavigableMap<UUID, ObjectNode> rows) {}

  private State current(SharedDshBinding binding, ResourceAccess.Reader reader, String kind) {
    UUID placement = UUID.fromString(binding.getPlacementId());
    db.lock("catalog-assignments");
    db.lock("shared-view/" + placement);
    var snapshot = resources.snapshot(binding);
    var policies =
        delivery.sharedSelections(reader, UUID.fromString(binding.getPhysicalInstanceId()));
    var selected = new HashMap<UUID, Map<String, Object>>();
    for (var row : choices.rows(binding)) {
      CatalogPlacementSelections.requireOwner(binding, row);
      if (number(row, "epoch") == binding.getEpoch())
        selected.put((UUID) row.get("package_id"), row);
    }
    var actual = new HashMap<String, auriga.contracts.resource.v1.UnifiedResourceRef>();
    snapshot.resources().forEach(r -> actual.put(r.getIdentity().getPackageId(), r));
    var ids = new TreeSet<UUID>(Comparator.comparing(UUID::toString));
    ids.addAll(policies.keySet());
    ids.addAll(selected.keySet());
    var rows = new TreeMap<UUID, ObjectNode>(Comparator.comparing(UUID::toString));
    for (UUID id : ids) {
      var choice = selected.get(id);
      var policy = policies.get(id);
      var material = actual.get(id.toString());
      JsonNode identity = choice == null ? policy.identity() : json(choice, "identity");
      if (material != null) {
        var value = material.getIdentity();
        identity =
            JSON.createObjectNode()
                .put("tenantId", value.getTenantId())
                .put("packageId", value.getPackageId())
                .put("versionId", value.getVersionId())
                .put("kind", value.getKind())
                .put("version", value.getVersion())
                .put("manifestDigest", value.getManifestDigest());
      }
      if (kind != null && !identity.path("kind").asString().equals(kind)) continue;
      delivery.placementVersion(reader, identity, material != null, false);
      String desired =
          material != null
              ? "ENABLED"
              : choice == null ? "DISABLED" : choice.get("desired_state").toString();
      boolean overridden =
          material != null
              && choice != null
              && (!desired.equals(choice.get("desired_state"))
                  || !identity.equals(json(choice, "identity")));
      var row =
          JSON.createObjectNode()
              .put("placementId", placement.toString())
              .put("selectionRevision", choice == null ? 0 : number(choice, "revision"))
              .put("installationRevision", installationRevision(placement, id))
              .put("desiredState", desired)
              .put(
                  "installationMode",
                  material == null
                      ? policy == null ? "optional" : policy.mode()
                      : material.getInstallationMode())
              .put("state", choice == null && material == null ? "available" : "applying")
              .put("policyOverride", overridden);
      row.set("identity", identity.deepCopy());
      rows.put(id, row);
    }
    return new State(snapshot, rows);
  }

  public JsonNode list(
      AurigaPrincipal actor, UUID placement, String kind, int limit, String cursor) {
    if (limit < 1 || limit > 200 || kind != null && !Set.of("plugin", "skill").contains(kind))
      throw invalid();
    var binding = resolve(actor, placement);
    var reader = access.member(actor);
    var state = tx.write(() -> current(binding, reader, kind));
    boolean observed = authority().report(state.snapshot().view()).isPresent();
    fence(actor, placement, binding);
    String scope =
        "placement/"
            + placement
            + "/"
            + binding.getEpoch()
            + "/"
            + binding.getRevision()
            + "/"
            + state.snapshot().view().getViewId()
            + "/"
            + Objects.toString(kind, "all");
    var position = cursors.decode(cursor, reader, scope, limit);
    if (position == null)
      position = cursors.initial(state.rows().isEmpty() ? new UUID(0, 0) : state.rows().lastKey());
    var result =
        JSON.createObjectNode()
            .put("placementId", placement.toString())
            .put(
                "mode",
                binding.getMode() == DshInstanceMode.DSH_INSTANCE_MODE_SHARED
                    ? "shared"
                    : "dedicated")
            .put("epoch", binding.getEpoch())
            .put("revision", binding.getRevision())
            .put("viewId", state.snapshot().view().getViewId())
            .put("nodeObserved", observed);
    var items = result.putArray("items");
    UUID after = position.after(), ceiling = position.ceiling();
    UUID last = after;
    boolean more = false;
    for (var entry : state.rows().entrySet()) {
      if (entry.getKey().toString().compareTo(after.toString()) <= 0
          || entry.getKey().toString().compareTo(ceiling.toString()) > 0) continue;
      if (items.size() == limit) {
        more = true;
        break;
      }
      var row = entry.getValue();
      if (observed && !row.path("state").asString().equals("available"))
        row.put(
            "state",
            row.path("policyOverride").asBoolean()
                ? "overridden"
                : CatalogStatePolicy.success(row.path("desiredState").asString()));
      items.add(row);
      last = entry.getKey();
    }
    if (more)
      result.put(
          "nextCursor",
          cursors.encode(
              reader,
              scope,
              limit,
              new ResourceCursor.Position(last, ceiling, position.expires())));
    return result;
  }
}
