package auriga.workspace.grpc;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.contracts.msgnet.model.*;
import auriga.contracts.node.v1.MsgnetControlReceipt;
import auriga.iam.api.*;
import com.google.protobuf.ByteString;
import java.util.*;
import org.junit.jupiter.api.Test;

class PinnedMsgnetNodeControlTest {
  @Test
  void deliversOnlyTrustedTargetsAndNeverUnlocksFromReceiptClaims() {
    var authority = mock(NodeControlAuthority.class);
    var ledger = mock(MessageNetworkControlLedger.class);
    UUID tenant = UUID.randomUUID(), node = UUID.randomUUID();
    var binding =
        new NodeControlAuthority.Binding(
            tenant.toString(),
            "1",
            new NodeControlPin().nodeId(node.toString()).targetId("fixed-target"));
    when(authority.binding(tenant, node)).thenReturn(binding);
    var service = new PinnedMsgnetNodeControl(authority, ledger);
    when(ledger.pending(tenant.toString(), "1", "fixed-target")).thenReturn(Optional.empty());
    assertTrue(service.controls(tenant, node).isEmpty());
    var wrong =
        new ControlPackage().tenantId(tenant.toString()).realm("1").targetId("claimed-target");
    when(ledger.pending(tenant.toString(), "1", "fixed-target")).thenReturn(Optional.of(wrong));
    assertThrows(RuntimeException.class, () -> service.controls(tenant, node));
    var pending =
        new ControlPackage().tenantId(tenant.toString()).realm("1").targetId("fixed-target");
    when(ledger.pending(tenant.toString(), "1", "fixed-target")).thenReturn(Optional.of(pending));
    byte[] raw = "{}".getBytes(java.nio.charset.StandardCharsets.UTF_8);
    when(ledger.canonicalControl(pending)).thenReturn(raw);
    assertEquals(
        ByteString.copyFrom(raw), service.controls(tenant, node).getFirst().getCanonicalJson());
    when(authority.verify(eq(tenant), eq(node), any(byte[].class), eq(pending)))
        .thenThrow(new IllegalArgumentException("untrusted receipt"));
    assertThrows(
        RuntimeException.class,
        () ->
            service.receipt(
                tenant,
                node,
                MsgnetControlReceipt.newBuilder()
                    .setCanonicalJson(ByteString.copyFrom(raw))
                    .build()));
    verify(ledger, never()).confirm(any());
    when(ledger.transport())
        .thenReturn(
            new SignedTransportTrustBundle()
                .payload(new TransportTrustBundle().tenantId("another-tenant").realm("1")));
    assertThrows(RuntimeException.class, () -> service.transports(tenant, node));
  }
}
