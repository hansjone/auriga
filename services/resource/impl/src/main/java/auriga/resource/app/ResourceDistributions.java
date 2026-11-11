package auriga.resource.app;

import static auriga.resource.persistence.ResourceWorkflowStore.json;
import static auriga.resource.persistence.ResourceWorkflowStore.number;
import static auriga.resource.web.ResourceBody.*;

import auriga.common.context.TenantContext;
import auriga.common.error.*;
import auriga.common.events.EventPublisher;
import auriga.common.persistence.UuidV7;
import auriga.common.security.*;
import auriga.resource.api.ResourceWorkspace;
import auriga.resource.objectstore.ObjectStore;
import auriga.resource.persistence.*;
import java.io.InputStream;
import java.net.URI;
import java.time.*;
import java.util.*;
import java.util.function.Supplier;
import org.springframework.scheduling.annotation.Scheduled;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** Durable intent reconciliation. Only workspace/node observations can advance to applied. */
public final class ResourceDistributions {
  private static final String MANAGE = "resource.distributions.manage";
  private final ResourceStore resources;
  private final ResourceWorkflowStore db;
  private final ResourceAccess access;
  private final ResourceWorkflow workflow;
  private final Supplier<ResourceWorkspace> workspace;
  private final Supplier<ResourceSigning> signing;
  private final Supplier<ObjectStore> objects;
  private final Supplier<String> base;
  private final ResourceCursor cursors;
  private final EventPublisher events;
  private final Clock clock;

  public ResourceDistributions(
      ResourceStore resources,
      ResourceWorkflowStore db,
      ResourceAccess access,
      ResourceWorkflow workflow,
      Supplier<ResourceWorkspace> workspace,
      Supplier<ResourceSigning> signing,
      Supplier<ObjectStore> objects,
      Supplier<String> base,
      ResourceCursor cursors,
      EventPublisher events,
      Clock clock) {
    this.resources = resources;
    this.db = db;
    this.access = access;
    this.workflow = workflow;
    this.workspace = workspace;
    this.signing = signing;
    this.objects = objects;
    this.base = base;
    this.cursors = cursors;
    this.events = events;
    this.clock = clock;
  }

  private ResourceWorkspace workspace() {
    var value = workspace.get();
    if (value == null) throw new AurigaException(CommonErrors.UNAVAILABLE);
    return value;
  }

  private ResourceWorkspace.Snapshot target(
      AurigaPrincipal actor, ResourceAccess.Reader r, UUID instance) {
    var result = workspace().snapshot(r.tenant(), instance);
    if (!actor.isAdminContext() && !result.owner().equals(r.account()))
      throw new AurigaException(CommonErrors.NOT_FOUND);
    access.target(r.tenant(), result.owner());
    return result;
  }

