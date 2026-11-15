package auriga.workspace.api;

import auriga.contracts.node.v1.*;
import java.util.UUID;

/**
 * Trusted service boundary. Adapters authenticate callers and bind TenantContext before invocation.
 */
public interface SharedDshAuthority {
  record Placement(SharedDshBinding binding, String state, long authGeneration) {}

  Placement resolve(UUID member, UUID placement, UUID platformSession);

  SharedDshSessionBinding register(SharedDshSessionBinding binding, String parentSessionId);

  long revoke(SharedDshRevocation revocation);

  SharedDshResourceView resourceView(SharedDshBinding binding);

  long reportResource(SharedDshResourceReport report);

  /**
   * Live IAM/member/resource authority; exceptions fail closed, never use push caches as authority.
   */
  interface Inputs {
    void requireMember(UUID tenant, UUID member);

    void requireSession(UUID tenant, UUID member, UUID platformSession);

    String authorizationSetDigest(UUID tenant, UUID member);

    SharedDshResourceView resourceView(SharedDshBinding binding);

    /** Recheck live IAM admin session, permission and strict 600-second step-up. */
    void requirePlacementWrite(UUID tenant, UUID actor, UUID adminSession, long now);
  }
}
