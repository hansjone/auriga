package auriga.workspace.app;

import static auriga.workspace.domain.SharedDshPolicy.*;

import auriga.common.error.*;
import auriga.common.persistence.UuidV7;
import auriga.workspace.api.SharedDshAuthority.Inputs;
import auriga.workspace.api.WorkspaceInputs;
import auriga.workspace.persistence.WorkspaceStore;
import java.time.Clock;
import java.util.*;
import java.util.function.Supplier;

/** Browser administration adapter; execution remains in the existing receipt pipeline. */
public final class SharedDshAdmin {
  public record Result(Map<String, Object> body, String etag) {}

  public record Member(UUID memberId, UUID legacyInstanceId, String targetMode) {}

  @com.fasterxml.jackson.annotation.JsonInclude(
      com.fasterxml.jackson.annotation.JsonInclude.Include.NON_NULL)
  public record PlanRequest(List<Member> members, String reason, Long exportUntil) {
    public PlanRequest(List<Member> members, String reason) {
      this(members, reason, null);
    }
  }

  public record PreflightRequest(List<Member> members) {}

  public record Legacy(
      UUID instanceId, String name, String phase, boolean ready, boolean sharedRebuildHold) {}

  private record Inventory(
      List<Legacy> instances, String mode, boolean dedicated, boolean maintenance) {
    String eligibility() {
      if (mode != null) return "already_migrated";
      if (maintenance || instances.stream().anyMatch(Legacy::sharedRebuildHold))
        return "rebuild_hold";
      if (instances.isEmpty()) return "no_legacy_instance";
      return instances.size() == 1 ? "eligible" : "multiple_legacy_instances";
    }
  }

  private WorkspaceInputs directory;

  @org.springframework.beans.factory.annotation.Autowired
  public void memberDirectory(WorkspaceInputs directory) {
    this.directory = directory;
  }

  private final WorkspaceStore store;
  private final SharedDshPlacements placements;
  private final SharedDshMaintenance maintenance;
  private final Inputs inputs;
  private final Clock clock;

  public SharedDshAdmin(
      WorkspaceStore store,
      SharedDshPlacements placements,
      SharedDshMaintenance maintenance,
      Inputs inputs,
      Clock clock) {
    this.store = store;
    this.placements = placements;
    this.maintenance = maintenance;
    this.inputs = inputs;
    this.clock = clock;
  }

  private long now() {
    return clock.instant().getEpochSecond();
  }

  private <T> T locked(Supplier<T> work) {
    try {
      return store.tx(
          () -> {
            store.lock();
            return work.get();
          });
    } catch (IllegalStateException unavailable) {
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    }
  }

  private static long number(Map<String, Object> row, String key) {
    return ((Number) row.get(key)).longValue();
  }

  private static String tag(Object value) {
    return "\""
        + HexFormat.of()
            .formatHex(
                auriga.workspace.domain.WorkspaceCrypto.hash(
                    WorkspaceService.JSON.writeValueAsBytes(
                        canonical(WorkspaceService.JSON.convertValue(value, Object.class)))))
        + "\"";
  }

  private static Object canonical(Object value) {
    if (value instanceof Map<?, ?> map) {
      var sorted = new TreeMap<String, Object>();
      map.forEach((k, v) -> sorted.put(k.toString(), canonical(v)));
      return sorted;
    }
    if (value instanceof Collection<?> list)
      return list.stream().map(SharedDshAdmin::canonical).toList();
    return value;
  }

  private Map<String, Object> switchBody(UUID id) {
    Map<String, Object> result =
        WorkspaceService.JSON.convertValue(placements.switchView(id), Map.class);
    result.values().removeIf(Objects::isNull);
    return result;
  }

  private static void match(String supplied, String actual) {
    if (supplied == null)
      throw new AurigaException(
          ErrorCode.of("common.validation_failed", 428, io.grpc.Status.Code.FAILED_PRECONDITION));
    if (!actual.equals(supplied)) throw new AurigaException(CommonErrors.PRECONDITION_FAILED);
  }

  private Map<String, Object> entitlement(UUID member) {
    store.jdbc.update(
        "INSERT INTO shared_dsh_entitlement(tenant_id,member_id) VALUES(?,?) ON CONFLICT DO NOTHING",
        store.tenant(),
        member);
    var r =
        store.one(
            "SELECT * FROM shared_dsh_entitlement WHERE tenant_id=? AND member_id=?",
            store.tenant(),
            member);
    return Map.of("memberId", member, "granted", r.get("granted"), "revision", r.get("revision"));
  }

