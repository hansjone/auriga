package auriga.resource.app;

import static auriga.resource.persistence.ResourceWorkflowStore.*;
import static auriga.resource.web.ResourceBody.JSON;

import auriga.resource.persistence.ResourceWorkflowStore;
import java.util.*;

/** Resource-owned per-placement CAS history. Caller holds its shared-view lock and transaction. */
final class CatalogSharedInstallations {
  private final ResourceWorkflowStore db;

  CatalogSharedInstallations(ResourceWorkflowStore db) {
    this.db = db;
  }

  Map<UUID, Long> reconcile(UUID placement, Map<UUID, CatalogAssignments.Effective> choices) {
    return reconcile(placement, choices, false);
  }

  Map<UUID, Long> reconcile(
      UUID placement, Map<UUID, CatalogAssignments.Effective> choices, boolean dedicated) {
    var before = new HashMap<UUID, Map<String, Object>>();
    for (var row :
        db.rows(
            "SELECT * FROM resource.catalog_shared_installation WHERE tenant_id=:tenant AND placement_id=:placement ORDER BY package_id FOR UPDATE",
            db.args("placement", placement))) before.put((UUID) row.get("package_id"), row);
    var packages = new TreeSet<UUID>(Comparator.comparing(UUID::toString));
    packages.addAll(before.keySet());
    packages.addAll(choices.keySet());
    var revisions = new HashMap<UUID, Long>();
    for (var pack : packages) {
      var old = before.get(pack);
      var choice = choices.get(pack);
      boolean active = choice != null && (dedicated || !choice.mode().equals("optional"));
      if (!active && (old == null || !Boolean.TRUE.equals(old.get("active")))) continue;
      var selection =
          active
              ? JSON.createObjectNode()
                  .put("installationMode", choice.mode())
                  .put("releaseSequence", choice.sequence())
                  .set("identity", choice.identity())
              : json(old, "selection");
      if (active && old != null) {
        var prior = json(old, "selection");
        long previous = prior.path("releaseSequence").asLong();
        if (choice.sequence() < previous
            || choice.sequence() == previous && !prior.path("identity").equals(choice.identity()))
          throw CatalogStatePolicy.conflict();
      }
      long revision = old == null ? 0 : number(old, "revision");
      boolean changed =
          old == null
              || active != Boolean.TRUE.equals(old.get("active"))
              || !ResourceSigning.canonical(selection)
                  .equals(ResourceSigning.canonical(json(old, "selection")));
      if (changed) {
        revision = CatalogStatePolicy.advance(revision, revision);
        db.update(
            "INSERT INTO resource.catalog_shared_installation(tenant_id,placement_id,package_id,revision,active,selection) VALUES(:tenant,:placement,:package,:revision,:active,CAST(:selection AS jsonb)) ON CONFLICT(tenant_id,placement_id,package_id) DO UPDATE SET revision=excluded.revision,active=excluded.active,selection=excluded.selection",
            db.args(
                "placement",
                placement,
                "package",
                pack,
                "revision",
                revision,
                "active",
                active,
                "selection",
                selection.toString()));
      }
      if (active) revisions.put(pack, revision);
    }
    return Map.copyOf(revisions);
  }
}
