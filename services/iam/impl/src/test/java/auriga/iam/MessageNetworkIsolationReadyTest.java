package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.contracts.msgnet.model.*;
import auriga.iam.api.*;
import java.util.*;
import java.util.concurrent.atomic.*;
import org.junit.jupiter.api.Test;

class MessageNetworkIsolationReadyTest {
  @Test
  void independentReceiptFreshLedgerAndVerifiedSignedReadersPrecedeReady() throws Exception {
    var f = new MessageNetworkProofFixture();
    var store = mock(IamStore.class);
    var ledger = mock(MessageNetworkControlLedger.class);
    var nodes = mock(NodeControlAuthority.class);
    when(nodes.binding(f.tenant, f.node))
        .thenReturn(
            new NodeControlAuthority.Binding(
                f.tenant.toString(),
                "1",
                new NodeControlPin().nodeId(f.node.toString()).targetId("node-target")));
    var current = f.record().state(IsolationRecord.StateEnum.QUARANTINED);
    when(ledger.isolation(f.tenant.toString(), "1", "node-target")).thenReturn(current);
    var request = f.record().state(IsolationRecord.StateEnum.READY).newChainReady(true);
    when(ledger.transport()).thenAnswer(call -> f.transport());
    when(ledger.transportHistoryDocument(anyString())).thenAnswer(call -> f.history());
    when(ledger.rootControlDocument())
        .thenAnswer(call -> MessageNetworkProofFixture.bytes(f.control));
    when(ledger.expectedSelectors(any())).thenAnswer(call -> f.selectors(call.getArgument(0)));
    var corruptNonce = new AtomicBoolean();
    var stale = new AtomicBoolean();
    var tamper = new AtomicBoolean();
    when(ledger.confirmationDocument(any()))
        .thenAnswer(
            call -> {
              TransportConfirmationQuery q = call.getArgument(0);
              byte[] nonce = Base64.getUrlDecoder().decode(q.getChallenge());
              if (corruptNonce.get()) nonce[0] ^= 1;
              MessageNetworkProofFixture.payload(f.evidence)
                  .put("challenge", Base64.getUrlEncoder().withoutPadding().encodeToString(nonce));
              MessageNetworkProofFixture.payload(f.evidence)
                  .put(
                      "issued_at", f.clock.instant().minusSeconds(stale.get() ? 61 : 0).toString());
              f.signEvidence();
              var wire = f.evidence.deepCopy();
              if (tamper.get())
                ((tools.jackson.databind.node.ObjectNode) wire.path("runtime_auth"))
                    .put("kid", "untrusted-runtime");
              return MessageNetworkProofFixture.bytes(wire);
            });
    when(ledger.enforceIsolation(request)).thenReturn(request);
    var execution =
        new MessageNetworkIsolationExecution(
            store,
            new MessageNetworkTargets("[]".getBytes(java.nio.charset.StandardCharsets.UTF_8)),
            nodes,
            ledger,
            record -> fail("readiness cannot substitute for real disconnection"),
            f.clock,
            f.verifier());
    assertThrows(IllegalStateException.class, () -> execution.ready(f.tenant, f.node, request));
    verify(ledger, never()).transport();
    current.state(IsolationRecord.StateEnum.CONFIRMED).identityCompromised(true);
    assertThrows(IllegalStateException.class, () -> execution.ready(f.tenant, f.node, request));
    current.identityCompromised(false);
    tamper.set(true);
    assertThrows(IllegalStateException.class, () -> execution.ready(f.tenant, f.node, request));
    verify(ledger, never()).enforceIsolation(any());
    tamper.set(false);
    stale.set(true);
    assertThrows(IllegalStateException.class, () -> execution.ready(f.tenant, f.node, request));
    stale.set(false);
    corruptNonce.set(true);
    assertThrows(IllegalStateException.class, () -> execution.ready(f.tenant, f.node, request));
    corruptNonce.set(false);
    f.authorized.put(f.readerId, null);
    assertThrows(IllegalStateException.class, () -> execution.ready(f.tenant, f.node, request));
    f.authorized.put(f.readerId, MessageNetworkProofFixture.digest(f.materialSelector));
    f.row().put("phase", "loaded");
    assertThrows(IllegalStateException.class, () -> execution.ready(f.tenant, f.node, request));
    f.row().put("phase", "handshake_verified");
    assertEquals(request, execution.ready(f.tenant, f.node, request));
    f.identities.put(f.readerId, "replaced-reader");
    assertThrows(IllegalStateException.class, () -> execution.ready(f.tenant, f.node, request));
    f.identities.put(f.readerId, f.identityId);
    f.row().put("phase", "isolated").put("isolation_enforced", true).remove("selector");
    assertThrows(IllegalStateException.class, () -> execution.ready(f.tenant, f.node, request));
    f.authorized.put(f.readerId, null);
    assertEquals(request, execution.ready(f.tenant, f.node, request));
    f.authorized.remove(f.readerId);
    assertThrows(IllegalStateException.class, () -> execution.ready(f.tenant, f.node, request));
    verify(ledger, times(2)).enforceIsolation(request);
  }
}
