package auriga.workspace.app;

import static auriga.workspace.domain.WorkspaceErrors.*;
import static auriga.workspace.persistence.WorkspaceStore.*;

import auriga.common.error.*;
import auriga.common.persistence.UuidV7;
import auriga.contracts.node.v1.*;
import auriga.workspace.api.WorkspaceInputs;
import auriga.workspace.persistence.WorkspaceStore;
import java.util.*;
import tools.jackson.databind.node.ObjectNode;

/** Management use cases; transport adapters never issue SQL. */
public final class WorkspaceAdmin {
  private final WorkspaceService service;
  private final WorkspaceStore store;
  private final WorkspaceInputs inputs;

  public WorkspaceAdmin(WorkspaceService service, WorkspaceInputs inputs) {
    this.service = service;
    this.store = service.store;
    this.inputs = inputs;
  }

  public Map<String, Object> patch(UUID id, ObjectNode body, String ifMatch) {
    var patch =
        WorkspaceService.JSON.treeToValue(
            body, auriga.workspace.web.model.UpdateInstanceRequest.class);
    return store.tx(
        () -> {
          store.lock();
          var row = store.instance(id);
          var previous = WorkspaceService.spec(row);
          if (ifMatch != null && !ifMatch.equals("\"" + previous.getGeneration() + "\""))
            throw new AurigaException(CommonErrors.PRECONDITION_FAILED);
          var next = previous.toBuilder();
          var quota = inputs.quota(store.tenant(), (UUID) row.get("owner_id"));
          if (patch.getAlwaysOn() != null) {
            if (patch.getAlwaysOn() && !quota.getAlwaysOnAllowed())
              throw new AurigaException(NOT_ALLOWED);
            store.jdbc.update(
                "UPDATE instance SET always_on=? WHERE tenant_id=? AND id=?",
                patch.getAlwaysOn(),
                store.tenant(),
                id);
          }
          if (body.has("dshVersionPin")) {
            String version = patch.getDshVersionPin();
            if (version == null)
              version =
                  service.defaultVersion(inputs.policy(store.tenant()).getDefaultDshVersion());
            var template =
                service.templates.get(
                    version,
                    previous.getRuntime(),
                    previous.getDsh().getReleaseEnvelope().getOs(),
                    previous.getDsh().getReleaseEnvelope().getArch());
            next.setDsh(template.getDsh());
            store.jdbc.update(
                "UPDATE instance SET pinned_version=? WHERE tenant_id=? AND id=?",
                patch.getDshVersionPin(),
                store.tenant(),
                id);
          }
          if (patch.getResourcesOverride() != null) {
            var override = patch.getResourcesOverride();
            var resources = previous.getResources().toBuilder();
            if (override.getCpuMillis() != null) resources.setCpuMillis(override.getCpuMillis());
            if (override.getMemoryMb() != null) resources.setMemoryMb(override.getMemoryMb());
            if (override.getDiskGb() != null) resources.setDiskGb(override.getDiskGb());
            valid(
                resources.getCpuMillis() >= 100
                    && resources.getMemoryMb() >= 256
                    && resources.getDiskGb() >= 1);
            if (quota.hasCpuMillis() && resources.getCpuMillis() > quota.getCpuMillis()
                || quota.hasMemoryMb() && resources.getMemoryMb() > quota.getMemoryMb()
                || quota.hasDiskGb() && resources.getDiskGb() > quota.getDiskGb())
              throw new AurigaException(NOT_ALLOWED);
            if (row.get("node_id") != null) {
              var node = store.node((UUID) row.get("node_id"));
              if (node.get("hello") != null)
                try {
                  var capacity = Hello.parseFrom((byte[]) node.get("hello")).getCapacity();
                  long cpu = resources.getCpuMillis(),
                      memory = resources.getMemoryMb(),
                      disk = resources.getDiskGb();
                  for (var sibling :
                      store.jdbc.queryForList(
                          "SELECT * FROM instance WHERE tenant_id=? AND node_id=? AND id<>? AND (NOT deleted OR phase<>'absent')",
                          store.tenant(),
                          row.get("node_id"),
                          id)) {
                    var reservation = WorkspaceService.spec(sibling).getResources();
                    cpu = Math.addExact(cpu, reservation.getCpuMillis());
                    memory = Math.addExact(memory, reservation.getMemoryMb());
                    disk = Math.addExact(disk, reservation.getDiskGb());
                  }
                  if (cpu > capacity.getCpuMillis()
                      || memory > capacity.getMemoryMb()
                      || disk > capacity.getDiskGb()) throw new AurigaException(ADMISSION_FULL);
                } catch (com.google.protobuf.InvalidProtocolBufferException e) {
                  throw new IllegalStateException(e);
                }
            }
            next.setResources(resources);
            store.jdbc.update(
                "UPDATE instance SET resources_override=? WHERE tenant_id=? AND id=?",
                resources.build().toByteArray(),
                store.tenant(),
                id);
          }
          if (body.has("nodeId")) {
            if (patch.getNodeId() != null) store.node(patch.getNodeId());
            store.jdbc.update(
                "UPDATE instance SET requested_node_id=?,migration_pending=? WHERE tenant_id=? AND id=?",
                patch.getNodeId(),
                patch.getNodeId() != null && !patch.getNodeId().equals(row.get("node_id")),
                store.tenant(),
                id);
          }
          if (!next.build().equals(previous))
            service.saveSpec(row, next.setGeneration(previous.getGeneration() + 1).build());
          return store.instance(id);
        });
  }

