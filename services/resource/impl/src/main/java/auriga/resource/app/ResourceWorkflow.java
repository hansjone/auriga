package auriga.resource.app;

import static auriga.resource.persistence.ResourceWorkflowStore.json;
import static auriga.resource.persistence.ResourceWorkflowStore.number;
import static auriga.resource.web.ResourceBody.*;

import auriga.common.audit.*;
import auriga.common.error.*;
import auriga.common.events.EventPublisher;
import auriga.common.persistence.UuidV7;
import auriga.common.security.AurigaPrincipal;
import auriga.resource.archive.TarGzipArchiveValidator;
import auriga.resource.objectstore.*;
import auriga.resource.persistence.*;
import auriga.resource.web.model.*;
import java.io.*;
import java.nio.charset.StandardCharsets;
import java.security.*;
import java.time.*;
import java.util.*;
import java.util.function.*;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** Immutable bytes, exact review binding and serialized tenant-scoped workflow mutations. */
public final class ResourceWorkflow {
  public static final long MAX = 9007199254740991L;
  private final ResourceStore resources;
  private final ResourceWorkflowStore db;
  private final ResourceAccess access;
  private final Supplier<ObjectStore> objects;
  private final Supplier<ResourceSigning> signing;
  private final EventPublisher events;
  private final AuditLogger audit;
  private final Clock clock;

  public ResourceWorkflow(
      ResourceStore resources,
      ResourceWorkflowStore db,
      ResourceAccess access,
      Supplier<ObjectStore> objects,
      Supplier<ResourceSigning> signing,
      EventPublisher events,
      Clock clock) {
    this.resources = resources;
    this.db = db;
    this.access = access;
    this.objects = objects;
    this.signing = signing;
    this.events = events;
    this.clock = clock;
    this.audit = new OutboxAuditLogger(events);
  }

  public static void match(String etag, long revision) {
    if (etag == null || !etag.equals("\"" + revision + "\""))
      throw new AurigaException(CommonErrors.PRECONDITION_FAILED);
    if (revision >= MAX) throw new AurigaException(CommonErrors.CONFLICT);
  }

  private ResourceAccess.Reader authorize(AurigaPrincipal actor, String permission, boolean admin) {
    if (permission.equals(ResourceAccess.READ)) return access.member(actor);
    if (permission.equals("resource.levels.admin")) return access.owner(actor);
    return admin ? access.administrator(actor, permission) : access.memberWriter(actor, permission);
  }

  JsonNode operation(
      AurigaPrincipal actor,
      String permission,
      boolean admin,
      String op,
      String key,
      ObjectNode body,
      String etag,
      Consumer<ResourceAccess.Reader> guard,
      Function<ResourceAccess.Reader, JsonNode> action) {
    return operation(actor, permission, admin, op, key, body, etag, guard, action, true);
  }

  JsonNode operation(
      AurigaPrincipal actor,
      String permission,
      boolean admin,
      String op,
      String key,
      ObjectNode body,
      String etag,
      Consumer<ResourceAccess.Reader> guard,
      Function<ResourceAccess.Reader, JsonNode> action,
      boolean replayResponse) {
    authorize(actor, permission, admin);
    if (key == null || !key.matches("[\\x21-\\x7E]{1,128}")) throw invalid();
    String fingerprint;
    try {
      fingerprint =
          hash(op + "\n" + Objects.toString(etag, "") + "\n" + ResourceSigning.canonical(body));
    } catch (IllegalArgumentException e) {
      throw invalid();
    }
    try {
      return resources.write(
          () -> {
            db.lock(actor.subject() + ":" + op + ":" + key);
            var r = authorize(actor, permission, admin);
            guard.accept(r);
            var replay = db.replay(r.account(), op, key, fingerprint, clock.instant());
            if (replay.isPresent()) {
              if (!replayResponse) throw new AurigaException(CommonErrors.CONFLICT);
              return replay.get();
            }
            var result = action.apply(r);
            var receipt = replayResponse ? result : JSON.createObjectNode().put("completed", true);
            db.remember(r.account(), op, key, fingerprint, receipt, clock.instant());
            return result;
          });
    } catch (org.springframework.dao.DataIntegrityViolationException e) {
      throw new AurigaException(CommonErrors.CONFLICT);
    }
  }

