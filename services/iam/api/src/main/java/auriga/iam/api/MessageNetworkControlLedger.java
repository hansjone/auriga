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

  /** Native nodes require C's node-aware port, not the generic installation receipt endpoint. */
  default IsolationRecord confirmNode(
      java.util.UUID tenant, java.util.UUID node, InstallationReceipt receipt) {
    throw new UnsupportedOperationException("node-aware control ledger required");
  }

  SignedTransportTrustBundle transport();

  /** Frozen signed documents retain absent fields exactly across the trust boundary. */
  default byte[] rootControlDocument() {
    throw new UnsupportedOperationException();
  }

  default byte[] transportHistoryDocument(String digest) {
    throw new UnsupportedOperationException();
  }

  default byte[] confirmationDocument(TransportConfirmationQuery query) {
    throw new UnsupportedOperationException();
  }

  default TransportPredecessor history(String digest) {
    throw new UnsupportedOperationException();
  }

  default TransportTransitionProof transition(String authorityDigest) {
    throw new UnsupportedOperationException();
  }

  default SignedTransportConfirmation confirmation(TransportConfirmationQuery query) {
    throw new UnsupportedOperationException();
  }

  /** C's independent material-authorization snapshot, never reconstructed from evidence. */
  default ExpectedSelectors expectedSelectors(ExpectedSelectorsQuery query) {
    throw new UnsupportedOperationException();
  }

  default ExpectedSelectorsQuery expectedSelectorsQuery(byte[] raw) {
    throw new UnsupportedOperationException();
  }

  /** Caller has committed its deny fence and independently confirmed disconnection. */
  default IsolationRecord enforceIsolation(IsolationRecord record) {
    throw new UnsupportedOperationException();
  }

  default Publication publication() {
    throw new UnsupportedOperationException();
  }

  default byte[] canonicalPublic(Object value) {
    throw new UnsupportedOperationException();
  }

  default IsolationRecord isolationRequest(byte[] raw) {
    throw new UnsupportedOperationException();
  }

  default TransportConfirmationQuery confirmationQuery(byte[] raw) {
    throw new UnsupportedOperationException();
  }

  byte[] canonicalControl(ControlPackage value);

  byte[] canonicalTransport(SignedTransportTrustBundle value);
}
