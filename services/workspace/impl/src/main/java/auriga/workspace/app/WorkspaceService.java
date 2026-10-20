package auriga.workspace.app;

import static auriga.workspace.domain.WorkspaceErrors.*;
import static auriga.workspace.persistence.WorkspaceStore.*;

import auriga.common.context.TenantContext;
import auriga.common.error.*;
import auriga.common.events.EventPublisher;
import auriga.common.persistence.UuidV7;
import auriga.common.security.AurigaPrincipal;
import auriga.contracts.node.v1.*;
import auriga.contracts.org.v1.InstanceQuota;
import auriga.contracts.org.v1.MemberStatus;
import auriga.workspace.api.WorkspaceInputs;
import auriga.workspace.domain.*;
import auriga.workspace.persistence.WorkspaceStore;
import java.nio.charset.StandardCharsets;
import java.time.Clock;
import java.time.Duration;
import java.time.Instant;
import java.util.*;
import org.springframework.transaction.support.TransactionSynchronization;
import org.springframework.transaction.support.TransactionSynchronizationManager;
import tools.jackson.databind.json.JsonMapper;

/** Transactional orchestration: changes desired specifications; only the node executes them. */
public final class WorkspaceService {
  /** Wake jobs fire this long before the alarm so DSH is ready when it runs. */
  static final Duration WAKE_LEAD = Duration.ofSeconds(120);

  public static final JsonMapper JSON = JsonMapper.builder().build();
  public final WorkspaceStore store;
  private final WorkspaceInputs inputs;
  public final WorkspaceTemplates templates;
  private final WorkspaceCrypto crypto;
  private final EventPublisher events;
  private final auriga.common.audit.AuditLogger audits;
  private volatile java.util.function.Consumer<UUID> revoked = node -> {};
  public final Clock clock;
  private volatile Runnable changed = () -> {};

  public WorkspaceService(
      WorkspaceStore store,
      WorkspaceInputs inputs,
      WorkspaceTemplates templates,
      WorkspaceCrypto crypto,
      EventPublisher events,
      Clock clock) {
    this.store = store;
    this.inputs = inputs;
    this.templates = templates;
    this.crypto = crypto;
    this.events = events;
    this.audits = new auriga.common.audit.OutboxAuditLogger(events);
    this.clock = clock;
  }

  public void onChanged(Runnable changed) {
    this.changed = changed;
  }

  public void onNodeRevoked(java.util.function.Consumer<UUID> listener) {
    revoked = listener;
  }

  public void audit(auriga.common.audit.AuditEntry entry) {
    audits.record(entry);
  }

  public void nodeRevokedAfterCommit(UUID node) {
    TransactionSynchronizationManager.registerSynchronization(
        new TransactionSynchronization() {
          @Override
          public void afterCommit() {
            revoked.accept(node);
          }
        });
  }

  public NodeEnrollment.Token joinToken(
      NodeEnrollment enrollment, String name, int minutes, UUID actor, String key) {
    if (key == null) return enrollment.createToken(name, minutes, actor);
    valid(!key.isBlank() && key.length() <= 256);
    String aad =
        store.tenant()
            + "/"
            + actor
            + "/join/"
            + HexFormat.of().formatHex(WorkspaceCrypto.hash(key));
    byte[] hash = WorkspaceCrypto.hash(JSON.writeValueAsBytes(Arrays.asList(name, minutes)));
    var existing = store.tx(() -> savedJoinToken(actor, key, hash, aad));
    if (existing != null) return existing;
    var prepared = enrollment.prepareToken(name, minutes, actor);
    return store.tx(
        () -> {
          store.lock();
          var saved = savedJoinToken(actor, key, hash, aad);
          if (saved != null) return saved;
          var token = enrollment.saveToken(prepared);
          store.jdbc.update(
              "INSERT INTO workspace_request(id,tenant_id,actor_id,request_key,request_hash,response,expires_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(tenant_id,actor_id,request_key) DO UPDATE SET request_hash=excluded.request_hash,response=excluded.response,expires_at=excluded.expires_at",
              UuidV7.next(clock),
              store.tenant(),
              actor,
              WorkspaceCrypto.hash(key),
              hash,
              crypto.encrypt(JSON.writeValueAsBytes(token), aad),
              time(clock.instant().plusSeconds(86400)));
          return token;
        });
  }

