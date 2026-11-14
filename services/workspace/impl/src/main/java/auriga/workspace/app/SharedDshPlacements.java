package auriga.workspace.app;

import static auriga.workspace.domain.SharedDshPolicy.*;

import auriga.common.error.*;
import auriga.common.persistence.UuidV7;
import auriga.contracts.node.v1.*;
import auriga.workspace.api.SharedDshAuthority;
import auriga.workspace.persistence.WorkspaceStore;
import java.time.Clock;
import java.util.*;

/**
 * Durable authority. Not auto-enabled: trusted IAM, resource, node and entry adapters are
 * mandatory.
 */
public final class SharedDshPlacements implements SharedDshAuthority {
  /** Probe must use the normal edge page, validate the expected binding, and fail closed. */
  public interface EntryProbe {
    boolean ready(SharedDshBinding binding);
  }

  public record Delivery(UUID id, String kind, byte[] payload) {}

  private final WorkspaceStore store;
  private final Inputs inputs;
  private final EntryProbe entry;
  private final Clock clock;

  public SharedDshPlacements(WorkspaceStore store, Inputs inputs, EntryProbe entry, Clock clock) {
    this.store = store;
    this.inputs = inputs;
    this.entry = entry;
    this.clock = clock;
  }

  private long now() {
    return clock.instant().getEpochSecond();
  }

  private <T> T locked(java.util.function.Supplier<T> work) {
    return store.tx(
        () -> {
          store.lock();
          return work.get();
        });
  }

  private UUID tenant() {
    return store.tenant();
  }

  private Map<String, Object> member(UUID member) {
    return store.one(
        "SELECT * FROM shared_dsh_placement WHERE tenant_id=? AND member_id=? FOR UPDATE",
        tenant(),
        member);
  }

  private Map<String, Object> operation(UUID id) {
    return store.one(
        "SELECT * FROM shared_dsh_operation WHERE tenant_id=? AND id=? FOR UPDATE", tenant(), id);
  }

  private static long number(Map<String, Object> row, String key) {
    return ((Number) row.get(key)).longValue();
  }

  public static SharedDshBinding binding(Map<String, Object> row) {
    return SharedDshBinding.newBuilder()
        .setTenantId(row.get("tenant_id").toString())
        .setMemberId(row.get("member_id").toString())
        .setPlacementId(row.get("id").toString())
        .setPhysicalInstanceId(row.get("pool_id").toString())
        .setMode(mode(row.get("mode").toString()))
        .setEpoch(number(row, "epoch"))
        .setRevision(number(row, "revision"))
        .build();
  }

  private void emit(String kind, com.google.protobuf.MessageLite payload) {
    store.jdbc.update(
        "INSERT INTO shared_dsh_outbox(id,tenant_id,kind,payload,created_at) SELECT ?,?,?,?,? WHERE NOT EXISTS (SELECT 1 FROM shared_dsh_outbox WHERE tenant_id=? AND kind=? AND payload=? AND NOT delivered)",
        UuidV7.next(),
        tenant(),
        kind,
        payload.toByteArray(),
        now(),
        tenant(),
        kind,
        payload.toByteArray());
  }

  private void requireAuthority(Map<String, Object> row, UUID session) {
    UUID member = (UUID) row.get("member_id");
    inputs.requireMember(tenant(), member);
    inputs.requireSession(tenant(), member, session);
    var pool =
        store.one(
            "SELECT * FROM shared_dsh_pool WHERE tenant_id=? AND id=?",
            tenant(),
            row.get("pool_id"));
    conflict(
        pool.get("authorization_digest").equals(inputs.authorizationSetDigest(tenant(), member)));
    conflict(row.get("state").equals("ready") && pool.get("state").equals("ready"));
  }