  public void delete(UUID id, boolean purge, UUID actor) {
    store.tx(
        () -> {
          store.lock();
          var row = store.instance(id);
          var previous = WorkspaceService.spec(row);
          service.saveSpec(
              row,
              previous.toBuilder()
                  .setGeneration(previous.getGeneration() + 1)
                  .setDesired(DesiredState.DESIRED_STATE_STOPPED)
                  .build());
          store.jdbc.update(
              "UPDATE workspace_job SET done=true WHERE tenant_id=? AND instance_id=? AND NOT done",
              store.tenant(),
              id);
          store.jdbc.update(
              "UPDATE instance SET deleted=true,purge_data=?,ready=false,phase=CASE WHEN node_id IS NULL THEN 'absent' ELSE 'deleting' END,deletion_actor=?,deletion_event_sent=(node_id IS NULL),updated_at=? WHERE tenant_id=? AND id=?",
              purge,
              actor,
              time(service.clock.instant()),
              store.tenant(),
              id);
          if (row.get("node_id") != null) {
            long generation = previous.getGeneration() + 1;
            var command =
                DeleteInstance.newBuilder()
                    .setInstanceId(id.toString())
                    .setGeneration(generation)
                    .setPurgeData(purge)
                    .build();
            store.jdbc.update(
                "INSERT INTO workspace_job(id,tenant_id,instance_id,kind,due_at,command,generation) VALUES(?,?,?,'delete',?,?,?)",
                UuidV7.next(service.clock),
                store.tenant(),
                id,
                time(service.clock.instant()),
                command.toByteArray(),
                generation);
          }
          if (row.get("node_id") == null)
            service.publish(
                "instance.deleted",
                store.one("SELECT * FROM instance WHERE tenant_id=? AND id=?", store.tenant(), id),
                Map.of("dataPurged", purge, "actorId", actor.toString()));
          return null;
        });
  }

  public Map<String, Object> node(
      UUID id, auriga.workspace.web.model.UpdateNodeRequest patch, boolean drain) {
    return store.tx(
        () -> {
          store.lock();
          store.node(id);
          if (patch != null) {
            if (patch.getName() != null) {
              valid(patch.getName().length() <= 64);
              store.jdbc.update(
                  "UPDATE node SET name=? WHERE tenant_id=? AND id=?",
                  patch.getName(),
                  store.tenant(),
                  id);
            }
            if (patch.getLabels() != null)
              store.jdbc.update(
                  "UPDATE node SET labels=?::jsonb WHERE tenant_id=? AND id=?",
                  WorkspaceService.JSON.writeValueAsString(patch.getLabels()),
                  store.tenant(),
                  id);
            if (patch.getSchedulable() != null)
              store.jdbc.update(
                  "UPDATE node SET schedulable=? WHERE tenant_id=? AND id=?",
                  patch.getSchedulable(),
                  store.tenant(),
                  id);
          }
          if (drain) {
            store.jdbc.update(
                "UPDATE node SET draining=true,schedulable=false WHERE tenant_id=? AND id=?",
                store.tenant(),
                id);
            store.jdbc.update(
                "UPDATE instance SET migration_pending=true WHERE tenant_id=? AND node_id=?",
                store.tenant(),
                id);
          }
          return store.node(id);
        });
  }

  public void revoke(UUID id) {
    revoke(id, false);
  }

  public void revoke(UUID id, boolean force) {
    store.tx(
        () -> {
          store.lock();
          // Same lock order as a session: lease first, then node/instances.
          store.jdbc.update(
              "DELETE FROM node_session WHERE tenant_id=? AND node_id=?", store.tenant(), id);
          store.node(id);
          if (!force
              && store.jdbc.queryForObject(
                      "SELECT count(*) FROM instance WHERE tenant_id=? AND node_id=? AND (NOT deleted OR phase<>'absent')",
                      Long.class,
                      store.tenant(),
                      id)
                  > 0) throw new AurigaException(NODE_NOT_EMPTY);
          store.jdbc.update(
              "UPDATE node SET revoked=true,schedulable=false WHERE tenant_id=? AND id=?",
              store.tenant(),
              id);
          int affected =
              force
                  ? store.jdbc.update(
                      "UPDATE instance SET migration_pending=true,ready=false WHERE tenant_id=? AND node_id=? AND NOT deleted",
                      store.tenant(),
                      id)
                  : 0;
          service.audit(
              auriga.common.audit.AuditEntry.builder("workspace.node.revoke")
                  .target("node", id.toString())
                  .detail("force", force)
                  .detail("instancesPendingMigration", affected)
                  .build());
          service.nodeRevokedAfterCommit(id);
          return null;
        });
  }

  public void defaultRuntime(String version) {
    service.templates.get(version, null, null, null);
    store.tx(
        () -> {
          store.lock();
          store.jdbc.update(
              "INSERT INTO workspace_setting(id,tenant_id,default_version) VALUES(?,?,?) ON CONFLICT(tenant_id) DO UPDATE SET default_version=excluded.default_version",
              UuidV7.next(service.clock),
              store.tenant(),
              version);
          for (var row :
              store.jdbc.queryForList(
                  "SELECT * FROM instance WHERE tenant_id=? AND pinned_version IS NULL AND NOT deleted FOR UPDATE",
                  store.tenant())) {
            var spec = WorkspaceService.spec(row);
            var template =
                service.templates.get(
                    version,
                    spec.getRuntime(),
                    spec.getDsh().getReleaseEnvelope().getOs(),
                    spec.getDsh().getReleaseEnvelope().getArch());
            if (!spec.getDsh().equals(template.getDsh()))
              service.saveSpec(
                  row,
                  spec.toBuilder()
                      .setDsh(template.getDsh())
                      .setGeneration(spec.getGeneration() + 1)
                      .build());
          }
          return null;
        });
  }
}
