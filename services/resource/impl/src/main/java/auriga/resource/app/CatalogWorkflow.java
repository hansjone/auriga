package auriga.resource.app;

import static auriga.resource.persistence.ResourceWorkflowStore.json;
import static auriga.resource.persistence.ResourceWorkflowStore.number;
import static auriga.resource.web.ResourceBody.*;

import auriga.common.error.*;
import auriga.common.events.EventPublisher;
import auriga.common.persistence.UuidV7;
import auriga.common.security.AurigaPrincipal;
import auriga.common.security.PrincipalType;
import auriga.resource.archive.PluginArchiveValidator;
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
  private final ResourceStore tx;
  private final ResourceWorkflowStore db;
  private final ResourceAccess access;
  private final ResourceOperations operations;
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
      ResourceOperations operations,
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
    return mutate(a, permission, admin, op, key, etag, body, guard, action, true);
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
      Function<ResourceAccess.Reader, JsonNode> action,
      boolean replayResponse) {
    if (!writes.getAsBoolean()) throw new AurigaException(CommonErrors.UNAVAILABLE);
    if (etag == null || key == null) throw invalid();
    return operations.operation(
        a, permission, admin, "catalog/" + op, key, body, etag, guard, action, replayResponse);
  }

  public void requireSubmissionAccess(AurigaPrincipal actor, String permission, boolean stepUp) {
    access.catalogWriter(actor, permission, clock.instant(), stepUp);
  }

  private JsonNode mutateSubmission(
      AurigaPrincipal actor,
      String permission,
      boolean stepUp,
      String op,
      String key,
      String etag,
      ObjectNode body,
      Consumer<ResourceAccess.Reader> guard,
      Function<ResourceAccess.Reader, JsonNode> action) {
    if (!writes.getAsBoolean()) throw new AurigaException(CommonErrors.UNAVAILABLE);
    if (etag == null || key == null) throw invalid();
    return operations.operation(
        actor,
        "catalog/" + op,
        key,
        body,
        etag,
        guard,
        action,
        true,
        () -> access.catalogWriter(actor, permission, clock.instant(), stepUp));
  }

  private static void match(String etag, long rev) {
    ResourceOperations.match(etag, rev);
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

  String visible(ResourceAccess.Reader r) {
    return " AND (p.visibility='tenant' OR (p.visibility='members' AND NOT :external) OR (p.visibility='groups' AND p.group_ids && CAST(:groups AS uuid[])))";
  }

  Map<String, Object> args(ResourceAccess.Reader r, Object... pairs) {
    var a = db.args(pairs);
    a.put("external", r.external());
    a.put("groups", "{" + String.join(",", r.groups().stream().map(UUID::toString).toList()) + "}");
    return a;
  }

  Map<String, Object> pkg(ResourceAccess.Reader r, UUID p, boolean own) {
    return pkg(r, p, own, true);
  }

  Map<String, Object> pkg(ResourceAccess.Reader r, UUID p, boolean own, boolean lock) {
    var row =
        db.row(
            "SELECT p.* FROM resource.catalog_package p WHERE p.tenant_id=:tenant AND p.id=:p"
                + visible(r)
                + (lock ? " FOR UPDATE" : ""),
            args(r, "p", p));
    if (own && !r.account().equals(row.get("owner_id"))) throw forbidden();
    return row;
  }

  Map<String, Object> ver(ResourceAccess.Reader r, UUID p, UUID v, boolean admin) {
    return ver(r, p, v, admin, true);
  }

  Map<String, Object> ver(ResourceAccess.Reader r, UUID p, UUID v, boolean admin, boolean lock) {
    pkg(r, p, false, lock);
    var row =
        db.row(
            "SELECT * FROM resource.catalog_version WHERE tenant_id=:tenant AND package_id=:p AND id=:v"
                + (lock ? " FOR UPDATE" : ""),
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

  private Map<Object, Map<String, Object>> marketReleases(
      ResourceAccess.Reader reader, List<Map<String, Object>> visiblePackages) {
    var versions =
        visiblePackages.stream()
            .filter(
                pack ->
                    "plugin".equals(pack.get("kind")) && Boolean.TRUE.equals(pack.get("enabled")))
            .map(pack -> pack.get("default_version_id"))
            .filter(Objects::nonNull)
            .toList();
    var byPackage = new HashMap<Object, Map<String, Object>>();
    if (versions.isEmpty()) return byPackage;
    for (var release :
        db.rows(
            """
        SELECT v.*,
         (SELECT COUNT(DISTINCT client_id) FROM resource.catalog_installation
           WHERE tenant_id=:tenant AND package_id=v.package_id
             AND COALESCE(market_installed, document->'report'->>'state' IN ('applied','disabled')))
         + (SELECT COUNT(*) FROM resource.catalog_instance_installation
           WHERE tenant_id=:tenant AND package_id=v.package_id AND market_installed) AS installs,
         (SELECT COUNT(*) FROM resource.catalog_plugin_star
           WHERE tenant_id=:tenant AND package_id=v.package_id) AS stars,
         EXISTS (SELECT 1 FROM resource.catalog_plugin_star
           WHERE tenant_id=:tenant AND package_id=v.package_id AND account_id=:account) AS starred
        FROM resource.catalog_version v
        WHERE v.tenant_id=:tenant AND v.id IN (:versions) AND v.state='published'
        """,
            db.args("versions", versions, "account", reader.account())))
      byPackage.put(release.get("package_id"), release);
    return byPackage;
  }

  private Map<Object, Map<String, Object>> latestVersions(
      ResourceAccess.Reader reader, List<Map<String, Object>> visiblePackages) {
    var ids = visiblePackages.stream().map(pack -> pack.get("id")).toList();
    var byPackage = new HashMap<Object, Map<String, Object>>();
    if (ids.isEmpty()) return byPackage;
    for (var row :
        db.rows(
            """
        SELECT DISTINCT ON (v.package_id) v.*
        FROM resource.catalog_version v
        WHERE v.tenant_id=:tenant AND v.package_id IN (:packages)
          AND (v.author_id=:account OR v.state IN ('published','deprecated','withdrawn'))
        ORDER BY v.package_id, v.id DESC
        """,
            db.args("packages", ids, "account", reader.account())))
      byPackage.put(row.get("package_id"), row);
    return byPackage;
  }

  private String resolveDisplayName(
      ResourceAccess.Reader reader, Object storedName, Object accountId) {
    if (storedName instanceof String name && !name.isBlank()) return name;
    if (reader != null && accountId instanceof UUID account)
      return access.displayName(reader.tenant(), account).orElse(null);
    return null;
  }

  private ObjectNode marketPackage(
      ResourceAccess.Reader reader,
      Map<String, Object> pack,
      Map<String, Object> release,
      Map<String, Object> latest) {
    var result = packageJson(pack);
    if (latest != null) {
      var manifest = json(latest, "manifest");
      var summary =
          result
              .putObject("latestVersion")
              .put("versionId", latest.get("id").toString())
              .put("version", latest.get("version").toString())
              .put("state", latest.get("state").toString())
              .put("description", manifest.path("description").asString())
              .put("createdAt", instant(latest.get("created_at")).toString());
      String authorName =
          resolveDisplayName(reader, latest.get("market_author_name"), latest.get("author_id"));
      if (authorName != null) summary.put("authorName", authorName);
      if (latest.get("market_author_department") instanceof String department
          && !department.isBlank()) summary.put("authorDepartment", department);
    }
    if (release == null) return result;
    var manifest = json(release, "manifest");
    var market =
        result
            .putObject("market")
            .put("authorId", release.get("author_id").toString())
            .put("version", release.get("version").toString())
            .put("dshVersionRange", manifest.path("plugin").path("dshVersionRange").asString())
            .put("description", manifest.path("description").asString())
            .put("installationCount", number(release, "installs"))
            .put("starCount", number(release, "stars"))
            .put("starred", Boolean.TRUE.equals(release.get("starred")));
    var tags = market.putArray("tags");
    manifest.path("plugin").path("tags").forEach(tags::add);
    if (release.get("market_author_name") instanceof String name && !name.isBlank())
      market.put("authorName", name);
    if (release.get("market_author_department") instanceof String department
        && !department.isBlank()) market.put("authorDepartment", department);
    if (release.get("market_published_at") != null)
      market.put("publishedAt", instant(release.get("market_published_at")).toString());
    else {
      var issuedAt = json(release, "envelope").path("statement").path("issuedAt");
      if (issuedAt.isString()) market.put("publishedAt", issuedAt.asString());
    }
    return result;
  }

  public JsonNode star(AurigaPrincipal actor, UUID id, ObjectNode body, String key) {
    if (!writes.getAsBoolean()) throw new AurigaException(CommonErrors.UNAVAILABLE);
    if (!body.path("starred").isBoolean()) throw invalid();
    boolean starred = body.path("starred").asBoolean();
    return operations.operation(
        actor,
        ResourceAccess.READ,
        false,
        "catalog/star/" + id,
        key,
        body,
        null,
        reader -> {
          var pack = pkg(reader, id, false);
          if (!marketReleases(reader, List.of(pack)).containsKey(id)) throw missing();
        },
        reader -> {
          var args = db.args("pkg", id, "account", reader.account());
          if (starred)
            db.update(
                """
            INSERT INTO resource.catalog_plugin_star(tenant_id,package_id,account_id)
            VALUES(:tenant,:pkg,:account) ON CONFLICT DO NOTHING
            """,
                args);
          else
            db.update(
                """
            DELETE FROM resource.catalog_plugin_star
            WHERE tenant_id=:tenant AND package_id=:pkg AND account_id=:account
            """,
                args);
          var count =
              db.row(
                  """
          SELECT COUNT(*) AS total FROM resource.catalog_plugin_star
          WHERE tenant_id=:tenant AND package_id=:pkg
          """,
                  args);
          return JSON.createObjectNode()
              .put("starred", starred)
              .put("starCount", number(count, "total"));
        });
  }

  private static Instant instant(Object t) {
    return t instanceof Timestamp x ? x.toInstant() : ((OffsetDateTime) t).toInstant();
  }

  ObjectNode versionJson(Map<String, Object> v) {
    return versionJson(null, v);
  }

  ObjectNode versionJson(ResourceAccess.Reader r, Map<String, Object> v) {
    var n = JSON.createObjectNode();
    for (String k : List.of("id", "version", "digest", "state")) n.put(k, v.get(k).toString());
    n.put("packageId", v.get("package_id").toString())
        .put("kind", json(v, "manifest").path("kind").asString())
        .put("revision", number(v, "revision"))
        .put("createdAt", instant(v.get("created_at")).toString())
        .put("authorId", v.get("author_id").toString());
    String authorName = resolveDisplayName(r, v.get("market_author_name"), v.get("author_id"));
    if (authorName != null) n.put("authorName", authorName);
    if (v.get("market_author_department") instanceof String department && !department.isBlank())
      n.put("authorDepartment", department);
    if (v.get("change_notes") instanceof String changeNotes && !changeNotes.isBlank())
      n.put("changeNotes", changeNotes);
    n.set("manifest", json(v, "manifest"));
    if (v.get("review_id") != null) n.put("reviewId", v.get("review_id").toString());
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

  void assignmentRecorded(ResourceAccess.Reader reader, UUID id) {
    operations.recorded(reader, "resource.distribution.request", id);
  }

  public JsonNode create(AurigaPrincipal actor, ObjectNode body, String key, String etag) {
    String name = text(body, "name", 64),
        kind = text(body, "kind", 16),
        visibility = text(body, "visibility", 16);
    if (!name.matches("[a-z0-9]+(?:-[a-z0-9]+)*")
        || !Set.of("plugin", "skill").contains(kind)
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
    return mutateSubmission(
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

  private Map<String, Object> ownedSubmissionPackage(ResourceAccess.Reader reader, UUID id) {
    var pack = pkg(reader, id, true);
    if (!Set.of("skill", "plugin").contains(pack.get("kind"))) throw forbidden();
    return pack;
  }

  public JsonNode packageDetail(AurigaPrincipal actor, UUID id) {
    if (actor.type() == PrincipalType.DEVICE) {
      ResourceAccess.Reader reader;
      try {
        reader = access.member(actor);
      } catch (AurigaException failure) {
        if (!failure.errorCode().equals(CommonErrors.FORBIDDEN)) throw failure;
        var contributor = access.desktopContributor(actor);
        return tx.write(() -> packageJson(ownedSubmissionPackage(contributor, id)));
      }
      return tx.write(() -> visibleMarketPackage(reader, id));
    }
    return detail(actor, id, null, false);
  }

  public JsonNode detail(AurigaPrincipal actor, UUID p, UUID v, boolean admin) {
    var r = admin ? access.administrator(actor, "resource.versions.review") : access.member(actor);
    return tx.write(
        () -> {
          if (v != null) return versionJson(r, ver(r, p, v, admin));
          return visibleMarketPackage(r, p);
        });
  }

  private ObjectNode visibleMarketPackage(ResourceAccess.Reader reader, UUID id) {
    var pack = pkg(reader, id, false);
    var single = List.of(pack);
    return marketPackage(
        reader,
        pack,
        marketReleases(reader, single).get(id),
        latestVersions(reader, single).get(id));
  }

  public JsonNode list(
      AurigaPrincipal actor, UUID p, String kind, int limit, String cursor, String phase) {
    if (limit < 1
        || limit > 200
        || kind != null && !Set.of("plugin", "skill", "tool").contains(kind)) throw invalid();
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
          var page = rows.subList(0, Math.min(limit, rows.size()));
          boolean packagePage = p == null && phase == null;
          Map<Object, Map<String, Object>> releases =
              packagePage ? marketReleases(r, page) : Map.of();
          Map<Object, Map<String, Object>> latest =
              packagePage ? latestVersions(r, page) : Map.of();
          for (var row : page)
            items.add(
                packagePage
                    ? marketPackage(r, row, releases.get(row.get("id")), latest.get(row.get("id")))
                    : phase != null && phase.equals("review")
                        ? reviewJson(r, (UUID) row.get("review_id"))
                        : versionJson(r, row));
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
    var r = access.catalogWriter(actor, ResourceAccess.UPLOAD, clock.instant(), true);
    boolean desktop = actor.type() == PrincipalType.DEVICE;
    Function<ResourceAccess.Reader, Map<String, Object>> ownerPackage =
        reader -> desktop ? ownedSubmissionPackage(reader, p) : pkg(reader, p, true);
    var pack = tx.write(() -> ownerPackage.apply(r));
    String version = text(metadata, "version", 128),
        expected = text(metadata, "expectedDigest", 71),
        changeNotes =
            metadata.has("changeNotes")
                ? auriga.common.observability.SensitiveDataMasker.mask(
                    text(metadata, "changeNotes", 4096))
                : null;
    var m = CatalogManifest.parse(raw, pack.get("name").toString(), version, expected);
    if (!pack.get("kind").equals(m.path("kind").asString())) throw invalid();
    if (inputs.size() != m.path("artifacts").size()) {
      inputs.forEach(CatalogWorkflow::close);
      throw invalid();
    }
    var staged = new ArrayList<ObjectStore.Staged>();
    var store = objects.get();
    try {
      boolean plugin = m.path("kind").asString().equals("plugin");
      String floor = plugin ? "r2" : "r1";
      for (int i = 0; i < inputs.size(); i++) {
        var stage = store.putStaged(r.tenant(), inputs.get(i), plugin ? 67108864 : 5242880);
        staged.add(stage);
        var a = m.path("artifacts").get(i);
        if (!stage.object().digest().equals(a.path("digest").asString())
            || stage.object().sizeBytes() != a.path("sizeBytes").asLong()) throw invalid();
        floor =
            plugin
                ? PluginArchiveValidator.validate(store, r.tenant(), stage, m)
                : SkillArchiveValidator.validate(
                    store.openStaged(r.tenant(), stage.id()), stage.object().sizeBytes(), m);
      }
      final String risk = floor;
      var fingerprint = metadata.deepCopy();
      fingerprint.put("rawDigest", expected);
      return mutateSubmission(
          actor,
          ResourceAccess.UPLOAD,
          true,
          "upload/" + p,
          key,
          etag,
          fingerprint,
          ownerPackage::apply,
          x -> {
            var locked = ownerPackage.apply(x);
            match(etag, number(locked, "revision"));
            UUID id = UuidV7.next();
            for (var s : staged) {
              db.lock("object/" + s.object().digest());
              store.commitIfAbsent(x.tenant(), s.id(), s.object());
            }
            var author = access.authorSnapshot(actor, x);
            db.update(
                "INSERT INTO resource.catalog_version(id,tenant_id,package_id,author_id,version,digest,manifest_bytes,manifest,state,minimum_risk,risk,change_notes,market_author_name,market_author_department,created_at) VALUES(:id,:tenant,:p,:author,:version,:digest,:bytes,CAST(:manifest AS jsonb),'draft',:risk,:risk,:changeNotes,:authorName,:authorDepartment,:now)",
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
                    "changeNotes",
                    changeNotes,
                    "authorName",
                    author.name(),
                    "authorDepartment",
                    author.department(),
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
            return versionJson(x, ver(x, p, id, false));
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
    String reason = text(body, "reason", 1024);
    return mutateSubmission(
        actor,
        "resource.versions.submit",
        true,
        "submit/" + p + "/" + v,
        key,
        etag,
        body,
        r -> {
          if (actor.type() == PrincipalType.DEVICE) ownedSubmissionPackage(r, p);
          var row = ver(r, p, v, false);
          if (!r.account().equals(row.get("author_id"))) throw forbidden();
        },
        r -> {
          var row = ver(r, p, v, false);
          match(etag, number(row, "revision"));
          if (!List.of("draft", "rejected").contains(row.get("state"))) throw conflict();
          UUID review = UuidV7.next();
          long rev = number(row, "revision") + 1;
          var author = access.authorSnapshot(actor, r);
          db.update(
              """
              UPDATE resource.catalog_version
              SET market_author_name=:name,market_author_department=:department
              WHERE tenant_id=:tenant AND id=:id
              """,
              db.args("id", v, "name", author.name(), "department", author.department()));
          db.update(
              "UPDATE resource.catalog_version SET state='pending_review',revision=:rev,review_id=:review WHERE tenant_id=:tenant AND id=:id",
              db.args("id", v, "rev", rev, "review", review));
          db.update(
              "INSERT INTO resource.catalog_review(id,tenant_id,package_id,version_id,version_revision,digest,status,submit_reason,created_at) VALUES(:id,:tenant,:p,:v,:rev,:digest,'pending',:submitReason,:now)",
              db.args(
                  "id",
                  review,
                  "p",
                  p,
                  "v",
                  v,
                  "rev",
                  rev,
                  "digest",
                  row.get("digest"),
                  "submitReason",
                  auriga.common.observability.SensitiveDataMasker.mask(reason),
                  "now",
                  Timestamp.from(clock.instant())));
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
    String authorName = resolveDisplayName(r, v.get("market_author_name"), v.get("author_id"));
    if (authorName != null) n.put("authorName", authorName);
    if (v.get("market_author_department") instanceof String department && !department.isBlank())
      n.put("authorDepartment", department);
    if (row.get("submit_reason") instanceof String submitReason && !submitReason.isBlank())
      n.put("submitReason", submitReason);
    Object submittedAt =
        row.get("created_at") != null ? row.get("created_at") : v.get("created_at");
    if (submittedAt != null) n.put("submittedAt", instant(submittedAt).toString());
    var previous =
        db.rows(
            """
            SELECT id, version FROM resource.catalog_version
            WHERE tenant_id=:tenant AND package_id=:p AND id<>:v AND state='published'
            ORDER BY release_sequence DESC NULLS LAST, id DESC
            LIMIT 1
            """,
            db.args("p", row.get("package_id"), "v", row.get("version_id")));
    if (!previous.isEmpty()) {
      n.put("previousPublishedVersionId", previous.getFirst().get("id").toString());
      n.put("previousPublishedVersion", previous.getFirst().get("version").toString());
    }
    n.set("version", versionJson(r, v));
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
      String reviewerName = resolveDisplayName(r, d.get("reviewer_name"), d.get("reviewer_id"));
      if (reviewerName != null) node.put("reviewerName", reviewerName);
      if (d.get("reviewer_department") instanceof String reviewerDepartment
          && !reviewerDepartment.isBlank()) node.put("reviewerDepartment", reviewerDepartment);
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
      return false;
    }
  }

  private boolean quorum(ResourceAccess.Reader r, Map<String, Object> v, UUID review) {
    var input =
        JSON.createObjectNode()
            .put("tenantId", r.tenant().toString())
            .put("authorId", v.get("author_id").toString())
            .put("versionRevision", number(v, "revision"))
            .put("digest", v.get("digest").toString());
    var reviews = input.putArray("reviews");
    var principals = input.putArray("currentPrincipals");
    var seen = new HashSet<UUID>();
    for (var d :
        db.rows(
            "SELECT * FROM resource.catalog_decision WHERE tenant_id=:tenant AND review_id=:id ORDER BY id",
            db.args("id", review))) {
      UUID reviewer = (UUID) d.get("reviewer_id");
      reviews.add(
          JSON.createObjectNode()
              .put("tenantId", r.tenant().toString())
              .put("reviewerId", reviewer.toString())
              .put("role", d.get("role").toString())
              .put("decision", d.get("decision").toString())
              .put("versionRevision", number(d, "version_revision"))
              .put("digest", d.get("digest").toString()));
      if (seen.add(reviewer)) {
        var p =
            JSON.createObjectNode()
                .put("tenantId", r.tenant().toString())
                .put("reviewerId", reviewer.toString());
        var permissions = p.putArray("permissions");
        if (qualified(r, reviewer, "resource.versions.review"))
          permissions.add("resource.versions.review");
        if (qualified(r, reviewer, CatalogReviewPolicy.permission()))
          permissions.add(CatalogReviewPolicy.permission());
        principals.add(p);
      }
    }
    return CatalogReviewPolicy.count(input).allowed(v.get("risk").toString());
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
          if (role.equals("security"))
            access.target(r.tenant(), r.account(), CatalogReviewPolicy.permission());
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
          var reviewer = access.authorSnapshot(actor, r);
          db.update(
              "INSERT INTO resource.catalog_decision(id,tenant_id,review_id,reviewer_id,reviewer_name,reviewer_department,role,decision,reason,version_revision,digest,created_at) VALUES(:id,:tenant,:review,:reviewer,:reviewerName,:reviewerDepartment,:role,:decision,:reason,:rev,:digest,:now)",
              db.args(
                  "id",
                  UuidV7.next(),
                  "review",
                  id,
                  "reviewer",
                  r.account(),
                  "reviewerName",
                  reviewer.name(),
                  "reviewerDepartment",
                  reviewer.department(),
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

  ObjectNode signDowngrade(ObjectNode authorization) {
    return signing.get().signDowngrade(authorization);
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
            throw new AurigaException(
                ErrorCode.of(
                    "resource.review_required", 409, io.grpc.Status.Code.FAILED_PRECONDITION));
          if (revoked(v)) throw forbidden();
          long seq = number(pack, "release_high_water") + 1;
          if (seq > ResourceOperations.MAX) throw conflict();
          var env = envelope(r, v, seq);
          db.update(
              "UPDATE resource.catalog_version SET state='published',revision=revision+1,release_sequence=:seq,envelope=CAST(:env AS jsonb),market_published_at=:published WHERE tenant_id=:tenant AND id=:id",
              db.args(
                  "id",
                  id,
                  "seq",
                  seq,
                  "env",
                  env.toString(),
                  "published",
                  Timestamp.from(clock.instant())));
          db.update(
              "UPDATE resource.catalog_package SET enabled=true,default_version_id=:v,release_high_water=:seq,revision=revision+1 WHERE tenant_id=:tenant AND id=:p",
              db.args("p", p, "v", id, "seq", seq));
          var updated = ver(r, p, id, true);
          events.publish("resource.catalog_version_published", p.toString(), event(updated));
          operations.recorded(r, "resource.version.publish", id);
          return versionJson(r, updated);
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
            // Match publish's package-before-version lock order, including shared artifacts.
            for (var packageId :
                affected.stream().map(a -> (UUID) a.get("package_id")).distinct().sorted().toList())
              db.row(
                  "SELECT id FROM resource.catalog_package WHERE tenant_id=:tenant AND id=:id FOR UPDATE",
                  db.args("id", packageId));
            affected.replaceAll(
                a ->
                    db.row(
                        "SELECT * FROM resource.catalog_version WHERE tenant_id=:tenant AND id=:id FOR UPDATE",
                        db.args("id", a.get("id"))));
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
          return versionJson(r, ver(r, p, id, true));
        });
  }

  private void resignRevocations() {
    var old = db.revocations();
    long revision = old == null ? 1 : old.path("revision").asLong() + 1;
    if (revision > ResourceOperations.MAX) throw conflict();
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
    return nodeArtifact(reader, version, artifact, null, 0);
  }

  InputStream nodeArtifact(
      ResourceAccess.Reader reader,
      Map<String, Object> version,
      String artifact,
      String digest,
      long size) {
    var a =
        db.row(
            "SELECT * FROM resource.catalog_artifact WHERE tenant_id=:tenant AND package_id=:p AND version_id=:v AND artifact_id=:a",
            db.args("p", version.get("package_id"), "v", version.get("id"), "a", artifact));
    if (digest != null && (!digest.equals(a.get("digest")) || size != number(a, "size_bytes")))
      throw missing();
    return objects
        .get()
        .open(
            reader.tenant(),
            new ObjectStore.ObjectInfo(a.get("digest").toString(), number(a, "size_bytes")));
  }
}
