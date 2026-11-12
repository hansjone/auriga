package auriga.resource.app;

import static auriga.resource.persistence.ResourceWorkflowStore.json;
import static auriga.resource.persistence.ResourceWorkflowStore.number;
import static auriga.resource.web.ResourceBody.*;

import auriga.common.error.*;
import auriga.common.events.EventPublisher;
import auriga.common.persistence.UuidV7;
import auriga.common.security.AurigaPrincipal;
import auriga.resource.archive.SkillArchiveValidator;
import auriga.resource.objectstore.ObjectStore;
import auriga.resource.persistence.*;
import java.io.*;
import java.sql.Timestamp;
import java.time.*;
import java.util.*;
import java.util.function.*;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** Separate catalog storage/workflow: legacy plugin IDs never resolve through this service. */
public final class CatalogWorkflow {
  public static final String SECURITY_REVIEW = "resource.versions.security_review";
  private final ResourceStore tx;
  private final ResourceWorkflowStore db;
  private final ResourceAccess access;
  private final ResourceWorkflow operations;
  private final Supplier<ObjectStore> objects;
  private final Supplier<ResourceSigning> signing;
  private final BooleanSupplier writes;
  private final ResourceCursor cursors;
  private final EventPublisher events;
  private final Clock clock;

  public CatalogWorkflow(
      ResourceStore tx,
      ResourceWorkflowStore db,
      ResourceAccess access,
      ResourceWorkflow operations,
      Supplier<ObjectStore> objects,
      Supplier<ResourceSigning> signing,
      BooleanSupplier writes,
      ResourceCursor cursors,
      EventPublisher events,
      Clock clock) {
    this.tx = tx;
    this.db = db;
    this.access = access;
    this.operations = operations;
    this.objects = objects;
    this.signing = signing;
    this.writes = writes;
    this.cursors = cursors;
    this.events = events;
    this.clock = clock;
  }

  JsonNode mutate(
      AurigaPrincipal a,
      String permission,
      boolean admin,
      String op,
      String key,
      String etag,
      ObjectNode body,
      Consumer<ResourceAccess.Reader> guard,
      Function<ResourceAccess.Reader, JsonNode> action) {
    if (!writes.getAsBoolean()) throw new AurigaException(CommonErrors.UNAVAILABLE);
    if (etag == null || key == null) throw invalid();
    return operations.operation(
        a, permission, admin, "catalog/" + op, key, body, etag, guard, action);
  }

  private static void match(String etag, long rev) {
    ResourceWorkflow.match(etag, rev);
  }

  private static AurigaException missing() {
    return new AurigaException(CommonErrors.NOT_FOUND);
  }

  private static AurigaException forbidden() {
    return new AurigaException(CommonErrors.FORBIDDEN);
  }

  private static AurigaException conflict() {
    return new AurigaException(CommonErrors.CONFLICT);
  }

  private String visible(ResourceAccess.Reader r) {
    return " AND (p.visibility='tenant' OR (p.visibility='members' AND NOT :external) OR (p.visibility='groups' AND p.group_ids && CAST(:groups AS uuid[])))";
  }

  private Map<String, Object> args(ResourceAccess.Reader r, Object... pairs) {
    var a = db.args(pairs);
    a.put("external", r.external());
    a.put("groups", "{" + String.join(",", r.groups().stream().map(UUID::toString).toList()) + "}");
    return a;
  }

  Map<String, Object> pkg(ResourceAccess.Reader r, UUID p, boolean own) {
    var row =
        db.row(
            "SELECT p.* FROM resource.catalog_package p WHERE p.tenant_id=:tenant AND p.id=:p"
                + visible(r)
                + " FOR UPDATE",
            args(r, "p", p));
    if (own && !r.account().equals(row.get("owner_id"))) throw forbidden();
    return row;
  }

  Map<String, Object> ver(ResourceAccess.Reader r, UUID p, UUID v, boolean admin) {
    pkg(r, p, false);
    var row =
        db.row(
            "SELECT * FROM resource.catalog_version WHERE tenant_id=:tenant AND package_id=:p AND id=:v FOR UPDATE",
            db.args("p", p, "v", v));
    if (!admin
        && !r.account().equals(row.get("author_id"))
        && !List.of("published", "deprecated", "withdrawn").contains(row.get("state")))
      throw missing();
    return row;
  }

