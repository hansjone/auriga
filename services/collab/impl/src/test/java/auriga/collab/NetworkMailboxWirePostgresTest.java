package auriga.collab;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.collab.app.*;
import auriga.collab.grpc.*;
import auriga.common.context.*;
import auriga.common.persistence.*;
import auriga.contracts.iam.v1.NetworkAuthorityConnection;
import auriga.iam.api.NetworkAuthority;
import auriga.testsupport.PostgresExtension;
import java.security.KeyPairGenerator;
import java.time.*;
import java.util.*;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.transaction.support.TransactionTemplate;
import tools.jackson.databind.node.ObjectNode;

@ExtendWith(PostgresExtension.class)
class NetworkMailboxWirePostgresTest {
  static JdbcTemplate jdbc;
  static TenantTransactionManager manager;
  static NetworkMailboxWireService wire;
  static NetworkAuthorityClient iam;
  static NetworkSourceGrpcAuthority source;
  static NetworkSigningKeys keys;
  static final UUID TENANT = UUID.randomUUID();

  @BeforeAll
  static void setup() throws Exception {
    var roles =
        PostgresExtension.serviceRoles(
            NetworkMailboxWirePostgresTest.class, "wire_owner", "wire_app", "collab");
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
    iam = mock(NetworkAuthorityClient.class);
    when(iam.active(anyString(), anyString())).thenReturn(true);
    when(iam.check(any())).thenReturn(new NetworkAuthority.Decision(true, "allowed", Map.of()));
    when(iam.resolve(any(), eq("0")))
        .thenAnswer(
            i ->
                auriga.contracts.iam.v1.ResolveNetworkTargetResponse.newBuilder()
                    .setAllowed(true)
                    .setConnection(i.getArgument(0, NetworkAuthorityConnection.class))
                    .setLocal("0")
                    .setTargetType("system")
                    .setValidUntil(Instant.now().plusSeconds(60).toString())
                    .build());
    source = new NetworkSourceGrpcAuthority(iam, manager);
    keys = new NetworkSigningKeys(Clock.systemUTC(), 1);
    var pair = KeyPairGenerator.getInstance("Ed25519").generateKeyPair();
    keys.add(
        "2",
        "protocol_response",
        "wire-test",
        pair.getPublic(),
        pair.getPrivate(),
        Instant.now().minusSeconds(60),
        Instant.now().plusSeconds(3600),
        NetworkSigningKeys.State.ACTIVE);
    wire = newWire();
  }

  static NetworkMailboxWireService newWire() {
    return new NetworkMailboxWireService(
        new NetworkMailboxService(jdbc, manager, source, keys, Clock.systemUTC(), iam),
        new auriga.collab.network.local.LocalRegistry(
            jdbc, manager, source, iam, Clock.systemUTC()),
        jdbc,
        manager,
        source,
        keys,
        Clock.systemUTC());
  }

  @Test
  void readyReturnsDurableTypedRegistrationCounters() {
    call(
        () -> {
          var c =
              NetworkAuthorityGrpcTest.connection(TENANT).toBuilder()
                  .setNode("91")
                  .setIngress("typed-original")
                  .build();
          var initial =
              NetworkJson.parse(wire.ready(c, ready("typed-boot", "typed-nonce", "typed-ready")));
          var registry =
              new auriga.collab.network.local.LocalRegistry(
                  jdbc, manager, source, iam, Clock.systemUTC());
          var target =
              new auriga.collab.api.NetworkSourceAuthority.Target(
                  "2",
                  "91",
                  initial.path("node_identity").path("incarnation").asString(),
                  1,
                  "typed-nonce",
                  "register-typed",
                  Instant.now().plusSeconds(60));
          source.withTarget(
              c,
              target,
              () ->
                  registry.register(
                      TENANT,
                      """
              {"app_version":"1.0","type":"local_register","request_id":"register-typed","epoch":"1","boot_id":"typed-boot","candidate_local":"7","allocation_op_id":"typed-op","allocation_fingerprint":"sha256:%s","address_type":"subagent","expected_registration_revision":"0"}
              """
                          .formatted("a".repeat(64))));
          var request =
              (ObjectNode)
                  NetworkJson.parse(ready("typed-next", "typed-next-nonce", "typed-next-ready"));
          request.put("local_high_water", "7").put("registration_revision", "1");
          var result =
              NetworkJson.parse(
                  wire.ready(
                      c.toBuilder().setIngress("typed-next").build(), bytes(request.toString())));
          assertEquals("ready", result.path("status").asString());
          assertEquals("7", result.path("local_high_water").asString());
          assertEquals("1", result.path("registration_revision").asString());
          assertFalse(result.has("recovered_registration"));
          return null;
        });
  }

