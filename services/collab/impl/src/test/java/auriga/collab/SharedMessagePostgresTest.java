package auriga.collab;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import auriga.collab.app.*;
import auriga.collab.grpc.*;
import auriga.collab.network.keys.MsgnetRenewal;
import auriga.collab.network.local.*;
import auriga.common.context.*;
import auriga.common.persistence.*;
import auriga.contracts.collab.v1.CallSharedMessageRequest;
import auriga.contracts.iam.v1.*;
import auriga.contracts.node.v1.*;
import auriga.iam.api.NetworkAuthority;
import auriga.testsupport.PostgresExtension;
import com.google.protobuf.ByteString;
import java.security.KeyPairGenerator;
import java.time.*;
import java.time.temporal.ChronoUnit;
import java.util.*;
import java.util.function.Supplier;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.transaction.support.TransactionTemplate;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.json.JsonMapper;
import tools.jackson.databind.node.ObjectNode;

/**
 * Real local/mailbox/producer/receive ledgers. IAM external calls are doubles; IAM has its own PG
 * test.
 */
@ExtendWith(PostgresExtension.class)
class SharedMessagePostgresTest {
  static final JsonMapper JSON = JsonMapper.builder().build();
  static JdbcTemplate jdbc;
  static TenantTransactionManager manager;
  static SharedMessageService service;
  static final UUID TENANT = UUID.randomUUID();
  static final Map<String, SharedMessageContext> contexts = new HashMap<>();
  static final String NODE = UUID.randomUUID().toString();