  private List<Map<String, Object>> placement(UUID member) {
    return store.jdbc.queryForList(
        "SELECT * FROM shared_dsh_placement WHERE tenant_id=? AND member_id=?",
        store.tenant(),
        member);
  }

  private Result memberView(UUID member) {
    inputs.requireMember(store.tenant(), member);
    var body = new LinkedHashMap<String, Object>();
    body.put("memberId", member);
    body.put("entitlement", entitlement(member));
    var rows = placement(member);
    if (!rows.isEmpty()) {
      var r = rows.getFirst();
      body.put(
          "placement",
          Map.of(
              "memberId",
              member,
              "placementId",
              r.get("id"),
              "mode",
              r.get("mode"),
              "epoch",
              r.get("epoch"),
              "revision",
              r.get("revision"),
              "state",
              r.get("state")));
    }
    return new Result(body, tag(List.of(store.tenant(), body)));
  }

  public Result member(UUID member) {
    return locked(() -> memberView(member));
  }

  private Result mutate(
      UUID actor,
      UUID session,
      String kind,
      String target,
      String key,
      Object request,
      String etag,
      Supplier<Result> work) {
    valid(key != null && !key.isBlank() && key.length() <= 128);
    return locked(
        () -> {
          inputs.requirePlacementWrite(store.tenant(), actor, session, now());
          String fingerprint = tag(Arrays.asList(request, etag));
          var previous =
              store.jdbc.queryForList(
                  "SELECT * FROM shared_dsh_admin_request WHERE tenant_id=? AND actor_id=? AND kind=? AND target=? AND idempotency_key=?",
                  store.tenant(),
                  actor,
                  kind,
                  target,
                  key);
          if (!previous.isEmpty()) {
            var r = previous.getFirst();
            conflict(fingerprint.equals(r.get("fingerprint")));
            return new Result(
                WorkspaceService.JSON.readValue(r.get("response").toString(), Map.class),
                r.get("etag").toString());
          }
          var result = work.get();
          store.jdbc.update(
              "INSERT INTO shared_dsh_admin_request(tenant_id,actor_id,kind,target,idempotency_key,fingerprint,response,etag,created_at) VALUES(?,?,?,?,?,?,?,?,?)",
              store.tenant(),
              actor,
              kind,
              target,
              key,
              fingerprint,
              WorkspaceService.JSON.writeValueAsString(result.body()),
              result.etag(),
              now());
          return result;
        });
  }

  public Result grant(
      UUID actor,
      UUID session,
      UUID member,
      boolean granted,
      String reason,
      String key,
      String etag) {
    valid(reason != null && !reason.isBlank() && reason.length() <= 512);
    return mutate(
        actor,
        session,
        "entitlement",
        member.toString(),
        key,
        List.of(granted, reason),
        etag,
        () -> {
          match(etag, memberView(member).etag());
          if (!granted) {
            var rows = placement(member);
            conflict(rows.isEmpty() || !rows.getFirst().get("mode").equals("dedicated"));
            conflict(
                store.jdbc.queryForObject(
                        "SELECT count(*) FROM shared_dsh_operation o JOIN shared_dsh_placement p ON p.tenant_id=o.tenant_id AND p.id=o.placement_id WHERE p.tenant_id=? AND p.member_id=? AND (o.state NOT IN ('succeeded','failed') OR o.recovery_pending)",
                        Long.class,
                        store.tenant(),
                        member)
                    == 0);
          }
          store.jdbc.update(
              "UPDATE shared_dsh_entitlement SET granted=?,revision=revision+1 WHERE tenant_id=? AND member_id=?",
              granted,
              store.tenant(),
              member);
          var view = memberView(member);
          return new Result((Map<String, Object>) view.body().get("entitlement"), view.etag());
        });
  }

  private void entitled(UUID member, String target) {
    if (target.equals("dedicated") && !Boolean.TRUE.equals(entitlement(member).get("granted")))
      throw new AurigaException(CommonErrors.FORBIDDEN);
  }