  public JsonNode create(AurigaPrincipal actor, ObjectNode body, String key) {
    UUID instance = uuid(body, "instanceId");
    var selections = body.path("selections");
    if (!selections.isArray() || selections.size() < 1 || selections.size() > 128) throw invalid();
    Set<UUID> packages = new HashSet<>();
    for (var selection : selections) {
      if (!selection.isObject()
          || !new HashSet<>(selection.propertyNames())
              .equals(Set.of("packageId", "versionId", "action"))
          || !packages.add(uuid(selection, "packageId"))
          || !Set.of("install", "enable", "disable", "remove")
              .contains(text(selection, "action", 16))) throw invalid();
      uuid(selection, "versionId");
    }
    return workflow.operation(
        actor,
        MANAGE,
        false,
        "distribution/" + instance,
        key,
        body,
        null,
        r -> target(actor, r, instance),
        r -> {
          db.lock("distribution/" + instance);
          var snapshot = target(actor, r, instance);
          var owner = access.target(r.tenant(), snapshot.owner());
          for (var selection : selections) validateSelection(owner, snapshot, selection);
          long last =
              number(
                  db.row(
                      "SELECT COALESCE(max(selection_revision),0) AS revision FROM resource.resource_distribution WHERE tenant_id=:tenant AND instance_id=:instance",
                      db.args("instance", instance)),
                  "revision");
          long revision = Math.max(last, snapshot.selectionRevision()) + 1;
          if (revision > ResourceWorkflow.MAX) throw new AurigaException(CommonErrors.CONFLICT);
          UUID id = UuidV7.next();
          db.update(
              "INSERT INTO resource.resource_distribution(id,tenant_id,actor_id,instance_id,owner_id,administrator,selection_revision,selections,state,created_at) VALUES(:id,:tenant,:actor,:instance,:owner,:admin,:rev,CAST(:selections AS jsonb),'requested',:now)",
              db.args(
                  "id",
                  id,
                  "actor",
                  r.account(),
                  "instance",
                  instance,
                  "owner",
                  snapshot.owner(),
                  "admin",
                  actor.isAdminContext(),
                  "rev",
                  revision,
                  "selections",
                  selections.toString(),
                  "now",
                  OffsetDateTime.ofInstant(clock.instant(), ZoneOffset.UTC)));
          // Persist locator identities with the intent, before workspace can commit its independent
          // spec.
          for (var selection : selections) {
            UUID p = uuid(selection, "packageId"), v = uuid(selection, "versionId");
            var version = db.version(p, v);
            storeGrant(
                UuidV7.next(),
                snapshot,
                p,
                v,
                version.get("digest").toString(),
                revision,
                clock.instant().plusSeconds(300));
          }
          for (var plugin : snapshot.plugins())
            if (plugin.hasResourceEnvelope()) {
              var envelope = plugin.getResourceEnvelope();
              UUID p = UUID.fromString(envelope.getPackageId()),
                  v = UUID.fromString(envelope.getVersionId());
              if (!packages.contains(p))
                storeGrant(
                    UuidV7.next(),
                    snapshot,
                    p,
                    v,
                    plugin.getDigest(),
                    revision,
                    clock.instant().plusSeconds(300));
            }
          var currentRevocations = db.revocations();
          if (currentRevocations != null)
            db.enqueueRevocations(currentRevocations.path("revision").asLong());
          var data = new HashMap<String, Object>();
          data.put("distributionId", id.toString());
          data.put("instanceId", instance.toString());
          data.put("ownerId", snapshot.owner().toString());
          data.put("selectionRevision", revision);
          data.put("selections", JSON.convertValue(selections, List.class));
          events.publish("resource.distribution_requested", id.toString(), data, "1");
          workflow.recorded(r, "resource.distribution.request", id);
          return db.distributionJson(db.distribution(id));
        });
  }

  public JsonNode get(AurigaPrincipal actor, UUID id) {
    var reader = access.memberWriter(actor, MANAGE);
    return resources.write(
        () -> {
          var row =
              db.row(
                  "SELECT * FROM resource.resource_distribution WHERE tenant_id=:tenant AND id=:id",
                  db.args("id", id));
          target(actor, reader, (UUID) row.get("instance_id"));
          reconcile(row);
          return db.distributionJson(db.distribution(id));
        });
  }

  public JsonNode list(AurigaPrincipal actor, int limit, String cursor) {
    var r = access.administrator(actor, MANAGE);
    if (limit < 1 || limit > 200) throw invalid();
    var decoded = cursors.decode(cursor, r, "distributions", limit);
    return resources.write(
        () -> {
          var position =
              decoded == null
                  ? cursors.initial(
                      db
                          .rows(
                              "SELECT id FROM resource.resource_distribution WHERE tenant_id=:tenant ORDER BY id DESC LIMIT 1",
                              db.args())
                          .stream()
                          .map(row -> (UUID) row.get("id"))
                          .findFirst()
                          .orElse(new UUID(0, 0)))
                  : decoded;
          var rows =
              db.rows(
                  "SELECT * FROM resource.resource_distribution WHERE tenant_id=:tenant AND id>:after AND id<=:ceiling ORDER BY id LIMIT :limit",
                  db.args(
                      "after",
                      position.after(),
                      "ceiling",
                      position.ceiling(),
                      "limit",
                      limit + 1));
          var out = JSON.createObjectNode();
          var items = JSON.createArrayNode();
          int size = Math.min(rows.size(), limit);
          for (var row : rows.subList(0, size)) {
            reconcile(row);
            items.add(db.distributionJson(db.distribution((UUID) row.get("id"))));
          }
          out.set("items", items);
          if (rows.size() > limit)
            out.put(
                "nextCursor",
                cursors.encode(
                    r,
                    "distributions",
                    limit,
                    new ResourceCursor.Position(
                        (UUID) rows.get(size - 1).get("id"),
                        position.ceiling(),
                        position.expires())));
          return out;
        });
  }

