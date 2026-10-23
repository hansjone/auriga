package auriga.resource.api;

import auriga.contracts.org.v1.GetEffectivePermissionsResponse;
import auriga.contracts.org.v1.Member;
import java.util.UUID;

/** Trusted org inputs. The combined platform adapts OrgService; resource never reads org tables. */
public interface ResourceAccessInputs {
  Member member(UUID tenantId, UUID accountId);

  GetEffectivePermissionsResponse permissions(UUID tenantId, UUID accountId);

  boolean allowPersonalPlugins(UUID tenantId);
}
