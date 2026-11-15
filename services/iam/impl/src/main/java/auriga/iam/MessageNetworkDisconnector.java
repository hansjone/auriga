package auriga.iam;

import auriga.contracts.msgnet.model.IsolationRecord;

/** The edge owns the least-privilege broker CONNZ/KICK identity. */
@FunctionalInterface
interface MessageNetworkDisconnector {
  void disconnect(IsolationRecord record);
}