  private NodeEnrollment.Token savedJoinToken(UUID actor, String key, byte[] hash, String aad) {
    var rows =
        store.jdbc.queryForList(
            "SELECT * FROM workspace_request WHERE tenant_id=? AND actor_id=? AND request_key=? AND expires_at>?",
            store.tenant(),
            actor,
            WorkspaceCrypto.hash(key),
            time(clock.instant()));
    if (rows.isEmpty()) return null;
    var row = rows.getFirst();
    if (!java.security.MessageDigest.isEqual(hash, (byte[]) row.get("request_hash")))
      throw new AurigaException(CommonErrors.CONFLICT);
    return JSON.readValue(
        crypto.decrypt((byte[]) row.get("response"), aad), NodeEnrollment.Token.class);
  }

  private void notifyCommitted() {
    TransactionSynchronizationManager.registerSynchronization(
        new TransactionSynchronization() {
          @Override
          public void afterCommit() {
            changed.run();
          }
        });
  }

  public static InstanceSpec spec(Map<String, Object> row) {
    try {
      return InstanceSpec.parseFrom((byte[]) row.get("spec"));
    } catch (Exception e) {
      throw new IllegalStateException("Invalid stored instance specification", e);
    }
  }

  public record Open(Map<String, Object> instance, boolean created) {}

  public Open open(UUID owner) {
    UUID tenant = store.tenant();
    store.register(tenant, "tenant", tenant);
    var member = inputs.member(tenant, owner);
    if (member.getStatus() != MemberStatus.MEMBER_STATUS_ACTIVE)
      throw new AurigaException(NOT_ALLOWED);
    var quota = inputs.quota(tenant, owner);
    var policy = inputs.policy(tenant);
    if (quota.hasMaxInstances() && quota.getMaxInstances() == 0)
      throw new AurigaException(NOT_ALLOWED);
    var ids =
        store.tx(
            () ->
                store.jdbc.queryForList(
                    "SELECT id FROM instance WHERE tenant_id=? AND owner_id=? AND NOT deleted",
                    UUID.class,
                    tenant,
                    owner));
    UUID id = ids.isEmpty() ? UuidV7.next(clock) : ids.getFirst();
    store.register(id, "instance", tenant);
    return store.tx(
        () -> {
          store.lock();
          var existing =
              store.jdbc.queryForList(
                  "SELECT * FROM instance WHERE tenant_id=? AND owner_id=? AND NOT deleted FOR UPDATE",
                  tenant,
                  owner);
          if (!existing.isEmpty()) {
            var row = existing.getFirst();
            desire(row, true, "user", policy.getMaxConcurrentStarts());
            return new Open(store.instance((UUID) row.get("id")), false);
          }
          // A concurrent delete invalidated the preflight ID; retry with a fresh route.
          if (!ids.isEmpty()) throw new AurigaException(CommonErrors.CONFLICT);
          admission(policy.getMaxConcurrentStarts());
          InstanceSpec base =
              templates.get(defaultVersion(policy.getDefaultDshVersion()), null, null, null);
          Resources resources = resources(base.getResources(), quota);
          var built =
              base.toBuilder()
                  .setInstanceId(id.toString())
                  .setTenantId(tenant.toString())
                  .setOwnerId(owner.toString())
                  .setGeneration(1)
                  .setResources(resources)
                  .setDesired(DesiredState.DESIRED_STATE_RUNNING)
                  .build();
          byte[] token =
              ("ait_"
                      + id
                      + "."
                      + Base64.getUrlEncoder()
                          .withoutPadding()
                          .encodeToString(WorkspaceCrypto.random(32)))
                  .getBytes(StandardCharsets.UTF_8);
          store.jdbc.update(
              "INSERT INTO instance(id,tenant_id,owner_id,name,spec,generation,token_hash,token_encrypted,last_activity_at,created_at,updated_at) VALUES(?,?,?,?,?,1,?,?,?,?,?)",
              id,
              tenant,
              owner,
              member.getDisplayName(),
              built.toByteArray(),
              WorkspaceCrypto.hash(token),
              crypto.encrypt(token, tenant + "/" + id),
              time(clock.instant()),
              time(clock.instant()),
              time(clock.instant()));
          var row = store.instance(id);
          schedule(row);
          row = store.instance(id);
          saveSpec(row, spec(row));
          publish(
              "instance.created",
              row,
              Map.of(
                  "runtime",
                  runtime(spec(row)),
                  "dshVersion",
                  spec(row).getDsh().getVersion(),
                  "actorId",
                  owner.toString()));
          notifyCommitted();
          return new Open(row, true);
        });
  }

