package auriga.workspace.api;

import auriga.contracts.node.v1.MsgnetControlPackage;
import auriga.contracts.node.v1.MsgnetControlReceipt;
import auriga.contracts.node.v1.MsgnetTransportBundle;
import java.util.List;
import java.util.UUID;

/** Public control delivery port. The ledger retains pending items until durable acknowledgement. */
public interface MsgnetNodeControl {
  List<MsgnetControlPackage> controls(UUID tenant, UUID node);

  List<MsgnetTransportBundle> transports(UUID tenant, UUID node);

  /** The node comes from the authenticated NodeControl session, never the receipt's claims. */
  void receipt(UUID tenant, UUID node, MsgnetControlReceipt receipt);
}
