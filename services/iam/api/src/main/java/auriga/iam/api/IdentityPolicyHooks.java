package auriga.iam.api;

import java.util.UUID;
import tools.jackson.databind.JsonNode;

/** org/team-account integrations. Defaults fail closed until the owning module supplies them. */
public interface IdentityPolicyHooks {
  record UacMember(String displayName, String memberType) {}

  /** Owner-authoritative active membership. Upstream attributes cannot grant membership. */
  default java.util.Optional<UacMember> uacMember(UUID account) {
    return java.util.Optional.empty();
  }

  /** Only administrator-verified employee mappings may resolve a UAC subject. */
  default java.util.Optional<UUID> resolveUacEmployee(UUID provider, String employee) {
    return java.util.Optional.empty();
  }

  /** Must bind the invitation's trusted employee to this exact verified subject, atomically. */
  default boolean consumeUacInvitation(String code, UUID provider, String employee, UUID account) {
    return false;
  }

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