  private static Resources resources(Resources base, InstanceQuota quota) {
    var result = base.toBuilder();
    if (quota.hasCpuMillis())
      result.setCpuMillis(Math.min(base.getCpuMillis(), quota.getCpuMillis()));
    if (quota.hasMemoryMb()) result.setMemoryMb(Math.min(base.getMemoryMb(), quota.getMemoryMb()));
    if (quota.hasDiskGb()) result.setDiskGb(Math.min(base.getDiskGb(), quota.getDiskGb()));
    if (result.getCpuMillis() < 100 || result.getMemoryMb() < 256 || result.getDiskGb() < 1)
      throw new AurigaException(NOT_ALLOWED);
    return result.build();
  }

  private void admission(int maximum) {
    // Check desired via protobuf, rather than inventing a second representation in the database.
    long count =
        store
            .jdbc
            .queryForList(
                "SELECT * FROM instance WHERE tenant_id=? AND NOT deleted AND NOT ready AND phase NOT IN ('failed','running','pending_restart')",
                store.tenant())
            .stream()
            .filter(r -> spec(r).getDesired() == DesiredState.DESIRED_STATE_RUNNING)
            .count();
    if (count >= maximum) throw new AurigaException(ADMISSION_FULL);
  }

  private void schedule(Map<String, Object> row) {
    if (row.get("node_id") != null) return;
    var spec = spec(row);
    var nodes =
        store.jdbc.queryForList(
            "SELECT * FROM node WHERE tenant_id=? AND NOT revoked AND schedulable AND NOT draining AND last_seen_at>? ORDER BY id FOR UPDATE",
            store.tenant(),
            time(clock.instant().minusSeconds(60)));
    var skipped = new java.util.ArrayList<String>();
    for (var node : nodes) {
      try {
        if (node.get("hello") == null) {
          skipped.add(node.get("id") + ": no hello");
          continue;
        }
        Hello hello = Hello.parseFrom((byte[]) node.get("hello"));
        if (!hello.getDriversList().contains(spec.getRuntime())) {
          skipped.add(node.get("id") + ": runtime " + spec.getRuntime() + " not offered");
          continue;
        }
        var labels = JSON.readTree(node.get("labels").toString());
        if (spec.getLabelsMap().entrySet().stream()
            .anyMatch(e -> !e.getValue().equals(labels.path(e.getKey()).asText()))) {
          skipped.add(node.get("id") + ": labels " + spec.getLabelsMap() + " not matched");
          continue;
        }
        var current =
            store.jdbc.queryForList(
                "SELECT * FROM instance WHERE tenant_id=? AND node_id=? AND (NOT deleted OR phase<>'absent')",
                store.tenant(),
                node.get("id"));
        long cpu = 0, mem = 0, disk = 0;
        for (var assigned : current) {
          var r = spec(assigned).getResources();
          cpu = Math.addExact(cpu, r.getCpuMillis());
          mem = Math.addExact(mem, r.getMemoryMb());
          disk = Math.addExact(disk, r.getDiskGb());
        }
        var c = hello.getCapacity();
        var r = spec.getResources();
        if (c.hasMaxInstances() && current.size() >= c.getMaxInstances()
            || cpu > c.getCpuMillis() - r.getCpuMillis()
            || mem > c.getMemoryMb() - r.getMemoryMb()
            || disk > c.getDiskGb() - r.getDiskGb()) {
          skipped.add(node.get("id") + ": no capacity");
          continue;
        }
        var template =
            templates.get(
                spec.getDsh().getVersion(), spec.getRuntime(), hello.getOs(), hello.getArch());
        long history =
            store.jdbc.queryForObject(
                "SELECT count(*) FROM instance_spec WHERE tenant_id=? AND instance_id=?",
                Long.class,
                store.tenant(),
                row.get("id"));
        var assigned =
            spec.toBuilder()
                .setDsh(template.getDsh())
                .setGeneration(history > 0 ? spec.getGeneration() + 1 : spec.getGeneration());
        // Preserve the configured signed plugin list and seal instance credentials for this node.
        assigned.clearPlugins().addAllPlugins(template.getPluginsList());
        byte[] token =
            crypto.decrypt(
                (byte[]) row.get("token_encrypted"), store.tenant() + "/" + row.get("id"));
        assigned.setSecrets(
            spec.getSecrets().toBuilder()
                .setInstanceToken(
                    WorkspaceCrypto.seal(
                        (byte[]) node.get("sealing_key"),
                        spec.getInstanceId(),
                        "instance_token",
                        assigned.getGeneration(),
                        token)));
        store.jdbc.update(
            "UPDATE instance SET node_id=?,spec=? WHERE tenant_id=? AND id=?",
            node.get("id"),
            assigned.build().toByteArray(),
            store.tenant(),
            row.get("id"));
        if (history > 0) saveSpec(store.instance((UUID) row.get("id")), assigned.build());
        return;
      } catch (com.google.protobuf.InvalidProtocolBufferException e) {
        throw new IllegalStateException(e);
      } catch (AurigaException e) {
        if (e.errorCode() != CommonErrors.UNAVAILABLE) throw e;
        skipped.add(node.get("id") + ": no runtime template (" + e.getMessage() + ")");
      }
    }
    org.slf4j.LoggerFactory.getLogger(getClass())
        .info(
            "instance {} not scheduled: {} candidate node(s) seen in the last 60s; skipped {}",
            row.get("id"),
            nodes.size(),
            skipped);
  }