  @Test
  void missingReadyCapabilitiesCannotTakeOverAnExistingRoute() {
    call(
        () -> {
          var c =
              NetworkAuthorityGrpcTest.connection(TENANT).toBuilder()
                  .setNode("50")
                  .setIngress("ready-security")
                  .build();
          byte[] valid = ready("security-boot", "security-nonce", "security-ready");
          var response = NetworkJson.parse(wire.ready(c, valid));
          assertEquals(3, response.path("required_features").size());
          int calls = mockingDetails(iam).getInvocations().size();
          for (String side : List.of("features", "adapter_capabilities"))
            for (String missing :
                List.of("server_auth_v1", "source_attestation_v1", "inbox_only_v1")) {
              var invalid = (ObjectNode) NetworkJson.parse(valid);
              var original = invalid.path(side).deepCopy();
              invalid.putArray(side);
              for (var capability : original)
                if (!missing.equals(capability.asString())) invalid.withArray(side).add(capability);
              NetworkAuthorityGrpcTest.denied(
                  () ->
                      wire.ready(
                          c.toBuilder().setIngress("bad-" + side + "-" + missing).build(),
                          bytes(invalid.toString())),
                  "network.protocol_error");
            }
          assertEquals(calls, mockingDetails(iam).getInvocations().size());
          new TransactionTemplate(manager)
              .execute(
                  status -> {
                    assertEquals(
                        1,
                        jdbc.queryForObject(
                            "SELECT connection_epoch FROM collab.network_node WHERE tenant_id=? AND node_id='50'",
                            Integer.class,
                            TENANT));
                    assertEquals(
                        "ready-security",
                        jdbc.queryForObject(
                            "SELECT ingress_id FROM collab.network_node WHERE tenant_id=? AND node_id='50'",
                            String.class,
                            TENANT));
                    return null;
                  });
          return null;
        });
  }

