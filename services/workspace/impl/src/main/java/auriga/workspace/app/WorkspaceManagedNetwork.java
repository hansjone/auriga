package auriga.workspace.app;

import static auriga.workspace.persistence.WorkspaceStore.*;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.contracts.iam.v1.*;
import auriga.contracts.node.v1.DesiredState;
import auriga.iam.api.ManagedDeviceCredentials;
import auriga.workspace.domain.WorkspaceCrypto;
import java.nio.charset.StandardCharsets;
import java.util.*;
import tools.jackson.databind.node.ObjectNode;

/** Optional network provisioning. Remote calls never hold workspace row/advisory locks. */
public final class WorkspaceManagedNetwork {
  private final WorkspaceService service;
  private final WorkspaceCrypto crypto;
  private final ManagedDeviceCredentials devices;
  private final ObjectNode installation;
  private final WorkspaceManagedModels models;

  public WorkspaceManagedNetwork(
      WorkspaceService service,
      WorkspaceCrypto crypto,
      ManagedDeviceCredentials devices,
      ObjectNode installation) {
    this.service = service;
    this.crypto = crypto;
    this.devices = devices;
    this.installation = installation.deepCopy();
    this.models = new WorkspaceManagedModels(service, crypto, installation);
  }

  public void tick() {
    for (UUID tenant : service.store.tenants()) TenantContext.run(tenant, this::tickTenant);
  }

  public void tickTenant() {
    var store = service.store;
    var rows =
        store.tx(
            () ->
                store.jdbc.queryForList(
                    "WITH pending AS (SELECT id FROM instance WHERE tenant_id=? AND (managed_synced_version<>managed_credential_version OR managed_network_state IN ('ready','pending','deferred')) AND managed_retry_at<=? ORDER BY managed_retry_at,id LIMIT 32 FOR UPDATE SKIP LOCKED) UPDATE instance i SET managed_retry_at=? FROM pending p WHERE i.tenant_id=? AND i.id=p.id RETURNING i.*",
                    store.tenant(),
                    time(service.clock.instant()),
                    time(service.clock.instant().plusSeconds(30)),
                    store.tenant()));
    for (var row : rows) reconcile(row);
  }

