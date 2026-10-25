package auriga.collab.api;

import java.time.Instant;
import java.util.Map;
import java.util.UUID;

/**
 * Server-only adapter to authenticated plugin context and current identity/authorization storage.
 * Implementations must fail closed. No value here may come from model parameters or a submitted
 * message's identity fields. Source/human-intent selection belongs to the authenticated adapter.
 */
public interface NetworkSourceAuthority {
  Source currentSource(UUID tenantId);

  /**
   * Recheck current owner, source binding, policy and target read permission before each signing.
   */
  Instant authorizeDelivery(
      UUID tenantId, String messageJson, String sourceContextJson, String node);

  /** Returns the current authenticated READY/FETCH association, including the client's nonce. */
  Target currentTarget(UUID tenantId);

  /** Verified group/broadcast snapshot for a batch; null means no group authorization. */
  default Dispatch currentDispatch(UUID tenantId) {
    return null;
  }

  record Dispatch(
      String addressJson,
      String snapshotId,
      Map<String, String> membershipIds,
      Map<String, String> recipientTypes,
      String broadcastGrantId,
      String grantRevision,
      String subjectType) {}

  /** Internal verified state, not a wire DTO or an instruction to trust client-supplied values. */
  record Source(
      String realm,
      String node,
      String local,
      String addressType,
      String senderKind,
      String user,
      String displayLabel,
      String identityRevision,
      Instant observedAt,
      String sourceBinding,
      String sourceRevision,
      String policyRevision,
      Instant validUntil) {}

  record Target(
      String realm,
      String node,
      String incarnation,
      long epoch,
      String channelNonce,
      String requestId,
      Instant validUntil) {}
}
