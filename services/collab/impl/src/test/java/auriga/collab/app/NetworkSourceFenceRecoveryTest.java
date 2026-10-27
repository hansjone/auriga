package auriga.collab.app;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import auriga.collab.api.NetworkSourceAuthority.Target;
import auriga.collab.grpc.*;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.persistence.*;
import auriga.contracts.iam.v1.*;
import io.grpc.*;
import io.grpc.netty.shaded.io.grpc.netty.NettyServerBuilder;
import io.grpc.stub.StreamObserver;
import java.net.InetSocketAddress;
import java.sql.Timestamp;
import java.time.*;
import java.util.*;
import java.util.concurrent.TimeUnit;
import java.util.function.UnaryOperator;
import javax.sql.DataSource;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.EnumSource;
import org.junit.jupiter.params.provider.ValueSource;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.transaction.support.*;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** Production recovery and real gRPC; simulated JDBC/gates, not PG/IAM persistence evidence. */
class NetworkSourceFenceRecoveryTest {
  static final Instant NOW = Instant.parse("2026-10-27T00:00:00Z");

  static class Harness implements AutoCloseable {
    final UUID tenant = UuidV7.next();
    final DataSource database = mock(DataSource.class);
    final TenantTransactionManager manager = mock(TenantTransactionManager.class);
    final JdbcTemplate jdbc = mock(JdbcTemplate.class);
    final NetworkMailboxWireService wire = mock(NetworkMailboxWireService.class);
    final Map<String, Object> row = new LinkedHashMap<>(), slot = new LinkedHashMap<>();
    final ObjectNode frame;
    final RegisterAgentSourceRequest original;
    final RegisterAgentSourceResponse failure;
    final Server server;
    final ManagedChannel channel;
    final NetworkSourceControl control;
    final List<String> calls = new ArrayList<>();
    Status readFailure = Status.UNAVAILABLE, fenceFailure;
    boolean readReturnsReceipt, blockRefresh;
    UnaryOperator<RegisterAgentSourceResponse> echo = UnaryOperator.identity();
    Runnable afterRead = () -> {}, afterFence = () -> {};

