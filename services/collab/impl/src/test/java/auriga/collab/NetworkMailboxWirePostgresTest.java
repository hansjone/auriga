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
        jdbc,
        manager,
        source,
        keys,
        Clock.systemUTC());
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
          var data = NetworkJson.parse("{\"message_id\":\"" + message + "\"}");
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
          new TransactionTemplate(manager)
              .execute(
                  status -> {
                    String persisted =
                        jdbc.queryForObject(
                            "SELECT request_json FROM collab.network_wire_receipt WHERE tenant_id=? AND operation_id='op-r'",
                            String.class,
                            TENANT);
                    assertEquals(NetworkJson.canonical(NetworkJson.parse(frame)), persisted);
                    assertTrue(persisted.contains(proof));
                    return null;
                  });
          return null;
        });
  }

  static byte[] ready(String boot, String nonce, String id) {
    return bytes(
        """
        {"app_version":"1.0","type":"ready","request_id":"%s","channel_nonce":"%s","boot_id":"%s","features":[],"adapter_capabilities":[],"resume_cursor":"0","local_high_water":"0","registration_revision":"0","receipt_checkpoint":{"checkpoint_seq":"0","checkpoint_revision":"0","summary_digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},"keyset_version":"1","pending_registration":null}
        """
            .formatted(id, nonce, boot));
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