  private ObjectNode packageJson(Map<String, Object> p) {
    var n = JSON.createObjectNode();
    for (String k : List.of("id", "name", "kind")) n.put(k, p.get(k).toString());
    n.put("tenantId", p.get("tenant_id").toString())
        .put("ownerId", p.get("owner_id").toString())
        .put("level", "l3")
        .put("visibility", p.get("visibility").toString())
        .put("enabled", (Boolean) p.get("enabled"))
        .put("revision", number(p, "revision"))
        .put("createdAt", instant(p.get("created_at")).toString());
    if (p.get("default_version_id") == null) n.putNull("defaultVersionId");
    else n.put("defaultVersionId", p.get("default_version_id").toString());
    var groups = n.putArray("groupIds");
    try {
      for (Object g : (Object[]) ((java.sql.Array) p.get("group_ids")).getArray())
        groups.add(g.toString());
    } catch (java.sql.SQLException e) {
      throw new IllegalStateException(e);
    }
    return n;
  }

  private static Instant instant(Object t) {
    return t instanceof Timestamp x ? x.toInstant() : ((OffsetDateTime) t).toInstant();
  }

  ObjectNode versionJson(Map<String, Object> v) {
    var n = JSON.createObjectNode();
    for (String k : List.of("id", "version", "digest", "state")) n.put(k, v.get(k).toString());
    n.put("packageId", v.get("package_id").toString())
        .put("kind", json(v, "manifest").path("kind").asString())
        .put("revision", number(v, "revision"))
        .put("createdAt", instant(v.get("created_at")).toString());
    n.set("manifest", json(v, "manifest"));
    n.set(
        "risk",
        JSON.createObjectNode()
            .put("minimumLevel", v.get("minimum_risk").toString())
            .put("level", v.get("risk").toString())
            .put("requiredReviewers", v.get("risk").equals("r1") ? 1 : 2)
            .put("requiredSecurityReviewers", v.get("risk").equals("r1") ? 0 : 1));
    n.put(
        "manifestDownloadPath", base((UUID) v.get("package_id"), (UUID) v.get("id")) + "/manifest");
    if (v.get("release_sequence") != null) n.put("releaseSequence", number(v, "release_sequence"));
    if (v.get("envelope") != null) n.set("resourceEnvelope", json(v, "envelope"));
    return n;
  }

  static String base(UUID p, UUID v) {
    return "/api/resource/v1/catalog/packages/" + p + "/versions/" + v;
  }

  public JsonNode create(AurigaPrincipal actor, ObjectNode body, String key, String etag) {
    String name = text(body, "name", 64),
        kind = text(body, "kind", 16),
        visibility = text(body, "visibility", 16);
    if (!name.matches("[a-z0-9]+(?:-[a-z0-9]+)*")
        || !kind.equals("skill")
        || !Set.of("tenant", "members", "groups").contains(visibility)) throw invalid();
    var groups = new TreeSet<UUID>();
    if (body.has("groupIds")) {
      if (!body.path("groupIds").isArray() || body.path("groupIds").size() > 200) throw invalid();
      for (var g : body.path("groupIds")) {
        if (!g.isString()) throw invalid();
        try {
          if (!groups.add(UUID.fromString(g.asString()))) throw invalid();
        } catch (IllegalArgumentException e) {
          throw invalid();
        }
      }
    }
    return mutate(
        actor,
        ResourceAccess.UPLOAD,
        false,
        "create",
        key,
        etag,
        body,
        r -> {
          if (visibility.equals("members") && r.external()) throw forbidden();
          if (visibility.equals("groups") != !groups.isEmpty()) throw invalid();
          if (!r.groups().containsAll(groups)) throw forbidden();
          for (UUID g : groups) if (!access.groupExists(r.tenant(), g)) throw invalid();
        },
        r -> {
          if (!etag.equals("\"0\"")) throw new AurigaException(CommonErrors.PRECONDITION_FAILED);
          UUID id = UuidV7.next();
          db.update(
              "INSERT INTO resource.catalog_package(id,tenant_id,owner_id,name,kind,visibility,group_ids,created_at) VALUES(:id,:tenant,:owner,:name,:kind,:visibility,CAST(:groups AS uuid[]),:now)",
              db.args(
                  "id",
                  id,
                  "owner",
                  r.account(),
                  "name",
                  name,
                  "kind",
                  kind,
                  "visibility",
                  visibility,
                  "groups",
                  "{" + String.join(",", groups.stream().map(UUID::toString).toList()) + "}",
                  "now",
                  Timestamp.from(clock.instant())));
          operations.recorded(r, "resource.package.create", id);
          return packageJson(pkg(r, id, true));
        });
  }