  public static String hash(String text) {
    try {
      return HexFormat.of()
          .formatHex(
              MessageDigest.getInstance("SHA-256").digest(text.getBytes(StandardCharsets.UTF_8)));
    } catch (NoSuchAlgorithmException e) {
      throw new IllegalStateException(e);
    }
  }

  ModelPackage pkg(ResourceAccess.Reader r, UUID id, boolean owner, boolean shared) {
    resources.lockPackage(r, id).orElseThrow(() -> new AurigaException(CommonErrors.NOT_FOUND));
    var p =
        resources.packageById(r, id).orElseThrow(() -> new AurigaException(CommonErrors.NOT_FOUND));
    if (owner && p.getLevel() == ModelPackage.LevelEnum.L0)
      throw new AurigaException(CommonErrors.FORBIDDEN);
    if (owner && !p.getOwnerId().equals(r.account()))
      throw new AurigaException(CommonErrors.FORBIDDEN);
    if (shared
        && (p.getVisibility() == ModelPackage.VisibilityEnum.PRIVATE
            || p.getLevel() == ModelPackage.LevelEnum.L4))
      throw new AurigaException(CommonErrors.NOT_FOUND);
    return p;
  }

  private Map<String, Object> version(
      ResourceAccess.Reader r, UUID p, UUID v, boolean own, boolean shared) {
    pkg(r, p, false, shared);
    var row = db.version(p, v);
    if (own && !row.get("author_id").equals(r.account()))
      throw new AurigaException(CommonErrors.FORBIDDEN);
    return row;
  }

  void recorded(ResourceAccess.Reader r, String action, UUID id) {
    audit.record(
        AuditEntry.builder(action)
            .target("resource", id.toString())
            .actor(new AuditEntry.Actor("user", r.account().toString()))
            .build());
  }

  private static String safeReason(String reason) {
    return auriga.common.observability.SensitiveDataMasker.mask(reason)
        .replaceAll("(?i)https?://[^\\s]+", "[url omitted]");
  }

  private void recorded(ResourceAccess.Reader r, String action, UUID id, String reason) {
    audit.record(
        AuditEntry.builder(action)
            .target("resource", id.toString())
            .actor(new AuditEntry.Actor("user", r.account().toString()))
            .detail("reason", reason)
            .build());
  }

  private void state(UUID p, UUID v, String state) {
    db.update(
        "UPDATE resource.resource_version SET state=:state,revision=revision+1 WHERE tenant_id=:tenant AND package_id=:pkg AND id=:id AND revision<9007199254740991",
        db.args("pkg", p, "id", v, "state", state));
  }

  static void requireState(Map<String, Object> row, String... states) {
    if (number(row, "revision") >= MAX) throw new AurigaException(CommonErrors.CONFLICT);
    if (!List.of(states).contains(row.get("state"))
        || !Boolean.TRUE.equals(row.get("archive_verified")))
      throw new AurigaException(CommonErrors.CONFLICT);
  }

