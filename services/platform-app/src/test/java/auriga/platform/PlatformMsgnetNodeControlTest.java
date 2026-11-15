package auriga.platform;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.collab.api.NetworkRenewal;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.contracts.msgnet.model.ControlPackage;
import auriga.contracts.msgnet.model.InstallationReceipt;
import auriga.contracts.node.v1.MsgnetControlReceipt;
import com.google.protobuf.ByteString;
import java.util.UUID;
import org.junit.jupiter.api.Test;

class PlatformMsgnetNodeControlTest {
  @Test
  void publicControlIsBoundToTheAuthenticatedNodeAndTenant() {
    var renewal = mock(NetworkRenewal.class);
    var tenant = UUID.randomUUID();
    var node = UUID.randomUUID();
    var pack = new ControlPackage().packageId("package").targetId("target");
    when(renewal.pendingNodeControl(node.toString()))
        .thenAnswer(
            call -> {
              assertEquals(tenant, TenantContext.require());
              return pack;
            });
    var bridge = new PlatformMsgnetNodeControl(renewal);
    assertEquals(1, bridge.controls(tenant, node).size());
    assertTrue(
        bridge
            .controls(tenant, node)
            .getFirst()
            .getCanonicalJson()
            .toStringUtf8()
            .contains("package"));
    verify(renewal, never()).committedTransport(any());
    doThrow(new AurigaException(CommonErrors.NOT_FOUND))
        .when(renewal)
        .pendingNodeControl(node.toString());
    assertTrue(bridge.controls(tenant, node).isEmpty());
    doThrow(new AurigaException(CommonErrors.UNAVAILABLE))
        .when(renewal)
        .pendingNodeControl(node.toString());
    assertThrows(AurigaException.class, () -> bridge.controls(tenant, node));
  }

  @Test
  void nodeReceiptRejectsDuplicateKeysBeforeCallingTheLedger() {
    var renewal = mock(NetworkRenewal.class);
    var bridge = new PlatformMsgnetNodeControl(renewal);
    var receipt =
        MsgnetControlReceipt.newBuilder()
            .setCanonicalJson(ByteString.copyFromUtf8("{\"target_id\":\"a\",\"target_id\":\"b\"}"))
            .build();
    assertThrows(
        AurigaException.class, () -> bridge.receipt(UUID.randomUUID(), UUID.randomUUID(), receipt));
    verify(renewal, never()).confirmNodeControl(anyString(), any(InstallationReceipt.class));
  }
}