  ObjectNode revocations() {
    db.lock("revocations");
    var old = db.revocations();
    var now = clock.instant();
    if (old != null
        && !db.needsRevocationSignature()
        && Instant.parse(old.path("expiresAt").asString()).isAfter(now.plusSeconds(43200)))
      return old;
    long revision = old == null ? 1 : old.path("revision").asLong() + 1;
    if (revision > ResourceWorkflow.MAX) throw new AurigaException(CommonErrors.CONFLICT);
    var out =
        JSON.createObjectNode()
            .put("schemaVersion", 1)
            .put("tenantId", TenantContext.require().toString())
            .put("revision", revision)
            .put("issuedAt", ResourceSigning.time(now))
            .put("expiresAt", ResourceSigning.time(now.plusSeconds(86400)));
    out.set("revokedKids", old == null ? JSON.createArrayNode() : old.get("revokedKids"));
    out.set("revokedArtifacts", old == null ? JSON.createArrayNode() : old.get("revokedArtifacts"));
    out = signing.get().sign(out, true);
    db.saveRevocations(out);
    db.enqueueRevocations(revision);
    new auriga.common.audit.OutboxAuditLogger(events)
        .record(
            auriga.common.audit.AuditEntry.builder("resource.revocations.renew")
                .target("resource", TenantContext.require().toString())
                .actor(auriga.common.audit.AuditEntry.Actor.SYSTEM)
                .build());
    return out;
  }

  private JsonNode activeEnvelope(
      UUID p, UUID v, Map<String, Object> row, boolean retainedWithdrawal) {
    if (retainedWithdrawal) ResourceWorkflow.requireState(row, "withdrawn");
    else ResourceWorkflow.requireState(row, "published");
    var envelope = json(row, "envelope");
    if (envelope.isNull()) throw new AurigaException(CommonErrors.CONFLICT);
    if (workflow.revoked(envelope)) throw new AurigaException(CommonErrors.CONFLICT);
    if (Instant.parse(envelope.path("notAfter").asString())
        .isBefore(clock.instant().plusSeconds(21600))) {
      envelope =
          workflow.envelope(TenantContext.require(), p, v, row, number(row, "release_sequence"));
      if (workflow.revoked(envelope)) throw new AurigaException(CommonErrors.CONFLICT);
      db.update(
          "UPDATE resource.resource_version SET envelope=CAST(:body AS jsonb) WHERE tenant_id=:tenant AND id=:id",
          db.args("id", v, "body", envelope.toString()));
      new auriga.common.audit.OutboxAuditLogger(events)
          .record(
              auriga.common.audit.AuditEntry.builder("resource.version.renew")
                  .target("resource", v.toString())
                  .actor(auriga.common.audit.AuditEntry.Actor.SYSTEM)
                  .build());
    }
    return envelope;
  }

  JsonNode memberEnvelope(ResourceAccess.Reader reader, UUID p, UUID v) {
    var pack = workflow.pkg(reader, p, false, false);
    var row = db.version(p, v);
    long high =
        number(
            db.row(
                "SELECT release_high_water FROM resource.resource_package WHERE tenant_id=:tenant AND id=:id",
                db.args("id", p)),
            "release_high_water");
    if (!pack.getEnabled()
        || row.get("release_sequence") == null
        || number(row, "release_sequence") != high)
      throw new AurigaException(CommonErrors.NOT_FOUND);
    return activeEnvelope(p, v, row, false);
  }

  private String downloadBase() {
    String value = base.get();
    try {
      URI uri = URI.create(value);
      if (!"https".equals(uri.getScheme())
          || uri.getHost() == null
          || uri.getUserInfo() != null
          || uri.getQuery() != null
          || uri.getFragment() != null
          || !uri.getPath().equals("/internal/resource/v1/downloads/"))
        throw new IllegalArgumentException();
      return value;
    } catch (RuntimeException e) {
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    }
  }

