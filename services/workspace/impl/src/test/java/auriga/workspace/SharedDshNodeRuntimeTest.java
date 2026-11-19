package auriga.workspace;

import static auriga.workspace.domain.SharedDshPolicy.intent;
import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.events.EventPublisher;
import auriga.common.persistence.TenantTransactionManager;
import auriga.contracts.node.v1.*;
import auriga.workspace.api.SharedDshAuthority.Inputs;
import auriga.workspace.api.WorkspaceInputs;
import auriga.workspace.app.*;
import auriga.workspace.domain.*;
import auriga.workspace.grpc.NodeSessions;
import auriga.workspace.persistence.WorkspaceStore;
import io.grpc.Context;
import io.grpc.stub.StreamObserver;
import java.security.cert.X509Certificate;
import java.time.*;
import java.util.*;
import java.util.function.Supplier;
import org.junit.jupiter.api.Test;
import org.springframework.jdbc.core.JdbcTemplate;

/** Real session, certificate, lease and placement code with a synthetic SQL boundary. */
class SharedDshNodeRuntimeTest {
  @Test
  void directoryOnlyNodeCannotClaimAProductionPool() {
    var hello =
        Hello.newBuilder()
            .setOs("linux")
            .addDrivers(RuntimeKind.RUNTIME_KIND_DOCKER)
            .putLabels(SharedDshNodeRuntime.CAPABILITY, SharedDshNodeRuntime.PREPARATION);
    assertFalse(SharedDshNodeRuntime.supports(hello.build()));
    hello.putLabels(SharedDshNodeRuntime.PROCESS_CAPABILITY, SharedDshNodeRuntime.PROCESS);
    assertTrue(SharedDshNodeRuntime.supports(hello.build()));
  }

  @Test
  void explicitDedicatedUsesTheSameFencedPlacementPreparation() throws Exception {
    try (var f = new Fixture()) {
      var row = f.members.values().iterator().next();
      f.members.clear();
      row.put("mode", "dedicated");
      f.members.put((UUID) row.get("id"), row);
      f.outbox.clear();
      f.queue(intent(row.get("id").toString(), SharedDshPlacements.binding(row), "prepare-empty"));
      var peer = f.connect(f.node, true);
      assertEquals(1, peer.commands().size());
      assertEquals(
          DshInstanceMode.DSH_INSTANCE_MODE_DEDICATED,
          peer.commands().getFirst().getBinding().getMode());
      when(f.entry.ready(any())).thenReturn(true);
      peer.input.onNext(receipt(peer.commands().getFirst(), f.clock));
      assertEquals(0, f.pending());
      assertEquals("ready", row.get("state"));
    }
  }

  @Test
  void initialPreparationSurvivesReconnectAndAcknowledgesOnlyAfterTheEntryProbe() throws Exception {
    try (var f = new Fixture()) {
      var first = f.connect(f.node, true);
      var sent = first.commands();
      assertEquals(2, sent.size());
      assertEquals(Set.of(f.pool), new HashSet<>(f.owners.keySet()));
      assertEquals(f.node, f.owners.get(f.pool));
      assertEquals(2, f.pending());

      var other = f.connect(f.otherNode, true);
      assertTrue(other.commands().isEmpty());
      other.input.onNext(receipt(sent.getFirst(), f.clock));
      assertEquals(2, f.pending());

      first.input.onCompleted();
      var reconnected = f.connect(f.node, true);
      assertEquals(sent, reconnected.commands());
      reconnected.input.onNext(receipt(sent.getFirst(), f.clock));
      assertEquals(2, f.pending());
      assertTrue(f.members.values().stream().allMatch(r -> r.get("state").equals("pending")));

      when(f.entry.ready(any())).thenReturn(true);
      for (var command : sent) reconnected.input.onNext(receipt(command, f.clock));
      assertEquals(0, f.pending());
      assertTrue(f.members.values().stream().allMatch(r -> r.get("state").equals("ready")));
      reconnected.input.onNext(receipt(sent.getFirst(), f.clock));
      f.sessions.dispatch();
      assertEquals(2, reconnected.commands().size());
      assertNull(reconnected.error);
    }
  }