  public JsonNode upload(
      AurigaPrincipal actor,
      UUID p,
      ObjectNode metadata,
      InputStream archive,
      String key,
      String etag) {
    var r = access.writer(actor);
    resources.write(
        () -> {
          pkg(r, p, true, false);
          return true;
        });
    String version = text(metadata, "version", 128), digest = text(metadata, "expectedDigest", 71);
    if (!digest.matches("sha256:[0-9a-f]{64}") || !ResourceSemver.valid(version)) throw invalid();
    var manifest = metadata.path("manifest");
    if (!manifest.isObject()
        || !new HashSet<>(manifest.propertyNames())
            .equals(Set.of("name", "dshVersionRange", "os", "arch"))) throw invalid();
    String name = text(manifest, "name", 214);
    if (!name.matches("(@[a-z0-9._-]+/)?[a-z0-9._-]+")) throw invalid();
    text(manifest, "dshVersionRange", 128);
    String os = text(manifest, "os", 16), arch = text(manifest, "arch", 16);
    if (!Set.of("linux", "windows", "any").contains(os)
        || !Set.of("amd64", "arm64", "any").contains(arch)) throw invalid();
    if (metadata.has("changeNotes")) text(metadata, "changeNotes", 4096);
    ObjectStore store = objects.get();
    var stage = store.putStaged(r.tenant(), archive, ObjectStore.MAX_BYTES);
    try {
      var checked =
          new TarGzipArchiveValidator(store)
              .validate(
                  r.tenant(),
                  stage.id(),
                  stage.object(),
                  digest,
                  UploadMetadataManifest.OsEnum.fromValue(os));
      if (checked.packageManifest() == null) throw invalid();
      var packageJson = parse(checked.packageManifest(), 1048576, null, Set.of("name", "version"));
      if ((packageJson.has("engines") && !packageJson.path("engines").isObject())
          || (packageJson.has("auriga") && !packageJson.path("auriga").isObject())) throw invalid();
      if (!name.equals(text(packageJson, "name", 214))
          || !version.equals(text(packageJson, "version", 128))) throw invalid();
      if (packageJson.path("engines").has("dsh")
          && !text(manifest, "dshVersionRange", 128)
              .equals(text(packageJson.path("engines"), "dsh", 128))) throw invalid();
      var permissions = packageJson.path("auriga").path("permissions");
      var declared = JSON.createArrayNode();
      if (!permissions.isMissingNode()) {
        if (!permissions.isArray() || permissions.size() > 128) throw invalid();
        var unique = new HashSet<String>();
        for (var permission : permissions) {
          if (!permission.isString()
              || !permission.asString().matches("[a-zA-Z0-9._:/-]{1,128}")
              || !unique.add(permission.asString())) throw invalid();
          declared.add(permission.asString());
        }
      }
      ObjectNode stored = metadata.deepCopy();
      stored.set("permissions", declared);
      return operation(
          actor,
          ResourceAccess.UPLOAD,
          false,
          "upload/" + p,
          key,
          metadata,
          etag,
          current -> pkg(current, p, true, false),
          current -> {
            var pack = pkg(current, p, true, false);
            match(etag, pack.getRevision());
            if (!name.equals(pack.getName())) throw invalid();
            if (!db.rows(
                    "SELECT id FROM resource.resource_version WHERE tenant_id=:tenant AND package_id=:pkg AND version=:version",
                    db.args("pkg", p, "version", version))
                .isEmpty()) throw new AurigaException(CommonErrors.CONFLICT);
            db.lock("object/" + digest);
            store.commitIfAbsent(current.tenant(), stage.id(), stage.object());
            UUID id = UuidV7.next();
            db.update(
                "INSERT INTO resource.resource_version(id,tenant_id,package_id,author_id,version,digest,size_bytes,state,revision,created_at,manifest,files,archive_verified) VALUES(:id,:tenant,:pkg,:author,:version,:digest,:size,'draft',1,:created,CAST(:manifest AS jsonb),CAST(:files AS jsonb),true)",
                db.args(
                    "id",
                    id,
                    "pkg",
                    p,
                    "author",
                    current.account(),
                    "version",
                    version,
                    "digest",
                    digest,
                    "size",
                    stage.object().sizeBytes(),
                    "created",
                    OffsetDateTime.ofInstant(clock.instant(), ZoneOffset.UTC),
                    "manifest",
                    stored.toString(),
                    "files",
                    JSON.writeValueAsString(checked.inventory())));
            db.update(
                "UPDATE resource.resource_package SET revision=revision+1 WHERE tenant_id=:tenant AND id=:id",
                db.args("id", p));
            recorded(current, "resource.version.upload", id);
            return db.versionJson(db.version(p, id));
          });
    } finally {
      store.abort(r.tenant(), stage.id());
    }
  }