  private UUID reserve(UUID member, String mode) {
    String digest = inputs.authorizationSetDigest(tenant(), member);
    valid(digest != null && digest.matches("sha256:[a-f0-9]{64}"));
    if (mode.equals("shared")) {
      var candidates =
          store.jdbc.queryForList(
              "SELECT p.id FROM shared_dsh_pool p WHERE p.tenant_id=? AND p.mode='shared' AND p.authorization_digest=? AND p.state<>'blocked' AND p.memory_percent<80 AND (SELECT count(*) FROM shared_dsh_active a WHERE a.tenant_id=p.tenant_id AND a.pool_id=p.id)<4 AND (SELECT count(*) FROM shared_dsh_placement x WHERE x.tenant_id=p.tenant_id AND x.pool_id=p.id)+(SELECT count(*) FROM shared_dsh_operation o WHERE o.tenant_id=p.tenant_id AND o.target_pool=p.id AND o.state NOT IN ('succeeded','failed'))<20 ORDER BY p.id LIMIT 1",
              tenant(),
              digest);
      if (!candidates.isEmpty()) return (UUID) candidates.getFirst().get("id");
    }
    UUID pool = UuidV7.next();
    store.jdbc.update(
        "INSERT INTO shared_dsh_pool(id,tenant_id,mode,authorization_digest) VALUES(?,?,?,?)",
        pool,
        tenant(),
        mode,
        digest);
    return pool;
  }

  /** Must be called only after the portal adapter's Origin/CSRF checks. Does not issue tickets. */
  public Placement allocate(UUID member, UUID session) {
    return locked(
        () -> {
          inputs.requireMember(tenant(), member);
          inputs.requireSession(tenant(), member, session);
          var existing =
              store.jdbc.queryForList(
                  "SELECT * FROM shared_dsh_placement WHERE tenant_id=? AND member_id=?",
                  tenant(),
                  member);
          if (existing.isEmpty()) {
            // Existing dedicated users require an explicit maintenance export/rebuild window.
            conflict(
                store.jdbc.queryForObject(
                        "SELECT count(*) FROM instance WHERE tenant_id=? AND owner_id=? AND NOT deleted",
                        Long.class,
                        tenant(),
                        member)
                    == 0);
            UUID id = UuidV7.next(), pool = reserve(member, "shared");
            store.jdbc.update(
                "INSERT INTO shared_dsh_placement(id,tenant_id,member_id,pool_id,mode) VALUES(?,?,?,?,'shared')",
                id,
                tenant(),
                member,
                pool);
            emit("runtime", intent(id.toString(), binding(member(member)), "prepare-empty"));
          }
          var row = member(member);
          return new Placement(
              binding(row), row.get("state").toString(), number(row, "auth_generation"));
        });
  }

  @Override
  public Placement resolve(UUID member, UUID placement, UUID session) {
    return locked(
        () -> {
          var row = member(member);
          if (!row.get("id").equals(placement)) throw new AurigaException(CommonErrors.NOT_FOUND);
          requireAuthority(row, session);
          return new Placement(binding(row), "ready", number(row, "auth_generation"));
        });
  }

  public boolean confirmInitial(SharedDshRuntimeReceipt receipt) {
    return locked(
        () -> {
          var row =
              store.one(
                  "SELECT * FROM shared_dsh_placement WHERE tenant_id=? AND id=?",
                  tenant(),
                  UUID.fromString(receipt.getOperationId()));
          conflict(row.get("state").equals("pending"));
          var expected = intent(row.get("id").toString(), binding(row), "prepare-empty");
          if (!confirms(expected, receipt, now()) || !entry.ready(binding(row))) return false;
          inputs.requireMember(tenant(), (UUID) row.get("member_id"));
          var pool =
              store.one(
                  "SELECT * FROM shared_dsh_pool WHERE tenant_id=? AND id=?",
                  tenant(),
                  row.get("pool_id"));
          conflict(
              !pool.get("state").equals("blocked")
                  && pool.get("authorization_digest")
                      .equals(
                          inputs.authorizationSetDigest(tenant(), (UUID) row.get("member_id"))));
          store.jdbc.update(
              "UPDATE shared_dsh_pool SET state='ready' WHERE tenant_id=? AND id=?",
              tenant(),
              row.get("pool_id"));
          store.jdbc.update(
              "UPDATE shared_dsh_placement SET state='ready' WHERE tenant_id=? AND id=?",
              tenant(),
              row.get("id"));
          return true;
        });
  }