  private ObjectNode grant(
      ResourceWorkspace.Snapshot snapshot, UUID p, UUID v, String digest, long revision) {
    var rows =
        db.rows(
            "SELECT id,expires_at FROM resource.resource_download WHERE tenant_id=:tenant AND instance_id=:instance AND package_id=:pkg AND version_id=:version AND selection_revision=:rev ORDER BY id LIMIT 1",
            db.args("instance", snapshot.instance(), "pkg", p, "version", v, "rev", revision));
    UUID id = rows.isEmpty() ? UuidV7.next() : (UUID) rows.getFirst().get("id");
    var expiry =
        rows.isEmpty()
            ? clock.instant().plusSeconds(300)
            : ((java.sql.Timestamp) rows.getFirst().get("expires_at")).toInstant();
    storeGrant(id, snapshot, p, v, digest, revision, expiry);
    return JSON.createObjectNode()
        .put("downloadId", id.toString())
        .put("tenantId", TenantContext.require().toString())
        .put("nodeId", snapshot.node().toString())
        .put("instanceId", snapshot.instance().toString())
        .put("ownerId", snapshot.owner().toString())
        .put("packageId", p.toString())
        .put("versionId", v.toString())
        .put("digest", digest)
        .put("selectionRevision", revision)
        .put("expiresAt", expiry.toString())
        .put("url", downloadBase() + id);
  }

  private void storeGrant(
      UUID id,
      ResourceWorkspace.Snapshot snapshot,
      UUID p,
      UUID v,
      String digest,
      long revision,
      Instant expiry) {
    db.update(
        "INSERT INTO resource.resource_download(id,tenant_id,node_id,instance_id,owner_id,package_id,version_id,digest,selection_revision,expires_at) VALUES(:id,:tenant,:node,:instance,:owner,:pkg,:version,:digest,:rev,:expiry) ON CONFLICT(id) DO UPDATE SET node_id=excluded.node_id,expires_at=excluded.expires_at",
        db.args(
            "id",
            id,
            "node",
            snapshot.node(),
            "instance",
            snapshot.instance(),
            "owner",
            snapshot.owner(),
            "pkg",
            p,
            "version",
            v,
            "digest",
            digest,
            "rev",
            revision,
            "expiry",
            OffsetDateTime.ofInstant(expiry, ZoneOffset.UTC)));
  }

  private void reconcile(Map<String, Object> row) {
    UUID id = (UUID) row.get("id"), instance = (UUID) row.get("instance_id");
    try {
      db.lock("distribution/" + instance);
      row = db.distribution(id);
      if (row.get("state").equals("superseded")) return;
      var snapshot = workspace().snapshot(TenantContext.require(), instance);
      long revision = number(row, "selection_revision");
      if (snapshot.selectionRevision() > revision) {
        if (Set.of("requested", "resolved", "applying").contains(row.get("state")))
          db.update(
              "UPDATE resource.resource_distribution SET state='superseded',revision=revision+1 WHERE tenant_id=:tenant AND id=:id",
              db.args("id", id));
        return;
      }
      if (!snapshot.owner().equals(row.get("owner_id")))
        throw new AurigaException(CommonErrors.FORBIDDEN);
      var owner = access.target(TenantContext.require(), snapshot.owner());
      if (row.get("state").equals("requested")) {
        access.target(TenantContext.require(), (UUID) row.get("actor_id"), MANAGE);
        if (!Boolean.TRUE.equals(row.get("administrator"))
            && !snapshot.owner().equals(row.get("actor_id")))
          throw new AurigaException(CommonErrors.FORBIDDEN);
        if (snapshot.selectionRevision() > revision)
          throw new AurigaException(CommonErrors.CONFLICT);
        if (snapshot.node() == null) return;
        var changes = new ArrayList<ResourceWorkspace.Selection>();
        for (var selection : json(row, "selections")) {
          UUID p = uuid(selection, "packageId"), v = uuid(selection, "versionId");
          var pack = workflow.pkg(owner, p, false, false);
          validateSelection(owner, snapshot, selection);
          var version = db.version(p, v);
          if (Set.of("disable", "remove").contains(selection.path("action").asString())) {
            var old = selected(snapshot, p, v);
            changes.add(new ResourceWorkspace.Selection(old, selection.path("action").asString()));
            continue;
          }
          var envelope = activeEnvelope(p, v, version, false);
          var grant = grant(snapshot, p, v, version.get("digest").toString(), revision);
          changes.add(
              new ResourceWorkspace.Selection(
                  ResourceProtocol.plugin(
                      envelope,
                      pack.getLevel().getValue(),
                      grant.path("url").asString(),
                      grant.path("downloadId").asString(),
                      signing.get()),
                  selection.path("action").asString()));
        }
        var changed = new HashSet<String>();
        for (var selection : json(row, "selections"))
          changed.add(selection.path("packageId").asString());
        changes.addAll(retained(owner, snapshot, revision, changed));
        snapshot =
            workspace()
                .apply(
                    TenantContext.require(),
                    instance,
                    snapshot.owner(),
                    revision,
                    Boolean.TRUE.equals(row.get("administrator")),
                    changes,
                    ResourceProtocol.revocations(revocations()));
        db.update(
            "UPDATE resource.resource_distribution SET state='resolved',generation=:gen,revision=revision+1 WHERE tenant_id=:tenant AND id=:id",
            db.args("id", id, "gen", snapshot.generation()));
        return;
      }
      if (snapshot.selectionRevision() != revision)
        throw new AurigaException(CommonErrors.CONFLICT);
      snapshot =
          workspace()
              .refresh(
                  TenantContext.require(),
                  instance,
                  snapshot.owner(),
                  revision,
                  retained(owner, snapshot, revision, Set.of()),
                  ResourceProtocol.revocations(revocations()));
      long generation = snapshot.generation();
      String state =
          snapshot.observedGeneration() == generation && snapshot.ready()
              ? "applied"
              : snapshot.errorCode().isBlank() ? "applying" : "failed";
      if (!Objects.equals(row.get("generation"), generation)
          || !state.equals(row.get("state"))
          || !Objects.equals(row.get("observed_generation"), snapshot.observedGeneration())
          || !Objects.equals(row.get("ready"), snapshot.ready()))
        db.update(
            "UPDATE resource.resource_distribution SET state=:state,generation=:gen,observed_generation=:observed,ready=:ready,error_code=:error,applied_generation=CASE WHEN :state='applied' THEN :gen ELSE applied_generation END,revision=revision+1 WHERE tenant_id=:tenant AND id=:id",
            db.args(
                "id",
                id,
                "state",
                state,
                "gen",
                generation,
                "observed",
                snapshot.observedGeneration(),
                "ready",
                snapshot.ready(),
                "error",
                snapshot.errorCode().isBlank() ? null : snapshot.errorCode()));
    } catch (AurigaException e) {
      if (e.errorCode() == CommonErrors.UNAVAILABLE) return;
      db.update(
          "UPDATE resource.resource_distribution SET state='failed',error_code='resource.distribution_denied',revision=revision+1 WHERE tenant_id=:tenant AND id=:id AND state<>'failed'",
          db.args("id", id));
    } finally {
      db.update(
          "UPDATE resource.resource_distribution SET reconciled_at=:now WHERE tenant_id=:tenant AND id=:id",
          db.args("id", id, "now", OffsetDateTime.ofInstant(clock.instant(), ZoneOffset.UTC)));
    }
  }

