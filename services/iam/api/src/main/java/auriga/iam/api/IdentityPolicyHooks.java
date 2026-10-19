package auriga.iam.api;

import java.util.UUID;
import tools.jackson.databind.JsonNode;

/** org/team-account integrations. Defaults fail closed until the owning module supplies them. */
public interface IdentityPolicyHooks {
  /** Read-only validation before offering registration or hashing credentials. */
  default boolean validateInvitation(String code, UUID provider) {
    return false;
  }

  default boolean consumeInvitation(String code, UUID provider, JsonNode profile) {
    return false;
  }

  default boolean verifyMfa(UUID account, JsonNode input) {
    return false;
  }

  default void synchronize(UUID account, JsonNode profile, JsonNode mappings) {
    if (!mappings.isMissingNode() && !mappings.isEmpty())
      throw new IllegalStateException("Attribute mapping integration is not configured");
  }
}
