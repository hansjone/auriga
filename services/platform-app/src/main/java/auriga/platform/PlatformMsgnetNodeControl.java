package auriga.platform;

import auriga.collab.api.NetworkRenewal;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.contracts.msgnet.model.InstallationReceipt;
import auriga.contracts.node.v1.MsgnetControlPackage;
import auriga.contracts.node.v1.MsgnetControlReceipt;
import auriga.contracts.node.v1.MsgnetTransportBundle;
import auriga.workspace.api.MsgnetNodeControl;
import com.google.protobuf.ByteString;
import java.util.List;
import java.util.UUID;
import tools.jackson.databind.json.JsonMapper;

/** Public-only NodeControl bridge; node identity comes from workspace's authenticated session. */
final class PlatformMsgnetNodeControl implements MsgnetNodeControl {
  private final NetworkRenewal renewal;
  private static final JsonMapper JSON = JsonMapper.builder().build();

  PlatformMsgnetNodeControl(NetworkRenewal renewal) {
    this.renewal = renewal;
  }

  @Override
  public List<MsgnetControlPackage> controls(UUID tenant, UUID node) {
    return TenantContext.call(
        tenant,
        () -> {
          try {
            return List.of(
                MsgnetControlPackage.newBuilder()
                    .setCanonicalJson(bytes(renewal.pendingNodeControl(node.toString())))
                    .build());
          } catch (AurigaException failure) {
            if (CommonErrors.NOT_FOUND.code().equals(failure.errorCode().code())) return List.of();
            throw failure;
          }
        });
  }

  @Override
  public List<MsgnetTransportBundle> transports(UUID tenant, UUID node) {
    return TenantContext.call(
        tenant,
        () ->
            List.of(
                MsgnetTransportBundle.newBuilder()
                    .setCanonicalJson(bytes(renewal.nodeTransport(node.toString())))
                    .build()));
  }

  @Override
  public void receipt(UUID tenant, UUID node, MsgnetControlReceipt receipt) {
    if (receipt.getCanonicalJson().size() > 65536)
      throw new AurigaException(CommonErrors.VALIDATION_FAILED);
    TenantContext.run(
        tenant,
        () -> {
          try {
            String text =
                java.nio.charset.StandardCharsets.UTF_8
                    .newDecoder()
                    .onMalformedInput(java.nio.charset.CodingErrorAction.REPORT)
                    .decode(receipt.getCanonicalJson().asReadOnlyByteBuffer())
                    .toString();
            var raw = auriga.collab.app.NetworkJson.parse(text);
            var model = JSON.treeToValue(raw, InstallationReceipt.class);
            if (!auriga.collab.app.NetworkJson.canonical(raw)
                .equals(
                    auriga.collab.app.NetworkJson.canonical(
                        auriga.collab.network.keys.MsgnetNetworkRenewal.tree(model))))
              throw new AurigaException(CommonErrors.VALIDATION_FAILED);
            renewal.confirmNodeControl(node.toString(), model);
          } catch (java.nio.charset.CharacterCodingException malformed) {
            throw new AurigaException(CommonErrors.VALIDATION_FAILED);
          }
        });
  }

  private static ByteString bytes(Object value) {
    return ByteString.copyFromUtf8(
        auriga.collab.app.NetworkJson.canonical(
            auriga.collab.network.keys.MsgnetNetworkRenewal.tree(value), 1048576));
  }
}