  public JsonNode detail(AurigaPrincipal actor, UUID p, UUID v, boolean admin) {
    var r = admin ? access.administrator(actor, "resource.versions.review") : access.member(actor);
    return tx.write(
        () -> v == null ? packageJson(pkg(r, p, false)) : versionJson(ver(r, p, v, admin)));
  }

  public JsonNode list(
      AurigaPrincipal actor, UUID p, String kind, int limit, String cursor, String phase) {
    if (limit < 1 || limit > 200 || kind != null && !Set.of("skill", "tool").contains(kind))
      throw invalid();
    var r =
        phase == null
            ? access.member(actor)
            : access.administrator(
                actor,
                phase.equals("review") ? "resource.versions.review" : "resource.versions.publish");
    return tx.write(
        () -> {
          if (p != null) pkg(r, p, false);
          String scope = "catalog/" + p + "/" + kind + "/" + phase;
          String from =
              p == null && phase == null
                  ? "resource.catalog_package p"
                  : "resource.catalog_package p JOIN resource.catalog_version v ON v.tenant_id=p.tenant_id AND v.package_id=p.id";
          String id = p == null && phase == null ? "p.id" : "v.id";
          String where =
              " WHERE p.tenant_id=:tenant"
                  + visible(r)
                  + (kind == null ? "" : " AND p.kind=:kind")
                  + (p == null ? "" : " AND p.id=:p");
          if (phase != null)
            where +=
                phase.equals("review")
                    ? " AND v.state='pending_review'"
                    : " AND v.state IN ('approved','published','deprecated','withdrawn')";
          else if (p != null)
            where +=
                " AND (v.author_id=:account OR v.state IN ('published','deprecated','withdrawn'))";
          var a = args(r, "p", p, "kind", kind, "account", r.account(), "limit", limit + 1);
          var pos = cursors.decode(cursor, r, scope, limit);
          if (pos == null) {
            var ceil =
                db.rows(
                    "SELECT "
                        + id
                        + " AS id FROM "
                        + from
                        + where
                        + " ORDER BY "
                        + id
                        + " DESC LIMIT 1",
                    a);
            pos =
                cursors.initial(ceil.isEmpty() ? new UUID(0, 0) : (UUID) ceil.getFirst().get("id"));
          }
          a.put("after", pos.after());
          a.put("ceiling", pos.ceiling());
          var rows =
              db.rows(
                  "SELECT "
                      + (p == null && phase == null ? "p.*" : "v.*")
                      + " FROM "
                      + from
                      + where
                      + " AND "
                      + id
                      + ">:after AND "
                      + id
                      + "<=:ceiling ORDER BY "
                      + id
                      + " LIMIT :limit",
                  a);
          var result = JSON.createObjectNode();
          var items = result.putArray("items");
          for (var row : rows.subList(0, Math.min(limit, rows.size())))
            items.add(
                p == null && phase == null
                    ? packageJson(row)
                    : phase != null && phase.equals("review")
                        ? reviewJson(r, (UUID) row.get("review_id"))
                        : versionJson(row));
          if (rows.size() > limit)
            result.put(
                "nextCursor",
                cursors.encode(
                    r,
                    scope,
                    limit,
                    new ResourceCursor.Position(
                        (UUID) rows.get(limit - 1).get("id"), pos.ceiling(), pos.expires())));
          return result;
        });
  }

  public JsonNode upload(
      AurigaPrincipal actor,
      UUID p,
      ObjectNode metadata,
      byte[] raw,
      List<InputStream> inputs,
      String key,
      String etag) {
    try {
      return uploadOwned(actor, p, metadata, raw, inputs, key, etag);
    } finally {
      inputs.forEach(CatalogWorkflow::close);
    }
  }

