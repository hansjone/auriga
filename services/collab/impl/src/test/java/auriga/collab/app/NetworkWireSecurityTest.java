package auriga.collab.app;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.collab.grpc.NetworkSourceGrpcAuthority;
import auriga.common.error.AurigaException;
import auriga.common.persistence.TenantTransactionManager;
import java.time.Clock;
import java.util.*;
import org.junit.jupiter.api.Test;
import org.springframework.jdbc.core.JdbcTemplate;
import tools.jackson.databind.node.ObjectNode;

class NetworkWireSecurityTest {
  @Test
  void nonzeroLocalsUseCollabRegistrationAndNeverIamGuessing() {
    UUID tenant = UUID.randomUUID();
    var client = mock(auriga.collab.grpc.NetworkAuthorityClient.class);
    var manager = new TenantTransactionManager(mock(javax.sql.DataSource.class));
    var c =
        auriga.contracts.iam.v1.NetworkAuthorityConnection.newBuilder()
            .setTenantId(tenant.toString())
            .setRealm("2")
            .setNode("20")
            .build();
    var target =
        new auriga.collab.api.NetworkSourceAuthority.Target(
            "2", "20", "inc", 1, "nonce", "request", java.time.Instant.now().plusSeconds(60));
    var absent = new NetworkSourceGrpcAuthority(client, manager);
    absent.withTarget(
        c,
        target,
        () -> {
          assertEquals("system", absent.resolveTargetType(tenant, "2", "20", "0"));
          reject("common.unavailable", () -> absent.resolveTargetType(tenant, "2", "20", "1"));
          return null;
        });
    var registered =
        new NetworkSourceGrpcAuthority(
            client,
            manager,
            (tid, realm, node, local) ->
                new auriga.collab.api.NetworkLocalTargets.Registration(
                    realm, node, local, "subagent", "revision-1"));
    assertEquals(
        "subagent",
        registered.withTarget(
            c, target, () -> registered.resolveTargetType(tenant, "2", "20", "1")));
    var forged =
        new NetworkSourceGrpcAuthority(
            client,
            manager,
            (tid, realm, node, local) ->
                new auriga.collab.api.NetworkLocalTargets.Registration(
                    realm, "21", local, "session", "revision-1"));
    forged.withTarget(
        c,
        target,
        () -> {
          reject("network.unauthorized", () -> forged.resolveTargetType(tenant, "2", "20", "1"));
          return null;
        });
    verifyNoInteractions(client);
  }

  @Test
  void publicPluginReadyMatchesRequiredSecurityFeatures() throws Exception {
    var ready = pluginReady();
    NetworkSchemas.validate("protocol", "Ready", ready);
    assertEquals(
        Set.of("server_auth_v1", "source_attestation_v1", "inbox_only_v1"),
        new HashSet<>(NetworkWireCapabilities.verify(ready, false)));
    for (String side : List.of("features", "adapter_capabilities"))
      for (String feature : NetworkWireCapabilities.BASE) {
        var missing = ready.deepCopy();
        missing.putArray(side);
        for (var value : ready.path(side))
          if (!feature.equals(value.asString())) missing.withArray(side).add(value);
        reject("network.protocol_error", () -> NetworkWireCapabilities.verify(missing, false));
      }
    reject("network.protocol_error", () -> NetworkWireCapabilities.verify(ready, true));
    ready.withArray("features").add("broadcast_grants_v1");
    reject("network.protocol_error", () -> NetworkWireCapabilities.verify(ready, true));
    ready.withArray("adapter_capabilities").add("broadcast_grants_v1");
    assertTrue(NetworkWireCapabilities.verify(ready, true).contains("broadcast_grants_v1"));
  }

  @Test
  void missingReadyCapabilitiesNeverReachAuthorityOrDatabase() throws Exception {
    var core = mock(NetworkMailboxService.class);
    var jdbc = mock(JdbcTemplate.class);
    var source = mock(NetworkSourceGrpcAuthority.class);
    var wire =
        new NetworkMailboxWireService(
            core,
            jdbc,
            mock(TenantTransactionManager.class),
            source,
            new NetworkSigningKeys(Clock.systemUTC(), 1),
            Clock.systemUTC());
    var ready = pluginReady();
    ready.putArray("features");
    reject(
        "network.protocol_error",
        () ->
            wire.ready(
                null,
                NetworkJson.canonical(ready).getBytes(java.nio.charset.StandardCharsets.UTF_8)));
    verifyNoInteractions(core, jdbc, source);
  }

  @Test
  void receiptStagesAndStableProofHaveSeparateTransportIdentity() throws Exception {
    var message =
        (ObjectNode)
            NetworkEnvelopeSigningTest.fixture("frames.json")
                .path("frames")
                .path("delivery_broadcast")
                .path("value")
                .path("message")
                .deepCopy();
    NetworkSchemas.validate("Message", message);
    var target =
        NetworkJson.object()
            .put("version", 1)
            .put("realm", "2")
            .put("node", "64")
            .put("local", "0")
            .put("type", "system");
    var entry =
        NetworkJson.object()
            .put("recipient_id", "recipient-test")
            .put("status", "host_accepted")
            .put("message_digest", NetworkJson.digest(message))
            .put("host_request_id", "host-test")
            .put("expected_state_version", "1")
            .put("accepted_by", "system")
            .put("acceptance_mode", "inbox_only")
            .put("execution_status", "not_applicable")
            .put("evidence_digest", "sha256:" + "a".repeat(64));
    entry.set("target", target);
    NetworkReceiptProofs.stage(entry, message, target, null);
    var session = entry.deepCopy().put("accepted_by", "session");
    reject(
        "network.unauthorized", () -> NetworkReceiptProofs.stage(session, message, target, null));
    var forged = entry.deepCopy();
    forged.set(
        "assigned_address",
        target.deepCopy().put("node", "65").put("local", "1").put("type", "session"));
    var foreign = forged;
    reject(
        "network.unauthorized", () -> NetworkReceiptProofs.stage(foreign, message, target, null));
    String proof = NetworkReceiptProofs.stable(entry);
    assertEquals(
        proof, NetworkReceiptProofs.stable(entry.deepCopy().put("expected_state_version", "99")));
    assertNotEquals(
        proof, NetworkReceiptProofs.stable(entry.deepCopy().put("host_request_id", "other")));
    assertNotEquals(
        proof,
        NetworkReceiptProofs.stable(
            entry.deepCopy().put("evidence_digest", "sha256:" + "b".repeat(64))));
  }

  static ObjectNode pluginReady() throws Exception {
    try (var input =
        NetworkWireSecurityTest.class.getResourceAsStream("/network/plugin-ready.json")) {
      return (ObjectNode) NetworkJson.JSON.readTree(input);
    }
  }

  static void reject(String code, Runnable action) {
    assertEquals(code, assertThrows(AurigaException.class, action::run).errorCode().code());
  }
}
