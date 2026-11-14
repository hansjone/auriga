package auriga.workspace.app;

import static auriga.workspace.domain.SharedDshErrors.*;
import static auriga.workspace.domain.SharedDshPolicy.*;

import auriga.common.error.*;
import auriga.common.persistence.UuidV7;
import auriga.contracts.node.v1.*;
import auriga.workspace.api.SharedDshAuthority;
import auriga.workspace.domain.SharedDshEvents;
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
    // Join our caller's tenant transaction directly. Handled authority errors must not mark
    // the outer stop-receipt transaction rollback-only through a nested TransactionTemplate.
    if (org.springframework.transaction.support.TransactionSynchronizationManager
        .isActualTransactionActive()) {
      auriga.common.persistence.TenantTransactionManager.verifyTransactionTenant();
      store.lock();
      return work.get();
    }
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

  private void queue(String kind, byte[] bytes) {
    store.jdbc.update(
        "INSERT INTO shared_dsh_outbox(id,tenant_id,kind,payload,created_at) SELECT ?,?,?,?,? WHERE"
            + " NOT EXISTS (SELECT 1 FROM shared_dsh_outbox WHERE tenant_id=? AND kind=? AND"
            + " payload=? AND NOT delivered)",
        UuidV7.next(),
        tenant(),
        kind,
        bytes,
        now(),
        tenant(),
        kind,
        bytes);
  }

  private void emit(String kind, com.google.protobuf.MessageLite payload) {
    if (kind.equals("runtime")) queue(kind, payload.toByteArray());
    else emitJson(kind, SharedDshEvents.data(payload));
  }

  private void emitJson(String kind, Map<String, Object> data) {
    queue(kind, WorkspaceService.JSON.writeValueAsBytes(data));
  }

  private Map<String, Object> placement(UUID id) {
    return store.one("SELECT * FROM shared_dsh_placement WHERE tenant_id=? AND id=?", tenant(), id);
  }

  private void audit(Map<String, Object> op, String result, String failure) {
    var row = placement((UUID) op.get("placement_id"));
    var original = source(op);
    var event = new LinkedHashMap<String, Object>();
    event.put("operationId", op.get("id").toString());
    event.put("memberId", original.getMemberId());
    event.put("placementId", original.getPlacementId());
    event.put("fromMode", mode(original.getMode()));
    event.put("targetMode", op.get("target_mode"));
    event.put("epoch", number(row, "epoch"));
    event.put("revision", number(row, "revision"));
    event.put("sessionsDiscarded", Boolean.TRUE.equals(op.get("sessions_discarded")));
    event.put("actorId", op.get("actor_id").toString());
    if (failure != null) event.put("failureCode", failure);
    if (!result.equals("accepted") && op.get("purpose").equals("switch"))
      emitJson(
          result.equals("succeeded") ? "placement.switched" : "placement.switch_failed", event);
    var log = new LinkedHashMap<String, Object>(event);
    log.put("tenantId", tenant().toString());
    log.put("reason", op.get("reason"));
    log.put("stepUpRef", op.get("step_up_ref"));
    log.put("result", result);
    store.jdbc.update(
        "INSERT INTO shared_dsh_audit(id,tenant_id,operation_id,result,payload,created_at)"
            + " VALUES(?,?,?,?,?::jsonb,?)",
        UuidV7.next(),
        tenant(),
        op.get("id"),
        result,
        WorkspaceService.JSON.writeValueAsString(log),
        now());
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
    live(pool.get("authorization_digest").equals(inputs.authorizationSetDigest(tenant(), member)));
    live(row.get("state").equals("ready") && pool.get("state").equals("ready"));
  }

  private UUID reserve(UUID member, String mode) {
    String digest = inputs.authorizationSetDigest(tenant(), member);
    valid(digest != null && digest.matches("sha256:[a-f0-9]{64}"));
    if (mode.equals("shared")) {
      var candidates =
          store.jdbc.queryForList(
              "SELECT p.id FROM shared_dsh_pool p WHERE p.tenant_id=? AND p.mode='shared' AND"
                  + " p.authorization_digest=? AND p.state IN ('pending','ready') AND"
                  + " p.memory_percent<80 AND (SELECT count(*) FROM shared_dsh_placement x WHERE"
                  + " x.tenant_id=p.tenant_id AND x.pool_id=p.id)+(SELECT count(*) FROM"
                  + " shared_dsh_operation o WHERE o.tenant_id=p.tenant_id AND o.target_pool=p.id"
                  + " AND o.state NOT IN ('succeeded','failed'))+(SELECT count(*) FROM"
                  + " shared_dsh_operation r WHERE r.tenant_id=p.tenant_id AND r.rollback_pool=p.id"
                  + " AND r.recovery_pending AND r.placement_id NOT IN (SELECT x.id FROM"
                  + " shared_dsh_placement x WHERE x.tenant_id=p.tenant_id AND x.pool_id=p.id))<20"
                  + " ORDER BY p.id LIMIT 1",
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
    store.register(tenant(), "tenant", tenant());
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
                        "SELECT count(*) FROM instance WHERE tenant_id=? AND owner_id=? AND NOT"
                            + " deleted",
                        Long.class,
                        tenant(),
                        member)
                    == 0);
            UUID id = UuidV7.next(), pool = reserve(member, "shared");
            store.jdbc.update(
                "INSERT INTO shared_dsh_placement(id,tenant_id,member_id,pool_id,mode)"
                    + " VALUES(?,?,?,?,'shared')",
                id,
                tenant(),
                member,
                pool);
            emit("runtime", intent(id.toString(), binding(member(member)), "prepare-empty"));
          }
          var row = member(member);
          if (row.get("state").equals("unavailable")) {
            var recovering =
                store.jdbc.queryForList(
                    "SELECT * FROM shared_dsh_operation WHERE tenant_id=? AND placement_id=? AND"
                        + " recovery_pending",
                    tenant(),
                    row.get("id"));
            if (!recovering.isEmpty()) retryRecovery((UUID) recovering.getFirst().get("id"));
            else if (store.jdbc.queryForObject(
                    "SELECT count(*) FROM shared_dsh_operation WHERE tenant_id=? AND placement_id=?"
                        + " AND state NOT IN ('succeeded','failed')",
                    Long.class,
                    tenant(),
                    row.get("id"))
                == 0) prepareMember(row);
          }
          row = member(member);
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
          live(
              Set.of("pending", "ready").contains(pool.get("state").toString())
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
          live(
              requested.getEpoch() == current.getEpoch()
                  && requested.getRevision() == current.getRevision()
                  && requested.getAuthGeneration() == number(row, "auth_generation"));
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
          matched(expected.equals(requested));
          UUID session = UUID.fromString(expected.getSessionId());
          if (parent != null && !parent.isEmpty()) {
            var parentBinding = session(UUID.fromString(parent));
            matched(
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
            matched(
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
          live(
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
          live(
              number(row, "epoch") == revocation.getEpoch()
                  && number(row, "revision") == revocation.getRevision()
                  && number(row, "auth_generation") == revocation.getAuthGeneration());
          valid(
              Set.of("logout", "credential-invalid", "switch", "member-disabled")
                  .contains(revocation.getReason()));
          fence(row, revocation.getReason());
          for (var interrupted :
              store.jdbc.queryForList(
                  "SELECT * FROM shared_dsh_operation WHERE tenant_id=? AND placement_id=? AND"
                      + " state='failed' AND recovery_pending",
                  tenant(),
                  row.get("id"))) {
            if (interrupted.get("recovery_intent") != null)
              consume(parseIntent((byte[]) interrupted.get("recovery_intent")));
            if (interrupted.get("rollback_pool") != null
                && pool((UUID) interrupted.get("rollback_pool")).get("mode").equals("dedicated")) {
              var abandoned =
                  binding(member(member)).toBuilder()
                      .setEpoch(number(interrupted, "recovery_epoch"))
                      .setPhysicalInstanceId(interrupted.get("rollback_pool").toString())
                      .setMode(source(interrupted).getMode())
                      .build();
              requestStop(UuidV7.next(), abandoned, "credential-stop-unconfirmed");
            }
            store.jdbc.update(
                "UPDATE shared_dsh_operation SET"
                    + " recovery_stage='await-authority',recovery_intent=NULL,recovery_id=NULL,rollback_pool=NULL,recovery_epoch=?"
                    + " WHERE tenant_id=? AND id=?",
                number(member(member), "epoch"),
                tenant(),
                interrupted.get("id"));
          }
          var active =
              store.jdbc.queryForList(
                  "SELECT * FROM shared_dsh_operation WHERE tenant_id=? AND placement_id=? AND"
                      + " state NOT IN ('succeeded','failed')",
                  tenant(),
                  row.get("id"));
          if (!active.isEmpty()) for (var op : active) failLocked(op, "revoked");
          else if (revocation.getReason().equals("logout")
              && row.get("state").equals("ready")
              && pool((UUID) row.get("pool_id")).get("state").equals("ready"))
            store.jdbc.update(
                "UPDATE shared_dsh_placement SET state='ready' WHERE tenant_id=? AND id=?",
                tenant(),
                row.get("id"));
          return number(member(member), "revision");
        });
  }

  private void fence(Map<String, Object> row) {
    fence(row, "switch");
  }

  private void fence(Map<String, Object> row, String reason) {
    store.jdbc.update(
        "UPDATE shared_dsh_placement SET"
            + " epoch=epoch+1,revision=revision+1,auth_generation=auth_generation+1,state='unavailable'"
            + " WHERE tenant_id=? AND id=?",
        tenant(),
        row.get("id"));
    var next = member((UUID) row.get("member_id"));
    emit(
        "placement.revoked",
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
                  "SELECT * FROM shared_dsh_operation WHERE tenant_id=? AND actor_id=? AND"
                      + " idempotency_key=?",
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
          return enqueue(
              row, actor, target, key, fingerprint, "switch", reason, adminSession.toString());
        });
  }

  // Both migration and mode changes use one fenced, recoverable receipt pipeline.
  UUID rebuild(UUID member, UUID actor, String target, UUID batch, UUID adminSession) {
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
              "maintenance",
              "maintenance batch " + batch,
              adminSession.toString());
        });
  }

  private UUID enqueue(
      Map<String, Object> row,
      UUID actor,
      String target,
      String key,
      String fingerprint,
      String purpose,
      String reason,
      String stepUpRef) {
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
        "INSERT INTO"
            + " shared_dsh_operation(id,tenant_id,placement_id,actor_id,idempotency_key,fingerprint,source_binding,target_pool,target_mode,action_started_at,intent,purpose)"
            + " VALUES(?,?,?,?,?,?,?,?,?,?,?,?)",
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
    store.jdbc.update(
        "UPDATE shared_dsh_operation SET reason=?,step_up_ref=? WHERE tenant_id=? AND id=?",
        reason,
        stepUpRef,
        tenant(),
        id);
    audit(operation(id), "accepted", null);
    emit("runtime", command);
    return id;
  }

  private void currentResources(SharedDshBinding b, Map<String, Object> pool) {
    var view = inputs.resourceView(b);
    live(
        view.getSchemaVersion() == 1
            && view.getSource().equals("platform")
            && view.getBinding().equals(b)
            && view.getAuthorizationSetDigest().equals(pool.get("authorization_digest"))
            && view.getResourcesList().stream().allMatch(r -> r.getExpiresAt() > now()));
  }

  private Map<String, Object> pool(UUID id) {
    return store.one("SELECT * FROM shared_dsh_pool WHERE tenant_id=? AND id=?", tenant(), id);
  }

  private void consume(SharedDshRuntimeIntent command) {
    store.jdbc.update(
        "UPDATE shared_dsh_outbox SET delivered=true WHERE tenant_id=? AND kind='runtime' AND"
            + " payload=?",
        tenant(),
        command.toByteArray());
  }

  /** Only explicit pool stop receipts unlock recovery; a timeout is never a receipt. */
  public void recover() {
    locked(
        () -> {
          for (var op :
              store.jdbc.queryForList(
                  "SELECT * FROM shared_dsh_operation WHERE tenant_id=? AND state NOT IN"
                      + " ('succeeded','failed')",
                  tenant())) {
            var row = placement((UUID) op.get("placement_id"));
            if (number(row, "epoch") != source(op).getEpoch() + 1
                || !row.get("state").equals("switching")) {
              failLocked(op, "revoked");
              continue;
            }
            if (op.get("action").equals("cooperative-cancel")
                && now() - number(op, "action_started_at") >= CANCEL_SECONDS) {
              requestStop((UUID) op.get("id"), readIntent(op).getBinding(), "cancel-timeout");
              next(op, readIntent(op).getBinding(), "stop-pool", "blocked");
              store.jdbc.update(
                  "UPDATE shared_dsh_operation SET failure_code='stop-unconfirmed' WHERE"
                      + " tenant_id=? AND id=?",
                  tenant(),
                  op.get("id"));
              audit(operation((UUID) op.get("id")), "blocked", "stop-unconfirmed");
            } else if (!op.get("state").equals("committing")) emit("runtime", readIntent(op));
          }
          for (var stop :
              store.jdbc.queryForList(
                  "SELECT * FROM shared_dsh_pool_stop WHERE tenant_id=? AND state='waiting'",
                  tenant())) emit("runtime", parseIntent((byte[]) stop.get("intent")));
          for (var stop :
              store.jdbc.queryForList(
                  "SELECT * FROM shared_dsh_pool_stop WHERE tenant_id=? AND state='stopped'",
                  tenant())) recoverPool((UUID) stop.get("pool_id"));
          for (var op :
              store.jdbc.queryForList(
                  "SELECT * FROM shared_dsh_operation WHERE tenant_id=? AND state='failed' AND"
                      + " recovery_pending AND recovery_intent IS NOT NULL",
                  tenant())) emit("runtime", parseIntent((byte[]) op.get("recovery_intent")));
          for (var done :
              store.jdbc.queryForList(
                  "SELECT id FROM shared_dsh_operation WHERE tenant_id=? AND (state='committing' OR"
                      + " (state='failed' AND recovery_pending AND recovery_stage='probe'))",
                  tenant())) {
            UUID id = (UUID) done.get("id");
            var current = operation(id);
            try {
              if (current.get("state").equals("failed")) commitRecovery(id);
              else commit(id);
            } catch (AurigaException unavailable) {
              if (unavailable.errorCode() == CommonErrors.FORBIDDEN
                  || unavailable.errorCode() == CommonErrors.UNAUTHENTICATED)
                failLocked(current, "revoked");
            } catch (IllegalStateException unavailable) {
              /* Keep the durable probe phase for the next tick. */
            }
          }
          for (var row :
              store.jdbc.queryForList(
                  "SELECT * FROM shared_dsh_placement WHERE tenant_id=? AND state='pending'",
                  tenant()))
            emit("runtime", intent(row.get("id").toString(), binding(row), "prepare-empty"));
          return null;
        });
  }

  private static SharedDshRuntimeIntent parseIntent(byte[] bytes) {
    try {
      return SharedDshRuntimeIntent.parseFrom(bytes);
    } catch (com.google.protobuf.InvalidProtocolBufferException e) {
      throw new IllegalStateException(e);
    }
  }

  private void blockPool(String id) {
    UUID pool = UUID.fromString(id);
    store.jdbc.update(
        "UPDATE shared_dsh_pool SET state='blocked' WHERE tenant_id=? AND id=? AND"
            + " state<>'retired'",
        tenant(),
        pool);
    for (var row :
        store.jdbc.queryForList(
            "SELECT * FROM shared_dsh_placement WHERE tenant_id=? AND pool_id=? AND"
                + " state<>'switching'",
            tenant(),
            pool)) {
      if (!row.get("state").equals("unavailable")) fence(row, "credential-invalid");
      store.jdbc.update(
          "DELETE FROM shared_dsh_session WHERE tenant_id=? AND placement_id=?",
          tenant(),
          row.get("id"));
    }
  }

  private void requestStop(UUID operation, SharedDshBinding binding, String reason) {
    var command = intent(operation.toString(), binding, "stop-pool");
    blockPool(binding.getPhysicalInstanceId());
    store.jdbc.update(
        "INSERT INTO shared_dsh_pool_stop(id,tenant_id,pool_id,intent,reason,created_at)"
            + " VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING",
        operation,
        tenant(),
        UUID.fromString(binding.getPhysicalInstanceId()),
        command.toByteArray(),
        reason,
        now());
    var impact =
        SharedDshGuardianImpact.newBuilder()
            .setSchemaVersion(1)
            .setTenantId(tenant().toString())
            .setPhysicalInstanceId(binding.getPhysicalInstanceId())
            .setOperationId(operation.toString())
            .setReason(reason)
            .setScope("pool")
            .setState("blocked")
            .setObservedAt(now());
    for (var row :
        store.jdbc.queryForList(
            "SELECT * FROM shared_dsh_placement WHERE tenant_id=? AND pool_id=?",
            tenant(),
            UUID.fromString(binding.getPhysicalInstanceId()))) impact.addAffected(binding(row));
    if (impact.getAffectedCount() == 0) impact.addAffected(binding);
    emit("placement.guardian_impact", impact.build());
    emit("runtime", command);
  }

  private boolean stopped(SharedDshRuntimeReceipt receipt) {
    var rows =
        store.jdbc.queryForList(
            "SELECT * FROM shared_dsh_pool_stop WHERE tenant_id=? AND id=?",
            tenant(),
            UUID.fromString(receipt.getOperationId()));
    if (rows.isEmpty()) return false;
    var stop = rows.getFirst();
    var expected = parseIntent((byte[]) stop.get("intent"));
    if (!stop.get("state").equals("waiting")
        || receipt.getObservedAt() < number(stop, "created_at")
        || !confirms(expected, receipt, now())) return false;
    store.jdbc.update(
        "UPDATE shared_dsh_pool_stop SET receipt=?,state='stopped' WHERE tenant_id=? AND id=?",
        receipt.toByteArray(),
        tenant(),
        stop.get("id"));
    consume(expected);
    store.jdbc.update(
        "DELETE FROM shared_dsh_active WHERE tenant_id=? AND pool_id=?",
        tenant(),
        stop.get("pool_id"));
    UUID poolId = (UUID) stop.get("pool_id");
    var impact =
        SharedDshGuardianImpact.newBuilder()
            .setSchemaVersion(1)
            .setTenantId(tenant().toString())
            .setPhysicalInstanceId(poolId.toString())
            .setOperationId(receipt.getOperationId())
            .setReason(stop.get("reason").toString())
            .setScope("pool")
            .setState("stopped")
            .setObservedAt(receipt.getObservedAt());
    for (var row :
        store.jdbc.queryForList(
            "SELECT * FROM shared_dsh_placement WHERE tenant_id=? AND pool_id=?", tenant(), poolId))
      impact.addAffected(binding(row));
    if (impact.getAffectedCount() == 0) impact.addAffected(expected.getBinding());
    emit("placement.guardian_impact", impact.build());
    recoverPool(poolId);
    if (pool(poolId).get("mode").equals("dedicated")
        && store.jdbc.queryForObject(
                "SELECT count(*) FROM shared_dsh_placement WHERE tenant_id=? AND pool_id=?",
                Long.class,
                tenant(),
                poolId)
            == 0
        && store.jdbc.queryForObject(
                "SELECT count(*) FROM shared_dsh_operation WHERE tenant_id=? AND target_pool=? AND"
                    + " state NOT IN ('succeeded','failed')",
                Long.class,
                tenant(),
                poolId)
            == 0) retire(poolId);
    return true;
  }

  private void recoverPool(UUID id) {
    var p = pool(id);
    if (p.get("state").equals("retired")) return;
    if (store.jdbc.queryForObject(
            "SELECT count(*) FROM shared_dsh_pool_stop WHERE tenant_id=? AND pool_id=? AND"
                + " state='waiting'",
            Long.class,
            tenant(),
            id)
        > 0) return;
    for (var row :
        store.jdbc.queryForList(
            "SELECT * FROM shared_dsh_placement WHERE tenant_id=? AND pool_id=? AND"
                + " state='unavailable'",
            tenant(),
            id)) {
      if (store.jdbc.queryForObject(
              "SELECT count(*) FROM shared_dsh_operation WHERE tenant_id=? AND placement_id=? AND"
                  + " recovery_pending",
              Long.class,
              tenant(),
              row.get("id"))
          > 0) continue;
      try {
        prepareMember(row);
      } catch (AurigaException e) {
        if (e.errorCode() == CommonErrors.INTERNAL) throw e;
      } catch (IllegalStateException unavailable) {
        // A confirmed stop survives authority outages; no ready transition is made.
      }
    }
  }

  private void prepareMember(Map<String, Object> row) {
    var p = pool((UUID) row.get("pool_id"));
    if (p.get("state").equals("retired")) return;
    if (p.get("state").equals("blocked")
        && (store.jdbc.queryForObject(
                    "SELECT count(*) FROM shared_dsh_pool_stop WHERE tenant_id=? AND pool_id=? AND"
                        + " state='stopped'",
                    Long.class,
                    tenant(),
                    p.get("id"))
                == 0
            || store.jdbc.queryForObject(
                    "SELECT count(*) FROM shared_dsh_pool_stop WHERE tenant_id=? AND pool_id=? AND"
                        + " state='waiting'",
                    Long.class,
                    tenant(),
                    p.get("id"))
                > 0)) return;
    UUID member = (UUID) row.get("member_id");
    inputs.requireMember(tenant(), member);
    var view = inputs.resourceView(binding(row));
    live(
        view.getBinding().equals(binding(row))
            && view.getAuthorizationSetDigest().equals(p.get("authorization_digest"))
            && p.get("authorization_digest").equals(inputs.authorizationSetDigest(tenant(), member))
            && view.getResourcesList().stream().allMatch(r -> r.getExpiresAt() > now()));
    store.jdbc.update(
        "UPDATE shared_dsh_pool SET state='pending' WHERE tenant_id=? AND id=? AND state='blocked'",
        tenant(),
        p.get("id"));
    store.jdbc.update(
        "UPDATE shared_dsh_placement SET state='pending' WHERE tenant_id=? AND id=?",
        tenant(),
        row.get("id"));
    emit("runtime", intent(row.get("id").toString(), binding(row), "prepare-empty"));
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
        "UPDATE shared_dsh_outbox SET delivered=true WHERE tenant_id=? AND kind='runtime' AND"
            + " payload=?",
        tenant(),
        op.get("intent"));
    store.jdbc.update(
        "UPDATE shared_dsh_operation SET"
            + " intent=?,action=?,state=?,action_started_at=?,failure_code=NULL WHERE tenant_id=?"
            + " AND id=?",
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
          if (!uuid(receipt.getOperationId())) return false;
          UUID id = UUID.fromString(receipt.getOperationId());
          var recovery =
              store.jdbc.queryForList(
                  "SELECT * FROM shared_dsh_operation WHERE tenant_id=? AND recovery_id=? AND"
                      + " recovery_pending",
                  tenant(),
                  id);
          if (!recovery.isEmpty()) return recoveryReceipt(recovery.getFirst(), receipt);
          var rows =
              store.jdbc.queryForList(
                  "SELECT * FROM shared_dsh_operation WHERE tenant_id=? AND id=?", tenant(), id);
          if (rows.isEmpty()) return stopped(receipt);
          var op = rows.getFirst();
          if (op.get("state").equals("failed")) return stopped(receipt);
          if (Set.of("succeeded", "committing").contains(op.get("state").toString())) return false;
          var expected = readIntent(op);
          boolean matches =
              receipt.getSchemaVersion() == 1
                  && receipt.getBinding().equals(expected.getBinding())
                  && receipt.getAction().equals(expected.getAction())
                  && receipt.getObservedAt() >= number(op, "action_started_at")
                  && receipt.getObservedAt() <= now()
                  && uuid(receipt.getReceiptId());
          if (matches
              && receipt.getOutcome().equals("unconfirmed")
              && expected.getAction().equals("prepare-empty")) {
            failLocked(op, "prepare-failed");
            try {
              retryRecovery(id);
            } catch (AurigaException | IllegalStateException unavailable) {
              /* The failed state remains durable until authority returns. */
            }
            return true;
          }
          if (!matches || !confirms(expected, receipt, now())) return false;
          var row = placement((UUID) op.get("placement_id"));
          if (number(row, "epoch") != source(op).getEpoch() + 1
              || !row.get("state").equals("switching")) return false;
          consume(expected);
          switch (expected.getAction()) {
            case "cooperative-cancel" -> {
              if (source(op).getMode() == DshInstanceMode.DSH_INSTANCE_MODE_DEDICATED) {
                requestStop(id, expected.getBinding(), "credential-stop-unconfirmed");
                next(op, expected.getBinding(), "stop-pool", "blocked");
                store.jdbc.update(
                    "UPDATE shared_dsh_operation SET failure_code='stop-unconfirmed' WHERE"
                        + " tenant_id=? AND id=?",
                    tenant(),
                    id);
                audit(operation(id), "blocked", "stop-unconfirmed");
              } else next(op, expected.getBinding(), "discard-sessions", "preparing");
            }
            case "stop-pool" -> {
              if (!stopped(receipt)) return false;
              store.jdbc.update(
                  "UPDATE shared_dsh_operation SET source_stopped=true WHERE tenant_id=? AND id=?",
                  tenant(),
                  id);
              if (source(op).getMode() == DshInstanceMode.DSH_INSTANCE_MODE_DEDICATED)
                retire(UUID.fromString(source(op).getPhysicalInstanceId()));
              next(op, expected.getBinding(), "discard-sessions", "preparing");
            }
            case "discard-sessions" -> {
              discard(row);
              store.jdbc.update(
                  "UPDATE shared_dsh_operation SET sessions_discarded=true WHERE tenant_id=? AND"
                      + " id=?",
                  tenant(),
                  id);
              next(op, expected.getBinding(), "archive-reset", "preparing");
            }
            case "archive-reset" -> {
              store.jdbc.update(
                  "UPDATE shared_dsh_operation SET archive_id=?,archive_expires_at=? WHERE"
                      + " tenant_id=? AND id=?",
                  UUID.fromString(receipt.getArchiveId()),
                  receipt.getArchiveExpiresAt(),
                  tenant(),
                  id);
              next(op, target(op, row), "prepare-empty", "preparing");
            }
            case "prepare-empty" ->
                store.jdbc.update(
                    "UPDATE shared_dsh_operation SET state='committing' WHERE tenant_id=? AND id=?",
                    tenant(),
                    id);
            default -> {
              return false;
            }
          }
          return true;
        });
  }

  private void discard(Map<String, Object> row) {
    store.jdbc.update(
        "DELETE FROM shared_dsh_session WHERE tenant_id=? AND placement_id=?",
        tenant(),
        row.get("id"));
    store.jdbc.update(
        "DELETE FROM shared_dsh_active WHERE tenant_id=? AND member_id=?",
        tenant(),
        row.get("member_id"));
  }

  private void retire(UUID pool) {
    store.jdbc.update(
        "UPDATE shared_dsh_pool SET state='retired' WHERE tenant_id=? AND id=?", tenant(), pool);
    store.jdbc.update(
        "UPDATE shared_dsh_pool_stop SET state='retired' WHERE tenant_id=? AND pool_id=? AND"
            + " state='stopped'",
        tenant(),
        pool);
  }

  private void failLocked(Map<String, Object> op, String code) {
    if (Set.of("succeeded", "failed").contains(op.get("state").toString())) return;
    consume(readIntent(op));
    var row = placement((UUID) op.get("placement_id"));
    fence(row, "switch");
    store.jdbc.update(
        "UPDATE shared_dsh_operation SET"
            + " state='failed',failure_code=?,recovery_pending=true,recovery_stage='await-authority',recovery_intent=NULL"
            + " WHERE tenant_id=? AND id=?",
        code,
        tenant(),
        op.get("id"));
    store.jdbc.update(
        "UPDATE shared_dsh_operation SET recovery_epoch=? WHERE tenant_id=? AND id=?",
        number(placement((UUID) op.get("placement_id")), "epoch"),
        tenant(),
        op.get("id"));
    audit(operation((UUID) op.get("id")), "failed", code);
  }

  /**
   * Trusted adapter can report definitive preparation failure; silence/unconfirmed stop is not
   * failure proof.
   */
  public void failOperation(UUID id, String code) {
    valid(Set.of("prepare-failed", "revoked", "recovery-required").contains(code));
    locked(
        () -> {
          failLocked(operation(id), code);
          return null;
        });
  }

  public void retryRecovery(UUID id) {
    locked(
        () -> {
          var op = operation(id);
          conflict(op.get("state").equals("failed"));
          if (!Boolean.TRUE.equals(op.get("recovery_pending"))
              || !op.get("recovery_stage").equals("await-authority")) return null;
          inputs.requireMember(tenant(), UUID.fromString(source(op).getMemberId()));
          recoveryNext(op, "cancel-source", source(op), "cooperative-cancel");
          return null;
        });
  }

  private void recoveryNext(
      Map<String, Object> op, String stage, SharedDshBinding binding, String action) {
    if (op.get("recovery_intent") != null) consume(parseIntent((byte[]) op.get("recovery_intent")));
    UUID id = UuidV7.next();
    var command = intent(id.toString(), binding, action);
    store.jdbc.update(
        "UPDATE shared_dsh_operation SET"
            + " recovery_id=?,recovery_stage=?,recovery_intent=?,recovery_started_at=? WHERE"
            + " tenant_id=? AND id=?",
        id,
        stage,
        command.toByteArray(),
        now(),
        tenant(),
        op.get("id"));
    if (action.equals("stop-pool")) requestStop(id, binding, "credential-stop-unconfirmed");
    else emit("runtime", command);
  }

  private SharedDshBinding abandonedTarget(Map<String, Object> op) {
    return source(op).toBuilder()
        .setEpoch(source(op).getEpoch() + 1)
        .setRevision(source(op).getRevision() + 1)
        .setPhysicalInstanceId(op.get("target_pool").toString())
        .setMode(mode(op.get("target_mode").toString()))
        .build();
  }

  private boolean recoveryReceipt(Map<String, Object> op, SharedDshRuntimeReceipt receipt) {
    if (op.get("recovery_intent") == null
        || number(placement((UUID) op.get("placement_id")), "epoch")
            != number(op, "recovery_epoch")) return false;
    var expected = parseIntent((byte[]) op.get("recovery_intent"));
    if (receipt.getObservedAt() < number(op, "recovery_started_at")
        || !confirms(expected, receipt, now())) return false;
    consume(expected);
    String stage = op.get("recovery_stage").toString();
    var row = placement((UUID) op.get("placement_id"));
    switch (stage) {
      case "cancel-source" -> {
        if (source(op).getMode() == DshInstanceMode.DSH_INSTANCE_MODE_DEDICATED
            || pool(UUID.fromString(source(op).getPhysicalInstanceId()))
                .get("state")
                .equals("blocked")) recoveryNext(op, "stop-source", source(op), "stop-pool");
        else recoveryNext(op, "discard", source(op), "discard-sessions");
      }
      case "stop-source" -> {
        if (!stopped(receipt)) return false;
        if (source(op).getMode() == DshInstanceMode.DSH_INSTANCE_MODE_DEDICATED)
          retire(UUID.fromString(source(op).getPhysicalInstanceId()));
        recoveryNext(op, "discard", source(op), "discard-sessions");
      }
      case "discard" -> {
        discard(row);
        store.jdbc.update(
            "UPDATE shared_dsh_operation SET sessions_discarded=true WHERE tenant_id=? AND id=?",
            tenant(),
            op.get("id"));
        recoveryNext(op, "archive", source(op), "archive-reset");
      }
      case "archive" -> {
        store.jdbc.update(
            "UPDATE shared_dsh_operation SET archive_id=?,archive_expires_at=? WHERE tenant_id=?"
                + " AND id=?",
            UUID.fromString(receipt.getArchiveId()),
            receipt.getArchiveExpiresAt(),
            tenant(),
            op.get("id"));
        if (op.get("target_mode").equals("dedicated"))
          recoveryNext(op, "stop-target", abandonedTarget(op), "stop-pool");
        else recoveryNext(op, "cancel-target", abandonedTarget(op), "cooperative-cancel");
      }
      case "stop-target", "cancel-target" -> {
        if (stage.equals("stop-target")) {
          if (!stopped(receipt)) return false;
          retire((UUID) op.get("target_pool"));
        }
        UUID rollback = reserve((UUID) row.get("member_id"), mode(source(op).getMode()));
        store.jdbc.update(
            "UPDATE shared_dsh_operation SET rollback_pool=? WHERE tenant_id=? AND id=?",
            rollback,
            tenant(),
            op.get("id"));
        var binding =
            binding(row).toBuilder()
                .setPhysicalInstanceId(rollback.toString())
                .setMode(source(op).getMode())
                .build();
        recoveryNext(op, "prepare", binding, "prepare-empty");
      }
      case "prepare" ->
          store.jdbc.update(
              "UPDATE shared_dsh_operation SET recovery_stage='probe',recovery_intent=NULL WHERE"
                  + " tenant_id=? AND id=?",
              tenant(),
              op.get("id"));
      default -> {
        return false;
      }
    }
    return true;
  }

  public boolean commitRecovery(UUID id) {
    return locked(
        () -> {
          var op = operation(id);
          conflict(op.get("state").equals("failed"));
          if (!Boolean.TRUE.equals(op.get("recovery_pending"))) return true;
          conflict(op.get("recovery_stage").equals("probe"));
          var row = placement((UUID) op.get("placement_id"));
          live(number(row, "epoch") == number(op, "recovery_epoch"));
          inputs.requireMember(tenant(), (UUID) row.get("member_id"));
          var p = pool((UUID) op.get("rollback_pool"));
          live(
              Set.of("pending", "ready").contains(p.get("state").toString())
                  && p.get("authorization_digest")
                      .equals(
                          inputs.authorizationSetDigest(tenant(), (UUID) row.get("member_id"))));
          var b =
              binding(row).toBuilder()
                  .setMode(source(op).getMode())
                  .setPhysicalInstanceId(p.get("id").toString())
                  .build();
          currentResources(b, p);
          if (!entry.ready(b)) return false;
          store.jdbc.update(
              "UPDATE shared_dsh_pool SET state='ready' WHERE tenant_id=? AND id=?",
              tenant(),
              p.get("id"));
          store.jdbc.update(
              "UPDATE shared_dsh_placement SET pool_id=?,mode=?,state='ready' WHERE tenant_id=? AND"
                  + " id=?",
              p.get("id"),
              mode(source(op).getMode()),
              tenant(),
              row.get("id"));
          store.jdbc.update(
              "UPDATE shared_dsh_operation SET recovery_pending=false,recovery_id=NULL WHERE"
                  + " tenant_id=? AND id=?",
              tenant(),
              id);
          if (op.get("purpose").equals("maintenance")) {
            // Rollback creates a fresh empty placement too; the fenced legacy instance is no
            // longer an assigned runtime and must not retain its old scheduling reservation.
            store.jdbc.update(
                "UPDATE instance SET deleted=true,ready=false,purge_data=false,deletion_event_sent=false WHERE tenant_id=? AND id IN (SELECT legacy_instance_id FROM shared_dsh_maintenance WHERE tenant_id=? AND operation_id=?)",
                tenant(),
                tenant(),
                id);
          }
          audit(operation(id), "rolled-back", op.get("failure_code").toString());
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
          if (op.get("state").equals("failed")) throw new AurigaException(REVOKED);
          if (op.get("action").equals("stop-pool")) throw new AurigaException(STOP_UNCONFIRMED);
          if (source(op).getMode() == DshInstanceMode.DSH_INSTANCE_MODE_DEDICATED
              && !Boolean.TRUE.equals(op.get("source_stopped")))
            throw new AurigaException(STOP_UNCONFIRMED);
          conflict(
              op.get("state").equals("committing")
                  && Boolean.TRUE.equals(op.get("sessions_discarded"))
                  && op.get("archive_id") != null);
          var row =
              store.one(
                  "SELECT * FROM shared_dsh_placement WHERE tenant_id=? AND id=?",
                  tenant(),
                  op.get("placement_id"));
          live(
              row.get("state").equals("switching")
                  && number(row, "epoch") == source(op).getEpoch() + 1);
          var pool =
              store.one(
                  "SELECT * FROM shared_dsh_pool WHERE tenant_id=? AND id=?",
                  tenant(),
                  op.get("target_pool"));
          inputs.requireMember(tenant(), (UUID) row.get("member_id"));
          live(
              Set.of("pending", "ready").contains(pool.get("state").toString())
                  && pool.get("authorization_digest")
                      .equals(
                          inputs.authorizationSetDigest(tenant(), (UUID) row.get("member_id"))));
          currentResources(target(op, row), pool);
          if (!entry.ready(target(op, row))) return false;
          store.jdbc.update(
              "UPDATE shared_dsh_pool SET state='ready' WHERE tenant_id=? AND id=?",
              tenant(),
              op.get("target_pool"));
          int changed =
              store.jdbc.update(
                  "UPDATE shared_dsh_placement SET pool_id=?,mode=?,state='ready' WHERE tenant_id=?"
                      + " AND id=? AND epoch=? AND revision=? AND state='switching'",
                  op.get("target_pool"),
                  op.get("target_mode"),
                  tenant(),
                  row.get("id"),
                  number(row, "epoch"),
                  number(row, "revision"));
          conflict(changed == 1);
          store.jdbc.update(
              "UPDATE shared_dsh_operation SET state='succeeded',failure_code=NULL WHERE"
                  + " tenant_id=? AND id=?",
              tenant(),
              id);
          if (op.get("purpose").equals("maintenance")) {
            store.jdbc.update(
                "UPDATE instance SET"
                    + " deleted=true,ready=false,purge_data=false,deletion_event_sent=false WHERE"
                    + " tenant_id=? AND id IN (SELECT legacy_instance_id FROM"
                    + " shared_dsh_maintenance WHERE tenant_id=? AND operation_id=?)",
                tenant(),
                tenant(),
                id);
            store.jdbc.update(
                "UPDATE shared_dsh_maintenance SET state='succeeded' WHERE tenant_id=? AND"
                    + " operation_id=?",
                tenant(),
                id);
          }
          audit(operation(id), "succeeded", null);
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
                    "SELECT * FROM shared_dsh_outbox WHERE tenant_id=? AND NOT delivered ORDER BY"
                        + " created_at,id LIMIT 100",
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
          if (!requested.getTenantId().equals(tenant().toString()))
            throw new AurigaException(CommonErrors.NOT_FOUND);
          var row = member(UUID.fromString(requested.getMemberId()));
          matched(
              binding(row).equals(requested)
                  && requested.getTenantId().equals(tenant().toString()));
          inputs.requireMember(tenant(), UUID.fromString(requested.getMemberId()));
          var view = inputs.resourceView(requested);
          valid(
              view.getSchemaVersion() == 1
                  && view.getBinding().equals(requested)
                  && view.getSource().equals("platform"));
          UUID viewId = UUID.fromString(view.getViewId());
          live(
              view.getAuthorizationSetDigest()
                      .equals(
                          inputs.authorizationSetDigest(
                              tenant(), UUID.fromString(requested.getMemberId())))
                  && view.getResourcesList().stream().allMatch(r -> r.getExpiresAt() > now()));
          store.jdbc.update(
              "INSERT INTO shared_dsh_resource_view(tenant_id,placement_id,view_id,payload)"
                  + " VALUES(?,?,?,?) ON CONFLICT(tenant_id,placement_id) DO UPDATE SET"
                  + " view_id=excluded.view_id,payload=excluded.payload,report=NULL",
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
          if (!b.getTenantId().equals(tenant().toString()))
            throw new AurigaException(CommonErrors.NOT_FOUND);
          var row = member(UUID.fromString(b.getMemberId()));
          matched(binding(row).equals(b) && b.getTenantId().equals(tenant().toString()));
          var expected =
              store.one(
                  "SELECT * FROM shared_dsh_resource_view WHERE tenant_id=? AND placement_id=?",
                  tenant(),
                  row.get("id"));
          matched(expected.get("view_id").toString().equals(report.getViewId()));
          if (report.getStatus().equals("ready")) {
            var live = inputs.resourceView(b);
            live(
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
          emit("placement.resource_reported", report);
          if (!report.getStatus().equals("ready")) {
            String reason =
                switch (report.getReason()) {
                  case "revoked", "authorization-mismatch" -> "resource-revoked";
                  case "cleanup-unconfirmed" -> "credential-stop-unconfirmed";
                  default -> "resource-expired";
                };
            requestStop(UuidV7.next(), b, reason);
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