  public void saveSpec(Map<String, Object> row, InstanceSpec next) {
    var previous = spec(row);
    boolean restart =
        (boolean) row.get("restart_pending")
            || ("running".equals(row.get("phase"))
                && (!previous.getDsh().equals(next.getDsh())
                    || !previous.getResources().equals(next.getResources())
                    || !previous.getPluginsList().equals(next.getPluginsList())
                    || !previous.getProfile().equals(next.getProfile())));
    if (row.get("node_id") != null) {
      var node = store.node((UUID) row.get("node_id"));
      byte[] token =
          crypto.decrypt((byte[]) row.get("token_encrypted"), store.tenant() + "/" + row.get("id"));
      next =
          next.toBuilder()
              .setSecrets(
                  next.getSecrets().toBuilder()
                      .setInstanceToken(
                          WorkspaceCrypto.seal(
                              (byte[]) node.get("sealing_key"),
                              next.getInstanceId(),
                              "instance_token",
                              next.getGeneration(),
                              token)))
              .build();
    }
    store.jdbc.update(
        "UPDATE instance SET spec=?,ready=CASE WHEN generation<>? THEN false ELSE ready END,generation=?,restart_pending=?,updated_at=? WHERE tenant_id=? AND id=?",
        next.toByteArray(),
        next.getGeneration(),
        next.getGeneration(),
        restart,
        time(clock.instant()),
        store.tenant(),
        row.get("id"));
    store.jdbc.update(
        "INSERT INTO instance_spec(id,tenant_id,instance_id,generation,spec,created_at) VALUES(?,?,?,?,?,?) ON CONFLICT(tenant_id,instance_id,generation) DO NOTHING",
        UuidV7.next(clock),
        store.tenant(),
        row.get("id"),
        next.getGeneration(),
        next.toByteArray(),
        time(clock.instant()));
  }

  private void desire(Map<String, Object> row, boolean running, String reason, int maximum) {
    var previous = spec(row);
    var desired = running ? DesiredState.DESIRED_STATE_RUNNING : DesiredState.DESIRED_STATE_STOPPED;
    if (previous.getDesired() == desired) return;
    if (running) admission(maximum);
    saveSpec(
        row,
        previous.toBuilder()
            .setDesired(desired)
            .setGeneration(previous.getGeneration() + 1)
            .build());
    store.jdbc.update(
        "UPDATE instance SET start_reason=CASE WHEN ? THEN ? ELSE start_reason END,stop_reason=CASE WHEN ? THEN stop_reason ELSE ? END,ready=false,last_activity_at=? WHERE tenant_id=? AND id=?",
        running,
        reason,
        running,
        reason,
        time(clock.instant()),
        store.tenant(),
        row.get("id"));
    schedule(store.instance((UUID) row.get("id")));
    notifyCommitted();
  }