  private List<ResourceWorkspace.Selection> retained(
      ResourceAccess.Reader owner,
      ResourceWorkspace.Snapshot snapshot,
      long revision,
      Set<String> changed) {
    var result = new ArrayList<ResourceWorkspace.Selection>();
    for (var old : snapshot.plugins())
      if (old.hasResourceEnvelope()
          && !changed.contains(old.getResourceEnvelope().getPackageId())) {
        UUID p = UUID.fromString(old.getResourceEnvelope().getPackageId()),
            v = UUID.fromString(old.getResourceEnvelope().getVersionId());
        var pack = workflow.pkg(owner, p, false, false);
        var version = db.version(p, v);
        // An ordinary withdrawal may renew an already selected version, never its download grant.
        boolean withdrawn = version.get("state").equals("withdrawn");
        if (withdrawn && workflow.revoked(json(version, "envelope"))) continue;
        if (!withdrawn && (!version.get("state").equals("published") || !pack.getEnabled()))
          continue;
        if (!old.getDigest().equals(version.get("digest"))
            || old.getResourceEnvelope().getReleaseSequence()
                != number(version, "release_sequence"))
          throw new AurigaException(CommonErrors.CONFLICT);
        var authorization = withdrawn ? null : grant(snapshot, p, v, old.getDigest(), revision);
        result.add(
            new ResourceWorkspace.Selection(
                ResourceProtocol.plugin(
                    activeEnvelope(p, v, version, withdrawn),
                    pack.getLevel().getValue(),
                    withdrawn ? old.getUrl() : authorization.path("url").asString(),
                    withdrawn ? old.getDownloadId() : authorization.path("downloadId").asString(),
                    signing.get()),
                "retain"));
      }
    return result;
  }