  public void reconcile(Map<String, Object> row) {
    var store = service.store;
    UUID id = (UUID) row.get("id");
    long version = ((Number) row.get("managed_credential_version")).longValue();
    boolean running =
        !(boolean) row.get("deleted")
            && WorkspaceService.spec(row).getDesired() == DesiredState.DESIRED_STATE_RUNNING;
    try {
      if (!running) {
        devices.revoke(
            RevokeManagedDeviceRequest.newBuilder()
                .setTenantId(store.tenant().toString())
                .setInstanceId(id.toString())
                .setCredentialVersion(version)
                .build());
        complete(row, null, "stopped", null);
        return;
      }
      if (row.get("node_id") == null) return;
      var response =
          devices.issue(
              IssueManagedDeviceRequest.newBuilder()
                  .setTenantId(store.tenant().toString())
                  .setInstanceId(id.toString())
                  .setOwnerId(row.get("owner_id").toString())
                  .setCredentialVersion(version)
                  .build());
      try {
        models.reconcile(row, response);
      } catch (RuntimeException unavailable) {
        org.slf4j.LoggerFactory.getLogger(getClass())
            .warn("Managed model provisioning deferred instance={}", id);
      }
      if (Objects.equals(row.get("managed_synced_version"), row.get("managed_credential_version"))
          && row.get("managed_credential") != null) {
        if (!"ready".equals(row.get("managed_network_state"))) {
          store.tx(
              () -> {
                var current = lockCurrent(id, version);
                if (current != null)
                  store.jdbc.update(
                      "UPDATE instance SET managed_network_state='ready',managed_network_reason=NULL WHERE tenant_id=? AND id=?",
                      store.tenant(),
                      id);
                return null;
              });
          log(id, "ready", null);
        }
        return;
      }
      var file = installation.deepCopy();
      file.remove("modelBaseUrl");
      file.put("tenantId", store.tenant().toString());
      file.put("ownerId", row.get("owner_id").toString());
      file.put("instanceId", id.toString());
      file.put("deviceId", response.getDeviceId());
      file.put("credential", response.getCredential());
      file.put(
          "expiresAt",
          java.time.Instant.ofEpochSecond(response.getExpiresAt().getSeconds()).toString());
      complete(
          row,
          crypto.encrypt(
              file.toString().getBytes(StandardCharsets.UTF_8), store.tenant() + "/managed/" + id),
          "ready",
          null);
    } catch (RuntimeException failure) {
      String code =
          failure instanceof AurigaException e ? e.errorCode().code() : "common.unavailable";
      if (running && "network.revoked".equals(code)) {
        store.tx(
            () -> {
              var current = lockCurrent(id, version);
              if (current != null) service.managedSessionRevoked(current);
              return null;
            });
        log(id, "pending", "session_revoked");
      } else if (running && Set.of("common.forbidden", "common.not_found").contains(code)) {
        complete(row, null, "disabled", "permission_denied");
      } else {
        String state =
            store.tx(
                () -> {
                  var current = lockCurrent(id, version);
                  if (current == null) return null;
                  boolean released =
                      !service.clock.instant().isBefore(instant(current, "managed_wait_until"));
                  store.jdbc.update(
                      "UPDATE instance SET managed_network_state=?,managed_network_reason='issuer_unavailable',managed_restart_required=managed_restart_required OR ? WHERE tenant_id=? AND id=?",
                      released ? "deferred" : "pending",
                      released && current.get("managed_credential") == null,
                      store.tenant(),
                      id);
                  return released ? "deferred" : "pending";
                });
        if (state != null) log(id, state, "issuer_unavailable");
      }
    }
  }

  private Map<String, Object> lockCurrent(UUID id, long version) {
    var store = service.store;
    store.lock();
    var current =
        store.one(
            "SELECT * FROM instance WHERE tenant_id=? AND id=? FOR UPDATE", store.tenant(), id);
    return ((Number) current.get("managed_credential_version")).longValue() == version
        ? current
        : null;
  }

  private void complete(Map<String, Object> row, byte[] material, String state, String reason) {
    var store = service.store;
    UUID id = (UUID) row.get("id");
    long version = ((Number) row.get("managed_credential_version")).longValue();
    store.tx(
        () -> {
          var current = lockCurrent(id, version);
          if (current == null) return null;
          if (Objects.equals(
                  current.get("managed_synced_version"), current.get("managed_credential_version"))
              && state.equals(current.get("managed_network_state"))) return null;
          boolean refresh =
              material != null
                  && (Boolean.TRUE.equals(current.get("managed_restart_required"))
                      || current.get("managed_credential") == null
                          && !service
                              .clock
                              .instant()
                              .isBefore(instant(current, "managed_wait_until")));
          store.jdbc.update(
              "UPDATE instance SET managed_credential=?,managed_synced_version=?,managed_network_state=?,managed_network_reason=?,managed_restart_required=false WHERE tenant_id=? AND id=?",
              material,
              version,
              state,
              reason,
              store.tenant(),
              id);
          if (!(boolean) current.get("deleted")) {
            current = store.instance(id);
            var spec = WorkspaceService.spec(current);
            service.saveSpec(
                current, spec.toBuilder().setGeneration(spec.getGeneration() + 1).build());
            if (refresh) service.queueManagedMaterialRestart(id);
          }
          return null;
        });
    log(id, state, reason);
  }

  private void log(UUID id, String state, String reason) {
    org.slf4j.LoggerFactory.getLogger(getClass())
        .info("Managed network provisioning instance={} state={} reason={}", id, state, reason);
  }
}
