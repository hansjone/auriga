package auriga.collab.app;

import auriga.collab.api.CollabAdmission;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.contracts.collab.v1.Actor;
import auriga.contracts.org.v1.GetEffectivePermissionsResponse;
import auriga.contracts.org.v1.MemberStatus;
import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import java.util.Objects;
import java.util.function.Function;

/** Rechecks authoritative device state and effective member permissions on every operation. */
public final class OnlineCollabAdmission implements CollabAdmission {
  private final java.util.function.Consumer<Actor> devices;
  private final Function<Actor, GetEffectivePermissionsResponse> permissions;

  public OnlineCollabAdmission(
      java.util.function.Consumer<Actor> devices,
      Function<Actor, GetEffectivePermissionsResponse> permissions) {
    this.devices = Objects.requireNonNull(devices);
    this.permissions = Objects.requireNonNull(permissions);
  }

  @Override
  public void verify(Actor actor, Operation operation) {
    if (!"DEVICE".equals(actor.getCredentialKind())
        || actor.getAccountId().isBlank()
        || actor.getCredentialSubject().isBlank()
        || !TenantContext.require().toString().equals(actor.getTenantId()))
      throw new AurigaException(CommonErrors.FORBIDDEN);
    try {
      devices.accept(actor);
      GetEffectivePermissionsResponse current = permissions.apply(actor);
      if (current.getStatus() != MemberStatus.MEMBER_STATUS_ACTIVE
          || !current.getPermissionsList().contains("collab.messages.use"))
        throw new AurigaException(CommonErrors.FORBIDDEN);
    } catch (StatusRuntimeException e) {
      if (e.getStatus().getCode() == Status.Code.PERMISSION_DENIED
          || e.getStatus().getCode() == Status.Code.NOT_FOUND
          || e.getStatus().getCode() == Status.Code.UNAUTHENTICATED)
        throw new AurigaException(CommonErrors.FORBIDDEN);
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    }
  }
}
