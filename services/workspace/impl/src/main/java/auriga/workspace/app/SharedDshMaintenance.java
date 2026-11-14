package auriga.workspace.app;

import static auriga.workspace.domain.SharedDshPolicy.*;

import auriga.common.error.*;
import auriga.common.persistence.UuidV7;
import auriga.contracts.node.v1.DesiredState;
import auriga.workspace.api.SharedDshAuthority.Inputs;
import auriga.workspace.persistence.WorkspaceStore;
import java.time.Clock;
import java.util.*;

/** Explicit export window and durable bulk rebuild. No implicit migration at first login. */
public final class SharedDshMaintenance {
  public record Choice(UUID member, UUID legacyInstance, boolean continueDedicated) {}

  public record Progress(
      UUID member,
      UUID legacyInstance,
      boolean continueDedicated,
      String state,
      UUID operation,
      String operationState,
      String action,
      UUID archive,
      Long archiveExpiresAt) {}

  private final WorkspaceStore store;
  private final SharedDshPlacements placements;
  private final Inputs inputs;
  private final Clock clock;

  public SharedDshMaintenance(
      WorkspaceStore store, SharedDshPlacements placements, Inputs inputs, Clock clock) {
    this.store = store;
    this.placements = placements;
    this.inputs = inputs;
    this.clock = clock;
  }

  private <T> T locked(java.util.function.Supplier<T> work) {
    return store.tx(
        () -> {
          store.lock();
          return work.get();
        });
  }

  private long now() {
    return clock.instant().getEpochSecond();
  }

  private void admin(UUID actor, UUID session) {
    inputs.requirePlacementWrite(store.tenant(), actor, session, now());
  }

  /**
   * Same batch and exact choices are idempotent. A changed choice requires a new reviewed batch.
   */
  public void plan(
      UUID actor, UUID adminSession, UUID batch, long exportUntil, List<Choice> choices) {
    valid(
        batch != null
            && exportUntil >= 0
            && exportUntil <= 9007199254740991L
            && choices != null
            && !choices.isEmpty());
    valid(new HashSet<>(choices.stream().map(Choice::member).toList()).size() == choices.size());
    locked(
        () -> {
          admin(actor, adminSession);
          var previous = rows(batch);
          if (!previous.isEmpty()) {
            conflict(previous.size() == choices.size());
            for (var choice : choices) {
              var old =
                  previous.stream()
                      .filter(r -> r.get("member_id").equals(choice.member()))
                      .findFirst()
                      .orElseThrow(() -> new AurigaException(CommonErrors.CONFLICT));
              conflict(
                  old.get("legacy_instance_id").equals(choice.legacyInstance())
                      && old.get("continue_dedicated").equals(choice.continueDedicated())
                      && ((Number) old.get("export_until")).longValue() == exportUntil);
            }
            return null;
          }
          conflict(exportUntil >= now());
          for (var choice : choices) {
            inputs.requireMember(store.tenant(), choice.member());
            var legacy = store.instance(choice.legacyInstance());
            if (!legacy.get("owner_id").equals(choice.member()))
              throw new AurigaException(CommonErrors.NOT_FOUND);
            conflict(!Boolean.TRUE.equals(legacy.get("shared_rebuild_hold")));
            conflict(
                store.jdbc.queryForObject(
                        "SELECT count(*) FROM shared_dsh_placement WHERE tenant_id=? AND member_id=?",
                        Long.class,
                        store.tenant(),
                        choice.member())
                    == 0);
            // One old physical execution per member; ambiguous legacy inventories require
            // correction first.
            conflict(
                store.jdbc.queryForObject(
                        "SELECT count(*) FROM instance WHERE tenant_id=? AND owner_id=? AND NOT deleted",
                        Long.class,
                        store.tenant(),
                        choice.member())
                    == 1);
            store.jdbc.update(
                "INSERT INTO shared_dsh_maintenance(tenant_id,member_id,continue_dedicated,batch_id,legacy_instance_id,export_until,planned_by,planned_at) VALUES(?,?,?,?,?,?,?,?)",
                store.tenant(),
                choice.member(),
                choice.continueDedicated(),
                batch,
                choice.legacyInstance(),
                exportUntil,
                actor,
                now());
          }
          return null;
        });
  }

