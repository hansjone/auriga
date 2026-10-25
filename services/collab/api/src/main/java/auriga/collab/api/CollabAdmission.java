package auriga.collab.api;

import auriga.contracts.collab.v1.Actor;

/** Online authoritative credential, owner, membership and action authorization. */
@FunctionalInterface
public interface CollabAdmission {
  enum Operation {
    REGISTER,
    BIND,
    DISCOVER,
    OPEN,
    REPLY,
    READ,
    PULL,
    ACK,
    REVOKE,
    RECEIVE
  }

  /** Fail closed on unavailable authority. A cached endpoint row is not authorization. */
  void verify(Actor actor, Operation operation);
}