  public JsonNode submit(
      AurigaPrincipal actor, UUID p, UUID v, ObjectNode body, String key, String etag) {
    String reason = text(body, "reason", 1024);
    return operation(
        actor,
        "resource.versions.submit",
        false,
        "submit/" + v,
        key,
        body,
        etag,
        r -> version(r, p, v, true, true),
        r -> {
          var row = version(r, p, v, true, true);
          match(etag, number(row, "revision"));
          requireState(row, "draft");
          state(p, v, "pending_review");
          var next = db.version(p, v);
          UUID id = UuidV7.next();
          db.update(
              "INSERT INTO resource.resource_review(id,tenant_id,package_id,version_id,digest,version_revision,state,revision,submitter_id,reason,created_at) VALUES(:id,:tenant,:pkg,:version,:digest,:rev,'pending',1,:actor,:reason,:created)",
              db.args(
                  "id",
                  id,
                  "pkg",
                  p,
                  "version",
                  v,
                  "digest",
                  row.get("digest"),
                  "rev",
                  number(next, "revision"),
                  "actor",
                  r.account(),
                  "reason",
                  reason,
                  "created",
                  OffsetDateTime.ofInstant(clock.instant(), ZoneOffset.UTC)));
          recorded(r, "resource.version.submit", v);
          return db.reviewJson(db.review(id));
        });
  }

  public JsonNode draft(
      AurigaPrincipal actor, UUID p, UUID v, ObjectNode body, String key, String etag) {
    text(body, "reason", 1024);
    return operation(
        actor,
        ResourceAccess.UPLOAD,
        false,
        "draft/" + v,
        key,
        body,
        etag,
        r -> version(r, p, v, true, false),
        r -> {
          var row = version(r, p, v, true, false);
          match(etag, number(row, "revision"));
          requireState(row, "rejected");
          state(p, v, "draft");
          recorded(r, "resource.version.draft", v);
          return db.versionJson(db.version(p, v));
        });
  }

  private Map<String, Object> reviewGuard(ResourceAccess.Reader r, UUID id) {
    var review = db.review(id);
    version(r, (UUID) review.get("package_id"), (UUID) review.get("version_id"), false, true);
    return review;
  }

  public JsonNode decide(AurigaPrincipal actor, UUID id, ObjectNode body, String key, String etag) {
    String reason = text(body, "reason", 1024), decision = text(body, "decision", 16);
    if (!Set.of("approve", "reject").contains(decision)) throw invalid();
    return operation(
        actor,
        "resource.versions.review",
        true,
        "review/" + id,
        key,
        body,
        etag,
        r -> reviewGuard(r, id),
        r -> {
          var review = reviewGuard(r, id);
          UUID p = (UUID) review.get("package_id"), v = (UUID) review.get("version_id");
          var row = db.version(p, v);
          if (row.get("author_id").equals(r.account()))
            throw new AurigaException(CommonErrors.FORBIDDEN);
          match(etag, number(review, "revision"));
          requireState(row, "pending_review");
          if (!review.get("state").equals("pending")
              || counter(body, "versionRevision") != number(row, "revision")
              || number(review, "version_revision") != number(row, "revision")
              || !text(body, "digest", 71).equals(row.get("digest"))
              || !row.get("digest").equals(review.get("digest")))
            throw new AurigaException(CommonErrors.CONFLICT);
          String next = decision.equals("approve") ? "approved" : "rejected";
          db.update(
              "UPDATE resource.resource_review SET state=:state,revision=revision+1,reviewer_id=:actor,reason=:reason,decided_at=:now WHERE tenant_id=:tenant AND id=:id",
              db.args(
                  "id",
                  id,
                  "state",
                  next,
                  "actor",
                  r.account(),
                  "reason",
                  reason,
                  "now",
                  OffsetDateTime.ofInstant(clock.instant(), ZoneOffset.UTC)));
          state(p, v, next);
          recorded(r, "resource.review." + decision, v);
          return db.reviewJson(db.review(id));
        });
  }