  @Test
  void firstContactRequiresAllowedSystemStageAndNeverInventsSessionAssignment() {
    call(
        () -> {
          var c =
              NetworkAuthorityGrpcTest.connection(TENANT).toBuilder()
                  .setNode("51")
                  .setIngress("contact-security")
                  .build();
          wire.ready(c, ready("contact-boot", "contact-nonce", "contact-ready"));
          UUID message = UUID.randomUUID(),
              delivery = UUID.randomUUID(),
              recipient = UUID.randomUUID();
          var data =
              storedMessage(message, "51", false)
                  .put("kind", "first_contact")
                  .put("intent", "request")
                  .put("contact_scope", "conversation");
          new TransactionTemplate(manager)
              .execute(
                  status -> {
                    jdbc.update(
                        "INSERT INTO collab.network_message(id,tenant_id,realm,kind,body,source_node,source_local,message_json,source_context_json) VALUES (?,?,'2','first_contact','hello','63','1',?,'{}')",
                        message,
                        TENANT,
                        data.toString());
                    jdbc.update(
                        "INSERT INTO collab.network_mail_item(id,tenant_id,message_id,target_realm,target_node,mail_seq) VALUES (?,?,?,'2','51',1)",
                        delivery,
                        TENANT,
                        message);
                    jdbc.update(
                        "INSERT INTO collab.network_recipient(id,tenant_id,delivery_id,target_local) VALUES (?,?,?,'0')",
                        recipient,
                        TENANT,
                        delivery);
                    jdbc.update(
                        "INSERT INTO collab.network_contact(id,tenant_id,message_id,source_realm,source_node,source_local,target_realm,target_node,expires_at) VALUES (?,? ,?,'2','63','1','2','51',?)",
                        message,
                        TENANT,
                        message,
                        java.sql.Timestamp.from(Instant.now().plusSeconds(3600)));
                    jdbc.update(
                        "UPDATE collab.network_node SET head_seq=1 WHERE tenant_id=? AND node_id='51'",
                        TENANT);
                    return null;
                  });
          String frame = NetworkMailboxWireFrames.receipt("51", delivery, recipient, data);
          NetworkAuthorityGrpcTest.denied(
              () -> wire.receipt(c, bytes(frame)), "network.unauthorized");
          new TransactionTemplate(manager)
              .execute(
                  status -> {
                    assertEquals(
                        0,
                        jdbc.queryForObject(
                            "SELECT count(*) FROM collab.network_acceptance_proof WHERE tenant_id=? AND recipient_id=?",
                            Integer.class,
                            TENANT,
                            recipient));
                    jdbc.update(
                        "UPDATE collab.network_contact SET status='allowed' WHERE tenant_id=? AND id=?",
                        TENANT,
                        message);
                    return null;
                  });
          assertTrue(
              NetworkJson.parse(wire.receipt(c, bytes(frame)))
                  .path("mail_item_closed")
                  .asBoolean());
          var session =
              (ObjectNode)
                  NetworkJson.parse(
                      frame
                          .replace("op-r", "op-session")
                          .replace("\"accepted_by\":\"system\"", "\"accepted_by\":\"session\""));
          ((ObjectNode) session.path("results").get(0))
              .set(
                  "assigned_address",
                  NetworkJson.parse(
                      "{\"version\":1,\"realm\":\"2\",\"node\":\"51\",\"local\":\"1\",\"type\":\"session\"}"));
          NetworkAuthorityGrpcTest.denied(
              () -> wire.receipt(c, bytes(session.toString())), "network.unauthorized");
          new TransactionTemplate(manager)
              .execute(
                  status -> {
                    assertEquals(
                        1,
                        jdbc.queryForObject(
                            "SELECT count(*) FROM collab.network_acceptance_proof WHERE tenant_id=? AND recipient_id=?",
                            Integer.class,
                            TENANT,
                            recipient));
                    return null;
                  });
          return null;
        });
  }