  @Override
  public SharedDshSessionBinding register(SharedDshSessionBinding requested, String parent) {
    return locked(
        () -> {
          valid(requested.getUnknownFields().asMap().isEmpty());
          if (!requested.getTenantId().equals(tenant().toString()))
            throw new AurigaException(CommonErrors.NOT_FOUND);
          UUID member = UUID.fromString(requested.getMemberId());
          var row = member(member);
          requireAuthority(row, UUID.fromString(requested.getPlatformSessionId()));
          var current = binding(row);
          var expected =
              SharedDshSessionBinding.newBuilder()
                  .setTenantId(current.getTenantId())
                  .setMemberId(current.getMemberId())
                  .setPlacementId(current.getPlacementId())
                  .setPhysicalInstanceId(current.getPhysicalInstanceId())
                  .setMode(current.getMode())
                  .setEpoch(current.getEpoch())
                  .setRevision(current.getRevision())
                  .setAuthGeneration(number(row, "auth_generation"))
                  .setPlatformSessionId(requested.getPlatformSessionId())
                  .setSessionId(requested.getSessionId())
                  .build();
          conflict(expected.equals(requested));
          UUID session = UUID.fromString(expected.getSessionId());
          if (parent != null && !parent.isEmpty()) {
            var parentBinding = session(UUID.fromString(parent));
            conflict(
                parentBinding.toBuilder()
                    .setSessionId(expected.getSessionId())
                    .build()
                    .equals(expected));
          }
          var existing =
              store.jdbc.queryForList(
                  "SELECT binding FROM shared_dsh_session WHERE tenant_id=? AND id=?",
                  tenant(),
                  session);
          if (!existing.isEmpty()) {
            conflict(
                Arrays.equals((byte[]) existing.getFirst().get("binding"), expected.toByteArray()));
            return expected;
          }
          store.jdbc.update(
              "INSERT INTO shared_dsh_session(id,tenant_id,placement_id,binding) VALUES(?,?,?,?)",
              session,
              tenant(),
              row.get("id"),
              expected.toByteArray());
          return expected;
        });
  }

  /** Always recheck current authority, not just registration bytes. */
  public SharedDshSessionBinding resolveSession(UUID id) {
    return locked(
        () -> {
          var registered = session(id);
          var current =
              resolve(
                  UUID.fromString(registered.getMemberId()),
                  UUID.fromString(registered.getPlacementId()),
                  UUID.fromString(registered.getPlatformSessionId()));
          conflict(
              current.binding().getEpoch() == registered.getEpoch()
                  && current.binding().getRevision() == registered.getRevision()
                  && current.authGeneration() == registered.getAuthGeneration());
          return registered;
        });
  }

  private SharedDshSessionBinding session(UUID id) {
    try {
      return SharedDshSessionBinding.parseFrom(
          (byte[])
              store
                  .one(
                      "SELECT binding FROM shared_dsh_session WHERE tenant_id=? AND id=?",
                      tenant(),
                      id)
                  .get("binding"));
    } catch (com.google.protobuf.InvalidProtocolBufferException e) {
      throw new IllegalStateException(e);
    }
  }

  @Override
  public long revoke(SharedDshRevocation revocation) {
    return locked(
        () -> {
          valid(
              revocation.getSchemaVersion() == 1
                  && revocation.getRevokedAt() >= 0
                  && revocation.getRevokedAt() <= now());
          if (!revocation.getTenantId().equals(tenant().toString()))
            throw new AurigaException(CommonErrors.NOT_FOUND);
          UUID member = UUID.fromString(revocation.getMemberId());
          var row = member(member);
          if (!row.get("id").toString().equals(revocation.getPlacementId()))
            throw new AurigaException(CommonErrors.NOT_FOUND);
          conflict(
              number(row, "epoch") == revocation.getEpoch()
                  && number(row, "revision") == revocation.getRevision()
                  && number(row, "auth_generation") == revocation.getAuthGeneration());
          valid(
              Set.of("logout", "credential-invalid", "switch", "member-disabled")
                  .contains(revocation.getReason()));
          fence(row, revocation.getReason());
          return number(member(member), "revision");
        });
  }

  private void fence(Map<String, Object> row) {
    fence(row, "switch");
  }

