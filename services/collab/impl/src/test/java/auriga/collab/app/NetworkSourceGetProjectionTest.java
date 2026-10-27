package auriga.collab.app;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import auriga.collab.api.NetworkSourceAuthority.Target;
import auriga.collab.grpc.NetworkAuthorityClient;
import auriga.collab.grpc.NetworkSourceGrpcAuthority;
import auriga.collab.network.local.LocalRegistry;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.persistence.TenantTransactionManager;
import auriga.common.persistence.UuidV7;
import auriga.contracts.iam.v1.*;
import io.grpc.*;
import io.grpc.netty.shaded.io.grpc.netty.NettyServerBuilder;
import io.grpc.stub.StreamObserver;
import java.math.BigDecimal;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.security.KeyPairGenerator;
import java.security.Signature;
import java.sql.Timestamp;
import java.time.*;
import java.util.*;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;
import javax.sql.DataSource;
import org.junit.jupiter.api.Test;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.transaction.support.SimpleTransactionStatus;
import org.springframework.transaction.support.TransactionSynchronizationManager;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/**
 * Production Get/projection/final observation/signer/schema, real gRPC; JDBC/gates are simulations.
 */
class NetworkSourceGetProjectionTest {
  static final Instant NOW = Instant.parse("2026-10-27T04:21:51Z");

  static final class Harness implements AutoCloseable {
    final UUID tenant = UUID.randomUUID();
    final Clock clock = Clock.fixed(NOW, ZoneOffset.UTC);
    final JdbcTemplate jdbc = mock(JdbcTemplate.class);
    final DataSource database = mock(DataSource.class);
    final TenantTransactionManager manager = mock(TenantTransactionManager.class);
    final NetworkSigningKeys keys = new NetworkSigningKeys(clock, 1);
    final AtomicInteger gets = new AtomicInteger(), observationWrites = new AtomicInteger();
    final Map<String, Object> binding = new LinkedHashMap<>(),
        lineage = new LinkedHashMap<>(),
        local = new LinkedHashMap<>(),
        slot = new LinkedHashMap<>(),
        witness = new LinkedHashMap<>();
    final ObjectNode frame;
    final NetworkAuthorityConnection connection;
    final NetworkAgentSource original;
    final NetworkMailboxWireService wire;
    final NetworkSourceControl control;
    final Server server;
    final ManagedChannel channel;
    final Instant budget;
    ObjectNode candidate;
    GetAgentSourceRequest lastRequest;
    GetAgentSourceResponse lastReply;
    Runnable afterObservation = () -> {};