  private JsonNode uploadOwned(
      AurigaPrincipal actor,
      UUID p,
      ObjectNode metadata,
      byte[] raw,
      List<InputStream> inputs,
      String key,
      String etag) {
    var r = access.writer(actor);
    var pack = tx.write(() -> pkg(r, p, true));
    String version = text(metadata, "version", 128),
        expected = text(metadata, "expectedDigest", 71);
    var m = CatalogManifest.parse(raw, pack.get("name").toString(), version, expected);
    if (!pack.get("kind").equals(m.path("kind").asString())) throw invalid();
    if (inputs.size() != m.path("artifacts").size()) {
      inputs.forEach(CatalogWorkflow::close);
      throw invalid();
    }
    var staged = new ArrayList<ObjectStore.Staged>();
    var store = objects.get();
    try {
      String floor = "r1";
      for (int i = 0; i < inputs.size(); i++) {
        var stage = store.putStaged(r.tenant(), inputs.get(i), 5242880);
        staged.add(stage);
        var a = m.path("artifacts").get(i);
        if (!stage.object().digest().equals(a.path("digest").asString())
            || stage.object().sizeBytes() != a.path("sizeBytes").asLong()) throw invalid();
        floor =
            SkillArchiveValidator.validate(
                store.openStaged(r.tenant(), stage.id()), stage.object().sizeBytes(), m);
      }
      final String risk = floor;
      var fingerprint = metadata.deepCopy();
      fingerprint.put("rawDigest", expected);
      return mutate(
          actor,
          ResourceAccess.UPLOAD,
          false,
          "upload/" + p,
          key,
          etag,
          fingerprint,
          x -> pkg(x, p, true),
          x -> {
            var locked = pkg(x, p, true);
            match(etag, number(locked, "revision"));
            UUID id = UuidV7.next();
            for (var s : staged) {
              db.lock("object/" + s.object().digest());
              store.commitIfAbsent(x.tenant(), s.id(), s.object());
            }
            db.update(
                "INSERT INTO resource.catalog_version(id,tenant_id,package_id,author_id,version,digest,manifest_bytes,manifest,state,minimum_risk,risk,created_at) VALUES(:id,:tenant,:p,:author,:version,:digest,:bytes,CAST(:manifest AS jsonb),'draft',:risk,:risk,:now)",
                db.args(
                    "id",
                    id,
                    "p",
                    p,
                    "author",
                    x.account(),
                    "version",
                    version,
                    "digest",
                    expected,
                    "bytes",
                    raw,
                    "manifest",
                    m.toString(),
                    "risk",
                    risk,
                    "now",
                    Timestamp.from(clock.instant())));
            for (var a : m.path("artifacts"))
              db.update(
                  "INSERT INTO resource.catalog_artifact(id,tenant_id,package_id,version_id,artifact_id,digest,size_bytes,role) VALUES(:id,:tenant,:p,:v,:artifact,:digest,:size,:role)",
                  db.args(
                      "id",
                      UuidV7.next(),
                      "p",
                      p,
                      "v",
                      id,
                      "artifact",
                      a.path("id").asString(),
                      "digest",
                      a.path("digest").asString(),
                      "size",
                      a.path("sizeBytes").asLong(),
                      "role",
                      a.path("role").asString()));
            db.update(
                "UPDATE resource.catalog_package SET revision=revision+1 WHERE tenant_id=:tenant AND id=:p",
                db.args("p", p));
            operations.recorded(x, "resource.version.upload", id);
            return versionJson(ver(x, p, id, false));
          });
    } finally {
      RuntimeException cleanup = null;
      for (var stage : staged)
        try {
          store.abort(r.tenant(), stage.id());
        } catch (RuntimeException e) {
          if (cleanup == null) cleanup = e;
          else cleanup.addSuppressed(e);
        }
      if (cleanup != null) throw cleanup;
    }
  }

  private static void close(InputStream in) {
    try {
      in.close();
    } catch (IOException e) {
      /* all remaining streams are still closed */
    }
  }

  public JsonNode submit(
      AurigaPrincipal actor, UUID p, UUID v, ObjectNode body, String key, String etag) {
    text(body, "reason", 1024);
    return mutate(
        actor,
        "resource.versions.submit",
        false,
        "submit/" + p + "/" + v,
        key,
        etag,
        body,
        r -> {
          var row = ver(r, p, v, false);
          if (!r.account().equals(row.get("author_id"))) throw forbidden();
        },
        r -> {
          var row = ver(r, p, v, false);
          match(etag, number(row, "revision"));
          if (!List.of("draft", "rejected").contains(row.get("state"))) throw conflict();
          UUID review = UuidV7.next();
          long rev = number(row, "revision") + 1;
          db.update(
              "UPDATE resource.catalog_version SET state='pending_review',revision=:rev,review_id=:review WHERE tenant_id=:tenant AND id=:id",
              db.args("id", v, "rev", rev, "review", review));
          db.update(
              "INSERT INTO resource.catalog_review(id,tenant_id,package_id,version_id,version_revision,digest,status) VALUES(:id,:tenant,:p,:v,:rev,:digest,'pending')",
              db.args("id", review, "p", p, "v", v, "rev", rev, "digest", row.get("digest")));
          operations.recorded(r, "resource.version.submit", v);
          return reviewJson(r, review);
        });
  }

