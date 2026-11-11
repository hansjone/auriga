package auriga.resource.app;

import static auriga.resource.persistence.ResourceWorkflowStore.json;
import static auriga.resource.persistence.ResourceWorkflowStore.number;
import static auriga.resource.web.ResourceBody.*;

import auriga.common.error.*;
import auriga.common.security.AurigaPrincipal;
import auriga.resource.objectstore.ObjectStore;
import auriga.resource.persistence.*;
import java.io.InputStream;
import java.security.SecureRandom;
import java.time.*;
import java.util.*;
import java.util.function.Supplier;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** Authenticated desktop selections and member reports are separate from workspace node reports. */
public final class ResourceMembers {
  private final ResourceStore resources;
  private final ResourceWorkflowStore db;
  private final ResourceAccess access;
  private final ResourceWorkflow workflow;
  private final ResourceDistributions distributions;
  private final Supplier<ObjectStore> objects;
  private final Clock clock;

  public ResourceMembers(
      ResourceStore resources,
      ResourceWorkflowStore db,
      ResourceAccess access,
      ResourceWorkflow workflow,
      ResourceDistributions distributions,
      Supplier<ObjectStore> objects,
      Clock clock) {
    this.resources = resources;
    this.db = db;
    this.access = access;
    this.workflow = workflow;
    this.distributions = distributions;
    this.objects = objects;
    this.clock = clock;
  }

  private UUID client(AurigaPrincipal actor, UUID client) {
    access.member(actor);
    if (actor.deviceId() != null && !actor.deviceId().equals(client.toString()))
      throw new AurigaException(CommonErrors.NOT_FOUND);
    return client;
  }

  private Map<String, Object> args(ResourceAccess.Reader r, UUID client, UUID pkg) {
    return db.args("member", r.account(), "client", client, "pkg", pkg);
  }

  private Map<String, Object> installation(ResourceAccess.Reader r, UUID client, UUID pkg) {
    return db.row(
        "SELECT * FROM resource.resource_member_installation WHERE tenant_id=:tenant AND member_id=:member AND client_id=:client AND package_id=:pkg FOR UPDATE",
        args(r, client, pkg));
  }

  private void lock(ResourceAccess.Reader r, UUID client, UUID pkg) {
    db.lock("member/" + r.account() + "/" + client + "/" + pkg);
  }

  private ObjectNode view(Map<String, Object> row) {
    var result = JSON.createObjectNode();
    for (var pair :
        Map.of(
                "clientId",
                "client_id",
                "packageId",
                "package_id",
                "versionId",
                "version_id",
                "digest",
                "digest",
                "level",
                "level",
                "desired",
                "desired",
                "state",
                "state")
            .entrySet()) result.put(pair.getKey(), row.get(pair.getValue()).toString());
    result.put("revision", number(row, "revision"));
    result.put("releaseSequence", number(row, "release_sequence"));
    result.put("reportSource", "member");
    if (row.get("reported_revision") != null)
      result.put("reportedRevision", number(row, "reported_revision"));
    if (row.get("reported_at") != null)
      result.put(
          "reportedAt", ((java.sql.Timestamp) row.get("reported_at")).toInstant().toString());
    if (row.get("error_code") != null) result.put("errorCode", row.get("error_code").toString());
    return result;
  }

  public JsonNode ticket(AurigaPrincipal actor, ObjectNode body, String key) {
    UUID client = client(actor, uuid(body, "clientId")),
        pkg = uuid(body, "packageId"),
        version = uuid(body, "versionId");
    String digest = text(body, "digest", 71);
    return workflow.operation(
        actor,
        ResourceAccess.READ,
        false,
        "member-ticket/" + client,
        key,
        body,
        null,
        r -> admit(r, pkg, version, digest),
        r -> {
          var envelope = admit(r, pkg, version, digest);
          byte[] entropy = new byte[32];
          new SecureRandom().nextBytes(entropy);
          String ticket = Base64.getUrlEncoder().withoutPadding().encodeToString(entropy);
          Instant expiry = clock.instant().plusSeconds(60);
          db.update(
              "INSERT INTO resource.resource_member_ticket(tenant_id,member_id,client_id,ticket_hash,package_id,version_id,digest,envelope,expires_at) VALUES(:tenant,:member,:client,:hash,:pkg,:version,:digest,CAST(:envelope AS jsonb),:expiry)",
              db.args(
                  "member",
                  r.account(),
                  "client",
                  client,
                  "hash",
                  ResourceWorkflow.hash(ticket),
                  "pkg",
                  pkg,
                  "version",
                  version,
                  "digest",
                  digest,
                  "envelope",
                  envelope.toString(),
                  "expiry",
                  OffsetDateTime.ofInstant(expiry, ZoneOffset.UTC)));
          var result =
              JSON.createObjectNode()
                  .put("ticket", ticket)
                  .put("clientId", client.toString())
                  .put("expiresAt", ResourceSigning.time(expiry));
          result.set("resourceEnvelope", envelope);
          result.set("revocations", distributions.revocations());
          workflow.recorded(r, "resource.member.ticket", version);
          return result;
        });
  }

