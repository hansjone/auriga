package auriga.collab.api.network.group;

import auriga.collab.api.network.group.NetworkGroups.*;
import java.time.Instant;
import java.util.List;
import java.util.UUID;

/**
 * Mandatory trusted adapters; there is deliberately no permissive implementation. Every method
 * fails closed on unavailable IAM/local authority. No network call while holding a database lock:
 * adapters consume already authenticated evidence and transaction-consistent authority projections.
 */
public interface NetworkGroupAuthority {
  Actor current();

  void checkSource(Actor source);

  /** Current organization ceilings; counts/bytes describe the complete frozen target set. */
  void checkSend(
      Actor source, String group, int nodes, int locals, int bodyBytes, boolean ephemeral);

  void checkTarget(Actor source, Address target, boolean broadcast);

  enum TargetAccess {
    ALLOWED,
    REVOKED
  }

  /**
   * Current per-recipient decision for an already saved paged snapshot. REVOKED means confirmed
   * loss of this target's authorization only. Source-wide denial and transient IAM/offline/backlog
   * failures must throw, never become REVOKED. No exception-code guessing or default allow adapter.
   */
  TargetAccess dispatchTarget(Actor source, Address target, boolean broadcast);

  /**
   * Existing direct relation or shared FIRST_CONTACT/human_dialogue, with private uniform status.
   */
  void checkHumanUnicast(Actor source, Address target, int bodyBytes);

  /** Current target read permission; never require the original sender's expired send grant. */
  void checkHistoricalRead(Actor target, Item original);

  void requireHumanUi(Actor source);

  void requireAdministrator();

  void requireWorker();

  void allowCreate(Actor source);

  /** Complete, tenant/realm scoped, transaction-consistent registered system-node snapshot. */
  List<Address> broadcastSnapshot(String realm);

  /**
   * Use FIRST_CONTACT/group_join through the shared first-contact budget/cooldown/privacy channel.
   * Only sys or an existing direct relationship local; group identity MUST NOT reset budget keys.
   * Returns a durable contact reference; no bespoke invitation notification or bypass is allowed.
   */
  UUID reserveInvitation(
      Actor source, Address target, String group, UUID invitation, Instant expiresAt);

  /**
   * Verify the real user's approved contact/decision and durable permission for exactly selected,
   * including tenant/node ownership, existing local registration/lifecycle and revisions. invited
   * is the original contact target, not a replacement source. selected is an untrusted candidate
   * until this call succeeds; actor always remains the authenticated approver. committed=true is
   * only an original-approval/result reconciliation: authenticate its owner and exact target
   * without issuing a new permission or extending the original deadline. No session creation.
   */
  void approveJoin(
      Actor actor,
      Address invited,
      Address selected,
      UUID contact,
      UUID invitation,
      UUID membership,
      long revision,
      boolean committed);

  /**
   * Current target authentication plus durable evidence; an ACK is never a node-wide ACK. ACCEPTED
   * returns the independently verified durable acceptance time, never a client timestamp.
   */
  Instant verifyReceipt(Actor target, UUID message, Recipient recipient, Outcome outcome);

  /**
   * Verify a committed private reply or explicit decline, bound to the original source/recipient.
   */
  void verifyResponse(
      Actor target,
      Item original,
      Recipient recipient,
      String key,
      int revision,
      Response response);
}