  private Map<String, Object> review(ResourceAccess.Reader r, UUID id) {
    var row =
        db.row(
            "SELECT * FROM resource.catalog_review WHERE tenant_id=:tenant AND id=:id FOR UPDATE",
            db.args("id", id));
    pkg(r, (UUID) row.get("package_id"), false);
    return row;
  }

  ObjectNode reviewJson(ResourceAccess.Reader r, UUID id) {
    var row = review(r, id);
    var v = ver(r, (UUID) row.get("package_id"), (UUID) row.get("version_id"), true);
    var n =
        JSON.createObjectNode()
            .put("reviewId", id.toString())
            .put("authorId", v.get("author_id").toString())
            .put("revision", number(row, "revision"))
            .put("status", row.get("status").toString());
    n.set("version", versionJson(v));
    var decisions = n.putArray("decisions");
    for (var d :
        db.rows(
            "SELECT * FROM resource.catalog_decision WHERE tenant_id=:tenant AND review_id=:id ORDER BY id",
            db.args("id", id))) {
      var node =
          JSON.createObjectNode()
              .put("reviewerId", d.get("reviewer_id").toString())
              .put("role", d.get("role").toString())
              .put("decision", d.get("decision").toString())
              .put("reason", d.get("reason").toString())
              .put("digest", d.get("digest").toString())
              .put("versionRevision", number(d, "version_revision"))
              .put("createdAt", instant(d.get("created_at")).toString());
      decisions.add(node);
    }
    return n;
  }

  public JsonNode reviewDetail(AurigaPrincipal actor, UUID id) {
    var r = access.administrator(actor, "resource.versions.review");
    return tx.write(() -> reviewJson(r, id));
  }

  private boolean qualified(ResourceAccess.Reader r, UUID reviewer, String permission) {
    try {
      access.target(r.tenant(), reviewer, permission);
      return true;
    } catch (AurigaException e) {
      if (e.errorCode().equals(CommonErrors.FORBIDDEN)) return false;
      throw e;
    }
  }

  private boolean quorum(ResourceAccess.Reader r, Map<String, Object> v, UUID review) {
    var latest = new LinkedHashMap<UUID, Map<String, Object>>();
    for (var d :
        db.rows(
            "SELECT * FROM resource.catalog_decision WHERE tenant_id=:tenant AND review_id=:id AND version_revision=:rev AND digest=:digest ORDER BY id",
            db.args("id", review, "rev", number(v, "revision"), "digest", v.get("digest"))))
      latest.put((UUID) d.get("reviewer_id"), d);
    int count = 0;
    boolean security = false;
    for (var e : latest.entrySet()) {
      if (e.getKey().equals(v.get("author_id"))
          || !e.getValue().get("decision").equals("approve")
          || !qualified(r, e.getKey(), "resource.versions.review")) continue;
      boolean sec = e.getValue().get("role").equals("security");
      if (sec && !qualified(r, e.getKey(), SECURITY_REVIEW)) continue;
      count++;
      security |= sec;
    }
    return v.get("risk").equals("r1") ? count >= 1 : count >= 2 && security;
  }