  public Result switchMode(
      UUID actor,
      UUID session,
      UUID member,
      String target,
      String reason,
      String key,
      String etag) {
    return mutate(
        actor,
        session,
        "create-switch",
        member.toString(),
        key,
        List.of(target, reason),
        etag,
        () -> {
          match(etag, memberView(member).etag());
          entitled(member, target);
          var rows = placement(member);
          if (rows.isEmpty()) throw new AurigaException(CommonErrors.NOT_FOUND);
          UUID id =
              placements.switchMode(
                  actor,
                  session,
                  member,
                  target,
                  number(rows.getFirst(), "revision"),
                  "http:" + UuidV7.next(),
                  reason);
          return new Result(switchBody(id), memberView(member).etag());
        });
  }

  public Result operation(UUID id) {
    return locked(
        () -> {
          var body = switchBody(id);
          return new Result(body, tag(body));
        });
  }

  public Result lookup(UUID actor, UUID member, String key) {
    valid(key != null && !key.isBlank() && key.length() <= 128);
    return locked(
        () -> {
          inputs.requireMember(store.tenant(), member);
          var rows =
              store.jdbc.queryForList(
                  "SELECT * FROM shared_dsh_admin_request WHERE tenant_id=? AND actor_id=? AND kind='create-switch' AND target=? AND idempotency_key=? AND created_at>?",
                  store.tenant(),
                  actor,
                  member.toString(),
                  key,
                  now() - 86400);
          if (rows.isEmpty()) throw new AurigaException(CommonErrors.NOT_FOUND);
          var r = rows.getFirst();
          var original = WorkspaceService.JSON.readValue(r.get("response").toString(), Map.class);
          UUID id = UUID.fromString(original.get("operationId").toString());
          return new Result(
              WorkspaceService.JSON.convertValue(placements.switchView(id), Map.class),
              r.get("etag").toString());
        });
  }

  // Scope every join to the tenant. Group before paging so one owner with several instances
  // stays on one page. Include maintenance reservations created by other administration paths.
  private Map<UUID, Inventory> inventory(UUID owner) {
    String sql =
        """
        SELECT i.owner_id,i.id,i.name,i.phase,i.ready,i.shared_rebuild_hold,
               p.mode,coalesce(e.granted,false) AS dedicated,(m.member_id IS NOT NULL) AS maintenance
          FROM instance i
          LEFT JOIN shared_dsh_placement p ON (p.tenant_id,p.member_id)=(i.tenant_id,i.owner_id)
          LEFT JOIN shared_dsh_entitlement e ON (e.tenant_id,e.member_id)=(i.tenant_id,i.owner_id)
          LEFT JOIN shared_dsh_maintenance m ON (m.tenant_id,m.member_id)=(i.tenant_id,i.owner_id)
         WHERE i.tenant_id=? AND NOT i.deleted
        """;
    Object[] args =
        owner == null ? new Object[] {store.tenant()} : new Object[] {store.tenant(), owner};
    var rows =
        store.jdbc.queryForList(
            sql + (owner == null ? "" : " AND i.owner_id=?") + " ORDER BY i.owner_id,i.id", args);
    var result = new LinkedHashMap<UUID, Inventory>();
    for (var row : rows) {
      var entry =
          result.computeIfAbsent(
              (UUID) row.get("owner_id"),
              ignored ->
                  new Inventory(
                      new ArrayList<>(),
                      (String) row.get("mode"),
                      Boolean.TRUE.equals(row.get("dedicated")),
                      Boolean.TRUE.equals(row.get("maintenance"))));
      entry
          .instances()
          .add(
              new Legacy(
                  (UUID) row.get("id"),
                  (String) row.get("name"),
                  (String) row.get("phase"),
                  Boolean.TRUE.equals(row.get("ready")),
                  Boolean.TRUE.equals(row.get("shared_rebuild_hold"))));
    }
    return result;
  }

  private Map<String, Object> identity(UUID owner) {
    var fields = new LinkedHashMap<String, Object>();
    if (directory == null) return fields;
    try {
      var profile = directory.member(store.tenant(), owner);
      if (profile != null
          && owner.toString().equals(profile.getAccountId())
          && store.tenant().toString().equals(profile.getTenantId())) {
        fields.put("displayName", profile.getDisplayName());
        fields.put("email", profile.getEmail());
        fields.put(
            "memberType",
            profile.getMemberType().name().replace("MEMBER_TYPE_", "").toLowerCase(Locale.ROOT));
        fields.put(
            "memberStatus",
            profile.getStatus().name().replace("MEMBER_STATUS_", "").toLowerCase(Locale.ROOT));
      }
    } catch (RuntimeException unavailable) {
      // Presentation is best effort. All mutations still require the live member authority.
    }
    return fields;
  }

