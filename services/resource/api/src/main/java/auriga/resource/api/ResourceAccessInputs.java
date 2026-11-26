package auriga.resource.api;

import auriga.common.security.AurigaPrincipal;
import auriga.contracts.org.v1.GetEffectivePermissionsResponse;
import auriga.contracts.org.v1.Member;
import java.util.UUID;

/** Trusted inputs from identity and organization services. */
public interface ResourceAccessInputs {
  Member member(UUID tenantId, UUID accountId);

  GetEffectivePermissionsResponse permissions(UUID tenantId, UUID accountId);

  boolean allowPersonalPlugins(UUID tenantId);

  /** The authenticated actor's verified department display name, if available. */
  default String authorDepartment(AurigaPrincipal actor) {
    return "";
  }

  default boolean groupExists(UUID tenantId, UUID groupId) {
    return false;
  }
}