  public JsonNode decide(AurigaPrincipal actor, UUID id, ObjectNode body, String key, String etag) {
    String role = text(body, "role", 16),
        decision = text(body, "decision", 16),
        reason = text(body, "reason", 1024);
    if (!Set.of("general", "security").contains(role)
        || !Set.of("approve", "reject").contains(decision)) throw invalid();
    return mutate(
        actor,
        "resource.versions.review",
        true,
        "decide/" + id,
        key,
        etag,
        body,
        r -> {
          if (role.equals("security")) access.target(r.tenant(), r.account(), SECURITY_REVIEW);
          review(r, id);
        },
        r -> {
          var review = review(r, id);
          match(etag, number(review, "revision"));
          var v = ver(r, (UUID) review.get("package_id"), (UUID) review.get("version_id"), true);
          if (r.account().equals(v.get("author_id"))) throw forbidden();
          if (!id.equals(v.get("review_id"))
              || !Set.of("pending_review", "approved").contains(v.get("state"))) throw conflict();
          if (counter(body, "versionRevision") != number(v, "revision")
              || !text(body, "digest", 71).equals(v.get("digest")))
            throw new AurigaException(CommonErrors.PRECONDITION_FAILED);
          if (body.has("riskLevel")) {
            String risk = text(body, "riskLevel", 2);
            if (!Set.of("r1", "r2", "r3").contains(risk)
                || risk.compareTo(v.get("risk").toString()) < 0) throw invalid();
            if (!risk.equals(v.get("risk"))) {
              db.update(
                  "UPDATE resource.catalog_version SET risk=:risk,revision=revision+1,state='pending_review' WHERE tenant_id=:tenant AND id=:id",
                  db.args("id", v.get("id"), "risk", risk));
              db.update(
                  "UPDATE resource.catalog_review SET version_revision=version_revision+1,revision=revision+1,status='pending' WHERE tenant_id=:tenant AND id=:id",
                  db.args("id", id));
              operations.recorded(r, "resource.review.decide", id);
              return reviewJson(r, id);
            }
          }
          if (number(review, "revision") >= 200) throw conflict();
          db.update(
              "INSERT INTO resource.catalog_decision(id,tenant_id,review_id,reviewer_id,role,decision,reason,version_revision,digest,created_at) VALUES(:id,:tenant,:review,:reviewer,:role,:decision,:reason,:rev,:digest,:now)",
              db.args(
                  "id",
                  UuidV7.next(),
                  "review",
                  id,
                  "reviewer",
                  r.account(),
                  "role",
                  role,
                  "decision",
                  decision,
                  "reason",
                  auriga.common.observability.SensitiveDataMasker.mask(reason),
                  "rev",
                  number(v, "revision"),
                  "digest",
                  v.get("digest"),
                  "now",
                  Timestamp.from(clock.instant())));
          String status =
              decision.equals("reject") ? "rejected" : quorum(r, v, id) ? "approved" : "pending";
          db.update(
              "UPDATE resource.catalog_review SET status=:status,revision=revision+1 WHERE tenant_id=:tenant AND id=:id",
              db.args("id", id, "status", status));
          db.update(
              "UPDATE resource.catalog_version SET state=:state WHERE tenant_id=:tenant AND id=:id",
              db.args(
                  "id",
                  v.get("id"),
                  "state",
                  status.equals("pending") ? "pending_review" : status));
          operations.recorded(r, "resource.review.decide", id);
          return reviewJson(r, id);
        });
  }

  ObjectNode envelope(ResourceAccess.Reader r, Map<String, Object> v, long sequence) {
    var m = json(v, "manifest");
    var now = clock.instant();
    var n =
        JSON.createObjectNode()
            .put("schemaVersion", 2)
            .put("tenantId", r.tenant().toString())
            .put("packageId", v.get("package_id").toString())
            .put("versionId", v.get("id").toString())
            .put("releaseSequence", sequence)
            .put("notAfter", ResourceSigning.time(now.plusSeconds(86400)));
    var statement =
        JSON.createObjectNode()
            .put("purpose", "resource")
            .put("kind", m.path("kind").asString())
            .put("name", m.path("name").asString())
            .put("version", v.get("version").toString())
            .put("manifestDigest", v.get("digest").toString())
            .put("issuedAt", ResourceSigning.time(now));
    var digests = statement.putArray("artifactDigests");
    CatalogManifest.artifactDigests(m).forEach(digests::add);
    n.set("statement", statement);
    return signing.get().sign(n, false);
  }