  @Test
  void readyIsIdempotentAndNewIngressFencesOldAcrossInstances() {
    call(
        () -> {
          var c = NetworkAuthorityGrpcTest.connection(TENANT);
          byte[] ready = ready("boot-a", "nonce-a", "ready-a");
          var first = NetworkJson.parse(wire.ready(c, ready));
          assertEquals("ready", first.path("status").asString());
          assertEquals("1", first.path("epoch").asString());
          assertEquals("nonce-a", first.path("server_auth").path("channel_nonce").asString());
          assertEquals("1", NetworkJson.parse(wire.ready(c, ready)).path("epoch").asString());
          assertEquals("head", NetworkJson.parse(wire.head(c)).path("type").asString());
          var page =
              NetworkJson.parse(
                  wire.fetch(
                      c,
                      bytes(
                          """
          {"app_version":"1.0","type":"fetch","request_id":"fetch-a","epoch":"1","resume_cursor":"0","continuation":null,"max_items":1,"max_bytes":65536}
          """)));
          assertEquals("fetch_result", page.path("type").asString());
          assertEquals(
              "fetch-a", page.path("page").path("server_auth").path("request_id").asString());
          var next = c.toBuilder().setIngress("ingress-next").build();
          assertEquals(
              "2",
              NetworkJson.parse(wire.ready(next, ready("boot-b", "nonce-b", "ready-b")))
                  .path("epoch")
                  .asString());
          NetworkAuthorityGrpcTest.denied(() -> wire.head(c), "network.stale_epoch");
          var restarted = newWire();
          assertEquals("2", NetworkJson.parse(restarted.head(next)).path("epoch").asString());
          NetworkAuthorityGrpcTest.denied(() -> restarted.head(c), "network.stale_epoch");
          NetworkAuthorityGrpcTest.denied(() -> wire.ready(c, ready), "network.stale_epoch");
          NetworkAuthorityGrpcTest.denied(
              () -> wire.head(c.toBuilder().setNode("21").build()), "network.stale_epoch");
          var recovery = (ObjectNode) NetworkJson.parse(ready("boot-c", "nonce-c", "ready-c"));
          recovery.put("local_high_water", "8");
          var third = c.toBuilder().setIngress("ingress-third").build();
          assertEquals(
              "recovery_conflict",
              NetworkJson.parse(wire.ready(third, bytes(recovery.toString())))
                  .path("status")
                  .asString());
          NetworkAuthorityGrpcTest.denied(() -> wire.head(third), "network.recovery_conflict");
          return null;
        });
  }