    Harness() throws Exception {
      frame = NetworkSourceSchemasTest.frames().get("SourceRegister").deepCopy();
      var c =
          NetworkAuthorityConnection.newBuilder()
              .setTenantId(tenant.toString())
              .setRealm("2")
              .setNode("20")
              .setIngress("a".repeat(26))
              .setServerId("N" + "A".repeat(55))
              .setClientId("1")
              .setClientAccount("clients")
              .build();
      original =
          RegisterAgentSourceRequest.newBuilder()
              .setConnection(c)
              .setOperationId("op")
              .setScope(NetworkAgentSourceScope.NETWORK_AGENT_SOURCE_SCOPE_DURABLE_UNICAST_SEND)
              .setRegistration(
                  NetworkAgentSourceRegistration.newBuilder()
                      .setRegistrationId("reg")
                      .setAllocationOpId("alloc")
                      .setAllocationFingerprint("sha256:" + "a".repeat(64))
                      .setRegistrationRevision(1)
                      .setRealm("2")
                      .setNode("20")
                      .setLocal("1")
                      .setAddressType("session"))
              .build();
      var business = NetworkJson.object();
      for (String key :
          List.of("registration", "scope", "expected_revision", "predecessor_binding"))
        business.set(key, frame.path(key));
      row.put("id", UuidV7.next());
      row.put("tenant_id", tenant);
      row.put("method", "Register");
      row.put("operation_id", "op");
      row.put("realm", "2");
      row.put("node_id", "20");
      row.put("business_json", NetworkJson.canonical(business));
      row.put(
          "original_frame",
          NetworkJson.canonical(frame).getBytes(java.nio.charset.StandardCharsets.UTF_8));
      row.put("original_connection", c.toByteArray());
      row.put("iam_request", original.toByteArray());
      row.put(
          "iam_request_json", NetworkJson.canonical(NetworkSourceControl.requestJson(original)));
      row.put("request_digest", NetworkJson.digest(NetworkSourceControl.requestJson(original)));
      row.put("epoch", 1L);
      row.put("boot_id", "boot");
      row.put("status", "pending");
      row.put("lineage_id", UuidV7.next());
      row.put("iam_response", null);
      row.put("result_json", null);
      failure =
          RegisterAgentSourceResponse.newBuilder()
              .setDefinitivelyUncommitted(true)
              .setOperationId("op")
              .setRequestDigest(row.get("request_digest").toString())
              .setRejectionReason("original_connection_invalid_uncommitted")
              .build();
      frame.put("mode", "reconcile").putNull("native_witness");
      var current = c.toBuilder().setIngress("b".repeat(26)).setClientId("2").build();
      var gate =
          new NetworkMailboxWireService.SourceGate(
              current,
              frame,
              Map.of(),
              new Target("2", "20", "incarnation", 1, "nonce", "r", NOW.plusSeconds(30)),
              null,
              null);
      when(wire.sourceGate(any(), any())).thenReturn(gate);
      when(wire.refreshSourceGate(any(), anyBoolean()))
          .thenAnswer(
              call -> {
                if (blockRefresh) throw new AurigaException(CommonErrors.UNAVAILABLE);
                return NOW.plusSeconds(30);
              });
      when(wire.signSource(any(), any(), anyString(), any()))
          .thenAnswer(call -> NetworkJson.canonical(call.getArgument(1)));
      when(manager.getDataSource()).thenReturn(database);
      when(manager.getTransaction(any()))
          .thenAnswer(
              call -> {
                assertFalse(TransactionSynchronizationManager.isActualTransactionActive());
                TransactionSynchronizationManager.setActualTransactionActive(true);
                TransactionSynchronizationManager.bindResource(database, new Object());
                return new SimpleTransactionStatus();
              });
      doAnswer(
              call -> {
                clearTransaction();
                return null;
              })
          .when(manager)
          .commit(any());
      doAnswer(
              call -> {
                clearTransaction();
                return null;
              })
          .when(manager)
          .rollback(any());
      when(jdbc.getDataSource()).thenReturn(database);
      when(jdbc.queryForList(anyString(), any(Object[].class)))
          .thenAnswer(
              call -> {
                String sql = call.getArgument(0);
                if (sql.contains("pg_advisory_xact_lock")) return List.of();
                if (sql.contains("FROM collab.network_source_operation"))
                  return List.of(new LinkedHashMap<>(row));
                if (sql.contains("FROM collab.network_source_slot"))
                  return List.of(new LinkedHashMap<>(slot));
                if (sql.contains("FROM collab.network_source_lineage"))
                  return List.of(Map.of("id", row.get("lineage_id")));
                throw new AssertionError(sql);
              });
      when(jdbc.update(anyString(), any(Object[].class)))
          .thenAnswer(
              call -> {
                String sql = call.getArgument(0);
                Object[] args = (Object[]) call.getRawArguments()[1];
                if (sql.contains("INSERT INTO collab.network_source_slot")) {
                  slot.put("holder", args[5]);
                  slot.put("valid_until", args[6]);
                } else if (sql.contains("SET status=?,iam_response=?")) {
                  row.put("status", args[0]);
                  row.put("iam_response", args[1]);
                  row.put("result_json", args[2]);
                }
                return 1;
              });
      server =
          NettyServerBuilder.forAddress(new InetSocketAddress("127.0.0.1", 0))
              .addService(
                  new NetworkSourceAuthorityServiceGrpc.NetworkSourceAuthorityServiceImplBase() {
                    @Override
                    public void readAgentSourceReceipt(
                        ReadAgentSourceReceiptRequest r,
                        StreamObserver<RegisterAgentSourceResponse> o) {
                      assertEquals(original, r.getOriginalRequest());
                      calls.add("read");
                      afterRead.run();
                      if (readReturnsReceipt) {
                        o.onNext(echo.apply(failure));
                        o.onCompleted();
                      } else o.onError(readFailure.asRuntimeException());
                    }

                    @Override
                    public void fenceAgentSourceReceipt(
                        FenceAgentSourceReceiptRequest r,
                        StreamObserver<RegisterAgentSourceResponse> o) {
                      assertEquals(original, r.getOriginalRequest());
                      calls.add("fence");
                      afterFence.run();
                      if (fenceFailure != null) o.onError(fenceFailure.asRuntimeException());
                      else {
                        o.onNext(echo.apply(failure));
                        o.onCompleted();
                      }
                    }

                    @Override
                    public void registerAgentSource(
                        RegisterAgentSourceRequest r,
                        StreamObserver<RegisterAgentSourceResponse> o) {
                      calls.add("register");
                      o.onError(Status.INTERNAL.asRuntimeException());
                    }
                  })
              .build()
              .start();
      channel =
          ManagedChannelBuilder.forAddress("127.0.0.1", server.getPort()).usePlaintext().build();
      var source =
          new NetworkSourceGrpcAuthority(
              new NetworkAuthorityClient(channel, Duration.ofSeconds(2)), manager);
      control =
          new NetworkSourceControl(wire, jdbc, manager, source, Clock.fixed(NOW, ZoneOffset.UTC));
    }

    void clearTransaction() {
      TransactionSynchronizationManager.unbindResource(database);
      TransactionSynchronizationManager.setActualTransactionActive(false);
    }

    JsonNode recover() {
      return TenantContext.call(
          tenant,
          () ->
              NetworkJson.parse(
                  control.handle(
                      original.getConnection().toBuilder()
                          .setIngress("b".repeat(26))
                          .setClientId("2")
                          .build(),
                      NetworkJson.canonical(frame)
                          .getBytes(java.nio.charset.StandardCharsets.UTF_8),
                      "SourceRegister")));
    }

    void unknown() {
      var result = recover();
      assertEquals("network.outcome_unknown", result.path("code").asString(), result.toString());
      assertEquals("pending", row.get("status"));
      assertNull(row.get("iam_response"));
    }