  private void fence(Map<String, Object> row, String reason) {
    store.jdbc.update(
        "UPDATE shared_dsh_placement SET epoch=epoch+1,revision=revision+1,auth_generation=auth_generation+1,state='unavailable' WHERE tenant_id=? AND id=?",
        tenant(),
        row.get("id"));
    var next = member((UUID) row.get("member_id"));
    emit(
        "revocation",
        SharedDshRevocation.newBuilder()
            .setSchemaVersion(1)
            .setTenantId(tenant().toString())
            .setMemberId(row.get("member_id").toString())
            .setPlacementId(row.get("id").toString())
            .setEpoch(number(next, "epoch"))
            .setRevision(number(next, "revision"))
            .setAuthGeneration(number(next, "auth_generation"))
            .setReason(reason)
            .setRevokedAt(now())
            .build());
  }

  /** Caller must have admin/write and a live IAM session with strictly less than 600s step-up. */
  public UUID switchMode(
      UUID actor,
      UUID adminSession,
      UUID member,
      String target,
      long revision,
      String key,
      String reason) {
    valid(
        Set.of("shared", "dedicated").contains(target)
            && key != null
            && !key.isBlank()
            && key.length() <= 128
            && reason != null
            && !reason.isBlank()
            && reason.length() <= 512);
    return locked(
        () -> {
          inputs.requirePlacementWrite(tenant(), actor, adminSession, now());
          inputs.requireMember(tenant(), member);
          String fingerprint = member + ":" + target + ":" + revision + ":" + reason;
          var previous =
              store.jdbc.queryForList(
                  "SELECT * FROM shared_dsh_operation WHERE tenant_id=? AND actor_id=? AND idempotency_key=?",
                  tenant(),
                  actor,
                  key);
          if (!previous.isEmpty()) {
            conflict(previous.getFirst().get("fingerprint").equals(fingerprint));
            return (UUID) previous.getFirst().get("id");
          }
          var row = member(member);
          if (number(row, "revision") != revision)
            throw new AurigaException(CommonErrors.PRECONDITION_FAILED);
          conflict(row.get("state").equals("ready") && !row.get("mode").equals(target));
          return enqueue(row, actor, target, key, fingerprint, "switch");
        });
  }

  // Both migration and mode changes use one fenced, recoverable receipt pipeline.
  UUID rebuild(UUID member, UUID actor, String target, UUID batch) {
    return locked(
        () -> {
          var row = member(member);
          conflict(row.get("state").equals("ready"));
          return enqueue(
              row,
              actor,
              target,
              "maintenance:" + batch + ":" + member,
              batch + ":" + member + ":" + target,
              "maintenance");
        });
  }

  private UUID enqueue(
      Map<String, Object> row,
      UUID actor,
      String target,
      String key,
      String fingerprint,
      String purpose) {
    UUID member = (UUID) row.get("member_id");
    UUID pool = reserve(member, target), id = UuidV7.next();
    var source = binding(row);
    fence(row);
    store.jdbc.update(
        "UPDATE shared_dsh_placement SET state='switching' WHERE tenant_id=? AND id=?",
        tenant(),
        row.get("id"));
    var command = intent(id.toString(), source, "cooperative-cancel");
    store.jdbc.update(
        "INSERT INTO shared_dsh_operation(id,tenant_id,placement_id,actor_id,idempotency_key,fingerprint,source_binding,target_pool,target_mode,action_started_at,intent,purpose) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)",
        id,
        tenant(),
        row.get("id"),
        actor,
        key,
        fingerprint,
        source.toByteArray(),
        pool,
        target,
        now(),
        command.toByteArray(),
        purpose);
    emit("runtime", command);
    return id;
  }