  @Test
  void receiptPersistsEvidenceChecksVersionAndRejectsChangedOperation() {
    call(
        () -> {
          var c =
              NetworkAuthorityGrpcTest.connection(TENANT).toBuilder()
                  .setNode("30")
                  .setIngress("ingress-receipt")
                  .build();
          wire.ready(c, ready("boot-r", "nonce-r", "ready-r"));
          UUID message = UUID.randomUUID(),
              delivery = UUID.randomUUID(),
              recipient = UUID.randomUUID();
          var data = storedMessage(message, "30", false);
          new TransactionTemplate(manager)
              .execute(
                  status -> {
                    jdbc.update(
                        "INSERT INTO collab.network_message(id,tenant_id,realm,kind,body,source_node,source_local,message_json,source_context_json) VALUES (?,?,'2','message','hello','10','0',?,'{}')",
                        message,
                        TENANT,
                        data.toString());
                    jdbc.update(
                        "INSERT INTO collab.network_mail_item(id,tenant_id,message_id,target_realm,target_node,mail_seq) VALUES (?,?,?,'2','30',1)",
                        delivery,
                        TENANT,
                        message);
                    jdbc.update(
                        "INSERT INTO collab.network_recipient(id,tenant_id,delivery_id,target_local) VALUES (?,?,?,'0')",
                        recipient,
                        TENANT,
                        delivery);
                    jdbc.update(
                        "UPDATE collab.network_node SET head_seq=1 WHERE tenant_id=? AND node_id='30'",
                        TENANT);
                    return null;
                  });
          String proof = "sha256:" + "a".repeat(64);
          String frame = NetworkMailboxWireFrames.receipt("30", delivery, recipient, data);
          NetworkAuthorityGrpcTest.denied(
              () ->
                  wire.receipt(
                      c,
                      bytes(
                          frame.replace(
                              "\"expected_state_version\":\"1\"",
                              "\"expected_state_version\":\"0\""))),
              "network.conflict");
          var committed = NetworkJson.parse(wire.receipt(c, bytes(frame)));
          assertEquals("1", committed.path("committed_cursor").asString());
          assertTrue(committed.path("mail_item_closed").asBoolean());
          assertEquals(
              committed.path("results"),
              NetworkJson.parse(wire.receipt(c, bytes(frame))).path("results"));
          NetworkAuthorityGrpcTest.denied(
              () -> wire.receipt(c, bytes(frame.replace("host-r", "host-changed"))),
              "network.conflict");
          String retry =
              frame
                  .replace("\"operation_id\":\"op-r\"", "\"operation_id\":\"op-other\"")
                  .replace("receipt-r", "receipt-other");
          assertEquals(
              committed.path("results"),
              NetworkJson.parse(wire.receipt(c, bytes(retry))).path("results"));
          NetworkAuthorityGrpcTest.denied(
              () -> wire.receipt(c, bytes(retry.replace("host-r", "contradiction"))),
              "network.conflict");
          NetworkAuthorityGrpcTest.denied(
              () ->
                  wire.receipt(
                      c,
                      bytes(
                          retry
                              .replace("op-other", "op-third")
                              .replace("sha256:" + "a".repeat(64), "sha256:" + "b".repeat(64)))),
              "network.conflict");
          var start = new java.util.concurrent.CountDownLatch(1);
          try (var workers = java.util.concurrent.Executors.newVirtualThreadPerTaskExecutor()) {
            var futures = new java.util.ArrayList<java.util.concurrent.Future<String>>();
            for (int index = 0; index < 2; index++) {
              String concurrent = retry.replace("op-other", "op-concurrent-" + index);
              futures.add(
                  workers.submit(
                      () -> {
                        assertTrue(start.await(5, java.util.concurrent.TimeUnit.SECONDS));
                        return call(() -> wire.receipt(c, bytes(concurrent)));
                      }));
            }
            start.countDown();
            for (var result : futures)
              assertEquals(
                  committed.path("results"),
                  NetworkJson.parse(result.get(10, java.util.concurrent.TimeUnit.SECONDS))
                      .path("results"));
          } catch (Exception error) {
            throw new AssertionError(error);
          }
          new TransactionTemplate(manager)
              .execute(
                  status -> {
                    String persisted =
                        jdbc.queryForObject(
                            "SELECT request_json FROM collab.network_wire_receipt WHERE tenant_id=? AND realm=? AND node_id=? AND operation_id='op-r'",
                            String.class,
                            TENANT,
                            c.getRealm(),
                            c.getNode());
                    assertEquals(NetworkJson.canonical(NetworkJson.parse(frame)), persisted);
                    assertTrue(persisted.contains(proof));
                    assertEquals(
                        1,
                        jdbc.queryForObject(
                            "SELECT count(*) FROM collab.network_acceptance_proof WHERE tenant_id=? AND recipient_id=?",
                            Integer.class,
                            TENANT,
                            recipient));
                    assertEquals(
                        2,
                        jdbc.queryForObject(
                            "SELECT state_version FROM collab.network_recipient WHERE tenant_id=? AND id=?",
                            Integer.class,
                            TENANT,
                            recipient));
                    return null;
                  });
          return null;
        });
  }