  private static String reason(String code) {
    return switch (code) {
      case "eligible" -> "可迁移";
      case "already_migrated" -> "成员已有分配";
      case "multiple_legacy_instances" -> "存在多个旧实例，请先核对归属";
      case "rebuild_hold" -> "旧实例已在维护中";
      default -> "没有可迁移的旧实例";
    };
  }

  public Result candidates(String q, int limit, int offset, UUID owner) {
    valid(limit > 0 && limit <= 200 && offset >= 0 && (q == null || q.length() <= 256));
    String needle = Objects.toString(q, "").strip().toLowerCase(Locale.ROOT);
    var inventory = store.tx(() -> inventory(owner));
    var items = new ArrayList<Map<String, Object>>();
    int total = 0;
    for (var entry : inventory.entrySet()) {
      var member = entry.getKey();
      var inv = entry.getValue();
      if (needle.isEmpty() && (total < offset || items.size() == limit)) {
        total++;
        continue;
      }
      var body = identity(member);
      if (!needle.isEmpty()
          && !member.toString().contains(needle)
          && !List.of("displayName", "email").stream()
              .anyMatch(
                  k -> Objects.toString(body.get(k), "").toLowerCase(Locale.ROOT).contains(needle))
          && inv.instances().stream()
              .noneMatch(
                  i ->
                      i.name().toLowerCase(Locale.ROOT).contains(needle)
                          || i.instanceId().toString().contains(needle))) continue;
      if (++total <= offset || items.size() == limit) continue;
      body.put("memberId", member);
      body.put("dedicatedEntitled", inv.dedicated());
      body.put("hasPlacement", inv.mode() != null);
      body.put("currentMode", Objects.toString(inv.mode(), "legacy"));
      body.put("legacyInstances", inv.instances());
      body.put("eligibility", inv.eligibility());
      body.put("eligibilityReason", reason(inv.eligibility()));
      items.add(body);
    }
    return result(Map.of("items", items, "total", total));
  }

  private Result result(Map<String, Object> body) {
    return new Result(body, tag(List.of(store.tenant(), body)));
  }

  private static void validateMembers(List<Member> list) {
    valid(list != null && !list.isEmpty() && list.size() <= 1000);
    var members = new HashSet<UUID>();
    var instances = new HashSet<UUID>();
    for (var m : list) {
      valid(
          m != null
              && m.memberId() != null
              && m.legacyInstanceId() != null
              && m.targetMode() != null
              && Set.of("shared", "dedicated").contains(m.targetMode()));
      conflict(members.add(m.memberId()) && instances.add(m.legacyInstanceId()));
    }
  }

  private record Check(String issue, ErrorCode error, Legacy legacy, boolean dedicatedEntitled) {}

  private Check check(Member m) {
    try {
      inputs.requireMember(store.tenant(), m.memberId());
    } catch (AurigaException e) {
      if (e.errorCode().equals(CommonErrors.FORBIDDEN)
          || e.errorCode().equals(CommonErrors.NOT_FOUND))
        return new Check("成员不存在或当前不可迁移", e.errorCode(), null, false);
      throw e;
    } catch (IllegalStateException unavailable) {
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    }
    var inv = inventory(m.memberId()).get(m.memberId());
    if (inv == null
        || inv.instances().stream().noneMatch(i -> i.instanceId().equals(m.legacyInstanceId())))
      return new Check("旧实例已删除或归属已变更", CommonErrors.NOT_FOUND, null, false);
    if (!inv.eligibility().equals("eligible"))
      return new Check(reason(inv.eligibility()), CommonErrors.CONFLICT, null, inv.dedicated());
    if (m.targetMode().equals("dedicated") && !inv.dedicated())
      return new Check("请先授予独享授权", CommonErrors.FORBIDDEN, null, false);
    return new Check(reason("eligible"), null, inv.instances().getFirst(), inv.dedicated());
  }

