package auriga.iam.api;

import auriga.contracts.msgnet.model.ControlPackage;
import auriga.contracts.msgnet.model.InstallationReceipt;
import auriga.contracts.msgnet.model.NodeControlPin;
import java.util.UUID;

/** Independently enrolled public confirmation identities; never a material grant. */
public interface NodeControlAuthority {
  record Binding(String tenant, String realm, NodeControlPin pin) {}

  Binding binding(UUID authenticatedTenant, UUID authenticatedNode);

  /** Pending is read from C, not copied from the receipt. C commits nonce consumption. */
  InstallationReceipt verify(UUID tenant, UUID node, byte[] receipt, ControlPackage pending);
}