  @Test
  void concurrentContradictoryFirstProofsHaveExactlyOneWinner() throws Exception {
    var c =
        NetworkAuthorityGrpcTest.connection(TENANT).toBuilder()
            .setNode("52")
            .setIngress("proof-race")
            .build();
    call(() -> wire.ready(c, ready("race-boot", "race-nonce", "race-ready")));
    UUID message = UUID.randomUUID(), delivery = UUID.randomUUID(), recipient = UUID.randomUUID();
    var data = storedMessage(message, "52", false);
    call(
        () ->
            new TransactionTemplate(manager)
                .execute(
                    status -> {
                      jdbc.update(
                          "INSERT INTO collab.network_message(id,tenant_id,realm,kind,body,source_node,source_local,message_json,source_context_json) VALUES (?,?,'2','message','hello','63','0',?,'{}')",
                          message,
                          TENANT,
                          data.toString());
                      jdbc.update(
                          "INSERT INTO collab.network_mail_item(id,tenant_id,message_id,target_realm,target_node,mail_seq) VALUES (?,?,?,'2','52',1)",
                          delivery,
                          TENANT,
                          message);
                      jdbc.update(
                          "INSERT INTO collab.network_recipient(id,tenant_id,delivery_id,target_local) VALUES (?,?,?,'0')",
                          recipient,
                          TENANT,
                          delivery);
                      jdbc.update(
                          "UPDATE collab.network_node SET head_seq=1 WHERE tenant_id=? AND node_id='52'",
                          TENANT);
                      return null;
                    }));
    String first = NetworkMailboxWireFrames.receipt("52", delivery, recipient, data);
    String second = first.replace("op-r", "op-race-other").replace("host-r", "host-race-other");
    var start = new java.util.concurrent.CountDownLatch(1);
    var outcomes = new ArrayList<String>();
    try (var workers = java.util.concurrent.Executors.newVirtualThreadPerTaskExecutor()) {
      var futures = new ArrayList<java.util.concurrent.Future<String>>();
      for (String frame : List.of(first, second))
        futures.add(
            workers.submit(
                () -> {
                  assertTrue(start.await(5, java.util.concurrent.TimeUnit.SECONDS));
                  try {
                    call(() -> wire.receipt(c, bytes(frame)));
                    return "committed";
                  } catch (auriga.common.error.AurigaException error) {
                    return error.errorCode().code();
                  }
                }));
      start.countDown();
      for (var future : futures)
        outcomes.add(future.get(10, java.util.concurrent.TimeUnit.SECONDS));
    }
    assertEquals(1, Collections.frequency(outcomes, "committed"));
    assertEquals(1, Collections.frequency(outcomes, "network.conflict"));
    call(
        () ->
            new TransactionTemplate(manager)
                .execute(
                    status -> {
                      assertEquals(
                          1,
                          jdbc.queryForObject(
                              "SELECT count(*) FROM collab.network_acceptance_proof WHERE tenant_id=? AND recipient_id=?",
                              Integer.class,
                              TENANT,
                              recipient));
                      assertEquals(
                          1,
                          jdbc.queryForObject(
                              "SELECT count(*) FROM collab.network_wire_receipt WHERE tenant_id=? AND node_id='52'",
                              Integer.class,
                              TENANT));
                      assertEquals(
                          2,
                          jdbc.queryForObject(
                              "SELECT state_version FROM collab.network_recipient WHERE tenant_id=? AND id=?",
                              Integer.class,
                              TENANT,
                              recipient));
                      assertEquals(
                          1,
                          jdbc.queryForObject(
                              "SELECT committed_cursor FROM collab.network_node WHERE tenant_id=? AND node_id='52'",
                              Integer.class,
                              TENANT));
                      return null;
                    }));
  }

