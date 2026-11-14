package auriga.resource.app;

import static auriga.resource.persistence.ResourceWorkflowStore.json;
import static auriga.resource.persistence.ResourceWorkflowStore.number;
import static auriga.resource.web.ResourceBody.*;

import auriga.common.context.TenantContext;
import auriga.common.error.*;
import auriga.common.events.EventPublisher;
import auriga.common.persistence.UuidV7;
import auriga.common.security.AurigaPrincipal;
import auriga.contracts.resource.v1.*;
import auriga.resource.api.*;
import auriga.resource.persistence.*;
import com.networknt.schema.*;
import java.io.InputStream;
import java.sql.Timestamp;
import java.time.*;
import java.util.*;
import java.util.function.*;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/**
 * Exact selections, authorized tickets and transactional outbox intents; node reports stay trusted.
 */
public final class CatalogDelivery implements UnifiedResourceResolution {
  private final ResourceStore tx;
  private final ResourceWorkflowStore db;
  private final ResourceAccess access;
  private final CatalogWorkflow catalog;
  private final ResourceDistributions legacy;
  private final Supplier<UnifiedResourceWorkspace> workspace;
  private final EventPublisher events;
  private final Clock clock;
  private final ResourceCursor cursors;
  private final Supplier<ResourceWorkspace> tenants;
  private final SchemaRegistry schemas =
      SchemaRegistry.withDefaultDialect(
          SpecificationVersion.DRAFT_2020_12,
          b ->
              b.schemaIdResolvers(
                      r ->
                          r.mapPrefix(
                              "https://auriga.example.com/contracts/",
                              "classpath:auriga/contracts/"))
                  .schemaRegistryConfig(
                      SchemaRegistryConfig.builder().formatAssertionsEnabled(true).build()));

  public CatalogDelivery(
      ResourceStore tx,
      ResourceWorkflowStore db,
      ResourceAccess access,
      CatalogWorkflow catalog,
      ResourceDistributions legacy,
      Supplier<UnifiedResourceWorkspace> workspace,
      EventPublisher events,
      Clock clock,
      ResourceCursor cursors,
      Supplier<ResourceWorkspace> tenants) {
    this.tx = tx;
    this.db = db;
    this.access = access;
    this.catalog = catalog;
    this.legacy = legacy;
    this.workspace = workspace;
    this.events = events;
    this.clock = clock;
    this.cursors = cursors;
    this.tenants = tenants;
  }

  private void validate(String type, JsonNode n) {
    if (!schemas
        .getSchema(
            SchemaLocation.of(
                "classpath:auriga/contracts/resource/unified.schema.json#/$defs/" + type))
        .validate(n)
        .isEmpty()) throw invalid();
  }

  private static AurigaException conflict() {
    return new AurigaException(CommonErrors.CONFLICT);
  }

  private static AurigaException missing() {
    return new AurigaException(CommonErrors.NOT_FOUND);
  }

  private static void match(String etag, long rev) {
    ResourceWorkflow.match(etag, rev);
  }

  private UnifiedResourceWorkspace workspace() {
    var w = workspace.get();
    if (w == null) throw new AurigaException(CommonErrors.UNAVAILABLE);
    return w;
  }

  private UUID client(AurigaPrincipal actor, ObjectNode body) {
    var c = uuid(body, "clientId");
    access.member(actor);
    if (actor.deviceId() != null && !actor.deviceId().equals(c.toString())) throw missing();
    return c;
  }

  private Map<String, Object> installation(ResourceAccess.Reader r, UUID c, UUID p) {
    return db.row(
        "SELECT * FROM resource.catalog_installation WHERE tenant_id=:tenant AND owner_id=:owner AND client_id=:c AND package_id=:p FOR UPDATE",
        db.args("owner", r.account(), "c", c, "p", p));
  }