  public Map<String, Object> owned(UUID id, AurigaPrincipal principal) {
    var row = store.instance(id);
    if (!row.get("owner_id").toString().equals(principal.subject())
        && !(principal.isAdminContext() && principal.hasPermission("workspace.instances.admin")))
      throw new AurigaException(CommonErrors.NOT_FOUND);
    return row;
  }

  public Map<String, Object> operate(UUID id, AurigaPrincipal actor, String action) {
    var policy = inputs.policy(store.tenant());
    return store.tx(
        () -> {
          store.lock();
          var row = owned(id, actor);
          if (action.equals("start") || action.equals("restart")) {
            var owner = (UUID) row.get("owner_id");
            var quota = inputs.quota(store.tenant(), owner);
            if (inputs.member(store.tenant(), owner).getStatus()
                    != MemberStatus.MEMBER_STATUS_ACTIVE
                || quota.hasMaxInstances() && quota.getMaxInstances() == 0)
              throw new AurigaException(NOT_ALLOWED);
          }
          if (action.equals("restart")) {
            desire(
                row,
                true,
                actor.isAdminContext() ? "admin" : "user",
                policy.getMaxConcurrentStarts());
            queueRestart(id, actor.isAdminContext() ? "admin" : "user");
            notifyCommitted();
          } else {
            desire(
                row,
                action.equals("start"),
                actor.isAdminContext() ? "admin" : "user",
                policy.getMaxConcurrentStarts());
          }
          return store.instance(id);
        });
  }

  private void queueRestart(UUID id, String reason) {
    long generation = ((Number) store.instance(id).get("generation")).longValue();
    // Automatic reconciliation must not reset the retry budget for the same generation.
    if (reason.equals("apply_pending")
        && store.jdbc.queryForObject(
                "SELECT count(*) FROM workspace_job WHERE tenant_id=? AND instance_id=? AND kind='restart' AND generation=?",
                Long.class,
                store.tenant(),
                id,
                generation)
            > 0) return;
    Command command =
        Command.newBuilder()
            .setCommandId(UuidV7.next(clock).toString())
            .setInstanceId(id.toString())
            .setRestart(
                RestartCommand.newBuilder()
                    .setGrace(com.google.protobuf.Duration.newBuilder().setSeconds(30))
                    .setReason(reason))
            .build();
    store.jdbc.update(
        "INSERT INTO workspace_job(id,tenant_id,instance_id,kind,due_at,command,generation) SELECT ?,?,?,'restart',?,?,? WHERE NOT EXISTS(SELECT 1 FROM workspace_job WHERE tenant_id=? AND instance_id=? AND kind='restart' AND NOT done)",
        UUID.fromString(command.getCommandId()),
        store.tenant(),
        id,
        time(clock.instant()),
        command.toByteArray(),
        generation,
        store.tenant(),
        id);
  }

  public void publish(String type, Map<String, Object> row, Map<String, Object> extra) {
    Map<String, Object> ref = new LinkedHashMap<>();
    ref.put("instanceId", row.get("id").toString());
    ref.put("ownerId", row.get("owner_id").toString());
    ref.put("nodeId", row.get("node_id") == null ? null : row.get("node_id").toString());
    ref.put("generation", ((Number) row.get("generation")).longValue());
    Map<String, Object> payload = new LinkedHashMap<>(extra);
    payload.put("instance", ref);
    events.publish(type, row.get("id").toString(), payload);
  }

  public static String runtime(InstanceSpec spec) {
    return spec.getRuntime().name().replace("RUNTIME_KIND_", "").toLowerCase(Locale.ROOT);
  }

  public String defaultVersion(String policyDefault) {
    var versions =
        store.jdbc.queryForList(
            "SELECT default_version FROM workspace_setting WHERE tenant_id=?",
            String.class,
            store.tenant());
    return versions.isEmpty() ? policyDefault : versions.getFirst();
  }