  /** Recoverable timer; call after process restart as well. No ACK never means success. */
  public void recover() {
    locked(
        () -> {
          for (var op :
              store.jdbc.queryForList(
                  "SELECT * FROM shared_dsh_operation WHERE tenant_id=? AND state NOT IN ('succeeded','failed')",
                  tenant())) {
            if (op.get("action").equals("cooperative-cancel")
                && now() - number(op, "action_started_at") >= CANCEL_SECONDS) {
              var old = readIntent(op);
              blockPool(old.getBinding().getPhysicalInstanceId());
              var impact =
                  SharedDshGuardianImpact.newBuilder()
                      .setSchemaVersion(1)
                      .setTenantId(tenant().toString())
                      .setPhysicalInstanceId(old.getBinding().getPhysicalInstanceId())
                      .setOperationId(op.get("id").toString())
                      .setReason("cancel-timeout")
                      .setScope("pool")
                      .setState("blocked")
                      .setObservedAt(now());
              for (var affected :
                  store.jdbc.queryForList(
                      "SELECT * FROM shared_dsh_placement WHERE tenant_id=? AND pool_id=?",
                      tenant(),
                      UUID.fromString(old.getBinding().getPhysicalInstanceId())))
                impact.addAffected(binding(affected));
              emit("guardian", impact.build());
              next(op, old.getBinding(), "stop-pool", "blocked");
              store.jdbc.update(
                  "UPDATE shared_dsh_operation SET failure_code='stop-unconfirmed' WHERE tenant_id=? AND id=?",
                  tenant(),
                  op.get("id"));
            } else if (!op.get("state").equals("committing")) emit("runtime", readIntent(op));
          }
          for (var row :
              store.jdbc.queryForList(
                  "SELECT * FROM shared_dsh_placement WHERE tenant_id=? AND state='pending'",
                  tenant()))
            emit("runtime", intent(row.get("id").toString(), binding(row), "prepare-empty"));
          return null;
        });
  }

  private void blockPool(String pool) {
    UUID id = UUID.fromString(pool);
    store.jdbc.update(
        "UPDATE shared_dsh_pool SET state='blocked' WHERE tenant_id=? AND id=?", tenant(), id);
    // Every sibling becomes unavailable immediately. Recovery requires a separate verified rebuild.
    store.jdbc.update(
        "UPDATE shared_dsh_placement SET state='unavailable',epoch=epoch+1,revision=revision+1,auth_generation=auth_generation+1 WHERE tenant_id=? AND pool_id=? AND state<>'switching'",
        tenant(),
        id);
  }

  private static SharedDshRuntimeIntent readIntent(Map<String, Object> op) {
    try {
      return SharedDshRuntimeIntent.parseFrom((byte[]) op.get("intent"));
    } catch (com.google.protobuf.InvalidProtocolBufferException e) {
      throw new IllegalStateException(e);
    }
  }

  private void next(Map<String, Object> op, SharedDshBinding binding, String action, String state) {
    var command = intent(op.get("id").toString(), binding, action);
    store.jdbc.update(
        "UPDATE shared_dsh_outbox SET delivered=true WHERE tenant_id=? AND kind='runtime' AND payload=?",
        tenant(),
        op.get("intent"));
    store.jdbc.update(
        "UPDATE shared_dsh_operation SET intent=?,action=?,state=?,action_started_at=?,failure_code=NULL WHERE tenant_id=? AND id=?",
        command.toByteArray(),
        action,
        state,
        now(),
        tenant(),
        op.get("id"));
    emit("runtime", command);
  }

  public boolean receipt(SharedDshRuntimeReceipt receipt) {
    recover();
    return locked(
        () -> {
          var op = operation(UUID.fromString(receipt.getOperationId()));
          if (Set.of("succeeded", "failed", "committing").contains(op.get("state").toString()))
            return false;
          var expected = readIntent(op);
          if (receipt.getObservedAt() < number(op, "action_started_at")
              || !confirms(expected, receipt, now())) return false;
          var row =
              store.one(
                  "SELECT * FROM shared_dsh_placement WHERE tenant_id=? AND id=?",
                  tenant(),
                  op.get("placement_id"));
          // An intervening revocation must never be undone by late runtime receipts.
          if (number(row, "epoch") != source(op).getEpoch() + 1
              || !row.get("state").equals("switching")) return false;
          switch (expected.getAction()) {
            case "cooperative-cancel", "stop-pool" ->
                next(op, expected.getBinding(), "discard-sessions", "preparing");
            case "discard-sessions" -> {
              store.jdbc.update(
                  "DELETE FROM shared_dsh_session WHERE tenant_id=? AND placement_id=?",
                  tenant(),
                  op.get("placement_id"));
              store.jdbc.update(
                  "DELETE FROM shared_dsh_active WHERE tenant_id=? AND member_id=?",
                  tenant(),
                  row.get("member_id"));
              store.jdbc.update(
                  "UPDATE shared_dsh_operation SET sessions_discarded=true WHERE tenant_id=? AND id=?",
                  tenant(),
                  op.get("id"));
              next(op, expected.getBinding(), "archive-reset", "preparing");
            }
            case "archive-reset" -> {
              UUID archive = UUID.fromString(receipt.getArchiveId());
              store.jdbc.update(
                  "UPDATE shared_dsh_operation SET archive_id=?,archive_expires_at=? WHERE tenant_id=? AND id=?",
                  archive,
                  receipt.getArchiveExpiresAt(),
                  tenant(),
                  op.get("id"));
              next(op, target(op, row), "prepare-empty", "preparing");
            }
            case "prepare-empty" ->
                store.jdbc.update(
                    "UPDATE shared_dsh_operation SET state='committing' WHERE tenant_id=? AND id=?",
                    tenant(),
                    op.get("id"));
            default -> {
              return false;
            }
          }
          return true;
        });
  }