  public JsonNode publish(
      AurigaPrincipal actor,
      UUID p,
      UUID v,
      ObjectNode body,
      String key,
      String etag,
      boolean personal) {
    text(body, "reason", 1024);
    return operation(
        actor,
        personal ? ResourceAccess.UPLOAD : "resource.versions.publish",
        !personal,
        "publish/" + v,
        key,
        body,
        etag,
        r -> version(r, p, v, personal, !personal),
        r -> {
          var pack = pkg(r, p, personal, !personal);
          var row = db.version(p, v);
          match(etag, number(row, "revision"));
          if (personal) {
            if (pack.getLevel() != ModelPackage.LevelEnum.L4
                || pack.getVisibility() != ModelPackage.VisibilityEnum.PRIVATE
                || !r.personalPlugins()) throw new AurigaException(CommonErrors.FORBIDDEN);
            requireState(row, "draft");
          } else {
            if (row.get("author_id").equals(r.account()))
              throw new AurigaException(CommonErrors.FORBIDDEN);
            requireState(row, "approved");
            var reviews = db.reviews(v);
            if (reviews.stream()
                .noneMatch(
                    review ->
                        review.get("state").equals("approved")
                            && review.get("digest").equals(row.get("digest"))
                            && number(review, "version_revision") == number(row, "revision") - 1))
              throw new AurigaException(CommonErrors.CONFLICT);
          }
          if (pack.getRevision() >= MAX) throw new AurigaException(CommonErrors.CONFLICT);
          var locked =
              db.row(
                  "SELECT release_high_water FROM resource.resource_package WHERE tenant_id=:tenant AND id=:id FOR UPDATE",
                  db.args("id", p));
          var preceding =
              db.rows(
                  "SELECT version FROM resource.resource_version WHERE tenant_id=:tenant AND package_id=:pkg AND release_sequence=:seq",
                  db.args("pkg", p, "seq", number(locked, "release_high_water")));
          if (!preceding.isEmpty()) {
            String previous = preceding.getFirst().get("version").toString();
            if (!ResourceSemver.valid(previous)
                || !ResourceSemver.valid(row.get("version").toString())
                || ResourceSemver.compare(row.get("version").toString(), previous) < 0)
              throw new AurigaException(CommonErrors.CONFLICT);
          }
          long sequence = number(locked, "release_high_water") + 1;
          if (sequence > MAX) throw new AurigaException(CommonErrors.CONFLICT);
          var envelope = envelope(r.tenant(), p, v, row, sequence);
          db.update(
              "UPDATE resource.resource_version SET state='published',revision=revision+1,release_sequence=:seq,envelope=CAST(:envelope AS jsonb) WHERE tenant_id=:tenant AND id=:id",
              db.args("id", v, "seq", sequence, "envelope", envelope.toString()));
          db.update(
              "UPDATE resource.resource_package SET release_high_water=:seq,enabled=true,default_version_id=:version,revision=revision+1 WHERE tenant_id=:tenant AND id=:id AND revision<9007199254740991",
              db.args("id", p, "version", v, "seq", sequence));
          recorded(r, personal ? "resource.personal.sign" : "resource.version.publish", v);
          var next = db.version(p, v);
          events.publish("resource.version_published", v.toString(), event(next), "1");
          return db.versionJson(next);
        });
  }

  boolean revoked(JsonNode envelope) {
    var list = db.revocations();
    if (list == null) return false;
    for (var kid : list.path("revokedKids"))
      if (kid.asString().equals(envelope.path("signature").path("kid").asString())) return true;
    for (var artifact : list.path("revokedArtifacts"))
      if (artifact.path("packageId").equals(envelope.path("packageId"))
          && artifact.path("versionId").equals(envelope.path("versionId"))
          && artifact.path("digest").equals(envelope.path("statement").path("digest"))) return true;
    return false;
  }

  public ObjectNode envelope(UUID tenant, UUID p, UUID v, Map<String, Object> row, long sequence) {
    var now = clock.instant();
    var manifest = json(row, "manifest").path("manifest");
    var statement =
        JSON.createObjectNode()
            .put("purpose", "plugin")
            .put("name", manifest.path("name").asString())
            .put("version", row.get("version").toString())
            .put("os", manifest.path("os").asString())
            .put("arch", manifest.path("arch").asString())
            .put("digest", row.get("digest").toString())
            .put("issuedAt", ResourceSigning.time(now));
    var body =
        JSON.createObjectNode()
            .put("schemaVersion", 1)
            .put("tenantId", tenant.toString())
            .put("packageId", p.toString())
            .put("versionId", v.toString())
            .put("releaseSequence", sequence)
            .put("notAfter", ResourceSigning.time(now.plusSeconds(86400)));
    body.set("statement", statement);
    return signing.get().sign(body, false);
  }