  public Result preflight(PreflightRequest request) {
    valid(request != null);
    validateMembers(request.members());
    return store.tx(
        () -> {
          var items = new ArrayList<Map<String, Object>>();
          for (var m : request.members()) {
            var check = check(m);
            items.add(
                Map.of(
                    "memberId",
                    m.memberId(),
                    "eligible",
                    check.error() == null,
                    "reason",
                    check.issue(),
                    "dedicatedEntitled",
                    check.dedicatedEntitled()));
          }
          return result(Map.of("items", items, "capacityStatus", "unknown"));
        });
  }

  private List<Map<String, Object>> validatePlan(PlanRequest request, boolean snapshotNames) {
    valid(
        request != null
            && request.reason() != null
            && !request.reason().isBlank()
            && request.reason().length() <= 512
            && (request.exportUntil() == null
                || (request.exportUntil() >= 0 && request.exportUntil() <= 9007199254740991L)));
    validateMembers(request.members());
    var labels = new ArrayList<Map<String, Object>>();
    for (var m : request.members()) {
      store.instance(m.legacyInstanceId()); // Keep ownership locked until save/start commits.
      var check = check(m);
      if (check.error() != null) throw new AurigaException(check.error());
      if (snapshotNames) {
        var label = new LinkedHashMap<String, Object>();
        label.put("legacyInstanceName", check.legacy().name());
        var name = identity(m.memberId()).get("displayName");
        if (name != null) label.put("displayName", name);
        labels.add(label);
      }
    }
    return labels;
  }

  private record Stored(
      List<Member> members, String reason, Long exportUntil, List<Map<String, Object>> labels) {
    PlanRequest request() {
      return new PlanRequest(members, reason, exportUntil);
    }

    Map<String, Object> labels(int index) {
      return labels == null ? Map.of() : labels.get(index);
    }
  }

  private Map<String, Object> planRow(UUID plan) {
    return store.one(
        "SELECT * FROM shared_dsh_rebuild_plan WHERE tenant_id=? AND id=?", store.tenant(), plan);
  }

  private Stored request(Map<String, Object> row) {
    return WorkspaceService.JSON.readValue(row.get("request").toString(), Stored.class);
  }

  private Result planView(UUID id) {
    var row = planRow(id);
    var r = request(row);
    var body = summary(row, r);
    body.remove("memberCount");
    body.remove("started");
    var members = new ArrayList<Map<String, Object>>();
    for (var m : r.members()) {
      var item = new LinkedHashMap<>(r.labels(members.size()));
      item.put("memberId", m.memberId());
      item.put("legacyInstanceId", m.legacyInstanceId());
      item.put("targetMode", m.targetMode());
      members.add(item);
    }
    body.put("members", members);
    return result(body);
  }

  private Map<String, Object> summary(Map<String, Object> row, Stored r) {
    var body = new LinkedHashMap<String, Object>();
    var id = (UUID) row.get("id");
    boolean started = Boolean.TRUE.equals(row.get("started"));
    body.put("planId", id);
    body.put("revision", row.get("revision"));
    body.put("reason", r.reason());
    if (r.exportUntil() != null) body.put("exportUntil", r.exportUntil());
    body.put("started", started);
    body.put("memberCount", r.members().size());
    String state = "draft";
    if (started) {
      var counts =
          store.one(
              """
          SELECT count(*) FILTER (WHERE o.state='succeeded') AS done,
                 count(*) FILTER (WHERE o.state IN ('failed','blocked') OR o.id IS NULL) AS blocked
            FROM shared_dsh_maintenance m LEFT JOIN shared_dsh_operation o
              ON o.tenant_id=m.tenant_id AND o.id=m.operation_id
           WHERE m.tenant_id=? AND m.batch_id=?
          """,
              store.tenant(),
              id);
      state =
          number(counts, "done") == r.members().size()
              ? "completed"
              : number(counts, "blocked") > 0 ? "blocked" : "running";
    }
    body.put("state", state);
    return body;
  }

  public Result plans() {
    return store.tx(
        () ->
            result(
                Map.of(
                    "items",
                    store
                        .jdbc
                        .queryForList(
                            "SELECT * FROM shared_dsh_rebuild_plan WHERE tenant_id=? ORDER BY id DESC",
                            store.tenant())
                        .stream()
                        .map(row -> summary(row, request(row)))
                        .toList())));
  }

  public Result plan(UUID id) {
    return locked(() -> planView(id));
  }