  @Test
  void capabilityAndSchedulingAreRequiredBeforeTheFirstNodeClaim() throws Exception {
    try (var f = new Fixture()) {
      assertFalse(
          SharedDshNodeRuntime.supports(
              Hello.newBuilder()
                  .putLabels(SharedDshNodeRuntime.CAPABILITY, SharedDshNodeRuntime.PREPARATION)
                  .build()));
      f.connect(f.node, false);
      assertTrue(f.owners.isEmpty());
      f.nodes.get(f.otherNode).put("draining", true);
      assertTrue(f.connect(f.otherNode, true).commands().isEmpty());
      assertTrue(f.owners.isEmpty());
      f.nodes.get(f.otherNode).put("draining", false);
      f.sessions.dispatch();
      assertEquals(f.otherNode, f.owners.get(f.pool));
      assertEquals(2, f.pending());
    }
  }

  @Test
  void wrongBindingTenantGenerationAndStaleSessionCannotAcknowledge() throws Exception {
    try (var f = new Fixture()) {
      when(f.entry.ready(any())).thenReturn(true);
      var peer = f.connect(f.node, true);
      var command = peer.commands().getFirst();
      var good = receipt(command, f.clock).getSharedDshRuntimeReceipt();
      for (var binding :
          List.of(
              command.getBinding().toBuilder().setTenantId(UUID.randomUUID().toString()).build(),
              command.getBinding().toBuilder().setMemberId(UUID.randomUUID().toString()).build(),
              command.getBinding().toBuilder()
                  .setPhysicalInstanceId(UUID.randomUUID().toString())
                  .build(),
              command.getBinding().toBuilder().setEpoch(2).build(),
              command.getBinding().toBuilder().setRevision(2).build())) {
        peer.input.onNext(
            SessionRequest.newBuilder()
                .setSharedDshRuntimeReceipt(good.toBuilder().setBinding(binding))
                .build());
      }
      assertEquals(2, f.pending());
      assertNull(peer.error);

      // A new authoritative generation invalidates a receipt even while it is in flight.
      f.members.get(UUID.fromString(command.getBinding().getPlacementId())).put("revision", 2L);
      peer.input.onNext(receipt(command, f.clock));
      assertEquals(2, f.pending());

      // Model database takeover while the old Java observer still exists.
      f.holders.put(f.node, UUID.randomUUID());
      peer.input.onNext(receipt(peer.commands().getLast(), f.clock));
      assertNotNull(peer.error);
      assertEquals(2, f.pending());
    }
  }

  @Test
  void missingCertificateAndCrossTenantOutboxCannotReachANode() throws Exception {
    try (var f = new Fixture()) {
      var missing = new Peer();
      missing.input = f.sessions.session(missing);
      assertNotNull(missing.error);
      var outsider = UUID.randomUUID();
      f.addNode(outsider, UUID.randomUUID());
      assertTrue(f.connect(outsider, true).commands().isEmpty());
      assertTrue(f.owners.isEmpty());
      assertEquals(2, f.pending());
    }
  }

  @Test
  void onlyCanonicalInitialSharedIntentsAreDispatched() throws Exception {
    try (var f = new Fixture()) {
      var command = SharedDshRuntimeIntent.parseFrom((byte[]) f.outbox.getFirst().get("payload"));
      f.queue(command.toBuilder().setAction("archive-reset").build());
      f.queue(command.toBuilder().setOperationId(UUID.randomUUID().toString()).build());
      f.queue(command.toBuilder().setCancelWaitSeconds(1).build());
      f.queue(
          command.toBuilder()
              .setBinding(
                  command.getBinding().toBuilder()
                      .setMode(DshInstanceMode.DSH_INSTANCE_MODE_DEDICATED))
              .build());
      var peer = f.connect(f.node, true);
      assertEquals(2, peer.commands().size());
      assertEquals(6, f.pending());
    }
  }