  public void status(UUID node, InstanceStatus status) {
    UUID id;
    try {
      id = UUID.fromString(status.getInstanceId());
    } catch (IllegalArgumentException e) {
      return;
    }
    store.tx(
        () -> {
          var rows =
              store.jdbc.queryForList(
                  "SELECT * FROM instance WHERE tenant_id=? AND id=? AND node_id=? FOR UPDATE",
                  store.tenant(),
                  id,
                  node);
          if (rows.isEmpty()) {
            org.slf4j.LoggerFactory.getLogger(getClass())
                .warn("Ignoring status for an unassigned instance");
            return null;
          }
          var row = rows.getFirst();
          long generation = ((Number) row.get("generation")).longValue();
          boolean pendingPrevious =
              status.getPhase() == InstancePhase.INSTANCE_PHASE_PENDING_RESTART
                  && status.getObservedGeneration()
                      == ((Number) row.get("observed_generation")).longValue();
          if (status.getObservedGeneration() != generation && !pendingPrevious) return null;
          String phase =
              status.getPhase().name().replace("INSTANCE_PHASE_", "").toLowerCase(Locale.ROOT);
          if (status.getPhase() == InstancePhase.UNRECOGNIZED
              || status.getPhase() == InstancePhase.INSTANCE_PHASE_UNSPECIFIED) return null;
          if (phase.equals("absent")
              && (boolean) row.get("deleted")
              && !(boolean) row.get("deletion_event_sent")) {
            Map<String, Object> data = new LinkedHashMap<>();
            data.put("dataPurged", row.get("purge_data"));
            data.put(
                "actorId",
                row.get("deletion_actor") == null ? null : row.get("deletion_actor").toString());
            publish("instance.deleted", row, data);
            store.jdbc.update(
                "UPDATE instance SET deletion_event_sent=true WHERE tenant_id=? AND id=?",
                store.tenant(),
                id);
          }
          boolean ready =
              status.getReady()
                  && spec(row).getDesired() == DesiredState.DESIRED_STATE_RUNNING
                  && status.getObservedGeneration() == generation
                  && status.getPhase() == InstancePhase.INSTANCE_PHASE_RUNNING;
          if (ready && !Set.of("running", "pending_restart").contains(row.get("phase"))) {
            publish("instance.started", row, Map.of("reason", row.get("start_reason")));
            store.jdbc.update(
                "UPDATE instance SET started_at=? WHERE tenant_id=? AND id=?",
                time(clock.instant()),
                store.tenant(),
                id);
          }
          if (phase.equals("stopped") && !phase.equals(row.get("phase"))) {
            Instant started = instant(row, "started_at");
            publish(
                "instance.stopped",
                row,
                Map.of(
                    "reason",
                    row.get("stop_reason"),
                    "runningSeconds",
                    started == null
                        ? 0
                        : Math.max(0, Duration.between(started, clock.instant()).toSeconds())));
          }
          if (phase.equals("failed")
              && status.getError().getAttempts() >= 5
              && !phase.equals(row.get("phase")))
            publish("instance.failed", row, Map.of("code", status.getError().getCode()));
          store.jdbc.update(
              "UPDATE instance SET phase=?,ready=?,observed_generation=?,status=?,restart_pending=?,updated_at=? WHERE tenant_id=? AND id=?",
              phase,
              ready,
              status.getObservedGeneration(),
              status.toByteArray(),
              phase.equals("pending_restart") || !ready && (boolean) row.get("restart_pending"),
              time(clock.instant()),
              store.tenant(),
              id);
          return null;
        });
  }

  public void tick() {
    for (UUID tenant : store.tenants()) {
      try {
        TenantContext.run(tenant, this::tickTenant);
      } catch (RuntimeException e) {
        org.slf4j.LoggerFactory.getLogger(getClass())
            .error("Cannot reconcile workspace tenant {}", tenant, e);
      }
    }
  }

