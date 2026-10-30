package auriga.workspace.app;

import auriga.common.context.TenantContext;
import auriga.common.error.*;
import auriga.contracts.node.v1.DesiredState;
import auriga.contracts.org.v1.MemberStatus;
import auriga.contracts.workspace.v1.*;
import auriga.workspace.api.*;
import auriga.workspace.persistence.WorkspaceStore;
import java.util.UUID;

/** Independent read authority: never calls IAM and never consumes its device credential. */
public final class WorkspaceManagedBindings implements ManagedInstanceBindings {
  private final WorkspaceStore store;
  private final WorkspaceInputs inputs;

  public WorkspaceManagedBindings(WorkspaceStore store, WorkspaceInputs inputs) {
    this.store = store;
    this.inputs = inputs;
  }

  @Override
  public GetManagedInstanceBindingResponse get(GetManagedInstanceBindingRequest request) {
    if (!TenantContext.require().toString().equals(request.getTenantId())
        || TenantContext.PLATFORM_TENANT.equals(TenantContext.require()))
      throw new AurigaException(CommonErrors.FORBIDDEN);
    UUID id;
    try {
      id = UUID.fromString(request.getInstanceId());
      if (!id.toString().equals(request.getInstanceId())
          || !request.getUnknownFields().asMap().isEmpty()) throw new IllegalArgumentException();
    } catch (IllegalArgumentException e) {
      throw new AurigaException(CommonErrors.VALIDATION_FAILED);
    }
    var row =
        store.tx(
            () ->
                store.one("SELECT * FROM instance WHERE tenant_id=? AND id=?", store.tenant(), id));
    UUID owner = (UUID) row.get("owner_id");
    var member = inputs.member(store.tenant(), owner);
    boolean active =
        !(boolean) row.get("deleted")
            && WorkspaceService.spec(row).getDesired() == DesiredState.DESIRED_STATE_RUNNING
            && member.getStatus() == MemberStatus.MEMBER_STATUS_ACTIVE
            && owner.toString().equals(member.getAccountId())
            && store.tenant().toString().equals(member.getTenantId());
    return GetManagedInstanceBindingResponse.newBuilder()
        .setTenantId(store.tenant().toString())
        .setInstanceId(id.toString())
        .setOwnerId(owner.toString())
        .setActive(active)
        .setCredentialVersion(((Number) row.get("managed_credential_version")).longValue())
        .build();
  }
}
