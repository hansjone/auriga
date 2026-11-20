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

  /** Only verified employee mappings may resolve a UAC subject. */
  default java.util.Optional<UUID> resolveUacEmployee(UUID provider, String employee) {
    return java.util.Optional.empty();
  }

  /** Includes inactive preconfigured bindings so that linking cannot replace their owner. */
  default java.util.Optional<UUID> knownUacEmployee(UUID provider, String employee) {
    return resolveUacEmployee(provider, employee);
  }

  /** IAM calls this only after UAC and the login policy authenticate a new portal member. */
  default boolean provisionVerifiedUacMember(
      UUID provider, String employee, UUID account, String displayName) {
    return false;
  }

  /**
   * Replaces a UAC member's placeholder display name with "name employee" (or the employee number
   * when no name is known). Returns the new display name, or empty when nothing changed.
   */
  default java.util.Optional<String> uacDisplayName(UUID account, String employee, String name) {
    return java.util.Optional.empty();
  }

  /** Only an active ordinary member created by UAC first login may resume enrollment. */
  default boolean canResumeVerifiedUacMember(UUID provider, String employee, UUID account) {
    return false;
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
