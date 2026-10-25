package auriga.iam.api;

import java.math.BigInteger;
import java.time.Instant;
import java.util.Map;

/**
 * N1 network identity and organization ceilings. Every call requires a business TenantContext. IDs
 * on this API are canonical unsigned decimal strings; display addresses use hexadecimal. An allowed
 * decision is only an organization ceiling: collab must still check relationships, reserve quotas
 * transactionally, and the plugins must authorize their own local sessions.
 */
public interface NetworkAuthority {
  record Node(String realm, String node, String account, String device, boolean revoked) {
    public String systemAddress() {
      return "aur1:"
          + new BigInteger(realm).toString(16)
          + "."
          + new BigInteger(node).toString(16)
          + ".0";
    }
  }

  record Ticket(String ticket, Node identity, String serverAudience, Instant expiresAt) {}

  /** The gateway signs the NATS User JWT, using precisely this identity and expiry. */
  record Connection(Node identity, String serverAudience, Instant expiresAt) {}

  /** Trusted Broker connection identity. No device tokens or raw network tickets. */
  record IngressBinding(
      String serverId, String clientId, String userNkey, String ingress, String account) {}

  enum Operation {
    CONNECT,
    SEND,
    RECEIVE,
    FIRST_CONTACT,
    BROADCAST
  }

  enum Sender {
    HUMAN,
    AGENT
  }

  /**
   * Supplied only by trusted gateway/collab, after authenticating the ingress and sender kind.
   * Broadcast is checked for EACH targetNode; counts describe the complete immutable snapshot, with
   * exactly one system recipient per node. Permission does not reserve rate/backlog quotas.
   */
  record Check(
      String realm,
      String sourceNode,
      String targetNode,
      String local,
      Sender sender,
      Operation operation,
      long bodyBytes,
      long targetNodes,
      long targetLocals) {}

  record Decision(boolean allowed, String reason, Map<Limit, Long> limits) {
    public Decision {
      limits = Map.copyOf(limits);
    }
  }

  /** Platform ceilings from network design section 10; rates/counters belong to collab. */
  enum Limit {
    PAYLOAD_BYTES(65536),
    BODY_BYTES(16384),
    CONNECTION_SECONDS(3600),
    ATOMIC_TARGET_NODES(32),
    ATOMIC_TARGET_LOCALS(256),
    GROUP_MEMBERS(1024),
    GROUP_NODES(256),
    BROADCAST_NODES(10000),
    BROADCAST_SOURCE_PER_MINUTE(1),
    BROADCAST_TENANT_PER_MINUTE(5),
    GROUP_SOURCE_PER_MINUTE(60),
    GROUP_SOURCE_BURST(10),
    GROUP_PER_MINUTE(300),
    FIRST_LOCAL_PER_MINUTE(5),
    FIRST_LOCAL_BURST(3),
    FIRST_LOCAL_PENDING(10),
    FIRST_LOCAL_TARGETS_HOUR(20),
    FIRST_LOCAL_TARGETS_DAY(40),
    FIRST_NODE_PER_MINUTE(10),
    FIRST_NODE_BURST(5),
    FIRST_NODE_PENDING(30),
    FIRST_NODE_TARGETS_HOUR(50),
    FIRST_NODE_TARGETS_DAY(100),
    FIRST_USER_PER_MINUTE(20),
    FIRST_USER_BURST(10),
    FIRST_USER_PENDING(60),
    FIRST_USER_TARGETS_HOUR(100),
    FIRST_USER_TARGETS_DAY(200),
    FIRST_NODE_TARGET_PER_MINUTE(5),
    FIRST_USER_TARGET_PER_MINUTE(5),
    FIRST_TARGET_PER_MINUTE(30),
    FIRST_TARGET_BURST(10),
    FIRST_NODE_TARGET_PENDING(1),
    FIRST_USER_TARGET_PENDING(3),
    FIRST_TARGET_PENDING(100),
    FIRST_TARGET_PENDING_BYTES(1048576),
    FIRST_COOLDOWN_SECONDS(86400),
    FIRST_PENDING_SECONDS(86400),
    NODE_BACKLOG_MESSAGES(10000),
    NODE_BACKLOG_BYTES(67108864),
    TENANT_BACKLOG_MESSAGES(100000),
    TENANT_BACKLOG_BYTES(1073741824);

    private final long value;

    Limit(long value) {
      this.value = value;
    }

    public long defaultValue() {
      return value;
    }
  }

  record Configuration(
      boolean enabled, boolean firstContact, boolean broadcast, Map<Limit, Long> limits) {
    public Configuration {
      limits = Map.copyOf(limits);
    }
  }

  /** Idempotent per native device credential. A reinstall/clone must enroll a NEW credential. */
  Node register(String deviceAccessToken);

  Ticket prepare(String deviceAccessToken, String node, String serverAudience);

  /**
   * Exact svc:ai-gateway only. The audience MUST come from the verified Broker callout. A known
   * ticket is consumed even when the supplied node/device/audience binding is rejected.
   */
  Connection redeem(String ticket, String node, String device, String serverAudience);

  /** Wire gateway entry: persists the exact ingress binding before returning a grant. */
  Connection redeem(
      String ticket, String node, String device, String serverAudience, IngressBinding binding);

  /** Exact svc:ai-gateway / svc:collab only; revoked or unknown has the same denied response. */
  Decision check(Check request);

  /** Online query for gateway KICK reconciliation; rechecks backing session/account/device. */
  boolean active(String realm, String node);

  /** HQ admin + iam.network.manage only; changes are audited in the business transaction. */
  void configure(Configuration configuration);

  void nodePolicy(String sourceNode, String targetNode, boolean allowed);

  void grant(String node, Sender sender, String local, Operation operation, Instant expiresAt);

  void withdraw(String node, Sender sender, String local, Operation operation);

  void revoke(String node);
}