  private static SharedDshBinding source(Map<String, Object> op) {
    try {
      return SharedDshBinding.parseFrom((byte[]) op.get("source_binding"));
    } catch (com.google.protobuf.InvalidProtocolBufferException e) {
      throw new IllegalStateException(e);
    }
  }

  private SharedDshBinding target(Map<String, Object> op, Map<String, Object> row) {
    return binding(row).toBuilder()
        .setPhysicalInstanceId(op.get("target_pool").toString())
        .setMode(mode(op.get("target_mode").toString()))
        .build();
  }

  /** Called after prepare receipt; restart-safe, and success requires a normal page probe. */
  public boolean commit(UUID id) {
    return locked(
        () -> {
          var op = operation(id);
          if (op.get("state").equals("succeeded")) return true;
          conflict(
              op.get("state").equals("committing")
                  && Boolean.TRUE.equals(op.get("sessions_discarded"))
                  && op.get("archive_id") != null);
          var row =
              store.one(
                  "SELECT * FROM shared_dsh_placement WHERE tenant_id=? AND id=?",
                  tenant(),
                  op.get("placement_id"));
          conflict(
              row.get("state").equals("switching")
                  && number(row, "epoch") == source(op).getEpoch() + 1);
          var pool =
              store.one(
                  "SELECT * FROM shared_dsh_pool WHERE tenant_id=? AND id=?",
                  tenant(),
                  op.get("target_pool"));
          inputs.requireMember(tenant(), (UUID) row.get("member_id"));
          conflict(
              !pool.get("state").equals("blocked")
                  && pool.get("authorization_digest")
                      .equals(
                          inputs.authorizationSetDigest(tenant(), (UUID) row.get("member_id"))));
          if (!entry.ready(target(op, row))) return false;
          store.jdbc.update(
              "UPDATE shared_dsh_pool SET state='ready' WHERE tenant_id=? AND id=?",
              tenant(),
              op.get("target_pool"));
          int changed =
              store.jdbc.update(
                  "UPDATE shared_dsh_placement SET pool_id=?,mode=?,state='ready' WHERE tenant_id=? AND id=? AND epoch=? AND revision=? AND state='switching'",
                  op.get("target_pool"),
                  op.get("target_mode"),
                  tenant(),
                  row.get("id"),
                  number(row, "epoch"),
                  number(row, "revision"));
          conflict(changed == 1);
          store.jdbc.update(
              "UPDATE shared_dsh_operation SET state='succeeded',failure_code=NULL WHERE tenant_id=? AND id=?",
              tenant(),
              id);
          if (op.get("purpose").equals("maintenance")) {
            store.jdbc.update(
                "UPDATE instance SET deleted=true,ready=false,purge_data=false,deletion_event_sent=true WHERE tenant_id=? AND id IN (SELECT legacy_instance_id FROM shared_dsh_maintenance WHERE tenant_id=? AND operation_id=?)",
                tenant(),
                tenant(),
                id);
            store.jdbc.update(
                "UPDATE shared_dsh_maintenance SET state='succeeded' WHERE tenant_id=? AND operation_id=?",
                tenant(),
                id);
          }
          emit("placement.switched", target(op, row));
          return true;
        });
  }

