package auriga.platform;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.security.AuthorizationRules;
import auriga.common.security.PrincipalType;
import auriga.contracts.node.v1.SharedDshBinding;
import auriga.contracts.node.v1.SharedDshResourceView;
import auriga.contracts.org.v1.MemberStatus;
import auriga.contracts.org.v1.MemberType;
import auriga.iam.IamStore;
import auriga.iam.SessionManager;
import auriga.iam.api.PlacementAuthorization;
import auriga.workspace.api.SharedDshAuthority.Inputs;
import auriga.workspace.api.SharedDshAuthority.ResourceInputs;
import auriga.workspace.api.WorkspaceInputs;
import java.util.UUID;

/** Local combined deployment adapter. Each authority retains its own transaction and tenant. */
final class PlatformSharedDshInputs implements Inputs {
  private final WorkspaceInputs org;
  private final IamStore iam;
  private final SessionManager sessions;
  private final PlacementAuthorization placement;
  private final ResourceInputs resources;

  PlatformSharedDshInputs(
      WorkspaceInputs org,
      IamStore iam,
      SessionManager sessions,
      PlacementAuthorization placement,
      ResourceInputs resources) {
    this.org = org;
    this.iam = iam;
    this.sessions = sessions;
    this.placement = placement;
    this.resources = resources;
  }

  static void requireTenant(UUID tenant) {
    if (tenant == null
        || TenantContext.PLATFORM_TENANT.equals(tenant)
        || TenantContext.current().filter(tenant::equals).isEmpty())
      throw new AurigaException(CommonErrors.FORBIDDEN);
  }

  @Override
  public void requireMember(UUID tenant, UUID member) {
    requireTenant(tenant);
    var current = org.member(tenant, member);
    if (current == null
        || !tenant.toString().equals(current.getTenantId())
        || !member.toString().equals(current.getAccountId())
        || current.getStatus() != MemberStatus.MEMBER_STATUS_ACTIVE
        || (current.getMemberType() != MemberType.MEMBER_TYPE_MEMBER
            && current.getMemberType() != MemberType.MEMBER_TYPE_EXTERNAL))
      throw new AurigaException(CommonErrors.FORBIDDEN);
  }

  @Override
  public void requireSession(UUID tenant, UUID member, UUID platformSession) {
    requireTenant(tenant);
    iam.tx(
        () -> {
          var row = sessions.activeById(platformSession.toString());
          if (row == null
              || !tenant.toString().equals(row.path("tenantId").asString())
              || !member.toString().equals(row.path("accountId").asString())
              || !platformSession.toString().equals(row.path("sessionId").asString())
              || !"portal".equals(row.path("audience").asString())
              || row.has("managedInstance")
              || row.has("kind")) throw new AurigaException(CommonErrors.UNAUTHENTICATED);
          return null;
        });
  }

  @Override
  public String authorizationSetDigest(UUID tenant, UUID member) {
    requireTenant(tenant);
    return resources.authorizationSetDigest(tenant, member);
  }

  @Override
  public SharedDshResourceView resourceView(SharedDshBinding binding) {
    requireTenant(UUID.fromString(binding.getTenantId()));
    return resources.resourceView(binding);
  }

  @Override
  public void requirePlacementWrite(UUID tenant, UUID actor, UUID adminSession, long now) {
    requireTenant(tenant);
    var caller =
        AuthorizationRules.currentPrincipal()
            .orElseThrow(() -> new AurigaException(CommonErrors.UNAUTHENTICATED));
    if (caller.type() != PrincipalType.USER
        || !caller.isAdminContext()
        || !tenant.equals(caller.tenantId())
        || !actor.toString().equals(caller.subject())
        || !adminSession.toString().equals(caller.sessionId()))
      throw new AurigaException(CommonErrors.FORBIDDEN);
    // IAM rechecks the account, permission and step-up against its live clock.
    placement.requireWrite();
  }
}