    public void close() throws Exception {
      assertFalse(TransactionSynchronizationManager.isActualTransactionActive());
      assertFalse(TransactionSynchronizationManager.hasResource(database));
      channel.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
      server.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
    }
  }

  @Test
  void unavailableReadExplicitlyFencesOriginalOutsideEveryDatabaseScope() throws Exception {
    try (var h = new Harness()) {
      var result = h.recover();
      assertEquals("not_committed", result.path("result").asString());
      assertEquals(List.of("read", "fence"), h.calls);
      assertEquals("not_committed", h.row.get("status"));
      assertArrayEquals(h.failure.toByteArray(), (byte[]) h.row.get("iam_response"));
    }
  }

  @ParameterizedTest
  @EnumSource(
      value = Status.Code.class,
      names = {
        "INVALID_ARGUMENT",
        "ABORTED",
        "ALREADY_EXISTS",
        "UNAUTHENTICATED",
        "PERMISSION_DENIED",
        "CANCELLED",
        "DEADLINE_EXCEEDED",
        "UNIMPLEMENTED",
        "INTERNAL",
        "UNKNOWN",
        "NOT_FOUND",
        "RESOURCE_EXHAUSTED"
      })
  void onlyUnavailableCanEnterExplicitFence(Status.Code code) throws Exception {
    try (var h = new Harness()) {
      h.readFailure = Status.fromCode(code).withDescription("miss unavailable");
      h.unknown();
      assertEquals(List.of("read"), h.calls);
    }
  }

  @ParameterizedTest
  @ValueSource(strings = {"empty", "partial", "error", "gate", "row", "slot", "terminal"})
  void failedFenceOrChangedOriginalNeverCompletesPending(String fault) throws Exception {
    try (var h = new Harness()) {
      switch (fault) {
        case "empty" -> h.echo = value -> RegisterAgentSourceResponse.getDefaultInstance();
        case "partial" -> h.echo = value -> value.toBuilder().clearRequestDigest().build();
        case "error" -> h.fenceFailure = Status.UNAVAILABLE;
        case "gate" -> h.afterRead = () -> h.blockRefresh = true;
        case "row" -> h.afterFence = () -> h.row.put("original_frame", new byte[] {1});
        case "slot" -> h.afterRead = () -> h.slot.put("valid_until", Timestamp.from(NOW));
        case "terminal" ->
            h.afterRead =
                () -> {
                  h.row.put("status", "registered");
                  h.row.put("iam_response", new byte[] {1});
                };
        default -> throw new AssertionError(fault);
      }
      if (fault.equals("terminal")) {
        assertEquals("network.outcome_unknown", h.recover().path("code").asString());
        assertEquals("registered", h.row.get("status"));
        assertArrayEquals(new byte[] {1}, (byte[]) h.row.get("iam_response"));
        assertEquals(List.of("read"), h.calls);
      } else h.unknown();
    }
  }

  @Test
  void completeReadAndMalformedReadNeverFence() throws Exception {
    try (var h = new Harness()) {
      h.readReturnsReceipt = true;
      assertEquals("not_committed", h.recover().path("result").asString());
      assertEquals(List.of("read"), h.calls);
      h.calls.clear();
      h.readFailure = Status.UNAVAILABLE;
      h.readReturnsReceipt = false;
      assertEquals("network.outcome_unknown", h.recover().path("code").asString());
      assertEquals(List.of("read"), h.calls);
      assertEquals("not_committed", h.row.get("status"));
    }
    try (var h = new Harness()) {
      h.readReturnsReceipt = true;
      h.echo = value -> RegisterAgentSourceResponse.getDefaultInstance();
      h.unknown();
      assertEquals(List.of("read"), h.calls);
    }
  }

  @Test
  void cancelledOuterRequestNeverStartsFence() throws Exception {
    try (var h = new Harness();
        var context = Context.current().withCancellation()) {
      h.afterRead = () -> context.cancel(null);
      context.run(h::unknown);
      assertEquals(List.of("read"), h.calls);
    }
  }

  @Test
  @SuppressWarnings("unchecked")
  void exhaustedSharedBudgetIsNotReplacedByANewSourceRequestBudget() throws Exception {
    try (var h = new Harness()) {
      var field = NetworkAuthorityClient.class.getDeclaredField("SOURCE_DEADLINE");
      field.setAccessible(true);
      var budget = (ScopedValue<Long>) field.get(null);
      ScopedValue.where(budget, System.nanoTime() - 1)
          .run(
              () -> {
                assertEquals(
                    "network.expired",
                    assertThrows(AurigaException.class, h::recover).errorCode().code());
                assertEquals(0, NetworkAuthorityClient.sourceRemainingMillis());
              });
      assertEquals(List.of(), h.calls);
      assertEquals("pending", h.row.get("status"));
      assertNull(h.row.get("iam_response"));
    }
  }
}
