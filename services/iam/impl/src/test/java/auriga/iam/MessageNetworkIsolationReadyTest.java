package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.contracts.msgnet.model.*;
import auriga.iam.api.*;
import java.time.*;
import java.util.*;
import org.junit.jupiter.api.Test;

class MessageNetworkIsolationReadyTest {
  @Test
  void independentReceiptAndFreshCompleteGlobalReadersPrecedeReady() {
    UUID tenant = UUID.randomUUID(), node = UUID.randomUUID();
    var store = mock(IamStore.class);
    var ledger = mock(MessageNetworkControlLedger.class);
    var nodes = mock(NodeControlAuthority.class);
    var clock = Clock.fixed(Instant.parse("2026-11-15T00:00:00Z"), ZoneOffset.UTC);
    when(nodes.binding(tenant, node))
        .thenReturn(
            new NodeControlAuthority.Binding(
                tenant.toString(),
                "1",
                new NodeControlPin().nodeId(node.toString()).targetId("node-target")));
    var current =
        new IsolationRecord()
            .tenantId(tenant.toString())
            .realm("1")
            .targetId("node-target")
            .minimumControlRevision("2")
            .controlDigest("sha256:" + "2".repeat(64))
            .challenge("control-challenge")
            .state(IsolationRecord.StateEnum.QUARANTINED)
            .newChainReady(false)
            .identityCompromised(false);
    when(ledger.isolation(tenant.toString(), "1", "node-target")).thenReturn(current);
    var request =
        new IsolationRecord()
            .tenantId(tenant.toString())
            .realm("1")
            .targetId("node-target")
            .minimumControlRevision("2")
            .controlDigest(current.getControlDigest())
            .challenge(current.getChallenge())
            .state(IsolationRecord.StateEnum.READY)
            .newChainReady(true)
            .identityCompromised(false);
    var execution =
        new MessageNetworkIsolationExecution(
            store,
            new MessageNetworkTargets("[]".getBytes(java.nio.charset.StandardCharsets.UTF_8)),
            nodes,
            ledger,
            record -> fail("readiness cannot replace actual isolation"),
            clock);
    assertThrows(IllegalStateException.class, () -> execution.ready(tenant, node, request));
    verify(ledger, never()).transport();
    current.state(IsolationRecord.StateEnum.CONFIRMED).identityCompromised(true);
    assertThrows(IllegalStateException.class, () -> execution.ready(tenant, node, request));
    current.identityCompromised(false);
    var bundle =
        new SignedTransportTrustBundle()
            .payload(
                new TransportTrustBundle()
                    .tenantId(tenant.toString())
                    .realm("1")
                    .authorityId("authority")
                    .authorityDigest("sha256:" + "3".repeat(64))
                    .bundleVersion("1")
                    .phase(TransportTrustBundle.PhaseEnum.DUAL_TRUST)
                    .requiredReaderIds(List.of("network-reader")));
    when(ledger.transport()).thenReturn(bundle);
    var rows = new ArrayList<TransportReaderEvidence>();
    var stamp = new java.util.concurrent.atomic.AtomicReference<>(clock.instant());
    var corruptNonce = new java.util.concurrent.atomic.AtomicBoolean();
    when(ledger.confirmation(any()))
        .thenAnswer(
            call -> {
              TransportConfirmationQuery query = call.getArgument(0);
              return new SignedTransportConfirmation()
                  .payload(
                      new TransportConfirmation()
                          .tenantId(tenant.toString())
                          .realm("1")
                          .authorityId("authority")
                          .authorityDigest(bundle.getPayload().getAuthorityDigest())
                          .controlRevision("2")
                          .bundleVersion(query.getBundleVersion())
                          .bundleDigest(query.getBundleDigest())
                          .challenge(corruptNonce.get() ? "wrong" : query.getChallenge())
                          .issuedAt(stamp.get().toString())
                          .phase(
                              TransportConfirmation.PhaseEnum.valueOf(
                                  bundle.getPayload().getPhase().name()))
                          .requiredReaderIds(Set.of("network-reader"))
                          .readers(rows));
            });
    assertThrows(IllegalStateException.class, () -> execution.ready(tenant, node, request));
    String digest =
        "sha256:" + Secrets.hash(InstallationJson.canonical(Json.MAPPER.valueToTree(bundle)));
    var selector =
        new LocalProjectionSelector()
            .tenantId(tenant.toString())
            .realm("1")
            .installationId("network-installation")
            .bundleVersion("1")
            .bundleDigest(digest);
    rows.add(
        new TransportReaderEvidence()
            .readerId("network-reader")
            .identityId("network-installation")
            .selector(selector)
            .phase(TransportReaderEvidence.PhaseEnum.LOADED)
            .isolationEnforced(false));
    stamp.set(clock.instant().minusSeconds(61));
    assertThrows(IllegalStateException.class, () -> execution.ready(tenant, node, request));
    stamp.set(clock.instant());
    corruptNonce.set(true);
    assertThrows(IllegalStateException.class, () -> execution.ready(tenant, node, request));
    corruptNonce.set(false);
    bundle.getPayload().phase(TransportTrustBundle.PhaseEnum.NEW_LEAVES);
    selector.bundleDigest(
        "sha256:" + Secrets.hash(InstallationJson.canonical(Json.MAPPER.valueToTree(bundle))));
    assertThrows(IllegalStateException.class, () -> execution.ready(tenant, node, request));
    rows.getFirst().phase(TransportReaderEvidence.PhaseEnum.HANDSHAKE_VERIFIED);
    when(ledger.enforceIsolation(request)).thenReturn(request);
    assertEquals(request, execution.ready(tenant, node, request));
    verify(ledger, times(1)).enforceIsolation(request);
  }
}