  /** Starts only after the export window; restart/retry returns the persisted operation IDs. */
  public List<Progress> start(UUID actor, UUID adminSession, UUID batch) {
    return locked(
        () -> {
          admin(actor, adminSession);
          var rows = rows(batch);
          if (rows.isEmpty()) throw new AurigaException(CommonErrors.NOT_FOUND);
          for (var row : rows) {
            if (!row.get("state").equals("export-window")) continue;
            conflict(now() >= ((Number) row.get("export_until")).longValue());
            UUID member = (UUID) row.get("member_id"), id = (UUID) row.get("legacy_instance_id");
            inputs.requireMember(store.tenant(), member);
            var legacy = store.instance(id);
            if (!legacy.get("owner_id").equals(member))
              throw new AurigaException(CommonErrors.NOT_FOUND);
            var spec = WorkspaceService.spec(legacy);
            var stopped =
                spec.toBuilder()
                    .setGeneration(spec.getGeneration() + 1)
                    .setDesired(DesiredState.DESIRED_STATE_STOPPED)
                    .clearSecrets()
                    .build();
            store.jdbc.update(
                "UPDATE instance SET shared_rebuild_hold=true,spec=?,generation=?,ready=false,managed_credential=NULL,managed_model_credential=NULL,managed_credential_version=managed_credential_version+1 WHERE tenant_id=? AND id=?",
                stopped.toByteArray(),
                stopped.getGeneration(),
                store.tenant(),
                id);
            store.jdbc.update(
                "INSERT INTO instance_spec(id,tenant_id,instance_id,generation,spec,created_at) VALUES(?,?,?,?,?,?)",
                UuidV7.next(),
                store.tenant(),
                id,
                stopped.getGeneration(),
                stopped.toByteArray(),
                WorkspaceStore.time(clock.instant()));
            store.jdbc.update(
                "UPDATE workspace_job SET done=true WHERE tenant_id=? AND instance_id=? AND NOT done",
                store.tenant(),
                id);
            var digest = inputs.authorizationSetDigest(store.tenant(), member);
            valid(digest != null && digest.matches("sha256:[a-f0-9]{64}"));
            store.jdbc.update(
                "INSERT INTO shared_dsh_pool(id,tenant_id,mode,authorization_digest,state) VALUES(?,?,'dedicated',?,'ready')",
                id,
                store.tenant(),
                digest);
            store.jdbc.update(
                "INSERT INTO shared_dsh_placement(id,tenant_id,member_id,pool_id,mode,state) VALUES(?,?,?,?,'dedicated','ready')",
                UuidV7.next(),
                store.tenant(),
                member,
                id);
            UUID operation =
                placements.rebuild(
                    member,
                    actor,
                    Boolean.TRUE.equals(row.get("continue_dedicated")) ? "dedicated" : "shared",
                    batch);
            store.jdbc.update(
                "UPDATE shared_dsh_maintenance SET state='accepted',operation_id=? WHERE tenant_id=? AND member_id=?",
                operation,
                store.tenant(),
                member);
          }
          return progressRows(batch);
        });
  }

  public List<Progress> progress(UUID actor, UUID adminSession, UUID batch) {
    return locked(
        () -> {
          admin(actor, adminSession);
          if (rows(batch).isEmpty()) throw new AurigaException(CommonErrors.NOT_FOUND);
          return progressRows(batch);
        });
  }

  private List<Map<String, Object>> rows(UUID batch) {
    return store.jdbc.queryForList(
        "SELECT * FROM shared_dsh_maintenance WHERE tenant_id=? AND batch_id=? ORDER BY member_id",
        store.tenant(),
        batch);
  }

  private List<Progress> progressRows(UUID batch) {
    return store
        .jdbc
        .queryForList(
            "SELECT m.*,o.state AS operation_state,o.action,o.archive_id,o.archive_expires_at FROM shared_dsh_maintenance m LEFT JOIN shared_dsh_operation o ON o.tenant_id=m.tenant_id AND o.id=m.operation_id WHERE m.tenant_id=? AND m.batch_id=? ORDER BY m.member_id",
            store.tenant(),
            batch)
        .stream()
        .map(
            r ->
                new Progress(
                    (UUID) r.get("member_id"),
                    (UUID) r.get("legacy_instance_id"),
                    Boolean.TRUE.equals(r.get("continue_dedicated")),
                    "blocked".equals(r.get("operation_state"))
                        ? "blocked"
                        : r.get("state").toString(),
                    (UUID) r.get("operation_id"),
                    (String) r.get("operation_state"),
                    (String) r.get("action"),
                    (UUID) r.get("archive_id"),
                    (Long) r.get("archive_expires_at")))
        .toList();
  }
}
