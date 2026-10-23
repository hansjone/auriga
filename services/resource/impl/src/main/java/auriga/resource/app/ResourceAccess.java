package auriga.resource.app;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.security.AurigaPrincipal;
import auriga.common.security.PrincipalType;
import auriga.contracts.org.v1.MemberStatus;
import auriga.contracts.org.v1.MemberType;
import auriga.resource.api.ResourceAccessInputs;
import java.util.Set;
import java.util.UUID;
import java.util.stream.Collectors;

/**
 * A fresh authorization snapshot per operation, with no cached JWT membership/group assumptions.
 */
public final class ResourceAccess {
  public static final String READ = "resource.packages.read";
  public static final String UPLOAD = "resource.packages.upload";
  private final ResourceAccessInputs inputs;

  public ResourceAccess(ResourceAccessInputs inputs) {
    this.inputs = inputs;
  }

  public record Reader(
      UUID tenant, UUID account, boolean external, Set<UUID> groups, boolean personalPlugins) {}

  public Reader resolve(AurigaPrincipal principal) {
    return resolve(principal, READ);
  }

  public Reader writer(AurigaPrincipal principal) {
    if (principal == null) throw new AurigaException(CommonErrors.UNAUTHENTICATED);
    if (principal.type() != PrincipalType.USER
        || (principal.context() != auriga.common.security.TokenContext.PORTAL
            && principal.context() != auriga.common.security.TokenContext.ADMIN))
      throw new AurigaException(CommonErrors.FORBIDDEN);
    return resolve(principal, UPLOAD);
  }

  private Reader resolve(AurigaPrincipal principal, String permission) {
    if (principal == null) throw new AurigaException(CommonErrors.UNAUTHENTICATED);
    if (!principal.tenantId().equals(TenantContext.require())
        || !principal.hasPermission(permission)
        || (principal.type() != PrincipalType.USER && principal.type() != PrincipalType.DEVICE))
      throw new AurigaException(CommonErrors.FORBIDDEN);
    UUID account;
    try {
      account = UUID.fromString(principal.subject());
    } catch (IllegalArgumentException e) {
      throw new AurigaException(CommonErrors.FORBIDDEN);
    }
    try {
      var member = member(principal.tenantId(), account);
      var permissions = inputs.permissions(principal.tenantId(), account);
      if (!member.getTenantId().equals(principal.tenantId().toString())
          || !member.getAccountId().equals(account.toString())
          || member.getStatus() != MemberStatus.MEMBER_STATUS_ACTIVE
          || permissions.getStatus() != MemberStatus.MEMBER_STATUS_ACTIVE
          || !permissions.getPermissionsList().contains(permission)
          || (member.getMemberType() != MemberType.MEMBER_TYPE_MEMBER
              && member.getMemberType() != MemberType.MEMBER_TYPE_EXTERNAL)
          || member.getMemberType() != permissions.getMemberType())
        throw new AurigaException(CommonErrors.FORBIDDEN);
      return new Reader(
          principal.tenantId(),
          account,
          member.getMemberType() == MemberType.MEMBER_TYPE_EXTERNAL,
          member.getGroupIdsList().stream()
              .map(UUID::fromString)
              .collect(Collectors.toUnmodifiableSet()),
          inputs.allowPersonalPlugins(principal.tenantId()));
    } catch (AurigaException e) {
      if (e.errorCode().equals(CommonErrors.FORBIDDEN))
        throw new AurigaException(CommonErrors.FORBIDDEN);
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    } catch (RuntimeException e) {
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    }
  }

  private auriga.contracts.org.v1.Member member(UUID tenant, UUID account) {
    try {
      return inputs.member(tenant, account);
    } catch (AurigaException e) {
      if (e.errorCode().httpStatus() == 404) throw new AurigaException(CommonErrors.FORBIDDEN);
      throw e;
    }
  }
}