  @Test
  void runtimePaginationPassesUndeliverableRowsWithoutAcknowledgingThem() throws Exception {
    try (var f = new Fixture()) {
      var command = SharedDshRuntimeIntent.parseFrom((byte[]) f.outbox.getFirst().get("payload"));
      f.outbox.getFirst().put("id", UUID.fromString("ffffffff-ffff-4fff-8fff-fffffffffff1"));
      f.outbox.getLast().put("id", UUID.fromString("ffffffff-ffff-4fff-8fff-fffffffffff2"));
      for (int i = 0; i < 100; i++) {
        f.queue(command.toBuilder().setAction("stop-pool").build());
        f.outbox
            .getLast()
            .put("id", UUID.fromString(String.format("00000000-0000-4000-8000-%012d", i)));
      }
      var peer = f.connect(f.node, true);
      assertTrue(peer.commands().isEmpty());
      f.sessions.dispatch();
      assertEquals(2, peer.commands().size());
      assertEquals(102, f.pending());
      when(f.entry.ready(any())).thenReturn(true);
      for (var sent : peer.commands()) peer.input.onNext(receipt(sent, f.clock));
      assertEquals(100, f.pending());
    }
  }

  static SessionRequest receipt(SharedDshRuntimeIntent command, Clock clock) {
    return SessionRequest.newBuilder()
        .setSharedDshRuntimeReceipt(
            SharedDshRuntimeReceipt.newBuilder()
                .setSchemaVersion(1)
                .setOperationId(command.getOperationId())
                .setBinding(command.getBinding())
                .setAction(command.getAction())
                .setReceiptId(UUID.randomUUID().toString())
                .setObservedAt(clock.instant().getEpochSecond())
                .setOutcome("confirmed")
                .setWorkspaceEmpty(true))
        .build();
  }

  static final class Peer implements StreamObserver<SessionResponse> {
    final NodeSessions.Connection connection =
        new NodeSessions.Connection(
            mock(javax.net.ssl.SSLSession.class), new java.net.InetSocketAddress("127.0.0.1", 0));
    final List<SessionResponse> frames = new ArrayList<>();
    StreamObserver<SessionRequest> input;
    Throwable error;

    public void onNext(SessionResponse frame) {
      frames.add(frame);
    }

    public void onError(Throwable error) {
      this.error = error;
    }

    public void onCompleted() {}

    List<SharedDshRuntimeIntent> commands() {
      return frames.stream()
          .filter(SessionResponse::hasSharedDshRuntimeIntent)
          .map(SessionResponse::getSharedDshRuntimeIntent)
          .toList();
    }
  }

  static final class Fixture implements AutoCloseable {
    final Clock clock;
    final UUID tenant = UUID.randomUUID(), pool = UUID.randomUUID();
    final UUID node = UUID.randomUUID(), otherNode = UUID.randomUUID();
    final Map<UUID, Map<String, Object>> nodes = new HashMap<>();
    final Map<UUID, X509Certificate> certificates = new HashMap<>();
    final Map<UUID, java.security.KeyPair> certificateKeys = new HashMap<>();
    final Map<UUID, UUID> holders = new HashMap<>(), owners = new HashMap<>();
    final Map<UUID, Map<String, Object>> members = new LinkedHashMap<>();
    final List<Map<String, Object>> outbox = new ArrayList<>();
    final JdbcTemplate jdbc = mock(JdbcTemplate.class);
    final WorkspaceStore store =
        spy(new WorkspaceStore(jdbc, mock(TenantTransactionManager.class)));
    final SharedDshPlacements.EntryProbe entry = mock(SharedDshPlacements.EntryProbe.class);
    final NodeCa ca;
    final java.security.KeyPair caKey;
    final NodeEnrollment enrollment;
    final NodeSessions sessions;
    final Map<String, Object> poolRow;

    Fixture() throws Exception {
      this(null);
    }

