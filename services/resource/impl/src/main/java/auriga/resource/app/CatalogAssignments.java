package auriga.resource.app;

import static auriga.resource.persistence.ResourceWorkflowStore.json;
import static auriga.resource.persistence.ResourceWorkflowStore.number;
import static auriga.resource.web.ResourceBody.*;

import auriga.common.error.*;
import auriga.common.events.EventPublisher;
import auriga.common.persistence.UuidV7;
import auriga.resource.api.UnifiedResourceWorkspace;
import auriga.resource.persistence.ResourceWorkflowStore;
import io.grpc.Status;
import java.sql.Timestamp;
import java.time.Clock;
import java.util.*;
import java.util.function.Supplier;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** Tenant-scoped, exact-version assignments. Membership and package ACL are resolved live. */
final class CatalogAssignments {
  record Effective(ObjectNode identity, String mode, long sequence) {}

  private final ResourceWorkflowStore db;
  private final ResourceAccess access;
  private final CatalogWorkflow catalog;
  private final Supplier<UnifiedResourceWorkspace> workspace;
  private final EventPublisher events;
  private final Clock clock;

  CatalogAssignments(
      ResourceWorkflowStore db,
      ResourceAccess access,
      CatalogWorkflow catalog,
      Supplier<UnifiedResourceWorkspace> workspace,
      EventPublisher events,
      Clock clock) {
    this.db = db;
    this.access = access;
    this.catalog = catalog;
    this.workspace = workspace;
    this.events = events;
    this.clock = clock;
  }

  static int strength(String mode) {
    return switch (mode) {
      case "optional" -> 0;
      case "default" -> 1;
      case "required" -> 2;
      default -> throw invalid();
    };
  }

  static String mode(JsonNode ref) {
    String mode =
        ref.has("installationMode") ? ref.path("installationMode").asString() : "optional";
    strength(mode);
    return mode;
  }

  static void check(Effective policy, JsonNode identity, String action) {
    if (policy == null) return;
    if ((policy.mode.equals("required") && action.equals("disable"))
        || (!policy.mode.equals("optional") && action.equals("remove")))
      throw new AurigaException(
          ErrorCode.of("resource.installation_required", 403, Status.Code.PERMISSION_DENIED));
    if (!policy.mode.equals("optional")
        && !policy.identity.equals(identity)
        && Set.of("install", "enable").contains(action))
      throw new AurigaException(
          ErrorCode.of("resource.distribution_locked", 403, Status.Code.PERMISSION_DENIED));
  }

  private ObjectNode identity(ResourceAccess.Reader r, Map<String, Object> v) {
    return JSON.createObjectNode()
        .put("tenantId", r.tenant().toString())
        .put("packageId", v.get("package_id").toString())
        .put("versionId", v.get("id").toString())
        .put("kind", json(v, "manifest").path("kind").asString())
        .put("version", v.get("version").toString())
        .put("manifestDigest", v.get("digest").toString());
  }

  private Map<String, Object> version(ResourceAccess.Reader r, JsonNode identity) {
    if (!r.tenant().toString().equals(identity.path("tenantId").asString()))
      throw new AurigaException(CommonErrors.NOT_FOUND);
    var p = catalog.pkg(r, uuid(identity, "packageId"), false);
    var v = catalog.ver(r, uuid(identity, "packageId"), uuid(identity, "versionId"), false);
    if (!identity(r, v).equals(identity)
        || !v.get("state").equals("published")
        || !Boolean.TRUE.equals(p.get("enabled"))
        || catalog.revoked(v)) throw new AurigaException(CommonErrors.NOT_FOUND);
    return v;
  }

