package auriga.workspace.app;

import auriga.common.context.TenantContext;
import auriga.contracts.iam.v1.*;
import auriga.contracts.node.v1.DesiredState;
import auriga.iam.api.ManagedDeviceCredentials;
import auriga.workspace.domain.WorkspaceCrypto;
import java.nio.charset.StandardCharsets;
import java.util.*;
import tools.jackson.databind.node.ObjectNode;

/** Retryable post-commit provisioning. Network calls never hold workspace row/advisory locks. */
public final class WorkspaceManagedNetwork {
  private final WorkspaceService service;
  private final WorkspaceCrypto crypto;
  private final ManagedDeviceCredentials devices;
  private final ObjectNode installation;

  public WorkspaceManagedNetwork(
      WorkspaceService service,
      WorkspaceCrypto crypto,
      ManagedDeviceCredentials devices,
      ObjectNode installation) {
    this.service = service;
    this.crypto = crypto;
    this.devices = devices;
    this.installation = installation.deepCopy();
  }

  public void tick() {
    for (UUID tenant : service.store.tenants()) {
      TenantContext.run(tenant, this::tickTenant);
    }
  }

  public void tickTenant() {
    UUID tenant = service.store.tenant();
    var rows =
        service.store.tx(
            () ->
                service.store.jdbc.queryForList(
                    "WITH pending AS (SELECT id FROM instance WHERE tenant_id=? AND managed_synced_version<>managed_credential_version AND managed_retry_at<=? ORDER BY managed_retry_at,id LIMIT 32 FOR UPDATE SKIP LOCKED) UPDATE instance i SET managed_retry_at=? FROM pending p WHERE i.tenant_id=? AND i.id=p.id RETURNING i.*",
                    tenant,
                    auriga.workspace.persistence.WorkspaceStore.time(service.clock.instant()),
                    auriga.workspace.persistence.WorkspaceStore.time(
                        service.clock.instant().plusSeconds(30)),
                    tenant));
    for (var row : rows) {
      try {
        reconcile(row);
      } catch (RuntimeException unavailable) {
        // No remote response/secret or raw exception is logged. Next tick retries the same
        // version.
        org.slf4j.LoggerFactory.getLogger(getClass())
            .warn("Managed network provisioning deferred instance={}", row.get("id"));
      }
    }
  }

  public void reconcile(Map<String, Object> row) {
    var store = service.store;
    UUID id = (UUID) row.get("id");
    long version = ((Number) row.get("managed_credential_version")).longValue();
    boolean running =
        !(boolean) row.get("deleted")
            && WorkspaceService.spec(row).getDesired() == DesiredState.DESIRED_STATE_RUNNING;
    byte[] encrypted = null;
    if (running) {
      if (row.get("node_id") == null) return;
      var response =
          devices.issue(
              IssueManagedDeviceRequest.newBuilder()
                  .setTenantId(store.tenant().toString())
                  .setInstanceId(id.toString())
                  .setOwnerId(row.get("owner_id").toString())
                  .setCredentialVersion(version)
                  .build());
      var file = installation.deepCopy();
      file.put("tenantId", store.tenant().toString());
      file.put("ownerId", row.get("owner_id").toString());
      file.put("instanceId", id.toString());
      file.put("deviceId", response.getDeviceId());
      file.put("credential", response.getCredential());
      file.put(
          "expiresAt",
          java.time.Instant.ofEpochSecond(response.getExpiresAt().getSeconds()).toString());
      encrypted =
          crypto.encrypt(
              file.toString().getBytes(StandardCharsets.UTF_8), store.tenant() + "/managed/" + id);
    } else {
      devices.revoke(
          RevokeManagedDeviceRequest.newBuilder()
              .setTenantId(store.tenant().toString())
              .setInstanceId(id.toString())
              .setCredentialVersion(version)
              .build());
    }
    byte[] material = encrypted;
    store.tx(
        () -> {
          store.lock();
          var current =
              store.one(
                  "SELECT * FROM instance WHERE tenant_id=? AND id=? FOR UPDATE",
                  store.tenant(),
                  id);
          if (((Number) current.get("managed_credential_version")).longValue() != version
              || ((Number) current.get("managed_synced_version")).longValue() == version)
            return null;
          store.jdbc.update(
              "UPDATE instance SET managed_credential=?,managed_synced_version=? WHERE tenant_id=? AND id=?",
              material,
              version,
              store.tenant(),
              id);
          if ((boolean) current.get("deleted")) return null;
          current = store.instance(id);
          var spec = WorkspaceService.spec(current);
          service.saveSpec(
              current, spec.toBuilder().setGeneration(spec.getGeneration() + 1).build());
          return null;
        });
  }
}
