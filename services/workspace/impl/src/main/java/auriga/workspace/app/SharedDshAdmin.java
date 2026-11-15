package auriga.workspace.app;

import static auriga.workspace.domain.SharedDshPolicy.*;

import auriga.common.error.*;
import auriga.common.persistence.UuidV7;
import auriga.workspace.api.SharedDshAuthority.Inputs;
import auriga.workspace.persistence.WorkspaceStore;
import java.time.Clock;
import java.util.*;
import java.util.function.Supplier;

/** Browser administration adapter; execution remains in the existing receipt pipeline. */
public final class SharedDshAdmin {
  public record Result(Map<String, Object> body, String etag) {}

  public record Member(UUID memberId, UUID legacyInstanceId, String targetMode) {}

  public record PlanRequest(List<Member> members, String reason) {}

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

  private void validatePlan(PlanRequest request) {
    valid(
        request != null
            && request.reason() != null
            && !request.reason().isBlank()
            && request.reason().length() <= 512
            && request.members() != null
            && !request.members().isEmpty()
            && request.members().size() <= 1000);
    var members = new HashSet<UUID>();
    var instances = new HashSet<UUID>();
    for (var m : request.members()) {
      valid(
          m != null
              && m.memberId() != null
              && m.legacyInstanceId() != null
              && m.targetMode() != null
              && Set.of("shared", "dedicated").contains(m.targetMode()));
      conflict(members.add(m.memberId()) && instances.add(m.legacyInstanceId()));
      inputs.requireMember(store.tenant(), m.memberId());
      var legacy = store.instance(m.legacyInstanceId());
      if (!legacy.get("owner_id").equals(m.memberId()))
        throw new AurigaException(CommonErrors.NOT_FOUND);
      conflict(
          store.jdbc.queryForObject(
                  "SELECT count(*) FROM instance WHERE tenant_id=? AND owner_id=? AND NOT deleted",
                  Long.class,
                  store.tenant(),
                  m.memberId())
              == 1);
      conflict(placement(m.memberId()).isEmpty());
      entitled(m.memberId(), m.targetMode());
    }
  }

  private Map<String, Object> planRow(UUID plan) {
    return store.one(
        "SELECT * FROM shared_dsh_rebuild_plan WHERE tenant_id=? AND id=?", store.tenant(), plan);
  }

  private PlanRequest request(Map<String, Object> row) {
    return WorkspaceService.JSON.readValue(row.get("request").toString(), PlanRequest.class);
  }

  private Result planView(UUID id) {
    var row = planRow(id);
    var r = request(row);
    String state =
        Boolean.TRUE.equals(row.get("started"))
            ? progressView(id).body().get("state").toString()
            : "draft";
    var body =
        Map.<String, Object>of(
            "planId",
            id,
            "revision",
            row.get("revision"),
            "state",
            state,
            "members",
            r.members(),
            "reason",
            r.reason());
    return new Result(body, tag(List.of(store.tenant(), body)));
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
          validatePlan(request);
          UUID plan = id == null ? UuidV7.next() : id;
          if (id == null)
            store.jdbc.update(
                "INSERT INTO shared_dsh_rebuild_plan(tenant_id,id,request) VALUES(?,?,?)",
                store.tenant(),
                plan,
                WorkspaceService.JSON.writeValueAsString(request));
          else
            store.jdbc.update(
                "UPDATE shared_dsh_rebuild_plan SET request=?,revision=revision+1 WHERE tenant_id=? AND id=?",
                WorkspaceService.JSON.writeValueAsString(request),
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
          validatePlan(r);
          maintenance.plan(
              actor,
              session,
              id,
              now(),
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
    for (var m : request(row).members()) {
      var found =
          store.jdbc.queryForList(
              "SELECT o.* FROM shared_dsh_maintenance m LEFT JOIN shared_dsh_operation o ON o.tenant_id=m.tenant_id AND o.id=m.operation_id WHERE m.tenant_id=? AND m.batch_id=? AND m.member_id=?",
              store.tenant(),
              id,
              m.memberId());
      var p = placement(m.memberId());
      var v = new LinkedHashMap<String, Object>();
      v.put("memberId", m.memberId());
      v.put("revision", p.isEmpty() ? 1L : p.getFirst().get("revision"));
      var op = found.isEmpty() ? Map.<String, Object>of() : found.getFirst();
      String state = Objects.toString(op.get("state"), "blocked");
      v.put("state", state);
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