    Fixture(SharedDshNodeRuntime runtimeOverride) throws Exception {
      this(runtimeOverride, Clock.fixed(Instant.parse("2026-11-01T00:00:00Z"), ZoneOffset.UTC));
    }

    Fixture(SharedDshNodeRuntime runtimeOverride, Clock clock) throws Exception {
      this.clock = clock;
      caKey = TestPki.key();
      ca = new NodeCa(TestPki.ca(caKey, clock.instant()), caKey.getPrivate(), clock);
      doAnswer(c -> c.<Supplier<?>>getArgument(0).get()).when(store).tx(any());
      doAnswer(
              c ->
                  TenantContext.call(
                      c.<UUID>getArgument(0), () -> c.<Supplier<?>>getArgument(1).get()))
          .when(store)
          .in(any(), any());
      doNothing().when(store).lock();
      doAnswer(
              c -> {
                var row = nodes.get(c.<UUID>getArgument(0));
                if (!store.tenant().equals(row.get("tenant_id")))
                  throw new AurigaException(CommonErrors.NOT_FOUND);
                return row;
              })
          .when(store)
          .node(any());
      doAnswer(c -> nodes.get(c.<UUID>getArgument(0)).get("tenant_id"))
          .when(store)
          .route(any(), eq("node"));
      doAnswer(c -> query(c.getArgument(0), (Object[]) c.getRawArguments()[1]))
          .when(jdbc)
          .queryForList(anyString(), any(Object[].class));
      doAnswer(c -> update(c.getArgument(0), (Object[]) c.getRawArguments()[1]))
          .when(jdbc)
          .update(anyString(), any(Object[].class));
      addNode(node, tenant);
      addNode(otherNode, tenant);
      String digest = "sha256:" + "a".repeat(64);
      poolRow =
          new HashMap<>(Map.of("id", pool, "state", "pending", "authorization_digest", digest));
      for (int i = 0; i < 2; i++) {
        UUID id = UUID.randomUUID();
        var row =
            new HashMap<String, Object>(
                Map.of(
                    "id",
                    id,
                    "tenant_id",
                    tenant,
                    "member_id",
                    UUID.randomUUID(),
                    "pool_id",
                    pool,
                    "mode",
                    "shared",
                    "state",
                    "pending",
                    "epoch",
                    1L,
                    "revision",
                    1L));
        members.put(id, row);
        queue(intent(id.toString(), SharedDshPlacements.binding(row), "prepare-empty"));
      }
      var inputs = mock(Inputs.class);
      when(inputs.authorizationSetDigest(eq(tenant), any())).thenReturn(digest);
      var placements = new SharedDshPlacements(store, inputs, entry, clock);
      var workspace =
          new WorkspaceService(
              store,
              mock(WorkspaceInputs.class),
              new WorkspaceTemplates(List.of()),
              mock(WorkspaceCrypto.class),
              mock(EventPublisher.class),
              clock);
      enrollment =
          new NodeEnrollment(store, ca, clock, "https://workspace.invalid:7443", List.of());
      sessions =
          new NodeSessions(
              enrollment,
              workspace,
              runtimeOverride == null
                  ? new SharedDshNodeRuntime(store, placements)
                  : runtimeOverride);
    }

    void addNode(UUID id, UUID tid) throws Exception {
      var key = TestPki.key();
      certificateKeys.put(id, key);
      var cert = ca.issue(TestPki.csr(key), id);
      certificates.put(id, cert);
      nodes.put(
          id,
          new HashMap<>(
              Map.of(
                  "tenant_id",
                  tid,
                  "revoked",
                  false,
                  "schedulable",
                  true,
                  "draining",
                  false,
                  "certificate_serial",
                  cert.getSerialNumber().toString(),
                  "sealing_key",
                  new byte[32])));
    }

    void queue(SharedDshRuntimeIntent command) {
      outbox.add(
          new HashMap<>(
              Map.of(
                  "id",
                  UUID.randomUUID(),
                  "tenant_id",
                  tenant,
                  "kind",
                  "runtime",
                  "payload",
                  command.toByteArray(),
                  "delivered",
                  false)));
    }