  private JsonNode admit(ResourceAccess.Reader r, UUID pkg, UUID version, String digest) {
    var envelope = distributions.memberEnvelope(r, pkg, version);
    if (!envelope.path("statement").path("digest").asString().equals(digest))
      throw new AurigaException(CommonErrors.CONFLICT);
    return envelope;
  }

  public InputStream download(AurigaPrincipal actor, ObjectNode body, boolean range) {
    if (range) throw invalid();
    UUID client = client(actor, uuid(body, "clientId"));
    String ticket = text(body, "ticket", 43);
    if (!ticket.matches("[A-Za-z0-9_-]{43}")) throw invalid();
    var reader = access.member(actor);
    var info =
        resources.write(
            () -> {
              var r = access.member(actor);
              var row =
                  db.row(
                      "SELECT * FROM resource.resource_member_ticket WHERE tenant_id=:tenant AND member_id=:member AND client_id=:client AND ticket_hash=:hash FOR UPDATE",
                      db.args(
                          "member",
                          r.account(),
                          "client",
                          client,
                          "hash",
                          ResourceWorkflow.hash(ticket)));
              if (row.get("consumed_at") != null) throw new AurigaException(CommonErrors.CONFLICT);
              if (!((java.sql.Timestamp) row.get("expires_at"))
                  .toInstant()
                  .isAfter(clock.instant())) throw new AurigaException(CommonErrors.NOT_FOUND);
              UUID pkg = (UUID) row.get("package_id"), version = (UUID) row.get("version_id");
              admit(r, pkg, version, row.get("digest").toString());
              if (!clock
                  .instant()
                  .isBefore(Instant.parse(json(row, "envelope").path("notAfter").asString())))
                throw new AurigaException(CommonErrors.NOT_FOUND);
              db.update(
                  "UPDATE resource.resource_member_ticket SET consumed_at=:now WHERE tenant_id=:tenant AND ticket_hash=:hash",
                  db.args(
                      "hash",
                      ResourceWorkflow.hash(ticket),
                      "now",
                      OffsetDateTime.ofInstant(clock.instant(), ZoneOffset.UTC)));
              return new ObjectStore.ObjectInfo(
                  row.get("digest").toString(), number(db.version(pkg, version), "size_bytes"));
            });
    return objects.get().open(reader.tenant(), info);
  }