  private auriga.contracts.node.v1.PluginRef selected(
      ResourceWorkspace.Snapshot snapshot, UUID p, UUID v) {
    return snapshot.plugins().stream()
        .filter(
            old ->
                old.hasResourceEnvelope()
                    && old.getResourceEnvelope().getPackageId().equals(p.toString())
                    && old.getResourceEnvelope().getVersionId().equals(v.toString()))
        .findFirst()
        .orElseThrow(() -> new AurigaException(CommonErrors.CONFLICT));
  }

  private void validateSelection(
      ResourceAccess.Reader owner, ResourceWorkspace.Snapshot snapshot, JsonNode selection) {
    UUID p = uuid(selection, "packageId"), v = uuid(selection, "versionId");
    var pack = workflow.pkg(owner, p, false, false);
    var version = db.version(p, v);
    if (pack.getLevel().getValue().equals("l0")) throw new AurigaException(CommonErrors.FORBIDDEN);
    if (Set.of("disable", "remove").contains(selection.path("action").asString())) {
      selected(snapshot, p, v);
      return;
    }
    ResourceWorkflow.requireState(version, "published");
    if (!pack.getEnabled()) throw new AurigaException(CommonErrors.NOT_FOUND);
    long high =
        number(
            db.row(
                "SELECT release_high_water FROM resource.resource_package WHERE tenant_id=:tenant AND id=:id",
                db.args("id", p)),
            "release_high_water");
    if (number(version, "release_sequence") != high)
      throw new AurigaException(CommonErrors.CONFLICT);
  }

  public void propagate() {
    var pending =
        resources.write(
            () -> {
              if (db.revocations() == null) return List.<Map<String, Object>>of();
              revocations();
              return db.rows(
                  "SELECT instance_id FROM resource.resource_revocation_delivery WHERE tenant_id=:tenant AND completed_revision<revision ORDER BY instance_id",
                  db.args());
            });
    for (var row : pending) {
      try {
        resources.writeIndependent(
            () -> {
              UUID instance = (UUID) row.get("instance_id");
              db.lock("revocation-delivery/" + instance);
              if (db.rows(
                      "SELECT instance_id FROM resource.resource_revocation_delivery WHERE tenant_id=:tenant AND instance_id=:instance AND completed_revision<revision FOR UPDATE",
                      db.args("instance", instance))
                  .isEmpty()) return false;
              var list = db.revocations();
              if (list == null || db.needsRevocationSignature())
                throw new AurigaException(CommonErrors.UNAVAILABLE);
              workspace()
                  .revoke(TenantContext.require(), instance, ResourceProtocol.revocations(list));
              db.update(
                  "UPDATE resource.resource_revocation_delivery SET completed_revision=:rev WHERE tenant_id=:tenant AND instance_id=:instance AND revision<=:rev",
                  db.args("instance", instance, "rev", list.path("revision").asLong()));
              return true;
            });
      } catch (RuntimeException e) {
        retry(e);
      }
    }
  }

  public void propagateNow() {
    try {
      propagate();
    } catch (RuntimeException e) {
      org.slf4j.LoggerFactory.getLogger(getClass())
          .warn("Resource revocation propagation will retry");
    }
  }

  @Scheduled(fixedDelayString = "${auriga.resource.reconcile-ms:5000}")
  public void tick() {
    try {
      if (workspace.get() == null) return;
      for (UUID tenant : workspace().tenants())
        try {
          TenantContext.run(
              tenant,
              () -> {
                var candidates =
                    resources.write(
                        () ->
                            db.rows(
                                "SELECT * FROM resource.resource_distribution WHERE tenant_id=:tenant AND state IN ('requested','resolved','applying','applied') ORDER BY reconciled_at,id LIMIT 200",
                                db.args()));
                for (var row : candidates)
                  try {
                    resources.write(
                        () -> {
                          reconcile(row);
                          return true;
                        });
                  } catch (RuntimeException e) {
                    retry(e);
                  }
                if (!candidates.isEmpty() || resources.write(() -> db.revocations() != null)) {
                  resources.write(this::revocations);
                  propagate();
                }
                if (!candidates.isEmpty()) objects.get().cleanupExpiredStaging(tenant);
              });
        } catch (RuntimeException e) {
          retry(e);
        }
    } catch (RuntimeException e) {
      retry(e);
    }
  }