    int pending() {
      return (int) outbox.stream().filter(r -> !(boolean) r.get("delivered")).count();
    }

    Peer connect(UUID id, boolean capable) {
      var peer = new Peer();
      var context =
          Context.current()
              .withValues(
                  NodeSessions.PEER,
                  certificates.get(id),
                  NodeSessions.CONNECTION,
                  peer.connection);
      context.run(() -> peer.input = sessions.session(peer));
      var hello =
          Hello.newBuilder()
              .setNodeId(id.toString())
              .setProtocolVersion(1)
              .setOs("linux")
              .addDrivers(RuntimeKind.RUNTIME_KIND_DOCKER)
              .setSealingKeyId(
                  HexFormat.of().formatHex(WorkspaceCrypto.hash(new byte[32])).substring(0, 16));
      if (capable)
        hello
            .putLabels(SharedDshNodeRuntime.CAPABILITY, SharedDshNodeRuntime.PREPARATION)
            .putLabels(SharedDshNodeRuntime.PROCESS_CAPABILITY, SharedDshNodeRuntime.PROCESS);
      peer.input.onNext(SessionRequest.newBuilder().setHello(hello).build());
      peer.input.onNext(
          SessionRequest.newBuilder().setInventory(Inventory.getDefaultInstance()).build());
      assertNull(peer.error);
      return peer;
    }

    List<Map<String, Object>> query(String sql, Object[] args) {
      if (sql.startsWith("INSERT INTO node_session")) {
        UUID id = (UUID) args[2];
        if (holders.putIfAbsent(id, (UUID) args[3]) != null) return List.of();
        return List.of(Map.of("holder", args[3]));
      }
      if (sql.startsWith("SELECT * FROM shared_dsh_outbox")) {
        UUID after = (UUID) args[1];
        return outbox.stream()
            .filter(
                r ->
                    r.get("tenant_id").equals(args[0])
                        && !(boolean) r.get("delivered")
                        && (after == null
                            || r.get("id").toString().compareTo(after.toString()) > 0))
            .sorted(Comparator.comparing(r -> r.get("id").toString()))
            .limit(100)
            .toList();
      }
      if (sql.startsWith("SELECT node_id FROM shared_dsh_pool_node"))
        return owners.containsKey(args[1])
            ? List.of(Map.of("node_id", owners.get(args[1])))
            : List.of();
      if (sql.startsWith("SELECT x.* FROM shared_dsh_placement")) {
        var row = members.get(args[1]);
        return row != null
                && row.get("tenant_id").equals(args[0])
                && row.get("state").equals("pending")
            ? List.of(row)
            : List.of();
      }
      if (sql.startsWith("SELECT * FROM shared_dsh_placement"))
        return Optional.ofNullable(members.get(args[1])).stream().toList();
      if (sql.startsWith("SELECT * FROM shared_dsh_pool")) return List.of(poolRow);
      return List.of();
    }

    int update(String sql, Object[] args) {
      if (sql.startsWith("UPDATE node_session"))
        return Objects.equals(holders.get(args[1]), args[2]) ? 1 : 0;
      if (sql.startsWith("DELETE FROM node_session"))
        return holders.remove(args[1], args[2]) ? 1 : 0;
      if (sql.startsWith("INSERT INTO shared_dsh_pool_node")) {
        assertNull(owners.putIfAbsent((UUID) args[1], (UUID) args[2]));
      } else if (sql.startsWith("UPDATE shared_dsh_placement")) {
        members.get(args[1]).put("state", "ready");
      } else if (sql.startsWith("UPDATE shared_dsh_pool")) poolRow.put("state", "ready");
      else if (sql.startsWith("UPDATE shared_dsh_outbox")) {
        outbox.stream()
            .filter(r -> r.get("id").equals(args[1]))
            .forEach(r -> r.put("delivered", true));
      }
      return 1;
    }

    public void close() {
      sessions.close();
    }
  }
}