  public void tickTenant() {
    UUID tenant = store.tenant();
    var policy = inputs.policy(tenant);
    store.tx(
        () -> {
          store.lock();
          Instant now = clock.instant();
          store.jdbc.update(
              "UPDATE instance SET migration_pending=true WHERE tenant_id=? AND node_id IN (SELECT id FROM node WHERE tenant_id=? AND (revoked OR last_seen_at<?))",
              tenant,
              tenant,
              time(now.minusSeconds(60)));
          for (var row :
              store.jdbc.queryForList(
                  "SELECT * FROM instance WHERE tenant_id=? AND NOT deleted ORDER BY id FOR UPDATE",
                  tenant)) {
            UUID owner = (UUID) row.get("owner_id");
            var quota = inputs.quota(tenant, owner);
            if (inputs.member(tenant, owner).getStatus() != MemberStatus.MEMBER_STATUS_ACTIVE
                || quota.hasMaxInstances() && quota.getMaxInstances() == 0) {
              desire(row, false, "user_disabled", policy.getMaxConcurrentStarts());
              continue;
            }
            var original = spec(row);
            Resources baseline = original.getResources();
            if (row.get("resources_override") != null)
              try {
                baseline = Resources.parseFrom((byte[]) row.get("resources_override"));
              } catch (Exception e) {
                throw new IllegalStateException(e);
              }
            else
              baseline =
                  templates
                      .get(
                          original.getDsh().getVersion(),
                          original.getRuntime(),
                          original.getDsh().getSigned().getStatement().getOs(),
                          original.getDsh().getSigned().getStatement().getArch())
                      .getResources();
            Resources effective = resources(baseline, quota);
            if (!original.getResources().equals(effective)) {
              saveSpec(
                  row,
                  original.toBuilder()
                      .setResources(effective)
                      .setGeneration(original.getGeneration() + 1)
                      .build());
              row = store.instance((UUID) row.get("id"));
            }
            if (row.get("node_id") == null) {
              schedule(row);
              row = store.instance((UUID) row.get("id"));
            }
            var s = spec(row);
            if ((boolean) row.get("restart_pending")
                && s.getDesired() == DesiredState.DESIRED_STATE_RUNNING
                && Lifecycle.idle(
                    now,
                    instant(row, "last_activity_at"),
                    instant(row, "plugin_seen_at"),
                    (boolean) row.get("plugin_active"),
                    instant(row, "cpu_seen_at"),
                    (boolean) row.get("cpu_active"),
                    false,
                    Duration.ofMinutes(policy.getIdleMinutes()))) {
              queueRestart((UUID) row.get("id"), "apply_pending");
              continue;
            }
            // Do not idle-stop an instance whose alarm is about to fire: the wake job
            // pre-warms WAKE_LEAD early and is consumed while the instance still runs.
            Instant nextWake = instant(row, "next_wake_at");
            // Only a future alarm counts; a stale next_wake_at must not pin the instance.
            boolean alarmSoon =
                nextWake != null
                    && nextWake.isAfter(now)
                    && !nextWake.isAfter(now.plus(WAKE_LEAD).plus(WAKE_LEAD));
            if (s.getDesired() == DesiredState.DESIRED_STATE_RUNNING
                && !alarmSoon
                && Lifecycle.idle(
                    now,
                    instant(row, "last_activity_at"),
                    instant(row, "plugin_seen_at"),
                    (boolean) row.get("plugin_active"),
                    instant(row, "cpu_seen_at"),
                    (boolean) row.get("cpu_active"),
                    (boolean) row.get("always_on"),
                    Duration.ofMinutes(policy.getIdleMinutes())))
              desire(row, false, "idle", policy.getMaxConcurrentStarts());
          }
          for (var job :
              store.jdbc.queryForList(
                  "SELECT * FROM workspace_job WHERE tenant_id=? AND kind='wake' AND NOT done AND due_at<=? ORDER BY due_at FOR UPDATE SKIP LOCKED LIMIT 100",
                  tenant,
                  time(now))) {
            var row = store.instance((UUID) job.get("instance_id"));
            var wakeLog = org.slf4j.LoggerFactory.getLogger(getClass());
            if (!Boolean.TRUE.equals(policy.getWakeOnSchedule()))
              wakeLog.info("wake job for {} dropped: policy wakeOnSchedule is off", row.get("id"));
            if (Boolean.TRUE.equals(policy.getWakeOnSchedule())) {
              UUID owner = (UUID) row.get("owner_id");
              var quota = inputs.quota(tenant, owner);
              var memberStatus = inputs.member(tenant, owner).getStatus();
              if (memberStatus != MemberStatus.MEMBER_STATUS_ACTIVE
                  || quota.hasMaxInstances() && quota.getMaxInstances() == 0) {
                wakeLog.info(
                    "wake job for {} dropped: member {} quota maxInstances {}",
                    row.get("id"),
                    memberStatus,
                    quota.hasMaxInstances() ? quota.getMaxInstances() : "unset");
                store.jdbc.update(
                    "UPDATE workspace_job SET done=true WHERE tenant_id=? AND id=?",
                    tenant,
                    job.get("id"));
                continue;
              }
              try {
                boolean wasStopped = spec(row).getDesired() != DesiredState.DESIRED_STATE_RUNNING;
                desire(row, true, "wake", policy.getMaxConcurrentStarts());
                if (wasStopped) wakeLog.info("wake job for {} started the instance", row.get("id"));
              } catch (AurigaException e) {
                if (e.errorCode() == ADMISSION_FULL) {
                  wakeLog.info("wake job for {} deferred: admission full", row.get("id"));
                  continue;
                }
                throw e;
              }
            }
            store.jdbc.update(
                "UPDATE workspace_job SET done=true WHERE tenant_id=? AND id=?",
                tenant,
                job.get("id"));
          }
          notifyCommitted();
          return null;
        });
  }

