package auriga.workspace.app;

import auriga.common.context.TenantContext;
import auriga.common.error.*;
import auriga.iam.api.CredentialMetadataSource;
import auriga.workspace.api.ManagedCredentialRotation;
import java.util.*;

/**
 * Reuses lifecycle invalidation and its durable reconciliation queue; never calls IAM under a lock.
 */
public final class WorkspaceCredentialControl
    implements ManagedCredentialRotation, CredentialMetadataSource {
  private final WorkspaceService service;

  public WorkspaceCredentialControl(WorkspaceService service) {
    this.service = service;
  }

  private void tenant(UUID tenant) {
    if (!TenantContext.require().equals(tenant) || TenantContext.PLATFORM_TENANT.equals(tenant))
      throw new AurigaException(CommonErrors.FORBIDDEN);
  }

  @Override
  public long currentVersion(UUID tenant, UUID instance) {
    tenant(tenant);
    return service.store.tx(
        () ->
            ((Number) service.store.instance(instance).get("managed_credential_version"))
                .longValue());
  }

  @Override
  public long rotate(UUID tenant, UUID instance, long expected) {
    tenant(tenant);
    if (expected < 1 || expected >= 9007199254740991L)
      throw new AurigaException(CommonErrors.VALIDATION_FAILED);
    return service.store.tx(
        () -> {
          var row = service.store.instance(instance);
          long current = ((Number) row.get("managed_credential_version")).longValue();
          if (current > expected)
            return current; // A worker may already have observed the revoked session.
          if (current != expected) throw new AurigaException(CommonErrors.CONFLICT);
          service.managedSessionRevoked(row);
          return ((Number) service.store.instance(instance).get("managed_credential_version"))
              .longValue();
        });
  }

  @Override
  public String name() {
    return "workspace-node-certificates";
  }

  @Override
  public Snapshot snapshot() {
    return service.store.tx(
        () -> {
          var entries =
              service.store.jdbc.query(
                  "SELECT id,certificate_serial,certificate_expires_at,revoked FROM node WHERE tenant_id=? ORDER BY id",
                  (r, i) ->
                      new Entry(
                          "node:" + r.getObject("id"),
                          "node",
                          name(),
                          r.getString("certificate_serial"),
                          null,
                          r.getTimestamp("certificate_expires_at").toInstant(),
                          r.getBoolean("revoked") ? "revoked" : "active"),
                  service.store.tenant());
          return new Snapshot(
              entries,
              entries.isEmpty() ? List.of() : List.of(new Gap(name(), "issued_at_unavailable")));
        });
  }
}