  private void retry(RuntimeException e) {
    org.slf4j.LoggerFactory.getLogger(getClass())
        .warn(
            "Resource reconciliation will retry: {}",
            e instanceof AurigaException a ? a.errorCode().code() : "resource.unavailable");
  }

  private Map<String, Object> authorizeDownload(UUID node, UUID id, boolean expiry) {
    var row =
        db.row(
            "SELECT * FROM resource.resource_download WHERE tenant_id=:tenant AND id=:id",
            db.args("id", id));
    if (!node.equals(row.get("node_id"))) throw new AurigaException(CommonErrors.NOT_FOUND);
    if (expiry
        && !((java.sql.Timestamp) row.get("expires_at")).toInstant().isAfter(clock.instant()))
      throw new AurigaException(CommonErrors.FORBIDDEN);
    var snapshot = workspace().snapshot(TenantContext.require(), (UUID) row.get("instance_id"));
    if (!node.equals(snapshot.node())
        || !snapshot.owner().equals(row.get("owner_id"))
        || snapshot.selectionRevision() != number(row, "selection_revision"))
      throw new AurigaException(CommonErrors.NOT_FOUND);
    var selected =
        snapshot.plugins().stream()
            .filter(
                p ->
                    p.hasResourceEnvelope()
                        && p.getResourceEnvelope()
                            .getPackageId()
                            .equals(row.get("package_id").toString())
                        && p.getResourceEnvelope()
                            .getVersionId()
                            .equals(row.get("version_id").toString())
                        && p.getDigest().equals(row.get("digest"))
                        && p.getDownloadId().equals(id.toString()))
            .findFirst()
            .orElseThrow(() -> new AurigaException(CommonErrors.NOT_FOUND));
    if (!clock.instant().isBefore(Instant.parse(selected.getResourceEnvelope().getNotAfter())))
      throw new AurigaException(CommonErrors.NOT_FOUND);
    var reader = access.target(TenantContext.require(), snapshot.owner());
    var pkg = workflow.pkg(reader, (UUID) row.get("package_id"), false, false);
    var version = db.version(pkg.getId(), (UUID) row.get("version_id"));
    ResourceWorkflow.requireState(version, "published");
    if (!pkg.getEnabled()
        || !version.get("digest").equals(row.get("digest"))
        || workflow.revoked(json(version, "envelope")))
      throw new AurigaException(CommonErrors.NOT_FOUND);
    return row;
  }

  public InputStream download(UUID tenant, UUID node, UUID id, boolean range) {
    if (range) throw invalid();
    return TenantContext.call(
        tenant,
        () -> {
          var info =
              resources.write(
                  () -> {
                    var row = authorizeDownload(node, id, true);
                    return new ObjectStore.ObjectInfo(
                        row.get("digest").toString(),
                        number(
                            db.version((UUID) row.get("package_id"), (UUID) row.get("version_id")),
                            "size_bytes"));
                  });
          return objects.get().open(tenant, info);
        });
  }

  public JsonNode renew(UUID tenant, UUID node, UUID id, String key) {
    if (key == null || !key.matches("[\\x21-\\x7E]{1,128}")) throw invalid();
    return TenantContext.call(
        tenant,
        () ->
            resources.write(
                () -> {
                  db.lock("renew/" + id);
                  var row = authorizeDownload(node, id, false);
                  String op = "renew/" + id;
                  var previous = db.replay(node, op, key, id.toString(), clock.instant());
                  if (previous.isPresent()) return previous.get();
                  var snapshot = workspace().snapshot(tenant, (UUID) row.get("instance_id"));
                  storeGrant(
                      id,
                      snapshot,
                      (UUID) row.get("package_id"),
                      (UUID) row.get("version_id"),
                      row.get("digest").toString(),
                      number(row, "selection_revision"),
                      clock.instant().plusSeconds(300));
                  var result =
                      grant(
                          snapshot,
                          (UUID) row.get("package_id"),
                          (UUID) row.get("version_id"),
                          row.get("digest").toString(),
                          number(row, "selection_revision"));
                  db.remember(node, op, key, id.toString(), result, clock.instant());
                  return result;
                }));
  }

  public UUID authenticate(java.security.cert.X509Certificate[] chain) {
    return workspace().authenticate(chain);
  }

  public UUID nodeTenant(UUID node) {
    return workspace().nodeTenant(node);
  }

  public JsonNode revocationDocument(AurigaPrincipal actor) {
    access.resolve(actor);
    return resources.write(this::revocations);
  }
}