  public void wake(UUID node, WakeSchedule wake) {
    store.tx(
        () -> {
          UUID id = UUID.fromString(wake.getInstanceId());
          var rows =
              store.jdbc.queryForList(
                  "SELECT * FROM instance WHERE tenant_id=? AND id=? AND node_id=? AND NOT deleted FOR UPDATE",
                  store.tenant(),
                  id,
                  node);
          if (rows.isEmpty()) return null;
          Instant next =
              wake.hasNextWakeAt()
                  ? Instant.ofEpochSecond(
                      wake.getNextWakeAt().getSeconds(), wake.getNextWakeAt().getNanos())
                  : null;
          store.jdbc.update(
              "UPDATE instance SET next_wake_at=? WHERE tenant_id=? AND id=?",
              time(next),
              store.tenant(),
              id);
          store.jdbc.update(
              "UPDATE workspace_job SET done=true WHERE tenant_id=? AND instance_id=? AND kind='wake' AND NOT done",
              store.tenant(),
              id);
          if (next != null)
            store.jdbc.update(
                "INSERT INTO workspace_job(id,tenant_id,instance_id,kind,due_at) VALUES(?,?,?,'wake',?)",
                UuidV7.next(clock),
                store.tenant(),
                id,
                time(next.minus(WAKE_LEAD)));
          return null;
        });
  }

  public Map<String, Object> authenticateInstance(String token) {
    try {
      if (token == null || !token.startsWith("ait_") || token.length() > 256)
        throw new IllegalArgumentException();
      UUID id = UUID.fromString(token.substring(4, token.indexOf('.')));
      // The signed node template carries tenant identity; API authentication uses the registry.
      UUID tenant = store.route(id, "instance");
      return store.in(
          tenant,
          () -> {
            var row = store.instance(id);
            if (!java.security.MessageDigest.isEqual(
                (byte[]) row.get("token_hash"), WorkspaceCrypto.hash(token)))
              throw new AurigaException(CommonErrors.UNAUTHENTICATED);
            return row;
          });
    } catch (org.springframework.dao.DataAccessException
        | org.springframework.transaction.TransactionException e) {
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    } catch (AurigaException e) {
      if (e.errorCode() == CommonErrors.UNAVAILABLE) throw e;
      throw new AurigaException(CommonErrors.UNAUTHENTICATED);
    } catch (IllegalArgumentException e) {
      throw new AurigaException(CommonErrors.UNAUTHENTICATED);
    }
  }

  public void heartbeat(
      Map<String, Object> row, auriga.workspace.web.model.HeartbeatRequest request) {
    var activity = request.getActivity();
    boolean active =
        activity.getRunningTurns() > 0
            || activity.getOpenTerminals() > 0
            || activity.getActiveImBots() > 0
            || activity.getConnectedClients() > 0;
    store.tx(
        () -> {
          Instant now = clock.instant();
          Instant latest =
              Lifecycle.latestActivity(
                  instant(row, "last_activity_at"),
                  now,
                  active,
                  activity.getLastInteractionAt() == null
                      ? null
                      : activity.getLastInteractionAt().toInstant());
          store.jdbc.update(
              "UPDATE instance SET plugin_seen_at=?,plugin_active=?,last_activity_at=GREATEST(last_activity_at,?) WHERE tenant_id=? AND id=?",
              time(now),
              active,
              time(latest),
              store.tenant(),
              row.get("id"));
          return null;
        });
  }
}