  @BeforeAll
  static void setup() throws Exception {
    var roles =
        PostgresExtension.serviceRoles(
            SharedMessagePostgresTest.class,
            "shared_message_owner",
            "shared_message_app",
            "collab");
    CommonMigrations.migrate(roles.owner().dataSource(), "collab");
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas("collab")
                .defaultSchema("collab")
                .locations("classpath:db/collab")
                .load())
        .migrate();
    jdbc = new JdbcTemplate(roles.runtime().dataSource());
    manager = new TenantTransactionManager(roles.runtime().dataSource());
    var iam = mock(SharedMessageIamClient.class);
    when(iam.open(any()))
        .thenAnswer(
            call -> {
              OpenSharedMessageContextRequest r = call.getArgument(0);
              var saved = contexts.get(r.getOperationId());
              if (saved != null) {
                assertEquals(saved.getBinding(), r.getCapability().getBinding());
                return saved;
              }
              var c =
                  SharedMessageContext.newBuilder()
                      .setBinding(r.getCapability().getBinding())
                      .setContextId(r.getOperationId())
                      .setOperationId(r.getOperationId())
                      .setAuthenticatedNodeId(r.getAuthenticatedNodeId())
                      .setBootId(r.getBootId())
                      .setNativeBindingId(r.getNativeBindingId())
                      .setNativeBindingRevision(r.getNativeBindingRevision())
                      .setRealm("2")
                      .setNode(Integer.toString(contexts.size() + 1))
                      .setIncarnation(UUID.randomUUID().toString())
                      .setChannelNonce(UUID.randomUUID().toString())
                      .setSourceBinding(UUID.randomUUID().toString())
                      .setSourceRevision(1)
                      .setPolicyRevision(1)
                      .setNotBefore(
                          Instant.now().minusSeconds(1).truncatedTo(ChronoUnit.SECONDS).toString())
                      .setExpiresAt(
                          Instant.now()
                              .plusSeconds(3600)
                              .truncatedTo(ChronoUnit.SECONDS)
                              .toString())
                      .build();
              contexts.put(c.getContextId(), c);
              return c;
            });
    when(iam.read(anyString()))
        .thenAnswer(call -> Objects.requireNonNull(contexts.get(call.getArgument(0))));
    when(iam.read(anyString(), anyString()))
        .thenAnswer(
            call ->
                contexts.values().stream()
                    .filter(
                        c ->
                            c.getRealm().equals(call.getArgument(0))
                                && c.getNode().equals(call.getArgument(1)))
                    .findFirst()
                    .orElseThrow());
    var limits = new EnumMap<NetworkAuthority.Limit, Long>(NetworkAuthority.Limit.class);
    for (var limit : NetworkAuthority.Limit.values()) limits.put(limit, limit.defaultValue());
    when(iam.check(any())).thenReturn(new NetworkAuthority.Decision(true, "allowed", limits));
    when(iam.active(anyString(), anyString())).thenReturn(true);
    when(iam.activeBatch(anyList()))
        .thenAnswer(
            call -> {
              Map<NetworkAuthority.NodeReference, Boolean> result = new HashMap<>();
              for (var n : call.<List<NetworkAuthority.NodeReference>>getArgument(0))
                result.put(n, true);
              return result;
            });
    when(iam.sourceRequest(any())).thenAnswer(call -> call.<Supplier<?>>getArgument(0).get());
    var source = new SharedMessageSource(iam, manager, new LocalTargets(jdbc, manager));
    var keys = new NetworkSigningKeys(Clock.systemUTC(), 1);
    for (String purpose : List.of("business_delivery", "protocol_response", "management_state")) {
      var pair = KeyPairGenerator.getInstance("Ed25519").generateKeyPair();
      keys.add(
          "2",
          purpose,
          purpose,
          pair.getPublic(),
          pair.getPrivate(),
          Instant.now().minusSeconds(60),
          Instant.now().plusSeconds(3600),
          NetworkSigningKeys.State.ACTIVE);
    }
    var mailbox = new NetworkMailboxService(jdbc, manager, source, keys, Clock.systemUTC(), iam);
    var locals = new LocalRegistry(jdbc, manager, source, iam, Clock.systemUTC());
    var wire =
        new NetworkMailboxWireService(
            mailbox, locals, jdbc, manager, source, keys, Clock.systemUTC());
    var renewal = mock(MsgnetRenewal.class);
    when(renewal.publicControl()).thenReturn("{}");
    when(renewal.publication()).thenReturn("{}");
    when(renewal.announcement()).thenReturn("{}");
    service =
        new SharedMessageService(
            iam,
            source,
            mailbox,
            wire,
            locals,
            jdbc,
            manager,
            keys,
            () -> renewal,
            Clock.systemUTC());
  }

  @Test
  void bootstrapAllocatesWithoutEnablingReceiveAndDurableSendFetchAreIdempotent() {
    call(
        () -> {
          var a = new Native();
          var b = new Native();
          var first = invoke(a, "context", a.contextBody());
          var second = invoke(b, "context", b.contextBody());
          assertEquals(6, first.path("sourceGet").path("source").path("registration").size());
          assertEquals(8, first.path("registration").size());
          assertEquals("shared_node_http", first.path("connection").path("transport").asString());
          assertTrue(first.path("sourceGet").has("server_auth"));
          assertFalse(first.path("mailbox").path("receiveEnabled").asBoolean());
          assertEquals("1", first.path("mailbox").path("receiveVersion").asString());
          assertEquals("0", first.path("mailbox").path("headSeq").asString());
          assertEquals("0", first.path("mailbox").path("committedCursor").asString());
          var before = a.cap;
          a.cap =
              a.cap.toBuilder()
                  .setTaskId(UUID.randomUUID().toString())
                  .setJti(UUID.randomUUID().toString())
                  .build();
          var next = invoke(a, "context", a.contextBody());
          assertEquals(first.path("registration"), next.path("registration"));
          assertEquals(first.path("connection").path("node"), next.path("connection").path("node"));
          new TransactionTemplate(manager)
              .executeWithoutResult(
                  s ->
                      assertEquals(
                          false,
                          jdbc.queryForObject(
                              "SELECT receive_enabled FROM collab.network_local_registration WHERE tenant_id=? AND allocation_op_id=?",
                              Boolean.class,
                              TENANT,
                              b.operation)));
          var receive =
              frame("receive_set")
                  .put("epoch", "1")
                  .put("boot_id", b.boot)
                  .put("enabled", true)
                  .put("expected_version", "1");
          receive.set("registration", second.path("sourceGet").path("source").path("registration"));
          assertTrue(
              invoke(b, "receive", b.receiveBody("receiveSet", receive))
                  .path("enabled")
                  .asBoolean());
          var send =
              a.base().put("contextId", a.operation).put("messageId", UUID.randomUUID().toString());
          send.putObject("target")
              .put("realm", "2")
              .put("node", second.path("connection").path("node").asString())
              .put("local", "1");
          send.putObject("body").put("subtype", "text").put("text", "hello");
          var stored = invoke(a, "send", send);
          assertEquals("platform_stored", stored.path("status").asString());
          assertEquals(stored, invoke(a, "send", send));
          var changed = send.deepCopy();
          ((ObjectNode) changed.path("body")).put("text", "changed");
          assertThrows(RuntimeException.class, () -> invoke(a, "send", changed));
          var fetch =
              frame("fetch")
                  .put("epoch", "1")
                  .put("resume_cursor", "0")
                  .putNull("continuation")
                  .put("max_items", 8)
                  .put("max_bytes", 65536);
          var page = invoke(b, "receive", b.receiveBody("fetch", fetch));
          assertEquals("fetch_result", page.path("type").asString());
          assertEquals(1, page.path("items").size());
          assertTrue(page.path("page").has("server_auth"));
          assertTrue(page.path("items").get(0).has("server_auth"));
          new TransactionTemplate(manager)
              .executeWithoutResult(
                  s -> {
                    assertEquals(
                        1,
                        jdbc.queryForObject(
                            "SELECT count(*) FROM collab.network_message WHERE tenant_id=?",
                            Integer.class,
                            TENANT));
                    assertEquals(
                        1,
                        jdbc.queryForObject(
                            "SELECT count(*) FROM collab.network_shared_submit WHERE tenant_id=?",
                            Integer.class,
                            TENANT));
                  });
          return null;
        });
  }

  static JsonNode invoke(Native n, String op, ObjectNode body) {
    return NetworkJson.parse(
        service.call(
            CallSharedMessageRequest.newBuilder()
                .setCapability(n.cap)
                .setAuthenticatedNodeId(NODE)
                .setOperation(op)
                .setRequestJson(ByteString.copyFromUtf8(body.toString()))
                .build()));
  }

  static ObjectNode frame(String type) {
    return JSON.createObjectNode()
        .put("app_version", "1.0")
        .put("type", type)
        .put("request_id", UUID.randomUUID().toString());
  }

  static <T> T call(Supplier<T> work) {
    return RequestContext.start(null, null)
        .withPrincipal(NetworkMailboxGrpcTest.principal("svc:ai-gateway"))
        .call(() -> TenantContext.call(TENANT, work::get));
  }

  static final class Native {
    final String operation = UUID.randomUUID().toString(),
        boot = UUID.randomUUID().toString(),
        nativeId = UUID.randomUUID().toString();
    SharedDshCapability cap =
        SharedDshCapability.newBuilder()
            .setSchemaVersion(1)
            .setBinding(
                SharedDshSessionBinding.newBuilder()
                    .setTenantId(TENANT.toString())
                    .setMemberId(UUID.randomUUID().toString())
                    .setPlacementId(UUID.randomUUID().toString())
                    .setPhysicalInstanceId(UUID.randomUUID().toString())
                    .setMode(DshInstanceMode.DSH_INSTANCE_MODE_SHARED)
                    .setEpoch(1)
                    .setRevision(1)
                    .setAuthGeneration(1)
                    .setSessionId(UUID.randomUUID().toString())
                    .setPlatformSessionId(UUID.randomUUID().toString()))
            .setTaskId(UUID.randomUUID().toString())
            .setAudience("auriga.shared.message")
            .addAllScopes(List.of("message.send", "message.receive"))
            .setCredentialVersion(1)
            .setIssuedAt(Instant.now().getEpochSecond())
            .setExpiresAt(Instant.now().plusSeconds(600).getEpochSecond())
            .setJti(UUID.randomUUID().toString())
            .build();

    ObjectNode base() {
      var b =
          JSON.createObjectNode()
              .put("tenantId", TENANT.toString())
              .put("sessionId", cap.getBinding().getSessionId())
              .put("taskId", cap.getTaskId())
              .put("capability", "gateway-verified-synthetic")
              .put("bootId", boot);
      var now = Instant.now().truncatedTo(ChronoUnit.SECONDS);
      b.putObject("nativeWitness")
          .put("witness_id", UUID.randomUUID().toString())
          .put("binding_id", nativeId)
          .put("binding_revision", "1")
          .put("observed_at", now.toString())
          .put("valid_until", now.plusSeconds(30).toString());
      return b;
    }

    ObjectNode contextBody() {
      return base().put("operationId", operation);
    }

    ObjectNode receiveBody(String operationName, ObjectNode frame) {
      var b = base().put("contextId", operation).put("operation", operationName);
      b.set("frame", frame);
      return b;
    }
  }
}