  public JsonNode select(AurigaPrincipal actor, ObjectNode body, String key, String etag) {
    UUID client = client(actor, uuid(body, "clientId")),
        pkg = uuid(body, "packageId"),
        version = uuid(body, "versionId");
    String action = text(body, "action", 16), digest = text(body, "digest", 71);
    if (!Set.of("install", "enable", "disable", "remove").contains(action)) throw invalid();
    boolean enabling = Set.of("install", "enable").contains(action);
    return workflow.operation(
        actor,
        ResourceAccess.READ,
        false,
        "member-selection/" + client + "/" + pkg,
        key,
        body,
        etag,
        r -> {
          lock(r, client, pkg);
          if (enabling) admit(r, pkg, version, digest);
        },
        r -> {
          lock(r, client, pkg);
          var rows =
              db.rows(
                  "SELECT * FROM resource.resource_member_installation WHERE tenant_id=:tenant AND member_id=:member AND client_id=:client AND package_id=:pkg",
                  args(r, client, pkg));
          var old = rows.isEmpty() ? null : rows.getFirst();
          ResourceWorkflow.match(etag, old == null ? 0 : number(old, "revision"));
          if (!enabling && old == null) throw new AurigaException(CommonErrors.NOT_FOUND);
          if (action.equals("enable")
              && (old == null
                  || old.get("desired").equals("removed")
                  || !version.equals(old.get("version_id"))
                  || !digest.equals(old.get("digest"))))
            throw new AurigaException(CommonErrors.CONFLICT);
          var pack =
              db.row(
                  "SELECT level FROM resource.resource_package WHERE tenant_id=:tenant AND id=:id",
                  db.args("id", pkg));
          String level = pack.get("level").toString();
          if (level.equals("l0")
              || (level.equals("l1") && !enabling)
              || (level.equals("l2") && action.equals("remove")))
            throw new AurigaException(CommonErrors.FORBIDDEN);
          var selected = db.version(pkg, version);
          long sequence;
          if (enabling) {
            var envelope = admit(r, pkg, version, digest);
            sequence = envelope.path("releaseSequence").asLong();
            if (old != null
                && (sequence < number(old, "high_water")
                    || sequence == number(old, "release_sequence")
                        && (!version.equals(old.get("version_id"))
                            || !digest.equals(old.get("digest")))))
              throw new AurigaException(CommonErrors.CONFLICT);
          } else {
            if (old == null
                || old.get("desired").equals("removed")
                || !version.equals(old.get("version_id"))
                || !digest.equals(old.get("digest")))
              throw new AurigaException(CommonErrors.CONFLICT);
            sequence = number(old, "release_sequence");
          }
          if (!digest.equals(selected.get("digest")))
            throw new AurigaException(CommonErrors.CONFLICT);
          long high = old == null ? sequence : Math.max(sequence, number(old, "high_water"));
          String desired = enabling ? "enabled" : action.equals("disable") ? "disabled" : "removed";
          db.update(
              "INSERT INTO resource.resource_member_installation(tenant_id,member_id,client_id,package_id,version_id,digest,release_sequence,high_water,level,desired) VALUES(:tenant,:member,:client,:pkg,:version,:digest,:seq,:high,:level,:desired) ON CONFLICT(tenant_id,member_id,client_id,package_id) DO UPDATE SET version_id=excluded.version_id,digest=excluded.digest,release_sequence=excluded.release_sequence,high_water=excluded.high_water,level=excluded.level,desired=excluded.desired,state='applying',revision=resource.resource_member_installation.revision+1,error_code=NULL,reported_revision=NULL,reported_at=NULL",
              db.args(
                  "member",
                  r.account(),
                  "client",
                  client,
                  "pkg",
                  pkg,
                  "version",
                  version,
                  "digest",
                  digest,
                  "seq",
                  sequence,
                  "high",
                  high,
                  "level",
                  level,
                  "desired",
                  desired));
          workflow.recorded(r, "resource.member." + action, version);
          return view(installation(r, client, pkg));
        });
  }

  public JsonNode report(AurigaPrincipal actor, ObjectNode body, String key) {
    UUID client = client(actor, uuid(body, "clientId")),
        pkg = uuid(body, "packageId"),
        version = uuid(body, "versionId");
    String digest = text(body, "digest", 71), state = text(body, "state", 16);
    long revision = counter(body, "installationRevision");
    if (!Set.of("applied", "failed").contains(state)) throw invalid();
    return workflow.operation(
        actor,
        ResourceAccess.READ,
        false,
        "member-report/" + client + "/" + pkg,
        key,
        body,
        null,
        r -> {},
        r -> {
          lock(r, client, pkg);
          var row = installation(r, client, pkg);
          if (number(row, "revision") != revision
              || !version.equals(row.get("version_id"))
              || !digest.equals(row.get("digest")))
            throw new AurigaException(CommonErrors.CONFLICT);
          if (state.equals("applied") && row.get("desired").equals("enabled"))
            admit(r, pkg, version, digest);
          db.update(
              "UPDATE resource.resource_member_installation SET state=:state,reported_revision=:revision,reported_at=:now,error_code=:error WHERE tenant_id=:tenant AND member_id=:member AND client_id=:client AND package_id=:pkg",
              db.args(
                  "member",
                  r.account(),
                  "client",
                  client,
                  "pkg",
                  pkg,
                  "state",
                  state,
                  "revision",
                  revision,
                  "now",
                  OffsetDateTime.ofInstant(clock.instant(), ZoneOffset.UTC),
                  "error",
                  state.equals("failed") ? "resource.apply_failed" : null));
          workflow.recorded(r, "resource.member.report", version);
          return view(installation(r, client, pkg));
        });
  }

  public JsonNode list(AurigaPrincipal actor, UUID client, UUID after, int limit) {
    if (limit < 1 || limit > 200) throw invalid();
    client(actor, client);
    return resources.write(
        () -> {
          var r = access.member(actor);
          var items = JSON.createArrayNode();
          var rows =
              db.rows(
                  "SELECT * FROM resource.resource_member_installation WHERE tenant_id=:tenant AND member_id=:member AND client_id=:client AND package_id>:after ORDER BY package_id LIMIT :limit",
                  db.args(
                      "member",
                      r.account(),
                      "client",
                      client,
                      "after",
                      after == null ? new UUID(0, 0) : after,
                      "limit",
                      limit + 1));
          for (var row : rows.subList(0, Math.min(limit, rows.size()))) items.add(view(row));
          var result = JSON.createObjectNode();
          result.set("items", items);
          if (rows.size() > limit)
            result.put("nextAfterPackageId", rows.get(limit - 1).get("package_id").toString());
          return result;
        });
  }
}
