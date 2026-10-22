package auriga.org;

import auriga.common.context.TenantContext;
import auriga.contracts.org.v1.*;
import auriga.iam.api.InstanceAiAuthorization;
import auriga.iam.api.PermissionResolver;
import java.util.Set;
import java.util.UUID;

/** Resolves access-token permissions from org role bindings. */
public final class OrgPermissionResolver implements PermissionResolver, InstanceAiAuthorization {
  private final OrgService org;

  public OrgPermissionResolver(OrgService org) {
    this.org = org;
  }

  @Override
  public GetEffectivePermissionsResponse getEffectivePermissions(
      GetEffectivePermissionsRequest request) {
    if (!TenantContext.current()
        .map(UUID::toString)
        .filter(request.getTenantId()::equals)
        .isPresent())
      throw new auriga.common.error.AurigaException(auriga.common.error.CommonErrors.FORBIDDEN);
    UUID account = UUID.fromString(request.getAccountId());
    return org.store()
        .tx(
            () -> {
              if (org.store().member(account).isEmpty())
                throw new auriga.common.error.AurigaException(
                    auriga.common.error.CommonErrors.NOT_FOUND);
              return OrgGrpcServices.effectivePermissionsResponse(
                  org.getEffectivePermissions(account));
            });
  }

  @Override
  public Set<String> resolve(UUID tenantId, String subject, String context) {
    UUID account;
    try {
      account = UUID.fromString(subject);
    } catch (Exception e) {
      return Set.of();
    }
    return TenantContext.call(
        tenantId,
        () -> {
          var eff = org.getEffectivePermissions(account);
          if (!"active".equals(eff.status())) return Set.of();
          return Set.copyOf(eff.permissions());
        });
  }
}