  public JsonNode publish(
      AurigaPrincipal actor, UUID p, UUID id, ObjectNode body, String key, String etag) {
    text(body, "reason", 1024);
    return mutate(
        actor,
        "resource.versions.publish",
        true,
        "publish/" + p + "/" + id,
        key,
        etag,
        body,
        r -> ver(r, p, id, true),
        r -> {
          var pack = pkg(r, p, false);
          var v = ver(r, p, id, true);
          match(etag, number(v, "revision"));
          if (r.account().equals(v.get("author_id"))) throw forbidden();
          if (!v.get("state").equals("approved") || !quorum(r, v, (UUID) v.get("review_id")))
            throw conflict();
          if (revoked(v)) throw forbidden();
          long seq = number(pack, "release_high_water") + 1;
          if (seq > ResourceWorkflow.MAX) throw conflict();
          var env = envelope(r, v, seq);
          db.update(
              "UPDATE resource.catalog_version SET state='published',revision=revision+1,release_sequence=:seq,envelope=CAST(:env AS jsonb) WHERE tenant_id=:tenant AND id=:id",
              db.args("id", id, "seq", seq, "env", env.toString()));
          db.update(
              "UPDATE resource.catalog_package SET enabled=true,default_version_id=:v,release_high_water=:seq,revision=revision+1 WHERE tenant_id=:tenant AND id=:p",
              db.args("p", p, "v", id, "seq", seq));
          var updated = ver(r, p, id, true);
          events.publish("resource.catalog_version_published", p.toString(), event(updated));
          operations.recorded(r, "resource.version.publish", id);
          return versionJson(updated);
        });
  }

  ObjectNode event(Map<String, Object> v) {
    var n =
        JSON.createObjectNode()
            .put("packageId", v.get("package_id").toString())
            .put("versionId", v.get("id").toString())
            .put("version", v.get("version").toString())
            .put("kind", json(v, "manifest").path("kind").asString())
            .put("signatureVersion", 2)
            .put("manifestDigest", v.get("digest").toString())
            .put("revision", number(v, "revision"));
    var a = n.putArray("artifactDigests");
    CatalogManifest.artifactDigests(json(v, "manifest")).forEach(a::add);
    return n;
  }

  boolean revoked(Map<String, Object> v) {
    var digests = new HashSet<>(CatalogManifest.artifactDigests(json(v, "manifest")));
    digests.add(v.get("digest").toString());
    for (var row :
        db.rows(
            "SELECT * FROM resource.resource_revoked_artifact WHERE tenant_id=:tenant", db.args()))
      if (v.get("package_id").equals(row.get("package_id"))
              && v.get("id").equals(row.get("version_id"))
          || digests.contains(row.get("digest"))) return true;
    var list = db.revocations();
    if (list != null && v.get("envelope") != null)
      for (var kid : list.path("revokedKids"))
        if (kid.equals(json(v, "envelope").path("signature").path("kid"))) return true;
    return false;
  }

  public JsonNode withdraw(
      AurigaPrincipal actor, UUID p, UUID id, ObjectNode body, String key, String etag) {
    String mode = text(body, "mode", 16), reason = text(body, "reason", 1024);
    if (!Set.of("withdrawn", "malicious").contains(mode)) throw invalid();
    return mutate(
        actor,
        "resource.versions.publish",
        true,
        "withdraw/" + p + "/" + id,
        key,
        etag,
        body,
        r -> {
          db.lock("catalog-withdraw");
          ver(r, p, id, true);
        },
        r -> {
          var v = ver(r, p, id, true);
          match(etag, number(v, "revision"));
          if (!Set.of("published", "deprecated", "withdrawn").contains(v.get("state")))
            throw conflict();
          var affected = new ArrayList<Map<String, Object>>();
          affected.add(v);
          if (mode.equals("malicious")) {
            db.lock("revocations");
            var digests = new HashSet<>(CatalogManifest.artifactDigests(json(v, "manifest")));
            digests.add(v.get("digest").toString());
            for (var candidate :
                db.rows(
                    "SELECT * FROM resource.catalog_version WHERE tenant_id=:tenant AND id<>:id ORDER BY package_id,id",
                    db.args("id", id)))
              if (CatalogManifest.artifactDigests(json(candidate, "manifest")).stream()
                  .anyMatch(digests::contains)) affected.add(candidate);
            for (var a : affected) {
              var all = new HashSet<>(CatalogManifest.artifactDigests(json(a, "manifest")));
              all.add(a.get("digest").toString());
              for (String digest : all)
                db.update(
                    "INSERT INTO resource.resource_revoked_artifact(tenant_id,package_id,version_id,digest) VALUES(:tenant,:p,:v,:digest) ON CONFLICT DO NOTHING",
                    db.args("p", a.get("package_id"), "v", a.get("id"), "digest", digest));
            }
            resignRevocations();
          }
          for (var a : affected) {
            db.update(
                "UPDATE resource.catalog_version SET state='withdrawn',revision=revision+1 WHERE tenant_id=:tenant AND id=:id",
                db.args("id", a.get("id")));
            db.update(
                "UPDATE resource.catalog_package SET default_version_id=NULL,enabled=false,revision=revision+1 WHERE tenant_id=:tenant AND id=:p AND default_version_id=:v",
                db.args("p", a.get("package_id"), "v", a.get("id")));
            a.put("revision", number(a, "revision") + 1);
            var e = event(a);
            e.put("mode", mode)
                .put("reason", auriga.common.observability.SensitiveDataMasker.mask(reason));
            events.publish("resource.catalog_version_withdrawn", a.get("package_id").toString(), e);
          }
          operations.recorded(r, "resource.version.withdraw", id);
          return versionJson(ver(r, p, id, true));
        });
  }

