package auriga.collab.api.network.group;

import java.time.Instant;
import java.util.List;
import java.util.Map;
import java.util.UUID;

/**
 * Internal application API. Bind the authenticated tenant before calling; never bind model
 * identity.
 */
public interface NetworkGroups {
  enum Sender {
    HUMAN,
    AGENT
  }

  enum Intent {
    INFORMATION,
    REQUEST
  }

  enum Mode {
    ATOMIC,
    PAGED
  }

  enum Outcome {
    ACCEPTED,
    REJECTED,
    EXPIRED,
    CANCELED,
    SKIPPED,
    UNRESOLVED
  }

  enum Response {
    RESPONDED,
    DECLINED
  }

  enum Scope {
    USER,
    NODE,
    AGENT
  }

  record Address(String realm, String node, String local, String type) {}

  record Actor(String user, Address address, Sender sender, String binding, String label) {}

  record Group(String realm, String local, long revision) {}

  record Invitation(UUID id, UUID membershipId, long revision) {}

  record HumanSend(
      Address target,
      UUID thread,
      String key,
      String text,
      Intent intent,
      Instant expiresAt,
      UUID humanIntent) {
    public boolean inboxOnly() {
      return true;
    }
  }

  enum ProgressKind {
    PROGRESS,
    ONLINE
  }

  record Ephemeral(
      UUID eventId,
      Actor source,
      Group group,
      ProgressKind kind,
      long revision,
      String text,
      Instant expiresAt,
      List<Recipient> recipients) {
    public Ephemeral {
      recipients = List.copyOf(recipients);
    }
  }

  /**
   * No from/class/sender_kind/authorization_ref fields are accepted here. Text is external data.
   */
  record Send(
      String group,
      UUID thread,
      String key,
      String text,
      Intent intent,
      Mode mode,
      Instant expiresAt,
      UUID humanIntent) {}

  record Stored(UUID messageId, int totalMembers, int totalNodes, String dispatchStatus) {}

  record Recipient(UUID id, UUID membershipId, Address address) {}

  record Item(
      UUID messageId,
      String realm,
      String group,
      UUID thread,
      Actor source,
      String text,
      Intent intent,
      Instant expiresAt,
      long stateVersion,
      UUID grantId,
      long grantRevision,
      Scope grantScope,
      List<Recipient> recipients) {
    public Item {
      recipients = List.copyOf(recipients);
    }

    public boolean inboxOnly() {
      return true;
    }
  }

  record Statistics(
      int total,
      Map<String, Long> receipts,
      Map<String, Long> responses,
      boolean recalled,
      long stateVersion) {
    public Statistics {
      receipts = Map.copyOf(receipts);
      responses = Map.copyOf(responses);
    }
  }

  record Grant(UUID id, long revision) {}

  record GrantRequest(
      Scope scope,
      String user,
      String node,
      String local,
      String binding,
      Instant expiresAt,
      int maxNodes,
      int maxBytes,
      String reason) {}

  record StateUpdate(
      UUID id,
      UUID messageId,
      String node,
      long revision,
      String kind,
      Scope scope,
      String subject,
      Boolean frozen) {}

  Group create(String name);

  Group policy(String group, long expectedRevision, boolean adminsOnly, boolean deleted);

  Group capacity(String group, long expectedRevision, int maxMembers, int maxNodes);

  Group delegate(String group, long expectedRevision, UUID membership, boolean administrator);

  Invitation invite(String group, long expectedRevision, Address target, Instant expiresAt);

  /** Requires durable local group permission and a current shared first-contact decision. */
  Group join(UUID invitation, long expectedRevision);

  /**
   * Explicit selection from the authenticated approval path. A sys invitation may select the user's
   * existing local on the same node; a precise invitation cannot change target. Authority must
   * verify real consent/ownership/registration/durable permission before the candidate is
   * activated. Retries must select the same address; this never changes the actor or creates a host
   * session.
   */
  Group join(UUID invitation, long expectedRevision, Address selected);

  Group leave(String group, long expectedRevision);

  UUID humanIntent(Send request);

  UUID humanUnicastIntent(HumanSend request);

  Stored sendHumanUnicast(HumanSend request);

  UUID humanEphemeralIntent(Send request, ProgressKind kind, long revision);

  Stored send(Send request);

  /**
   * Transient checked snapshot only; trusted Core adapter must recheck each node at publication.
   */
  Ephemeral prepareEphemeral(Send request, ProgressKind kind, long revision);

  Grant grant(GrantRequest request);

  Grant revoke(UUID grant, long expectedRevision, String key, String reason);

  long freeze(
      Scope scope,
      String subject,
      long expectedRevision,
      boolean frozen,
      String key,
      String reason);

  long recall(UUID message, long expectedRevision, String key, String reason);

  /** Trusted background worker; commits at most 32 nodes, with a transactional mailbox sink. */
  int dispatch(UUID message);

  /**
   * Stable-key scan; advance after even when one plan is blocked. Also reconciles expired plans.
   */
  List<UUID> pendingDispatches(UUID after, int limit);

  void cancel(UUID message);

  /** Current target/source authorization is rechecked before every read/signing. */
  Item fetch(UUID message);

  void receipt(UUID message, UUID recipient, Outcome outcome);

  void respond(UUID message, UUID recipient, String key, int revision, Response response);

  Statistics status(UUID message);

  /** Trusted worker, bounded durable management-notice delivery, independent of original ACKs. */
  List<StateUpdate> pendingUpdates(int limit);

  void updateCommitted(UUID update);
}