    Harness(Instant budget) throws Exception {
      this.budget = budget;
      frame = NetworkSourceSchemasTest.frames().get("SourceGet").deepCopy();
      UUID registrationId = UuidV7.next(), lineageId = UuidV7.next();
      ((ObjectNode) frame.path("registration")).put("registration_id", registrationId.toString());
      ((ObjectNode) frame.path("native_witness"))
          .put("observed_at", NOW.toString())
          .put("valid_until", NOW.plusSeconds(30).toString());
      connection =
          NetworkAuthorityConnection.newBuilder()
              .setTenantId(tenant.toString())
              .setRealm("2")
              .setNode("20")
              .setIngress("ingress-test")
              .setServerId("server-test")
              .setClientId("12")
              .setClientAccount("clients-test")
              .build();
      var r = frame.path("registration");
      var registration =
          NetworkAgentSourceRegistration.newBuilder()
              .setRegistrationId(registrationId.toString())
              .setAllocationOpId(r.path("allocation_op_id").asString())
              .setAllocationFingerprint(r.path("allocation_fingerprint").asString())
              .setRegistrationRevision(1)
              .setRealm("2")
              .setNode("20")
              .setLocal("1")
              .setAddressType("session")
              .build();
      original =
          NetworkAgentSource.newBuilder()
              .setSourceBinding("source")
              .setOriginalConnection(connection)
              .setRegistration(registration)
              .setScope(NetworkAgentSourceScope.NETWORK_AGENT_SOURCE_SCOPE_DURABLE_UNICAST_SEND)
              .setRevision(1)
              .setState(NetworkAgentSourceState.NETWORK_AGENT_SOURCE_STATE_ACTIVE)
              .setOwnerUserId(UuidV7.next().toString())
              .setNotBefore(NOW.minusSeconds(1).toString())
              .setExpiresAt(NOW.plusSeconds(180).toString())
              .build();
      binding.put("id", UuidV7.next());
      binding.put("lineage_id", lineageId);
      binding.put("creation_receipt", original.toByteArray());
      binding.put("permission_version", 1L);
      binding.put("stop_version", 0L);
      lineage.put("id", lineageId);
      lineage.put("stopped", false);
      lineage.put("permission_version", 1L);
      lineage.put("stop_version", 0L);
      lineage.put("native_binding_json", "{\"binding_id\":\"b\",\"binding_revision\":\"1\"}");
      local.put("id", registrationId);
      local.put("status", "registered");
      local.put("allocation_op_id", registration.getAllocationOpId());
      local.put("allocation_fingerprint", registration.getAllocationFingerprint());
      local.put("registration_revision", BigDecimal.ONE);
      local.put("realm", "2");
      local.put("node_id", "20");
      local.put("local_id", BigDecimal.ONE);
      local.put("address_type", "session");
      when(manager.getDataSource()).thenReturn(database);
      when(manager.getTransaction(any())).thenAnswer(call -> new SimpleTransactionStatus());
      when(jdbc.getDataSource()).thenReturn(database);
      when(jdbc.queryForList(anyString(), any(Object[].class)))
          .thenAnswer(
              call -> {
                String sql = call.getArgument(0);
                if (sql.startsWith("SELECT b.*")) return List.of(new LinkedHashMap<>(binding));
                if (sql.contains("FROM collab.network_source_lineage"))
                  return List.of(new LinkedHashMap<>(lineage));
                if (sql.contains("FROM collab.network_local_registration"))
                  return List.of(new LinkedHashMap<>(local));
                if (sql.contains("FROM collab.network_source_slot"))
                  return List.of(new LinkedHashMap<>(slot));
                if (sql.contains("FROM collab.network_source_witness"))
                  return witness.isEmpty() ? List.of() : List.of(new LinkedHashMap<>(witness));
                throw new AssertionError("Unexpected read: " + sql);
              });
      when(jdbc.update(anyString(), any(Object[].class)))
          .thenAnswer(
              call -> {
                String sql = call.getArgument(0);
                Object[] args = (Object[]) call.getRawArguments()[1];
                if (sql.contains("INSERT INTO collab.network_source_slot")) {
                  slot.put("holder", args[5]);
                  slot.put("valid_until", args[6]);
                } else if (sql.contains("INSERT INTO collab.network_source_witness")) {
                  witness.put("witness_digest", args[5]);
                } else if (sql.contains("SET observation=?")) {
                  observationWrites.incrementAndGet();
                  binding.put("observation", args[0]);
                  binding.put("observed_at", args[4]);
                  binding.put("valid_until", args[5]);
                  binding.put("permission_version", args[9]);
                  binding.put("stop_version", args[8]);
                }
                return 1;
              });
      server =
          NettyServerBuilder.forAddress(new InetSocketAddress("127.0.0.1", 0))
              .addService(
                  new NetworkSourceAuthorityServiceGrpc.NetworkSourceAuthorityServiceImplBase() {
                    @Override
                    public void getAgentSource(
                        GetAgentSourceRequest request,
                        StreamObserver<GetAgentSourceResponse> observer) {
                      gets.incrementAndGet();
                      lastRequest = request;
                      assertEquals(connection, request.getConnection());
                      assertEquals(registration, request.getRegistration());
                      assertNotNull(Context.current().getDeadline());
                      lastReply =
                          GetAgentSourceResponse.newBuilder()
                              .setSource(original)
                              .setConnection(connection)
                              .setPolicyRevision(7)
                              .setObservedAt(NOW.toString())
                              .setValidUntil(NOW.plusSeconds(30).toString())
                              .setLineageStopped(false)
                              .build();
                      observer.onNext(lastReply);
                      observer.onCompleted();
                    }
                  })
              .build()
              .start();
      channel =
          ManagedChannelBuilder.forAddress("127.0.0.1", server.getPort()).usePlaintext().build();
      var authority =
          new NetworkSourceGrpcAuthority(
              new NetworkAuthorityClient(channel, Duration.ofSeconds(2)), manager);
      var pair = KeyPairGenerator.getInstance("Ed25519").generateKeyPair();
      keys.add(
          "2",
          "protocol_response",
          "get-key",
          pair.getPublic(),
          pair.getPrivate(),
          NOW.minusSeconds(30),
          NOW.plusSeconds(300),
          NetworkSigningKeys.State.ACTIVE);
      wire =
          spy(
              new NetworkMailboxWireService(
                  mock(NetworkMailboxService.class),
                  mock(LocalRegistry.class),
                  jdbc,
                  manager,
                  authority,
                  keys,
                  clock));
      var target = new Target("2", "20", UuidV7.next().toString(), 1, "nonce", "r", budget);
      var gate =
          new NetworkMailboxWireService.SourceGate(connection, frame, Map.of(), target, null, null);
      doReturn(gate).when(wire).sourceGate(any(), any());
      doNothing().when(wire).lockSourceGate(any(), anyBoolean());
      doNothing().when(wire).lockSourceErrorGate(any());
      doAnswer(
              call -> {
                if (observationWrites.get() > 0) afterObservation.run();
                return budget;
              })
          .when(wire)
          .refreshSourceGate(any());
      doAnswer(
              call -> {
                if (observationWrites.get() > 0) afterObservation.run();
                return budget;
              })
          .when(wire)
          .refreshSourceGate(any(), anyBoolean());
      doAnswer(
              call -> {
                if ("SourceGetResult".equals(call.getArgument(2)))
                  candidate = ((ObjectNode) call.getArgument(1)).deepCopy();
                return call.callRealMethod();
              })
          .when(wire)
          .signSource(any(), any(), anyString(), any());
      control = new NetworkSourceControl(wire, jdbc, manager, authority, clock);
    }