  ObjectNode create(ResourceAccess.Reader r, ObjectNode body) {
    db.lock("catalog-assignments");
    String scope = text(body, "scope", 16), mode = text(body, "installationMode", 16);
    strength(mode);
    UUID target = uuid(body, "targetId");
    var id = body.path("identity");
    version(r, id);
    switch (scope) {
      case "tenant" -> {
        if (!target.equals(r.tenant())) throw new AurigaException(CommonErrors.NOT_FOUND);
        var pack = catalog.pkg(r, uuid(id, "packageId"), false);
        if (!pack.get("visibility").equals("tenant"))
          throw new AurigaException(CommonErrors.NOT_FOUND);
      }
      case "member" -> version(access.target(r.tenant(), target), id);
      case "group" -> {
        if (!access.groupExists(r.tenant(), target))
          throw new AurigaException(CommonErrors.NOT_FOUND);
        var pack = catalog.pkg(r, uuid(id, "packageId"), false);
        if (pack.get("visibility").equals("groups")) {
          try {
            if (!Arrays.asList((Object[]) ((java.sql.Array) pack.get("group_ids")).getArray())
                .contains(target)) throw new AurigaException(CommonErrors.NOT_FOUND);
          } catch (java.sql.SQLException e) {
            throw new IllegalStateException(e);
          }
        }
      }
      case "instance" -> {
        var service = workspace.get();
        if (service == null) throw new AurigaException(CommonErrors.UNAVAILABLE);
        var snapshot = service.snapshot(r.tenant(), target);
        version(access.target(r.tenant(), snapshot.owner()), id);
      }
      default -> throw invalid();
    }
    UUID rule = UuidV7.next();
    db.update(
        "INSERT INTO resource.catalog_assignment(id,tenant_id,package_id,version_id,scope,target_id,installation_mode,created_at) VALUES(:id,:tenant,:p,:v,:scope,:target,:mode,:now)",
        db.args(
            "id",
            rule,
            "p",
            uuid(id, "packageId"),
            "v",
            uuid(id, "versionId"),
            "scope",
            scope,
            "target",
            target,
            "mode",
            mode,
            "now",
            Timestamp.from(clock.instant())));
    var result = view(r, row(rule));
    catalog.assignmentRecorded(r, rule);
    events.publish("resource.catalog_assignment_changed", rule.toString(), result);
    return result;
  }

  Map<String, Object> row(UUID id) {
    return db.row(
        "SELECT * FROM resource.catalog_assignment WHERE tenant_id=:tenant AND id=:id FOR UPDATE",
        db.args("id", id));
  }

  ObjectNode remove(ResourceAccess.Reader r, UUID id, String etag) {
    db.lock("catalog-assignments");
    var row = row(id);
    catalog.pkg(r, (UUID) row.get("package_id"), false);
    ResourceWorkflow.match(etag, number(row, "revision"));
    db.update(
        "UPDATE resource.catalog_assignment SET active=false,revision=revision+1 WHERE tenant_id=:tenant AND id=:id AND active",
        db.args("id", id));
    var result = view(r, row(id));
    catalog.assignmentRecorded(r, id);
    events.publish("resource.catalog_assignment_changed", id.toString(), result);
    return result;
  }

  ObjectNode view(ResourceAccess.Reader r, Map<String, Object> row) {
    var v = catalog.ver(r, (UUID) row.get("package_id"), (UUID) row.get("version_id"), false);
    return JSON.createObjectNode()
        .put("id", row.get("id").toString())
        .put("scope", row.get("scope").toString())
        .put("targetId", row.get("target_id").toString())
        .put("installationMode", row.get("installation_mode").toString())
        .put("revision", number(row, "revision"))
        .put("active", (Boolean) row.get("active"))
        .set("identity", identity(r, v));
  }

  Map<UUID, Effective> effective(ResourceAccess.Reader r, UUID instance, boolean cloud) {
    var result = new LinkedHashMap<UUID, Effective>();
    var rows =
        db.rows(
            "SELECT * FROM resource.catalog_assignment WHERE tenant_id=:tenant AND active AND (scope='tenant' OR (scope='member' AND target_id=:owner) OR (scope='group' AND target_id=ANY(CAST(:groups AS uuid[]))) OR (scope='instance' AND target_id=:instance AND :cloud)) ORDER BY package_id,id",
            db.args(
                "owner",
                r.account(),
                "groups",
                "{" + String.join(",", r.groups().stream().map(UUID::toString).toList()) + "}",
                "instance",
                instance,
                "cloud",
                cloud));
    for (var row : rows) {
      UUID p = (UUID) row.get("package_id");
      try {
        var v = catalog.ver(r, p, (UUID) row.get("version_id"), false);
        var pack = catalog.pkg(r, p, false);
        if (!v.get("state").equals("published")
            || !Boolean.TRUE.equals(pack.get("enabled"))
            || catalog.revoked(v)) continue;
        var next =
            new Effective(
                identity(r, v),
                row.get("installation_mode").toString(),
                number(v, "release_sequence"));
        var old = result.get(p);
        if (old != null)
          next =
              new Effective(
                  next.sequence > old.sequence ? next.identity : old.identity,
                  strength(next.mode) > strength(old.mode) ? next.mode : old.mode,
                  Math.max(next.sequence, old.sequence));
        result.put(p, next);
      } catch (AurigaException e) {
        if (!e.errorCode().equals(CommonErrors.NOT_FOUND)) throw e;
      }
    }
    return result;
  }
}