  private Map<String, Object> event(Map<String, Object> row) {
    return new HashMap<>(
        Map.of(
            "packageId",
            row.get("package_id").toString(),
            "versionId",
            row.get("id").toString(),
            "version",
            row.get("version"),
            "digest",
            row.get("digest"),
            "revision",
            number(row, "revision")));
  }

  public JsonNode withdraw(
      AurigaPrincipal actor, UUID p, UUID v, ObjectNode body, String key, String etag) {
    String reason = safeReason(text(body, "reason", 1024));
    String mode = text(body, "mode", 16);
    if (!Set.of("withdrawn", "malicious").contains(mode)) throw invalid();
    return operation(
        actor,
        "resource.versions.publish",
        true,
        "withdraw/" + v,
        key,
        body,
        etag,
        r -> version(r, p, v, false, true),
        r -> {
          var row = version(r, p, v, false, true);
          match(etag, number(row, "revision"));
          if (mode.equals("malicious")) requireState(row, "published", "deprecated", "withdrawn");
          else requireState(row, "published", "deprecated");
          if (pkg(r, p, false, true).getRevision() >= MAX)
            throw new AurigaException(CommonErrors.CONFLICT);
          if (mode.equals("malicious")) {
            db.lock("revocations");
            var old = db.revocations();
            var already =
                db.rows(
                    "SELECT version_id FROM resource.resource_revoked_artifact WHERE tenant_id=:tenant AND package_id=:pkg AND version_id=:version AND digest=:digest",
                    db.args("pkg", p, "version", v, "digest", row.get("digest")));
            if (!already.isEmpty()) return db.versionJson(row);
            db.update(
                "INSERT INTO resource.resource_revoked_artifact(tenant_id,package_id,version_id,digest) VALUES(:tenant,:pkg,:version,:digest) ON CONFLICT DO NOTHING",
                db.args("pkg", p, "version", v, "digest", row.get("digest")));
            var revoked =
                old == null ? JSON.createArrayNode() : old.withArray("revokedArtifacts").deepCopy();
            if (revoked.size() >= 8192) throw new AurigaException(CommonErrors.CONFLICT);
            revoked.add(
                JSON.createObjectNode()
                    .put("packageId", p.toString())
                    .put("versionId", v.toString())
                    .put("digest", row.get("digest").toString()));
            long revision = old == null ? 1 : old.path("revision").asLong() + 1;
            if (revision > MAX) throw new AurigaException(CommonErrors.CONFLICT);
            var now = clock.instant();
            var list =
                JSON.createObjectNode()
                    .put("schemaVersion", 1)
                    .put("tenantId", r.tenant().toString())
                    .put("revision", revision)
                    .put("issuedAt", ResourceSigning.time(now))
                    .put("expiresAt", ResourceSigning.time(now.plusSeconds(86400)));
            list.set("revokedKids", old == null ? JSON.createArrayNode() : old.get("revokedKids"));
            list.set("revokedArtifacts", revoked);
            db.saveRevocations(signing.get().sign(list, true));
            db.enqueueRevocations(revision);
          }
          state(p, v, "withdrawn");
          db.update(
              "UPDATE resource.resource_package SET enabled=CASE WHEN default_version_id=:version THEN false ELSE enabled END,revision=revision+1 WHERE tenant_id=:tenant AND id=:id",
              db.args("id", p, "version", v));
          recorded(r, "resource.version." + mode, v, reason);
          var next = db.version(p, v);
          var data = event(next);
          data.put("mode", mode);
          data.put("reason", reason);
          events.publish("resource.version_withdrawn", v.toString(), data, "1");
          return db.versionJson(next);
        });
  }