  public void admit(UUID member, UUID platformSession, UUID task) {
    locked(
        () -> {
          var row = member(member);
          requireAuthority(row, platformSession);
          var existing =
              store.jdbc.queryForList(
                  "SELECT task_id FROM shared_dsh_active WHERE tenant_id=? AND member_id=?",
                  tenant(),
                  member);
          if (!existing.isEmpty()) {
            conflict(existing.getFirst().get("task_id").equals(task));
            return null;
          }
          var pool =
              store.one(
                  "SELECT * FROM shared_dsh_pool WHERE tenant_id=? AND id=?",
                  tenant(),
                  row.get("pool_id"));
          long active =
              store.jdbc.queryForObject(
                  "SELECT count(*) FROM shared_dsh_active WHERE tenant_id=? AND pool_id=?",
                  Long.class,
                  tenant(),
                  row.get("pool_id"));
          conflict(
              number(pool, "memory_percent") < 80
                  && active < (row.get("mode").equals("shared") ? ACTIVE : 1));
          store.jdbc.update(
              "INSERT INTO shared_dsh_active(tenant_id,member_id,pool_id,task_id) VALUES(?,?,?,?)",
              tenant(),
              member,
              row.get("pool_id"),
              task);
          return null;
        });
  }

  public void release(UUID member, UUID task) {
    locked(
        () ->
            store.jdbc.update(
                "DELETE FROM shared_dsh_active WHERE tenant_id=? AND member_id=? AND task_id=?",
                tenant(),
                member,
                task));
  }

  /** Only trusted node reports, never browser supplied telemetry. */
  public void memory(UUID pool, int percent) {
    valid(percent >= 0 && percent <= 100);
    locked(
        () ->
            store.jdbc.update(
                "UPDATE shared_dsh_pool SET memory_percent=? WHERE tenant_id=? AND id=?",
                percent,
                tenant(),
                pool));
  }

  public List<Delivery> pending() {
    return store.tx(
        () ->
            store
                .jdbc
                .queryForList(
                    "SELECT * FROM shared_dsh_outbox WHERE tenant_id=? AND NOT delivered ORDER BY created_at,id LIMIT 100",
                    tenant())
                .stream()
                .map(
                    r ->
                        new Delivery(
                            (UUID) r.get("id"),
                            r.get("kind").toString(),
                            (byte[]) r.get("payload")))
                .toList());
  }

  public void delivered(UUID id) {
    store.tx(
        () ->
            store.jdbc.update(
                "UPDATE shared_dsh_outbox SET delivered=true WHERE tenant_id=? AND id=?",
                tenant(),
                id));
  }

  @Override
  public SharedDshResourceView resourceView(SharedDshBinding requested) {
    return locked(
        () -> {
          var row = member(UUID.fromString(requested.getMemberId()));
          conflict(
              binding(row).equals(requested)
                  && requested.getTenantId().equals(tenant().toString()));
          inputs.requireMember(tenant(), UUID.fromString(requested.getMemberId()));
          var view = inputs.resourceView(requested);
          valid(
              view.getSchemaVersion() == 1
                  && view.getBinding().equals(requested)
                  && view.getSource().equals("platform"));
          UUID viewId = UUID.fromString(view.getViewId());
          conflict(
              view.getAuthorizationSetDigest()
                      .equals(
                          inputs.authorizationSetDigest(
                              tenant(), UUID.fromString(requested.getMemberId())))
                  && view.getResourcesList().stream().allMatch(r -> r.getExpiresAt() > now()));
          store.jdbc.update(
              "INSERT INTO shared_dsh_resource_view(tenant_id,placement_id,view_id,payload) VALUES(?,?,?,?) ON CONFLICT(tenant_id,placement_id) DO UPDATE SET view_id=excluded.view_id,payload=excluded.payload,report=NULL",
              tenant(),
              row.get("id"),
              viewId,
              view.toByteArray());
          return view;
        });
  }

