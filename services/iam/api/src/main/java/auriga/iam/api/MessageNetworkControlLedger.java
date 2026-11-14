package auriga.iam.api;

import auriga.contracts.msgnet.model.*;
import java.util.Optional;

/**
 * C's authenticated internal ledger, with no private-material or identity registration operations.
 */
public interface MessageNetworkControlLedger {
  Optional<ControlPackage> pending(String tenant, String realm, String target);

  IsolationRecord isolation(String tenant, String realm, String target);

  IsolationRecord confirm(InstallationReceipt receipt);

  SignedTransportTrustBundle transport();

  byte[] canonicalControl(ControlPackage value);

  byte[] canonicalTransport(SignedTransportTrustBundle value);
}
