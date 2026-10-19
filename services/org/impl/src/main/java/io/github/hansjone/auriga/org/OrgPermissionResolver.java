package io.github.hansjone.auriga.org;

import io.github.hansjone.auriga.common.context.TenantContext;
import io.github.hansjone.auriga.iam.api.PermissionResolver;
import java.util.Set;
import java.util.UUID;

/** Resolves access-token permissions from org role bindings. */
public final class OrgPermissionResolver implements PermissionResolver {
  private final OrgService org;

  public OrgPermissionResolver(OrgService org) {
    this.org = org;
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