  @Override
  public long reportResource(SharedDshResourceReport report) {
    return locked(
        () -> {
          valid(
              report.getSchemaVersion() == 1
                  && report.getObservedAt() >= 0
                  && report.getObservedAt() <= now());
          valid(Set.of("ready", "rejected", "stopped").contains(report.getStatus()));
          valid(
              Set.of(
                      "verified",
                      "expired",
                      "revoked",
                      "authorization-mismatch",
                      "cleanup-unconfirmed")
                  .contains(report.getReason()));
          valid(!report.getStatus().equals("ready") || report.getReason().equals("verified"));
          var b = report.getBinding();
          var row = member(UUID.fromString(b.getMemberId()));
          conflict(binding(row).equals(b) && b.getTenantId().equals(tenant().toString()));
          var expected =
              store.one(
                  "SELECT * FROM shared_dsh_resource_view WHERE tenant_id=? AND placement_id=?",
                  tenant(),
                  row.get("id"));
          conflict(expected.get("view_id").toString().equals(report.getViewId()));
          if (report.getStatus().equals("ready")) {
            var live = inputs.resourceView(b);
            conflict(
                Arrays.equals(live.toByteArray(), (byte[]) expected.get("payload"))
                    && live.getResourcesList().stream().allMatch(r -> r.getExpiresAt() > now())
                    && live.getAuthorizationSetDigest()
                        .equals(
                            inputs.authorizationSetDigest(
                                tenant(), UUID.fromString(b.getMemberId()))));
          }
          store.jdbc.update(
              "UPDATE shared_dsh_resource_view SET report=? WHERE tenant_id=? AND placement_id=?",
              report.toByteArray(),
              tenant(),
              row.get("id"));
          emit("resource-report", report);
          if (!report.getStatus().equals("ready")) {
            blockPool(b.getPhysicalInstanceId());
            var impact =
                SharedDshGuardianImpact.newBuilder()
                    .setSchemaVersion(1)
                    .setTenantId(tenant().toString())
                    .setPhysicalInstanceId(b.getPhysicalInstanceId())
                    .setOperationId(UuidV7.next().toString())
                    .setReason(
                        report.getReason().equals("revoked")
                            ? "resource-revoked"
                            : "resource-expired")
                    .setScope("pool")
                    .setState("blocked")
                    .setObservedAt(now());
            for (var affected :
                store.jdbc.queryForList(
                    "SELECT * FROM shared_dsh_placement WHERE tenant_id=? AND pool_id=?",
                    tenant(),
                    UUID.fromString(b.getPhysicalInstanceId())))
              impact.addAffected(binding(affected));
            emit("guardian", impact.build());
            emit("runtime", intent(impact.getOperationId(), b, "stop-pool"));
          }
          return number(member(UUID.fromString(b.getMemberId())), "revision");
        });
  }

  public Placement current(UUID member, UUID session) {
    return locked(
        () -> {
          inputs.requireMember(tenant(), member);
          inputs.requireSession(tenant(), member, session);
          var row = member(member);
          return new Placement(
              binding(row), row.get("state").toString(), number(row, "auth_generation"));
        });
  }

  public auriga.workspace.web.model.DshPlacementSwitch switchView(UUID id) {
    return store.tx(
        () -> {
          var op = operation(id);
          if (!op.get("purpose").equals("switch"))
            throw new AurigaException(CommonErrors.NOT_FOUND);
          var original = source(op);
          var view =
              new auriga.workspace.web.model.DshPlacementSwitch(
                  id,
                  UUID.fromString(original.getMemberId()),
                  auriga.workspace.web.model.DshInstanceMode.fromValue(mode(original.getMode())),
                  auriga.workspace.web.model.DshInstanceMode.fromValue(
                      op.get("target_mode").toString()),
                  auriga.workspace.web.model.DshPlacementSwitch.StateEnum.fromValue(
                      op.get("state").toString()),
                  original.getRevision() + 1,
                  Boolean.TRUE.equals(op.get("sessions_discarded")));
          if (op.get("failure_code") != null)
            view.setFailureCode(
                auriga.workspace.web.model.DshPlacementSwitch.FailureCodeEnum.fromValue(
                    op.get("failure_code").toString()));
          return view;
        });
  }
}
