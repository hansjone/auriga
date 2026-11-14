package auriga.workspace.grpc;

import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.contracts.node.v1.*;
import auriga.iam.api.MessageNetworkControlLedger;
import auriga.iam.api.NodeControlAuthority;
import auriga.workspace.api.MsgnetNodeControl;
import com.google.protobuf.ByteString;
import java.util.*;

/** Public-only NodeControl relay; credentials and all private materials stay in the backend. */
public final class PinnedMsgnetNodeControl implements MsgnetNodeControl {
  private final NodeControlAuthority authority;
  private final MessageNetworkControlLedger ledger;

  public PinnedMsgnetNodeControl(
      NodeControlAuthority authority, MessageNetworkControlLedger ledger) {
    this.authority = Objects.requireNonNull(authority);
    this.ledger = Objects.requireNonNull(ledger);
  }

  private auriga.contracts.msgnet.model.ControlPackage pending(
      NodeControlAuthority.Binding binding) {
    var value =
        ledger.pending(binding.tenant(), binding.realm(), binding.pin().getTargetId()).orElse(null);
    if (value != null
        && (!binding.tenant().equals(value.getTenantId())
            || !binding.realm().equals(value.getRealm())
            || !binding.pin().getTargetId().equals(value.getTargetId())))
      throw new AurigaException(CommonErrors.FORBIDDEN);
    return value;
  }

  @Override
  public List<MsgnetControlPackage> controls(UUID tenant, UUID node) {
    var value = pending(authority.binding(tenant, node));
    return value == null
        ? List.of()
        : List.of(
            MsgnetControlPackage.newBuilder()
                .setCanonicalJson(ByteString.copyFrom(ledger.canonicalControl(value)))
                .build());
  }

  @Override
  public List<MsgnetTransportBundle> transports(UUID tenant, UUID node) {
    var binding = authority.binding(tenant, node);
    var value = ledger.transport();
    if (value != null
        && (!binding.tenant().equals(value.getPayload().getTenantId())
            || !binding.realm().equals(value.getPayload().getRealm())))
      throw new AurigaException(CommonErrors.FORBIDDEN);
    return value == null
        ? List.of()
        : List.of(
            MsgnetTransportBundle.newBuilder()
                .setCanonicalJson(ByteString.copyFrom(ledger.canonicalTransport(value)))
                .build());
  }

  @Override
  public void receipt(UUID tenant, UUID node, MsgnetControlReceipt wire) {
    if (wire.getCanonicalJson().size() > 65536)
      throw new AurigaException(CommonErrors.VALIDATION_FAILED);
    var binding = authority.binding(tenant, node);
    var value =
        authority.verify(tenant, node, wire.getCanonicalJson().toByteArray(), pending(binding));
    // No local READY flag. C atomically checks the outstanding challenge, durable
    // isolation/compromise status and new-chain readiness before committing.
    ledger.confirm(value);
  }
}