  public JsonNode detail(AurigaPrincipal actor, UUID p, UUID v, String permission) {
    var r = access.administrator(actor, permission);
    return resources.write(
        () -> {
          var pack = pkg(r, p, false, true);
          var row = db.version(p, v);
          var result = JSON.createObjectNode();
          result.set("resourcePackage", JSON.valueToTree(pack));
          result.set("version", db.versionJson(row));
          result.put("authorId", row.get("author_id").toString());
          result.put("revoked", row.get("envelope") != null && revoked(json(row, "envelope")));
          result.set("metadata", json(row, "manifest"));
          result.set("files", json(row, "files"));
          var history = JSON.createArrayNode();
          for (var review : db.reviews(v)) {
            var record = JSON.createObjectNode();
            record.set("review", db.reviewJson(review));
            record.put("reason", review.get("reason").toString());
            if (review.get("reviewer_id") != null)
              record.put("reviewerId", review.get("reviewer_id").toString());
            history.add(record);
          }
          result.set("reviews", history);
          var previous =
              db.rows(
                  "SELECT * FROM resource.resource_version WHERE tenant_id=:tenant AND package_id=:pkg AND id<>:id AND (:sequence=0 OR release_sequence<:sequence) AND state IN ('published','deprecated','withdrawn') ORDER BY release_sequence DESC LIMIT 1",
                  db.args(
                      "pkg",
                      p,
                      "id",
                      v,
                      "sequence",
                      row.get("release_sequence") == null ? 0 : number(row, "release_sequence")));
          if (!previous.isEmpty()) {
            result.set("previousVersion", db.versionJson(previous.getFirst()));
            result.set(
                "previousFiles",
                json(previous.getFirst(), "files").isArray()
                    ? json(previous.getFirst(), "files")
                    : JSON.createArrayNode());
          }
          return result;
        });
  }

  public JsonNode review(AurigaPrincipal actor, UUID id) {
    var r = access.administrator(actor, "resource.versions.review");
    return resources.write(() -> db.reviewJson(reviewGuard(r, id)));
  }

  public JsonNode reviewPage(AurigaPrincipal actor, VersionPage page) {
    access.administrator(actor, "resource.versions.review");
    return resources.write(
        () -> {
          var out = JSON.createObjectNode();
          var items = JSON.createArrayNode();
          for (var v : page.getItems())
            for (var review : db.reviews(v.getId()))
              if (review.get("state").equals("pending")
                  && number(review, "version_revision") == v.getRevision())
                items.add(db.reviewJson(review));
          out.set("items", items);
          if (page.getNextCursor() != null) out.put("nextCursor", page.getNextCursor());
          return out;
        });
  }

  public JsonNode catalogVersion(AurigaPrincipal actor, UUID p, UUID v) {
    var r = access.resolve(actor);
    return resources.write(
        () -> {
          pkg(r, p, false, false);
          resources
              .versionById(r, p, v)
              .orElseThrow(() -> new AurigaException(CommonErrors.NOT_FOUND));
          return db.versionJson(db.version(p, v));
        });
  }