  private void lock(ResourceAccess.Reader r, UUID c, UUID p) {
    db.lock("catalog-install/" + r.account() + "/" + c + "/" + p);
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

  private Map<String, Object> admit(ResourceAccess.Reader r, JsonNode identity, boolean active) {
    if (!identity.path("tenantId").asString().equals(r.tenant().toString())) throw missing();
    UUID p = uuid(identity, "packageId"), v = uuid(identity, "versionId");
    var pack = catalog.pkg(r, p, false);
    var row = catalog.ver(r, p, v, false);
    if (!identity(r, row).equals(identity)) throw missing();
    if (active && catalog.revoked(row)) throw missing();
    if (active
        && (!row.get("state").equals("published") || !Boolean.TRUE.equals(pack.get("enabled"))))
      throw missing();
    return row;
  }

  private void fresh(ResourceAccess.Reader reader, Map<String, Object> version) {
    var pack = catalog.pkg(reader, (UUID) version.get("package_id"), false);
    if (number(version, "release_sequence") != number(pack, "release_high_water")) throw conflict();
  }

  private ObjectNode ref(ResourceAccess.Reader r, Map<String, Object> v) {
    if (v.get("release_sequence") == null
        || !v.get("state").equals("published")
        || catalog.revoked(v)) throw missing();
    long sequence = number(v, "release_sequence");
    var env = catalog.envelope(r, v, sequence);
    var rev = legacy.revocations();
    String end = env.path("notAfter").asString(), re = rev.path("expiresAt").asString();
    String expiry = Instant.parse(end).isBefore(Instant.parse(re)) ? end : re;
    var n = JSON.createObjectNode();
    n.set("identity", identity(r, v));
    n.set("envelope", env);
    n.put(
            "manifestDownloadPath",
            CatalogWorkflow.base((UUID) v.get("package_id"), (UUID) v.get("id")) + "/manifest")
        .put("releaseSequence", sequence)
        .put("enabled", true);
    n.set(
        "lease",
        JSON.createObjectNode()
            .put("envelopeNotAfter", end)
            .put("revocationsExpiresAt", re)
            .put("expiresAt", expiry)
            .put("revocationRevision", rev.path("revision").asLong()));
    var a = n.putArray("artifacts");
    for (var item : json(v, "manifest").path("artifacts"))
      a.add(
          JSON.createObjectNode()
              .put("artifactId", item.path("id").asString())
              .put("digest", item.path("digest").asString())
              .put("sizeBytes", item.path("sizeBytes").asLong())
              .put(
                  "downloadPath",
                  CatalogWorkflow.base((UUID) v.get("package_id"), (UUID) v.get("id"))
                      + "/artifacts/"
                      + item.path("id").asString()));
    return n;
  }

  private void saveInstallation(Map<String, Object> row, ObjectNode document, long revision) {
    db.update(
        "UPDATE resource.catalog_installation SET document=CAST(:doc AS jsonb),revision=:rev,version_id=:v,collected_generation=NULL WHERE tenant_id=:tenant AND id=:id",
        db.args(
            "id",
            row.get("id"),
            "doc",
            document.toString(),
            "rev",
            revision,
            "v",
            uuid(document.path("resource").path("identity"), "versionId")));
  }

  public JsonNode select(AurigaPrincipal actor, ObjectNode body, String key, String etag) {
    validate("UnifiedInstallationInput", body);
    UUID c = client(actor, body);
    var selection = body.path("selection");
    var id = selection.path("identity");
    UUID p = uuid(id, "packageId");
    String action = selection.path("action").asString();
    return catalog.mutate(
        actor,
        ResourceAccess.READ,
        false,
        "installation/" + c + "/" + p,
        key,
        etag,
        body,
        r -> admit(r, id, Set.of("install", "enable").contains(action)),
        r -> {
          lock(r, c, p);
          var v = admit(r, id, Set.of("install", "enable").contains(action));
          if (Set.of("install", "enable").contains(action)) fresh(r, v);
          var rows =
              db.rows(
                  "SELECT * FROM resource.catalog_installation WHERE tenant_id=:tenant AND owner_id=:o AND client_id=:c AND package_id=:p FOR UPDATE",
                  db.args("o", r.account(), "c", c, "p", p));
          long rev = rows.isEmpty() ? 0 : number(rows.getFirst(), "revision");
          match(etag, rev);
          if (rev == 0 && !action.equals("install")) throw missing();
          var old = rows.isEmpty() ? null : json(rows.getFirst(), "document");
          if (old != null
              && Set.of("disable", "remove").contains(action)
              && !old.path("resource").path("identity").equals(id)) throw conflict();
          var resource =
              Set.of("disable", "remove").contains(action)
                  ? old.path("resource").deepCopy()
                  : ref(r, v);
          CatalogStatePolicy.dispatch(
              (ObjectNode) resource,
              CatalogStatePolicy.advance(rev, rev),
              CatalogStatePolicy.stateForAction(action));
          var n =
              JSON.createObjectNode()
                  .put("clientId", c.toString())
                  .put("installationRevision", rev + 1)
                  .put(
                      "selectionRevision",
                      old == null ? 1 : old.path("selectionRevision").asLong() + 1)
                  .put("generation", old == null ? 1 : old.path("generation").asLong() + 1)
                  .put("state", "applying")
                  .put("reportSource", "member");
          n.set("resource", resource);
          if (rows.isEmpty())
            db.update(
                "INSERT INTO resource.catalog_installation(id,tenant_id,owner_id,client_id,package_id,version_id,revision,document) VALUES(:id,:tenant,:o,:c,:p,:v,:rev,CAST(:doc AS jsonb))",
                db.args(
                    "id",
                    UuidV7.next(),
                    "o",
                    r.account(),
                    "c",
                    c,
                    "p",
                    p,
                    "v",
                    v.get("id"),
                    "rev",
                    rev + 1,
                    "doc",
                    n.toString()));
          else saveInstallation(rows.getFirst(), n, rev + 1);
          return n;
        });
  }

  private ObjectNode bound(
      ResourceAccess.Reader r, UUID c, JsonNode body, String etag, boolean active) {
    var id = body.path("identity");
    // Match the package-before-installation lock order used by selection updates.
    admit(r, id, active);
    var row = installation(r, c, uuid(id, "packageId"));
    match(etag, number(row, "revision"));
    var doc = (ObjectNode) json(row, "document");
    if (!doc.path("resource").path("identity").equals(id)
        || number(row, "revision") != counter(body, "installationRevision"))
      throw CatalogStatePolicy.conflict();
    if (active
        && (!CatalogStatePolicy.desired(doc.path("resource")).equals("ENABLED")
            || !Set.of("applying", "applied", "pending_restart", "overridden", "failed")
                .contains(doc.path("state").asString()))) throw missing();
    return doc;
  }

  public JsonNode ticket(AurigaPrincipal actor, ObjectNode body, String key, String etag) {
    validate("UnifiedDownloadTicketInput", body);
    UUID c = client(actor, body);
    return catalog.mutate(
        actor,
        ResourceAccess.READ,
        false,
        "ticket/" + c,
        key,
        etag,
        body,
        r -> bound(r, c, body, etag, true),
        r -> {
          var doc = bound(r, c, body, etag, true);
          return mintTicket(r, c, doc);
        },
        false);
  }

  private JsonNode mintTicket(ResourceAccess.Reader r, UUID c, JsonNode doc) {
    var resource =
        CatalogStatePolicy.dispatch(
            (ObjectNode) doc.path("resource").deepCopy(),
            doc.path("installationRevision").asLong(),
            CatalogStatePolicy.desired(doc.path("resource")));
    Instant lease = Instant.parse(resource.path("lease").path("expiresAt").asString());
    if (!clock.instant().isBefore(lease)) throw missing();
    byte[] random = new byte[32];
    new java.security.SecureRandom().nextBytes(random);
    String ticket = Base64.getUrlEncoder().withoutPadding().encodeToString(random);
    Instant expiry = clock.instant().plusSeconds(60);
    if (expiry.isAfter(lease)) expiry = lease;
    var id = resource.path("identity");
    db.update(
        "INSERT INTO resource.catalog_ticket(id,tenant_id,owner_id,client_id,package_id,version_id,installation_revision,ticket_hash,expires_at) VALUES(:id,:tenant,:o,:c,:p,:v,:rev,:hash,:expiry)",
        db.args(
            "id",
            UuidV7.next(),
            "o",
            r.account(),
            "c",
            c,
            "p",
            uuid(id, "packageId"),
            "v",
            uuid(id, "versionId"),
            "rev",
            doc.path("installationRevision").asLong(),
            "hash",
            ResourceWorkflow.hash(ticket),
            "expiry",
            Timestamp.from(expiry)));
    var n =
        JSON.createObjectNode()
            .put("ticket", ticket)
            .put("clientId", c.toString())
            .put("expiresAt", ResourceSigning.time(expiry));
    n.set("resource", resource);
    n.set("revocations", legacy.revocations());
    return n;
  }

  public InputStream download(
      AurigaPrincipal actor, ObjectNode body, String key, String etag, boolean range) {
    validate("UnifiedDownloadInput", body);
    UUID c = client(actor, body);
    if (range || key == null || etag == null || !key.matches("[\\x21-\\x7E]{1,128}"))
      throw invalid();
    var r = access.member(actor);
    return tx.write(
        () -> {
          var ticket =
              db.row(
                  "SELECT * FROM resource.catalog_ticket WHERE tenant_id=:tenant AND owner_id=:o AND client_id=:c AND ticket_hash=:hash",
                  db.args(
                      "o",
                      r.account(),
                      "c",
                      c,
                      "hash",
                      ResourceWorkflow.hash(body.path("ticket").asString())));
          if (!((Timestamp) ticket.get("expires_at")).toInstant().isAfter(clock.instant()))
            throw missing();
          catalog.pkg(r, (UUID) ticket.get("package_id"), false);
          var row = installation(r, c, (UUID) ticket.get("package_id"));
          match(etag, number(row, "revision"));
          if (number(row, "revision") != number(ticket, "installation_revision")
              || !row.get("version_id").equals(ticket.get("version_id"))) throw missing();
          var id = json(row, "document").path("resource").path("identity");
          admit(r, id, true);
          if (!CatalogStatePolicy.desired(json(row, "document").path("resource")).equals("ENABLED"))
            throw missing();
          if (db.update(
                  "UPDATE resource.catalog_ticket SET consumed_at=:now WHERE tenant_id=:tenant AND id=:id AND consumed_at IS NULL AND expires_at>:now",
                  db.args("id", ticket.get("id"), "now", Timestamp.from(clock.instant())))
              != 1) throw conflict();
          return catalog.download(
              actor,
              (UUID) ticket.get("package_id"),
              (UUID) ticket.get("version_id"),
              text(body, "artifactId", 64),
              false);
        });
  }

  public JsonNode renew(AurigaPrincipal actor, ObjectNode body, String key, String etag) {
    validate("UnifiedRenewalInput", body);
    UUID c = client(actor, body);
    return catalog.mutate(
        actor,
        ResourceAccess.READ,
        false,
        "renew/" + c,
        key,
        etag,
        body,
        r -> bound(r, c, body, etag, true),
        r -> {
          var doc = bound(r, c, body, etag, true);
          if (counter(body, "selectionRevision") != doc.path("selectionRevision").asLong()
              || counter(body, "releaseSequence")
                  != doc.path("resource").path("releaseSequence").asLong()) throw conflict();
          var v = admit(r, body.path("identity"), true);
          var updated = ref(r, v);
          if (updated.path("releaseSequence").asLong() != counter(body, "releaseSequence"))
            throw conflict();
          CatalogStatePolicy.dispatch(
              updated,
              doc.path("resource").path("installationRevision").asLong(),
              CatalogStatePolicy.desired(doc.path("resource")));
          doc.set("resource", updated);
          saveInstallation(
              installation(r, c, uuid(body.path("identity"), "packageId")),
              doc,
              counter(body, "installationRevision"));
          return mintTicket(r, c, doc);
        },
        false);
  }

  public JsonNode report(AurigaPrincipal actor, ObjectNode body, String key, String etag) {
    validate("UnifiedMemberInstallationReport", body);
    UUID c = client(actor, body);
    return catalog.mutate(
        actor,
        ResourceAccess.READ,
        false,
        "report/" + c,
        key,
        etag,
        body,
        r -> bound(r, c, body, etag, false),
        r -> {
          var doc = bound(r, c, body, etag, false);
          if (installation(r, c, uuid(body.path("identity"), "packageId"))
                  .get("collected_generation")
              != null) throw CatalogStatePolicy.conflict();
          if (!CatalogStatePolicy.tuple(
              doc.path("resource"),
              doc.path("selectionRevision").asLong(),
              doc.path("generation").asLong(),
              body,
              "member")) throw CatalogStatePolicy.conflict();
          String state = body.path("state").asString();
          if (Set.of("applied", "disabled", "removed").contains(state)
              && !CatalogStatePolicy.acknowledged(
                  doc.path("resource"),
                  doc.path("selectionRevision").asLong(),
                  doc.path("generation").asLong(),
                  body,
                  "member")) throw CatalogStatePolicy.conflict();
          // The contract requires member; the server never derives node trust from this body.
          var report = body.deepCopy();
          report.put("reportSource", "member");
          doc.set("report", report);
          doc.put("state", state);
          saveInstallation(
              installation(r, c, uuid(body.path("identity"), "packageId")),
              doc,
              counter(body, "installationRevision"));
          if (state.equals("removed")) {
            var next = doc.deepCopy().put("generation", doc.path("generation").asLong() + 1);
            db.update(
                "UPDATE resource.catalog_installation SET document=CAST(:doc AS jsonb),collected_generation=:generation WHERE tenant_id=:tenant AND owner_id=:owner AND client_id=:client AND package_id=:pkg AND revision=:revision",
                db.args(
                    "doc",
                    next.toString(),
                    "generation",
                    next.path("generation").asLong(),
                    "owner",
                    r.account(),
                    "client",
                    c,
                    "pkg",
                    uuid(body.path("identity"), "packageId"),
                    "revision",
                    counter(body, "installationRevision")));
          }
          return doc;
        });
  }

  public JsonNode installations(
      AurigaPrincipal actor, UUID c, String kind, int limit, String cursor) {
    var b = JSON.createObjectNode().put("clientId", c.toString());
    client(actor, b);
    var r = access.member(actor);
    return page(
        r,
        "installations/" + c + "/" + kind,
        limit,
        cursor,
        "catalog_installation",
        " AND owner_id=:owner AND client_id=:client",
        db.args("owner", r.account(), "client", c),
        kind,
        false);
  }

  private JsonNode page(
      ResourceAccess.Reader r,
      String scope,
      int limit,
      String cursor,
      String table,
      String filter,
      Map<String, Object> a,
      String kind,
      boolean distribution) {
    if (limit < 1 || limit > 200 || kind != null && !Set.of("skill", "tool").contains(kind))
      throw invalid();
    return tx.write(
        () -> {
          String where = " WHERE tenant_id=:tenant" + filter;
          if (kind != null)
            where +=
                distribution
                    ? " AND EXISTS(SELECT 1 FROM jsonb_array_elements(document->'selections') s WHERE s->'identity'->>'kind'=:kind)"
                    : " AND document->'resource'->'identity'->>'kind'=:kind";
          a.put("kind", kind);
          var pos = cursors.decode(cursor, r, "catalog/" + scope, limit);
          if (pos == null) {
            var top =
                db.rows(
                    "SELECT id FROM resource." + table + where + " ORDER BY id DESC LIMIT 1", a);
            pos = cursors.initial(top.isEmpty() ? new UUID(0, 0) : (UUID) top.getFirst().get("id"));
          }
          a.put("after", pos.after());
          a.put("ceiling", pos.ceiling());
          a.put("limit", limit + 1);
          var rows =
              db.rows(
                  "SELECT * FROM resource."
                      + table
                      + where
                      + " AND id>:after AND id<=:ceiling ORDER BY id LIMIT :limit",
                  a);
          var n = JSON.createObjectNode();
          var items = n.putArray("items");
          for (var row : rows.subList(0, Math.min(rows.size(), limit)))
            items.add(distribution ? distributionView(r, row) : json(row, "document"));
          if (rows.size() > limit)
            n.put(
                "nextCursor",
                cursors.encode(
                    r,
                    "catalog/" + scope,
                    limit,
                    new ResourceCursor.Position(
                        (UUID) rows.get(limit - 1).get("id"), pos.ceiling(), pos.expires())));
          return n;
        });
  }

  private Map<String, Object> cloudRow(UUID instance, UUID pkg) {
    var rows =
        db.rows(
            "SELECT * FROM resource.catalog_instance_installation WHERE tenant_id=:tenant AND instance_id=:instance AND package_id=:pkg FOR UPDATE",
            db.args("instance", instance, "pkg", pkg));
    return rows.isEmpty() ? null : rows.getFirst();
  }

  private LinkedHashMap<UUID, ObjectNode> cloudResources(
      UUID instance, UUID owner, long selection) {
    var resources = new LinkedHashMap<UUID, ObjectNode>();
    for (var row :
        db.rows(
            "SELECT * FROM resource.catalog_instance_installation WHERE tenant_id=:tenant AND instance_id=:instance AND selection_revision=:selection AND collected_generation IS NULL ORDER BY package_id FOR UPDATE",
            db.args("instance", instance, "selection", selection))) {
      if (!owner.equals(row.get("owner_id"))) throw missing();
      resources.put((UUID) row.get("package_id"), (ObjectNode) json(row, "resource"));
    }
    return resources;
  }

  private void saveCloud(
      UUID instance, UUID owner, long selection, ObjectNode resource, boolean changed) {
    db.update(
        "INSERT INTO resource.catalog_instance_installation(id,tenant_id,instance_id,owner_id,package_id,revision,resource,selection_revision) VALUES(:id,:tenant,:instance,:owner,:pkg,:revision,CAST(:resource AS jsonb),:selection) ON CONFLICT(tenant_id,instance_id,package_id) DO UPDATE SET owner_id=EXCLUDED.owner_id,revision=EXCLUDED.revision,resource=EXCLUDED.resource,selection_revision=EXCLUDED.selection_revision,collected_generation=CASE WHEN :changed THEN NULL ELSE catalog_instance_installation.collected_generation END",
        db.args(
            "id",
            UuidV7.next(),
            "instance",
            instance,
            "owner",
            owner,
            "pkg",
            uuid(resource.path("identity"), "packageId"),
            "revision",
            resource.path("installationRevision").asLong(),
            "resource",
            resource.toString(),
            "selection",
            selection,
            "changed",
            changed));
  }

  private long reserve(UUID instance, UUID pkg) {
    var rows =
        db.rows(
            "INSERT INTO resource.catalog_instance_counter(tenant_id,instance_id,package_id,revision) VALUES(:tenant,:instance,:pkg,1) ON CONFLICT(tenant_id,instance_id,package_id) DO UPDATE SET revision=catalog_instance_counter.revision+1 WHERE catalog_instance_counter.revision<:maximum RETURNING revision",
            db.args("instance", instance, "pkg", pkg, "maximum", ResourceWorkflow.MAX));
    if (rows.isEmpty()) throw CatalogStatePolicy.conflict();
    return number(rows.getFirst(), "revision");
  }

  private boolean sameInstallation(JsonNode expected, UnifiedResourceRef actual) {
    return CatalogProtocol.identity(expected.path("identity")).equals(actual.getIdentity())
        && expected.path("installationRevision").asLong() == actual.getInstallationRevision()
        && CatalogStatePolicy.desired(expected).equals(CatalogStatePolicy.desired(actual));
  }

  /** Only the trusted workspace spec promotes an intent into the applied installation ledger. */
  private void synchronize(UnifiedResourceWorkspace.Snapshot snapshot) {
    var rows =
        db.rows(
            "SELECT * FROM resource.catalog_distribution WHERE tenant_id=:tenant AND instance_id=:instance AND selection_revision=:selection",
            db.args("instance", snapshot.instance(), "selection", snapshot.selectionRevision()));
    if (rows.isEmpty()) return;
    var row = rows.getFirst();
    if (!snapshot.owner().equals(row.get("owner_id"))
        || !Objects.equals(snapshot.node(), row.get("node_id"))) throw conflict();
    db.update(
        "UPDATE resource.catalog_distribution SET lifecycle='superseded',next_reconcile_at=NULL WHERE tenant_id=:tenant AND instance_id=:instance AND lifecycle='active' AND id<>:id",
        db.args("instance", snapshot.instance(), "id", row.get("id")));
    db.update(
        "UPDATE resource.catalog_distribution SET lifecycle='active' WHERE tenant_id=:tenant AND id=:id",
        db.args("id", row.get("id")));
    var resolution = hydrateExpectations(row);
    for (var item : resolution.path("resources")) {
      if (snapshot.resources().stream().noneMatch(actual -> sameInstallation(item, actual)))
        continue;
      var previous = cloudRow(snapshot.instance(), uuid(item.path("identity"), "packageId"));
      boolean changed =
          previous == null
              || number(previous, "revision") != item.path("installationRevision").asLong()
              || !CatalogStatePolicy.desired(json(previous, "resource"))
                  .equals(CatalogStatePolicy.desired(item));
      saveCloud(
          snapshot.instance(),
          snapshot.owner(),
          snapshot.selectionRevision(),
          (ObjectNode) item,
          changed);
    }
  }

  public JsonNode distribute(AurigaPrincipal actor, ObjectNode body, String key, String etag) {
    validate("UnifiedDistributionInput", body);
    UUID instance = uuid(body, "instanceId");
    return catalog.mutate(
        actor,
        "resource.distributions.manage",
        true,
        "distribution/" + instance,
        key,
        etag,
        body,
        r -> workspace().snapshot(r.tenant(), instance),
        r -> {
          db.lock("catalog-distribution/" + instance);
          var snapshot = workspace().snapshot(r.tenant(), instance);
          match(etag, snapshot.selectionRevision());
          if (snapshot.node() == null) throw new AurigaException(CommonErrors.UNAVAILABLE);
          var owner = access.target(r.tenant(), snapshot.owner());
          synchronize(snapshot);
          long revision =
              Math.max(
                      snapshot.selectionRevision(),
                      number(
                          db.row(
                              "SELECT COALESCE(max(selection_revision),0) AS revision FROM resource.catalog_distribution WHERE tenant_id=:tenant AND instance_id=:instance",
                              db.args("instance", instance)),
                          "revision"))
                  + 1;
          if (revision > ResourceWorkflow.MAX) throw conflict();
          UUID id = UuidV7.next();
          var resolution = JSON.createObjectNode().put("selectionRevision", revision);
          var resources = resolution.putArray("resources");
          var retained = cloudResources(instance, snapshot.owner(), snapshot.selectionRevision());
          var selected = new HashSet<UUID>();
          var grants = new LinkedHashMap<UUID, JsonNode>();
          for (var s : body.path("selections")) {
            UUID p = uuid(s.path("identity"), "packageId");
            if (!selected.add(p)) throw invalid();
            boolean enabling = Set.of("install", "enable").contains(s.path("action").asString());
            var v = admit(owner, s.path("identity"), enabling);
            ObjectNode resource;
            if (enabling) {
              fresh(owner, v);
              resource = ref(owner, v);
            } else {
              var previous = retained.get(p);
              if (previous == null || !previous.path("identity").equals(s.path("identity")))
                throw missing();
              resource = previous.deepCopy();
            }
            CatalogStatePolicy.dispatch(
                resource,
                reserve(instance, p),
                CatalogStatePolicy.stateForAction(s.path("action").asString()));
            retained.put(p, resource);
          }
          for (var entry : retained.entrySet()) {
            var resource = entry.getValue();
            var persisted = cloudRow(instance, entry.getKey());
            boolean changed = selected.contains(entry.getKey());
            if (!changed && persisted != null && persisted.get("collected_generation") != null)
              continue;
            UUID grant = UuidV7.next();
            grants.put(grant, resource.path("identity"));
            resource.put(
                "manifestDownloadPath",
                "/internal/resource/v1/catalog/downloads/" + grant + "/manifest");
            for (var artifact : resource.path("artifacts"))
              ((ObjectNode) artifact)
                  .put(
                      "downloadPath",
                      "/internal/resource/v1/catalog/downloads/"
                          + grant
                          + "/artifacts/"
                          + artifact.path("artifactId").asString());
            CatalogStatePolicy.dispatch(
                resource,
                resource.path("installationRevision").asLong(),
                CatalogStatePolicy.desired(resource));
            resources.add(resource);
          }
          resolution.set("revocations", legacy.revocations());
          var doc =
              JSON.createObjectNode()
                  .put("distributionId", id.toString())
                  .put("instanceId", instance.toString())
                  .put("ownerId", snapshot.owner().toString())
                  .put("selectionRevision", revision)
                  .put("generation", Math.max(1, snapshot.generation() + 1))
                  .put("state", "applying")
                  .put("reportSource", "node");
          doc.set("selections", body.path("selections"));
          doc.putArray("reports");
          db.update(
              "UPDATE resource.catalog_distribution SET lifecycle='superseded',next_reconcile_at=NULL WHERE tenant_id=:tenant AND instance_id=:instance AND lifecycle='pending'",
              db.args("instance", instance));
          db.update(
              "INSERT INTO resource.catalog_distribution(id,tenant_id,actor_id,instance_id,owner_id,node_id,selection_revision,fingerprint,document,resolution,next_reconcile_at) VALUES(:id,:tenant,:actor,:instance,:owner,:node,:rev,:fp,CAST(:doc AS jsonb),CAST(:resolution AS jsonb),:due)",
              db.args(
                  "id",
                  id,
                  "actor",
                  r.account(),
                  "instance",
                  instance,
                  "owner",
                  snapshot.owner(),
                  "node",
                  snapshot.node(),
                  "rev",
                  revision,
                  "fp",
                  ResourceWorkflow.hash(ResourceSigning.canonical(body)),
                  "doc",
                  doc.toString(),
                  "resolution",
                  resolution.toString(),
                  "due",
                  Timestamp.from(clock.instant())));
          for (var grant : grants.entrySet())
            db.update(
                "INSERT INTO resource.catalog_node_grant(id,tenant_id,distribution_id,package_id,version_id) VALUES(:id,:tenant,:d,:p,:v)",
                db.args(
                    "id",
                    grant.getKey(),
                    "d",
                    id,
                    "p",
                    uuid(grant.getValue(), "packageId"),
                    "v",
                    uuid(grant.getValue(), "versionId")));
          var event =
              JSON.createObjectNode()
                  .put("distributionId", id.toString())
                  .put("instanceId", instance.toString())
                  .put("ownerId", snapshot.owner().toString())
                  .put("selectionRevision", revision);
          event.set("selections", body.path("selections"));
          events.publish("resource.catalog_distribution_requested", instance.toString(), event);
          return doc;
        });
  }

  private ObjectNode distributionView(ResourceAccess.Reader r, Map<String, Object> row) {
    db.lock("catalog-distribution/" + row.get("instance_id"));
    row =
        db.row(
            "SELECT * FROM resource.catalog_distribution WHERE tenant_id=:tenant AND id=:id",
            db.args("id", row.get("id")));
    var doc = (ObjectNode) json(row, "document");
    var snapshot = workspace().snapshot(r.tenant(), (UUID) row.get("instance_id"));
    if (!snapshot.owner().equals(row.get("owner_id"))) throw missing();
    synchronize(snapshot);
    if (snapshot.selectionRevision() != number(row, "selection_revision")
            && row.get("lifecycle").equals("superseded")
        || snapshot.selectionRevision() > number(row, "selection_revision")) {
      doc.put("state", "superseded");
      return doc;
    }
    if (snapshot.selectionRevision() == number(row, "selection_revision")) {
      doc.put("generation", Math.max(1, snapshot.generation()));
      var reports = doc.putArray("reports");
      boolean applied = snapshot.ready() && snapshot.observedGeneration() == snapshot.generation();
      var successes = new HashSet<String>();
      for (var s : doc.path("selections")) {
        var identity = CatalogProtocol.identity(s.path("identity"));
        var expected =
            snapshot.resources().stream().filter(x -> x.getIdentity().equals(identity)).toList();
        var durable =
            cloudRow((UUID) row.get("instance_id"), uuid(s.path("identity"), "packageId"));
        if (expected.size() != 1
            || durable == null
            || expected.getFirst().getInstallationRevision() != number(durable, "revision")
            || number(durable, "selection_revision") != snapshot.selectionRevision()) {
          // A collected tombstone is complete only after a verified later-generation refresh.
          if (durable != null
              && durable.get("collected_generation") != null
              && snapshot.generation() >= number(durable, "collected_generation")
              && CatalogStatePolicy.desired(json(durable, "resource")).equals("REMOVED")) {
            successes.add("removed");
            continue;
          }
          applied = false;
          continue;
        }
        var resource = expected.getFirst();
        if (!CatalogStatePolicy.desired(resource)
            .equals(CatalogStatePolicy.desired(json(durable, "resource")))) {
          applied = false;
          continue;
        }
        var report =
            snapshot.reports().stream()
                .filter(
                    x ->
                        CatalogStatePolicy.acknowledged(
                            resource,
                            snapshot.selectionRevision(),
                            snapshot.generation(),
                            x,
                            "node"))
                .findFirst();
        if (report.isEmpty()) {
          applied = false;
          continue;
        }
        var x = report.get();
        var n =
            JSON.createObjectNode()
                .put("clientId", x.getClientId())
                .put("installationRevision", x.getInstallationRevision())
                .put("selectionRevision", x.getSelectionRevision())
                .put("generation", x.getGeneration())
                .put("observedGeneration", x.getObservedGeneration())
                .put("state", x.getState())
                .put("reportSource", "node")
                .put("observedAt", x.getObservedAt());
        n.set("identity", s.path("identity"));
        if (!x.getErrorCode().isEmpty()) n.put("errorCode", x.getErrorCode());
        reports.add(n);
        successes.add(x.getState());
      }
      doc.put(
          "state",
          applied ? (successes.size() == 1 ? successes.iterator().next() : "applied") : "applying");
    }
    return doc;
  }

  public JsonNode distributions(
      AurigaPrincipal actor, UUID instance, String kind, int limit, String cursor) {
    var r = access.administrator(actor, "resource.distributions.manage");
    return page(
        r,
        "distributions/" + instance + "/" + kind,
        limit,
        cursor,
        "catalog_distribution",
        instance == null ? "" : " AND instance_id=:instance",
        db.args("instance", instance),
        kind,
        true);
  }

  public JsonNode distribution(AurigaPrincipal actor, UUID id) {
    var r = access.administrator(actor, "resource.distributions.manage");
    return tx.write(
        () ->
            distributionView(
                r,
                db.row(
                    "SELECT * FROM resource.catalog_distribution WHERE tenant_id=:tenant AND id=:id",
                    db.args("id", id))));
  }

  @Override
  public ResolveUnifiedResourcesResponse resolve(ResolveUnifiedResourcesRequest request) {
    return resolve(request, null);
  }

  private ResolveUnifiedResourcesResponse resolve(
      ResolveUnifiedResourcesRequest request, UnifiedResourceWorkspace.Snapshot actual) {
    UUID tenant = TenantContext.require();
    if (!tenant.toString().equals(request.getTenantId())) throw missing();
    return tx.write(
        () -> {
          db.lock("catalog-distribution/" + request.getInstanceId());
          var row =
              db.row(
                  "SELECT * FROM resource.catalog_distribution WHERE tenant_id=:tenant AND instance_id=:instance AND selection_revision=:rev",
                  db.args(
                      "instance",
                      UUID.fromString(request.getInstanceId()),
                      "rev",
                      request.getSelectionRevision()));
          if (!row.get("owner_id").toString().equals(request.getOwnerId())
              || !row.get("node_id").toString().equals(request.getNodeId())) throw missing();
          var selections = json(row, "document").path("selections");
          var expected = new ArrayList<UnifiedSelection>();
          selections.forEach(s -> expected.add(CatalogProtocol.selection(s)));
          if (!expected.equals(request.getSelectionsList())) throw conflict();
          var snapshot =
              actual == null ? workspace().snapshot(tenant, (UUID) row.get("instance_id")) : actual;
          if (snapshot.selectionRevision() > request.getSelectionRevision()
              || !snapshot.owner().equals(row.get("owner_id"))
              || !Objects.equals(snapshot.node(), row.get("node_id"))) throw conflict();
          synchronize(snapshot);
          if (snapshot.selectionRevision() != request.getSelectionRevision()
              && row.get("lifecycle").equals("superseded")) throw conflict();
          var owner = access.target(tenant, (UUID) row.get("owner_id"));
          var resolution = hydrateExpectations(row);
          var out =
              ResolveUnifiedResourcesResponse.newBuilder()
                  .setSelectionRevision(request.getSelectionRevision())
                  .setRevocations(ResourceProtocol.revocations(legacy.revocations()));
          var refreshed = JSON.createArrayNode();
          for (var resource : resolution.path("resources")) {
            boolean enabling = CatalogStatePolicy.desired(resource).equals("ENABLED");
            var version = admit(owner, resource.path("identity"), false);
            var current = (ObjectNode) resource.deepCopy();
            var pack = catalog.pkg(owner, uuid(resource.path("identity"), "packageId"), false);
            if (enabling
                && (!version.get("state").equals("published")
                    || !Boolean.TRUE.equals(pack.get("enabled"))
                    || catalog.revoked(version))) {
              CatalogStatePolicy.dispatch(
                  current,
                  reserve(
                      (UUID) row.get("instance_id"), uuid(resource.path("identity"), "packageId")),
                  "DISABLED");
              enabling = false;
            }
            if (enabling
                && Instant.parse(current.path("lease").path("expiresAt").asString())
                    .isBefore(clock.instant().plusSeconds(300))) {
              var renewal = ref(owner, version);
              if (renewal.path("releaseSequence").asLong()
                  != current.path("releaseSequence").asLong()) throw conflict();
              current.set("envelope", renewal.path("envelope"));
              current.set("lease", renewal.path("lease"));
            }
            CatalogStatePolicy.dispatch(
                current,
                current.path("installationRevision").asLong(),
                CatalogStatePolicy.desired(current));
            out.addResources(CatalogProtocol.ref(current));
            refreshed.add(current);
          }
          var saved = (ObjectNode) resolution.deepCopy();
          saved.set("resources", refreshed);
          saved.set("revocations", legacy.revocations());
          db.update(
              "UPDATE resource.catalog_distribution SET resolution=CAST(:resolution AS jsonb) WHERE tenant_id=:tenant AND id=:id",
              db.args("id", row.get("id"), "resolution", saved.toString()));
          return out.build();
        });
  }

  private ObjectNode hydrateExpectations(Map<String, Object> row) {
    var resolution =
        (ObjectNode)
            json(
                db.row(
                    "SELECT resolution FROM resource.catalog_distribution WHERE tenant_id=:tenant AND id=:id",
                    db.args("id", row.get("id"))),
                "resolution");
    for (var item : resolution.path("resources")) {
      var resource = (ObjectNode) item;
      UUID pkg = uuid(resource.path("identity"), "packageId");
      if (!resource.has("installationRevision")
          || resource.path("installationRevision").asLong() == 0) {
        var persisted = cloudRow((UUID) row.get("instance_id"), pkg);
        long revision =
            persisted != null
                    && number(persisted, "selection_revision") == number(row, "selection_revision")
                    && json(persisted, "resource")
                        .path("identity")
                        .equals(resource.path("identity"))
                ? number(persisted, "revision")
                : reserve((UUID) row.get("instance_id"), pkg);
        String desired = CatalogStatePolicy.desired(resource);
        for (var selection : json(row, "document").path("selections"))
          if (selection.path("identity").equals(resource.path("identity")))
            desired = CatalogStatePolicy.stateForAction(selection.path("action").asString());
        CatalogStatePolicy.dispatch(resource, revision, desired);
      }
      CatalogStatePolicy.dispatch(
          resource,
          resource.path("installationRevision").asLong(),
          CatalogStatePolicy.desired(resource));
    }
    db.update(
        "UPDATE resource.catalog_distribution SET resolution=CAST(:resolution AS jsonb) WHERE tenant_id=:tenant AND id=:id",
        db.args("id", row.get("id"), "resolution", resolution.toString()));
    return resolution;
  }

  /**
   * Publish a new generation only after exact, trusted removed acknowledgements. Retain CAS rows.
   */
  private boolean collectTombstones(
      Map<String, Object> row, UnifiedResourceWorkspace.Snapshot snapshot) {
    if (!snapshot.ready() || snapshot.observedGeneration() != snapshot.generation()) return false;
    UUID instance = (UUID) row.get("instance_id");
    var resolution = hydrateExpectations(row);
    var removed = new LinkedHashMap<UUID, Long>();
    var remaining = JSON.createArrayNode();
    for (var resource : resolution.path("resources")) {
      UUID pkg = uuid(resource.path("identity"), "packageId");
      boolean collect = false;
      if (CatalogStatePolicy.desired(resource).equals("REMOVED")) {
        var expected =
            snapshot.resources().stream()
                .filter(
                    x ->
                        x.getIdentity().equals(CatalogProtocol.identity(resource.path("identity")))
                            && x.getInstallationRevision()
                                == resource.path("installationRevision").asLong()
                            && CatalogStatePolicy.desired(x).equals("REMOVED"))
                .toList();
        if (expected.size() == 1)
          collect =
              snapshot.reports().stream()
                  .anyMatch(
                      report ->
                          CatalogStatePolicy.acknowledged(
                              expected.getFirst(),
                              snapshot.selectionRevision(),
                              snapshot.generation(),
                              report,
                              "node"));
      }
      if (collect) removed.put(pkg, resource.path("installationRevision").asLong());
      else remaining.add(resource);
    }
    if (removed.isEmpty()) return false;
    var next =
        ResolveUnifiedResourcesResponse.newBuilder()
            .setSelectionRevision(snapshot.selectionRevision())
            .setRevocations(ResourceProtocol.revocations(legacy.revocations()));
    remaining.forEach(resource -> next.addResources(CatalogProtocol.ref(resource)));
    var observed =
        workspace()
            .refresh(
                TenantContext.require(),
                instance,
                snapshot.owner(),
                snapshot.selectionRevision(),
                next.build());
    if (observed == null
        || !observed.instance().equals(instance)
        || !observed.owner().equals(snapshot.owner())
        || !Objects.equals(observed.node(), snapshot.node())
        || observed.selectionRevision() != snapshot.selectionRevision()
        || observed.generation() != snapshot.generation() + 1) throw CatalogStatePolicy.conflict();
    if (observed.resources().size() != next.getResourcesCount()
        || !new HashSet<>(observed.resources()).equals(new HashSet<>(next.getResourcesList())))
      throw CatalogStatePolicy.conflict();
    for (var entry : removed.entrySet()) {
      if (observed.resources().stream()
          .anyMatch(
              resource -> resource.getIdentity().getPackageId().equals(entry.getKey().toString())))
        throw CatalogStatePolicy.conflict();
      var durable = cloudRow(instance, entry.getKey());
      if (durable == null
          || number(durable, "revision") != entry.getValue()
          || number(durable, "selection_revision") != snapshot.selectionRevision()
          || !CatalogStatePolicy.desired(json(durable, "resource")).equals("REMOVED"))
        throw CatalogStatePolicy.conflict();
      db.update(
          "UPDATE resource.catalog_instance_installation SET collected_generation=:generation WHERE tenant_id=:tenant AND instance_id=:instance AND package_id=:pkg AND revision=:revision AND selection_revision=:selection",
          db.args(
              "generation",
              observed.generation(),
              "instance",
              instance,
              "pkg",
              entry.getKey(),
              "revision",
              entry.getValue(),
              "selection",
              snapshot.selectionRevision()));
    }
    resolution.set("resources", remaining);
    db.update(
        "UPDATE resource.catalog_distribution SET resolution=CAST(:resolution AS jsonb) WHERE tenant_id=:tenant AND id=:id",
        db.args("id", row.get("id"), "resolution", resolution.toString()));
    return true;
  }

  public InputStream nodeDownload(UUID tenant, UUID node, UUID id, String artifact, boolean range) {
    if (range) throw invalid();
    return TenantContext.call(
        tenant,
        () ->
            tx.write(
                () -> {
                  var grant =
                      db.row(
                          "SELECT * FROM resource.catalog_node_grant WHERE tenant_id=:tenant AND id=:id",
                          db.args("id", id));
                  var row =
                      db.row(
                          "SELECT * FROM resource.catalog_distribution WHERE tenant_id=:tenant AND id=:id",
                          db.args("id", grant.get("distribution_id")));
                  if (!node.equals(row.get("node_id"))) throw missing();
                  var snapshot = workspace().snapshot(tenant, (UUID) row.get("instance_id"));
                  if (!snapshot.owner().equals(row.get("owner_id"))
                      || !node.equals(snapshot.node())
                      || snapshot.selectionRevision() != number(row, "selection_revision"))
                    throw missing();
                  JsonNode resource = null;
                  for (var item : json(row, "resolution").path("resources"))
                    if (item.path("identity")
                        .path("packageId")
                        .asString()
                        .equals(grant.get("package_id").toString())) resource = item;
                  if (resource == null
                      || !resource
                          .path("identity")
                          .path("versionId")
                          .asString()
                          .equals(grant.get("version_id").toString())
                      || !CatalogStatePolicy.desired(resource).equals("ENABLED")
                      || !clock
                          .instant()
                          .isBefore(
                              Instant.parse(resource.path("lease").path("expiresAt").asString())))
                    throw missing();
                  var owner = access.target(tenant, (UUID) row.get("owner_id"));
                  var v = admit(owner, resource.path("identity"), true);
                  if (artifact == null)
                    return new java.io.ByteArrayInputStream((byte[]) v.get("manifest_bytes"));
                  // Uses the same authorized immutable object selection as member download, without
                  // impersonation.
                  return catalog.nodeArtifact(owner, v, artifact);
                }));
  }

  @org.springframework.scheduling.annotation.Scheduled(
      fixedDelayString = "${auriga.resource.catalog-reconcile-ms:5000}")
  public void reconcile() {
    var directory = tenants.get();
    if (directory == null || workspace.get() == null) return;
    for (UUID tenant : directory.tenants())
      try {
        TenantContext.run(
            tenant,
            () -> {
              var pending =
                  tx.read(
                      () ->
                          db.rows(
                              "SELECT * FROM resource.catalog_revocation_delivery WHERE tenant_id=:tenant AND completed_revision<revision AND next_reconcile_at<=:now ORDER BY next_reconcile_at,id LIMIT 100",
                              db.args("now", Timestamp.from(clock.instant()))));
              for (var row : pending)
                try {
                  tx.write(
                      () -> {
                        db.lock("catalog-revoke/" + row.get("instance_id"));
                        var list = legacy.revocations();
                        workspace()
                            .revoke(
                                tenant,
                                (UUID) row.get("instance_id"),
                                ResourceProtocol.revocations(list));
                        db.update(
                            "UPDATE resource.catalog_revocation_delivery SET completed_revision=:rev WHERE tenant_id=:tenant AND id=:id AND revision<=:rev",
                            db.args("id", row.get("id"), "rev", list.path("revision").asLong()));
                        return true;
                      });
                } catch (RuntimeException e) {
                  retry();
                  tx.write(
                      () ->
                          db.update(
                              "UPDATE resource.catalog_revocation_delivery SET next_reconcile_at=:due WHERE tenant_id=:tenant AND id=:id AND completed_revision<revision",
                              db.args(
                                  "id",
                                  row.get("id"),
                                  "due",
                                  Timestamp.from(clock.instant().plusSeconds(5)))));
                }
              var active =
                  tx.read(
                      () ->
                          db.rows(
                              "SELECT * FROM resource.catalog_distribution WHERE tenant_id=:tenant AND lifecycle IN ('active','pending') AND next_reconcile_at<=:now ORDER BY next_reconcile_at,id LIMIT 100",
                              db.args("now", Timestamp.from(clock.instant()))));
              var instances = new HashSet<UUID>();
              for (var queued : active) {
                if (!instances.add((UUID) queued.get("instance_id"))) continue;
                try {
                  tx.write(
                      () -> {
                        UUID instance = (UUID) queued.get("instance_id");
                        db.lock("catalog-distribution/" + instance);
                        var snapshot = workspace().snapshot(tenant, instance);
                        synchronize(snapshot);
                        var current =
                            db.rows(
                                "SELECT * FROM resource.catalog_distribution WHERE tenant_id=:tenant AND instance_id=:instance AND selection_revision=:selection",
                                db.args(
                                    "instance",
                                    instance,
                                    "selection",
                                    snapshot.selectionRevision()));
                        if (current.isEmpty()) {
                          defer(instance);
                          return false;
                        }
                        var row = current.getFirst();
                        if (snapshot.selectionRevision() != number(row, "selection_revision")
                            || !snapshot.owner().equals(row.get("owner_id"))
                            || !Objects.equals(snapshot.node(), row.get("node_id"))) return false;
                        if (collectTombstones(row, snapshot)) {
                          defer(instance);
                          return true;
                        }
                        var request =
                            ResolveUnifiedResourcesRequest.newBuilder()
                                .setTenantId(tenant.toString())
                                .setOwnerId(row.get("owner_id").toString())
                                .setInstanceId(instance.toString())
                                .setNodeId(row.get("node_id").toString())
                                .setSelectionRevision(number(row, "selection_revision"));
                        json(row, "document")
                            .path("selections")
                            .forEach(
                                selection ->
                                    request.addSelections(CatalogProtocol.selection(selection)));
                        var resolution = resolve(request.build(), snapshot);
                        if (resolution.getResourcesCount() != snapshot.resources().size()
                            || !new HashSet<>(resolution.getResourcesList())
                                .equals(new HashSet<>(snapshot.resources()))) {
                          workspace()
                              .refresh(
                                  tenant,
                                  instance,
                                  snapshot.owner(),
                                  snapshot.selectionRevision(),
                                  resolution);
                          defer(instance);
                        } else schedule(row, snapshot, resolution);
                        return true;
                      });
                } catch (RuntimeException e) {
                  retry();
                  tx.write(
                      () -> {
                        db.lock("catalog-distribution/" + queued.get("instance_id"));
                        defer((UUID) queued.get("instance_id"));
                        return true;
                      });
                }
              }
            });
      } catch (RuntimeException e) {
        retry();
      }
  }

  private void defer(UUID instance) {
    db.update(
        "UPDATE resource.catalog_distribution SET next_reconcile_at=:due WHERE tenant_id=:tenant AND instance_id=:instance AND lifecycle IN ('active','pending')",
        db.args("instance", instance, "due", Timestamp.from(clock.instant().plusSeconds(5))));
  }

  private void schedule(
      Map<String, Object> row,
      UnifiedResourceWorkspace.Snapshot snapshot,
      ResolveUnifiedResourcesResponse resolution) {
    Instant due = null;
    for (var resource : resolution.getResourcesList()) {
      if (CatalogStatePolicy.desired(resource).equals("ENABLED")) {
        Instant lease = Instant.parse(resource.getLease().getExpiresAt()).minusSeconds(300);
        if (due == null || lease.isBefore(due)) due = lease;
      }
      if (CatalogStatePolicy.desired(resource).equals("REMOVED")
          || snapshot.reports().stream()
              .noneMatch(
                  report ->
                      CatalogStatePolicy.acknowledged(
                          resource,
                          snapshot.selectionRevision(),
                          snapshot.generation(),
                          report,
                          "node"))) due = clock.instant().plusSeconds(5);
    }
    if (!snapshot.ready() || snapshot.generation() != snapshot.observedGeneration())
      due = clock.instant().plusSeconds(5);
    if (due != null && !due.isAfter(clock.instant())) due = clock.instant().plusSeconds(5);
    db.update(
        "UPDATE resource.catalog_distribution SET next_reconcile_at=:due WHERE tenant_id=:tenant AND id=:id",
        db.args("id", row.get("id"), "due", due == null ? null : Timestamp.from(due)));
    db.update(
        "UPDATE resource.catalog_distribution SET next_reconcile_at=:due WHERE tenant_id=:tenant AND instance_id=:instance AND lifecycle='pending'",
        db.args(
            "instance",
            row.get("instance_id"),
            "due",
            Timestamp.from(clock.instant().plusSeconds(5))));
  }

  private static void retry() {
    org.slf4j.LoggerFactory.getLogger(CatalogDelivery.class)
        .warn("Catalog reconciliation will retry");
  }
}