    JsonNode get() {
      assertFalse(TransactionSynchronizationManager.isActualTransactionActive());
      return TenantContext.call(
          tenant, () -> NetworkJson.parse(control.handle(connection, bytes(frame), "SourceGet")));
    }

    void verifySignature(JsonNode reply) throws Exception {
      var unsigned = (ObjectNode) reply.deepCopy();
      String signature = unsigned.path("server_auth").path("signature").asString();
      ((ObjectNode) unsigned.path("server_auth")).remove("signature");
      var verifier = Signature.getInstance("Ed25519");
      verifier.initVerify(keys.publicKey("get-key"));
      verifier.update("Auriga.ServerEnvelope.v1\0".getBytes(StandardCharsets.UTF_8));
      verifier.update(bytes(unsigned));
      assertTrue(verifier.verify(Base64.getUrlDecoder().decode(signature)));
    }

    @Override
    public void close() throws Exception {
      channel.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
      server.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
      keys.close();
    }
  }

  @Test
  void activeGetWithFractionalBudgetProducesValidSignedWholeSecondObservation() throws Exception {
    try (var h = new Harness(NOW.plusMillis(29985))) {
      var reply = h.get();
      assertEquals(1, h.gets.get());
      assertNotNull(h.lastReply);
      assertTrue(h.lastReply.hasSource());
      assertEquals(h.connection, h.lastReply.getConnection());
      var projected =
          h.candidate
              .deepCopy()
              .put("request_id", "r")
              .put("valid_until", NOW.plusSeconds(29).toString());
      projected.set("server_auth", reply.path("server_auth"));
      NetworkSchemas.validate("source-control", "SourceGetResult", projected);
      var fractional = projected.deepCopy().put("valid_until", h.budget.toString());
      assertEquals(
          "network.protocol_error",
          assertThrows(
                  AurigaException.class,
                  () -> NetworkSchemas.validate("source-control", "SourceGetResult", fractional))
              .errorCode()
              .code());
      assertEquals(
          "source_get_result",
          reply.path("type").asString(),
          () -> "wire=" + reply + "; candidate=" + h.candidate);
      assertEquals("active", reply.path("source").path("state").asString());
      assertEquals(NOW.plusSeconds(29).toString(), reply.path("valid_until").asString());
      assertTrue(Instant.parse(reply.path("valid_until").asString()).isBefore(h.budget));
      assertEquals(Timestamp.from(NOW.plusSeconds(29)), h.binding.get("valid_until"));
      assertEquals(
          h.lastReply, GetAgentSourceResponse.parseFrom((byte[]) h.binding.get("observation")));
      assertArrayEquals(h.original.toByteArray(), (byte[]) h.binding.get("creation_receipt"));
      assertEquals(1, h.gets.get());
      assertEquals(1, h.observationWrites.get());
      NetworkSchemas.validate("source-control", "SourceGetResult", reply);
      h.verifySignature(reply);
    }
  }

  @Test
  void subsecondRemainingBudgetExpiresInsteadOfRoundingUpIntoNewPermission() throws Exception {
    try (var h = new Harness(NOW.plusMillis(985))) {
      // With less than one whole second, even an error envelope has no signable lifetime.
      assertEquals(
          "network.unauthorized", assertThrows(AurigaException.class, h::get).errorCode().code());
      assertEquals(0, h.observationWrites.get());
      assertNull(h.binding.get("observation"));
      assertEquals(1, h.gets.get());
    }
  }

  @Test
  void finalStopStillRemovesValidityAfterTheBoundedObservationWasSaved() throws Exception {
    try (var h = new Harness(NOW.plusMillis(29985))) {
      h.afterObservation =
          () -> {
            h.lineage.put("stopped", true);
            h.lineage.put("stop_version", 1L);
            h.binding.put("valid_until", null);
          };
      var reply = h.get();
      assertEquals("source_get_result", reply.path("type").asString(), reply.toString());
      assertEquals("stopped", reply.path("source").path("state").asString());
      assertFalse(reply.has("valid_until"));
      assertNull(h.binding.get("valid_until"));
      assertArrayEquals(h.original.toByteArray(), (byte[]) h.binding.get("creation_receipt"));
      assertEquals(1, h.gets.get());
      h.verifySignature(reply);
    }
  }

  static byte[] bytes(JsonNode node) {
    return NetworkJson.canonical(node).getBytes(StandardCharsets.UTF_8);
  }
}
