package auriga.iam.api;

import java.util.Set;
import java.util.UUID;

/** Replace with org GetEffectivePermissions when role bindings are available. */
public interface PermissionResolver {
  Set<String> resolve(UUID tenantId, String subject, String context);
}