  @Test
  void impossibleAcceptanceStagesHaveNoStateCursorOrProofSideEffects() {
    call(
        () -> {
          var c =
              NetworkAuthorityGrpcTest.connection(TENANT).toBuilder()
                  .setNode("40")
                  .setIngress("ingress-stage")
                  .build();
          wire.ready(c, ready("boot-stage", "nonce-stage", "ready-stage"));
          UUID message = UUID.randomUUID(),
              delivery = UUID.randomUUID(),
              recipient = UUID.randomUUID();
          var data = storedMessage(message, "40", true);
          new TransactionTemplate(manager)
              .execute(
                  status -> {
                    jdbc.update(
                        "INSERT INTO collab.network_message(id,tenant_id,realm,kind,body,source_node,source_local,message_json,source_context_json) VALUES (?,?,'2','message','hello','63','0',?,'{}')",
                        message,
                        TENANT,
                        data.toString());
                    jdbc.update(
                        "INSERT INTO collab.network_mail_item(id,tenant_id,message_id,target_realm,target_node,mail_seq) VALUES (?,?,?,'2','40',1)",
                        delivery,
                        TENANT,
                        message);
                    jdbc.update(
                        "INSERT INTO collab.network_recipient(id,tenant_id,delivery_id,target_local) VALUES (?,?,?,'0')",
                        recipient,
                        TENANT,
                        delivery);
                    jdbc.update(
                        "UPDATE collab.network_node SET head_seq=1 WHERE tenant_id=? AND node_id='40'",
                        TENANT);
                    return null;
                  });
          String valid = NetworkMailboxWireFrames.receipt("40", delivery, recipient, data);
          var foreign = (ObjectNode) NetworkJson.parse(valid);
          ((ObjectNode) foreign.path("results").get(0))
              .set(
                  "assigned_address",
                  NetworkJson.parse(
                      "{\"version\":1,\"realm\":\"2\",\"node\":\"41\",\"local\":\"1\",\"type\":\"session\"}"));
          for (String bad :
              List.of(
                  valid.replace("\"accepted_by\":\"system\"", "\"accepted_by\":\"session\""),
                  foreign.toString()))
            NetworkAuthorityGrpcTest.denied(
                () -> wire.receipt(c, bytes(bad)), "network.unauthorized");
          new TransactionTemplate(manager)
              .execute(
                  status -> {
                    assertEquals(
                        "pending",
                        jdbc.queryForObject(
                            "SELECT transport_status FROM collab.network_recipient WHERE tenant_id=? AND id=?",
                            String.class,
                            TENANT,
                            recipient));
                    assertEquals(
                        0,
                        jdbc.queryForObject(
                            "SELECT committed_cursor FROM collab.network_node WHERE tenant_id=? AND node_id='40'",
                            Integer.class,
                            TENANT));
                    assertEquals(
                        0,
                        jdbc.queryForObject(
                            "SELECT count(*) FROM collab.network_wire_receipt WHERE tenant_id=? AND node_id='40'",
                            Integer.class,
                            TENANT));
                    assertEquals(
                        0,
                        jdbc.queryForObject(
                            "SELECT count(*) FROM collab.network_acceptance_proof WHERE tenant_id=? AND recipient_id=?",
                            Integer.class,
                            TENANT,
                            recipient));
                    return null;
                  });
          return null;
        });
  }

  static ObjectNode storedMessage(UUID message, String node, boolean broadcast) {
    try (var input =
        NetworkMailboxWirePostgresTest.class.getResourceAsStream("/network-fixtures/frames.json")) {
      var frames = tools.jackson.databind.json.JsonMapper.builder().build().readTree(input);
      var data =
          (ObjectNode)
              frames
                  .path("frames")
                  .path(broadcast ? "delivery_broadcast" : "delivery_session_message")
                  .path("value")
                  .path("message")
                  .deepCopy();
      data.put("message_id", message.toString());
      if (!broadcast)
        data.set(
            "to",
            NetworkJson.parse(
                "{\"version\":1,\"realm\":\"2\",\"node\":\""
                    + node
                    + "\",\"local\":\"0\",\"type\":\"system\"}"));
      return data;
    } catch (java.io.IOException error) {
      throw new java.io.UncheckedIOException(error);
    }
  }

  static byte[] ready(String boot, String nonce, String id) {
    try (var input =
        NetworkMailboxWirePostgresTest.class.getResourceAsStream("/network/plugin-ready.json")) {
      var ready =
          (ObjectNode) tools.jackson.databind.json.JsonMapper.builder().build().readTree(input);
      return bytes(
          ready.put("boot_id", boot).put("channel_nonce", nonce).put("request_id", id).toString());
    } catch (java.io.IOException error) {
      throw new java.io.UncheckedIOException(error);
    }
  }

  static byte[] bytes(String value) {
    return value.getBytes(java.nio.charset.StandardCharsets.UTF_8);
  }

  static <T> T call(java.util.function.Supplier<T> work) {
    return RequestContext.start(null, null)
        .withPrincipal(NetworkMailboxGrpcTest.principal("svc:ai-gateway"))
        .call(() -> TenantContext.call(TENANT, work::get));
  }
}