  public Result save(
      UUID actor, UUID session, UUID id, PlanRequest request, String key, String etag) {
    return mutate(
        actor,
        session,
        id == null ? "create-plan" : "update-plan",
        id == null ? "new" : id.toString(),
        key,
        request,
        etag,
        () -> {
          if (id != null) {
            match(etag, planView(id).etag());
            conflict(!Boolean.TRUE.equals(planRow(id).get("started")));
          }
          var labels = validatePlan(request, true);
          String stored =
              WorkspaceService.JSON.writeValueAsString(
                  new Stored(request.members(), request.reason(), request.exportUntil(), labels));
          UUID plan = id == null ? UuidV7.next() : id;
          if (id == null)
            store.jdbc.update(
                "INSERT INTO shared_dsh_rebuild_plan(tenant_id,id,request) VALUES(?,?,?)",
                store.tenant(),
                plan,
                stored);
          else
            store.jdbc.update(
                "UPDATE shared_dsh_rebuild_plan SET request=?,revision=revision+1 WHERE tenant_id=? AND id=?",
                stored,
                store.tenant(),
                id);
          return planView(plan);
        });
  }

  public Result start(UUID actor, UUID session, UUID id, String key, String etag) {
    return mutate(
        actor,
        session,
        "start-plan",
        id.toString(),
        key,
        "confirmed:discard:archive-and-reset",
        etag,
        () -> {
          match(etag, planView(id).etag());
          var row = planRow(id);
          conflict(!Boolean.TRUE.equals(row.get("started")));
          var r = request(row);
          validatePlan(r.request(), false);
          maintenance.planForStart(
              actor,
              session,
              id,
              r.exportUntil() == null ? now() : r.exportUntil(),
              r.members().stream()
                  .map(
                      m ->
                          new SharedDshMaintenance.Choice(
                              m.memberId(),
                              m.legacyInstanceId(),
                              m.targetMode().equals("dedicated")))
                  .toList());
          var started = maintenance.start(actor, session, id);
          if (started.stream().anyMatch(m -> m.operation() == null))
            throw new AurigaException(CommonErrors.UNAVAILABLE);
          store.jdbc.update(
              "UPDATE shared_dsh_rebuild_plan SET started=true,revision=revision+1 WHERE tenant_id=? AND id=?",
              store.tenant(),
              id);
          return progressView(id);
        });
  }

  public Result progress(UUID id) {
    return locked(() -> progressView(id));
  }

  private Result progressView(UUID id) {
    var row = planRow(id);
    conflict(Boolean.TRUE.equals(row.get("started")));
    var members = new ArrayList<Map<String, Object>>();
    var stored = request(row);
    for (var m : stored.members()) {
      var found =
          store.jdbc.queryForList(
              "SELECT o.* FROM shared_dsh_maintenance m LEFT JOIN shared_dsh_operation o ON o.tenant_id=m.tenant_id AND o.id=m.operation_id WHERE m.tenant_id=? AND m.batch_id=? AND m.member_id=?",
              store.tenant(),
              id,
              m.memberId());
      var p = placement(m.memberId());
      var v = new LinkedHashMap<String, Object>(stored.labels(members.size()));
      v.put("memberId", m.memberId());
      v.put("revision", p.isEmpty() ? 1L : p.getFirst().get("revision"));
      var op = found.isEmpty() ? Map.<String, Object>of() : found.getFirst();
      String state = Objects.toString(op.get("state"), "blocked");
      v.put("state", state);
      if (op.get("action") != null) v.put("action", op.get("action"));
      v.put("sessionsDiscarded", Boolean.TRUE.equals(op.get("sessions_discarded")));
      v.put("workspaceEmpty", state.equals("succeeded"));
      if (op.get("id") != null) v.put("operationId", op.get("id"));
      if (Set.of("failed", "blocked").contains(state))
        v.put("failureCode", Objects.toString(op.get("failure_code"), "recovery-required"));
      members.add(v);
    }
    String state =
        members.stream().allMatch(m -> m.get("state").equals("succeeded"))
            ? "completed"
            : members.stream().anyMatch(m -> Set.of("failed", "blocked").contains(m.get("state")))
                ? "blocked"
                : "running";
    var body =
        Map.<String, Object>of(
            "planId", id, "revision", row.get("revision"), "state", state, "members", members);
    return new Result(body, tag(List.of(store.tenant(), body)));
  }
}