  private void resignRevocations() {
    var old = db.revocations();
    long revision = old == null ? 1 : old.path("revision").asLong() + 1;
    if (revision > ResourceWorkflow.MAX) throw conflict();
    var now = clock.instant();
    var list =
        JSON.createObjectNode()
            .put("schemaVersion", 1)
            .put("tenantId", auriga.common.context.TenantContext.require().toString())
            .put("revision", revision)
            .put("issuedAt", ResourceSigning.time(now))
            .put("expiresAt", ResourceSigning.time(now.plusSeconds(86400)));
    list.set("revokedKids", old == null ? JSON.createArrayNode() : old.path("revokedKids"));
    var all = list.putArray("revokedArtifacts");
    for (var row :
        db.rows(
            "SELECT * FROM resource.resource_revoked_artifact WHERE tenant_id=:tenant ORDER BY package_id,version_id,digest",
            db.args()))
      all.add(
          JSON.createObjectNode()
              .put("packageId", row.get("package_id").toString())
              .put("versionId", row.get("version_id").toString())
              .put("digest", row.get("digest").toString()));
    if (all.size() > 8192) throw conflict();
    db.saveRevocations(signing.get().sign(list, true));
    db.enqueueRevocations(revision);
    for (var row :
        db.rows(
            "SELECT DISTINCT instance_id FROM resource.catalog_distribution WHERE tenant_id=:tenant",
            db.args()))
      db.update(
          "INSERT INTO resource.catalog_revocation_delivery(id,tenant_id,instance_id,revision) VALUES(:id,:tenant,:instance,:rev) ON CONFLICT(tenant_id,instance_id) DO UPDATE SET revision=EXCLUDED.revision",
          db.args("id", UuidV7.next(), "instance", row.get("instance_id"), "rev", revision));
  }

  public InputStream download(
      AurigaPrincipal actor, UUID p, UUID id, String artifact, boolean range) {
    if (range) throw invalid();
    var r = access.member(actor);
    return tx.write(
        () -> {
          var pack = pkg(r, p, false);
          boolean reviewer =
              actor.isAdminContext() && actor.hasPermission("resource.versions.review");
          if (reviewer) access.administrator(actor, "resource.versions.review");
          var v = ver(r, p, id, reviewer);
          if (revoked(v)) throw missing();
          boolean author = r.account().equals(v.get("author_id"));
          if (!author
              && !reviewer
              && (!v.get("state").equals("published")
                  || !Boolean.TRUE.equals(pack.get("enabled"))
                  || revoked(v))) throw missing();
          if (artifact == null) return new ByteArrayInputStream((byte[]) v.get("manifest_bytes"));
          var a =
              db.row(
                  "SELECT * FROM resource.catalog_artifact WHERE tenant_id=:tenant AND package_id=:p AND version_id=:v AND artifact_id=:a",
                  db.args("p", p, "v", id, "a", artifact));
          return objects
              .get()
              .open(
                  r.tenant(),
                  new ObjectStore.ObjectInfo(a.get("digest").toString(), number(a, "size_bytes")));
        });
  }

  InputStream nodeArtifact(
      ResourceAccess.Reader reader, Map<String, Object> version, String artifact) {
    var a =
        db.row(
            "SELECT * FROM resource.catalog_artifact WHERE tenant_id=:tenant AND package_id=:p AND version_id=:v AND artifact_id=:a",
            db.args("p", version.get("package_id"), "v", version.get("id"), "a", artifact));
    return objects
        .get()
        .open(
            reader.tenant(),
            new ObjectStore.ObjectInfo(a.get("digest").toString(), number(a, "size_bytes")));
  }
}