  public JsonNode govern(AurigaPrincipal actor, UUID p, ObjectNode body, String etag) {
    String reason = safeReason(text(body, "reason", 1024));
    access.owner(actor);
    return resources.write(
        () -> {
          var r = access.owner(actor);
          var pack = pkg(r, p, false, true);
          match(etag, pack.getRevision());
          if (pack.getLevel() == ModelPackage.LevelEnum.L0)
            throw new AurigaException(CommonErrors.FORBIDDEN);
          String level = body.has("level") ? text(body, "level", 2) : pack.getLevel().getValue();
          if (!Set.of("l1", "l2", "l3").contains(level)) throw invalid();
          String visibility =
              body.has("visibility")
                  ? text(body, "visibility", 16)
                  : pack.getVisibility().getValue();
          if (!Set.of("tenant", "members", "groups").contains(visibility)) throw invalid();
          Set<UUID> groups = new LinkedHashSet<>();
          if (body.has("groupIds")) {
            if (!body.path("groupIds").isArray() || body.path("groupIds").size() > 200)
              throw invalid();
            for (var entry : body.path("groupIds")) {
              if (!entry.isString()) throw invalid();
              UUID group;
              try {
                group = UUID.fromString(entry.asString());
              } catch (IllegalArgumentException e) {
                throw invalid();
              }
              if (!groups.add(group) || !access.groupExists(r.tenant(), group)) throw invalid();
            }
          } else if (visibility.equals("groups")) groups.addAll(pack.getGroupIds());
          if (visibility.equals("groups") == groups.isEmpty()) throw invalid();
          boolean enabled = pack.getEnabled();
          if (body.has("enabled")) {
            if (!body.path("enabled").isBoolean()) throw invalid();
            enabled = body.path("enabled").asBoolean();
          }
          UUID defaultVersion = pack.getDefaultVersionId();
          if (body.has("defaultVersionId")) {
            defaultVersion = uuid(body, "defaultVersionId");
            var row = db.version(p, defaultVersion);
            requireState(row, "published");
            if (!Objects.equals(
                row.get("release_sequence"),
                db.row(
                        "SELECT release_high_water FROM resource.resource_package WHERE tenant_id=:tenant AND id=:id",
                        db.args("id", p))
                    .get("release_high_water"))) throw new AurigaException(CommonErrors.CONFLICT);
          }
          if (enabled) {
            if (defaultVersion == null) throw new AurigaException(CommonErrors.CONFLICT);
            var current = db.version(p, defaultVersion);
            requireState(current, "published");
            long high =
                number(
                    db.row(
                        "SELECT release_high_water FROM resource.resource_package WHERE tenant_id=:tenant AND id=:id",
                        db.args("id", p)),
                    "release_high_water");
            if (number(current, "release_sequence") != high)
              throw new AurigaException(CommonErrors.CONFLICT);
          }
          db.update(
              "UPDATE resource.resource_package SET level=:level,visibility=:visibility,enabled=:enabled,default_version_id=:version,revision=revision+1 WHERE tenant_id=:tenant AND id=:id",
              db.args(
                  "id",
                  p,
                  "level",
                  level,
                  "visibility",
                  visibility,
                  "enabled",
                  enabled,
                  "version",
                  defaultVersion));
          db.update(
              "DELETE FROM resource.resource_package_group WHERE tenant_id=:tenant AND package_id=:id",
              db.args("id", p));
          for (UUID group : groups)
            db.update(
                "INSERT INTO resource.resource_package_group(id,tenant_id,package_id,group_id) VALUES(:key,:tenant,:id,:group)",
                db.args("key", UuidV7.next(), "id", p, "group", group));
          recorded(r, "resource.package.govern", p, reason);
          // Return the authorized result even when the new visibility removes the caller's
          // membership.
          pack.setLevel(ModelPackage.LevelEnum.fromValue(level));
          pack.setVisibility(ModelPackage.VisibilityEnum.fromValue(visibility));
          pack.setGroupIds(groups);
          pack.setEnabled(enabled);
          pack.setDefaultVersionId(defaultVersion);
          pack.setRevision(pack.getRevision() + 1);
          return JSON.valueToTree(pack);
        });
  }

  public InputStream content(AurigaPrincipal actor, UUID p, UUID v, boolean range) {
    if (range) throw invalid();
    var r = access.resolve(actor);
    var info =
        resources.write(
            () -> {
              var pack = pkg(r, p, false, false);
              var row = db.version(p, v);
              if (!row.get("state").equals("published")
                  || !Boolean.TRUE.equals(row.get("archive_verified")))
                throw new AurigaException(CommonErrors.NOT_FOUND);
              if (!Boolean.TRUE.equals(pack.getEnabled())
                  || row.get("envelope") == null
                  || revoked(json(row, "envelope"))
                  || !clock
                      .instant()
                      .isBefore(Instant.parse(json(row, "envelope").path("notAfter").asString())))
                throw new AurigaException(CommonErrors.NOT_FOUND);
              return new ObjectStore.ObjectInfo(
                  row.get("digest").toString(), number(row, "size_bytes"));
            });
    return objects.get().open(r.tenant(), info);
  }
}
