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
  void remoteAuthorityRefusesInheritedTransactionsBeforeAnyRpc() {
    var client = mock(auriga.collab.grpc.NetworkAuthorityClient.class);
    var authority =
        new NetworkSourceGrpcAuthority(
            client, new TenantTransactionManager(mock(javax.sql.DataSource.class)));
    org.springframework.transaction.support.TransactionSynchronizationManager
        .setActualTransactionActive(true);
    try {
      reject(
          "common.unavailable",
          () ->
              authority.verify(
                  auriga.contracts.iam.v1.NetworkAuthorityConnection.getDefaultInstance()));
      reject(
          "common.unavailable",
          () -> authority.authorizeDelivery(UUID.randomUUID(), "{}", "{}", "20"));
    } finally {
      org.springframework.transaction.support.TransactionSynchronizationManager
          .setActualTransactionActive(false);
    }
    verifyNoInteractions(client);
  }

  @Test
  void onlineDecisionIsServiceBoundOperationBoundAndExpiresBeforeCommit() {
    UUID tenant = UUID.randomUUID();
    var now = new java.util.concurrent.atomic.AtomicReference<>(java.time.Instant.now());
    var clock =
        new Clock() {
          public java.time.ZoneId getZone() {
            return java.time.ZoneOffset.UTC;
          }

          public Clock withZone(java.time.ZoneId zone) {
            return this;
          }

          public java.time.Instant instant() {
            return now.get();
          }
        };
    var jdbc = mock(JdbcTemplate.class);
    var dataSource = mock(javax.sql.DataSource.class);
    when(jdbc.getDataSource()).thenReturn(dataSource);
    var manager = new TenantTransactionManager(dataSource);
    var source = mock(auriga.collab.api.NetworkSourceAuthority.class);
    when(source.currentTarget(tenant))
        .thenReturn(
            new auriga.collab.api.NetworkSourceAuthority.Target(
                "2", "20", "inc", 1, "nonce", "request", now.get().plusSeconds(60)));
    var iam = mock(auriga.iam.api.NetworkAuthority.class);
    when(iam.active("2", "20")).thenReturn(true);
    when(iam.check(any()))
        .thenReturn(new auriga.iam.api.NetworkAuthority.Decision(true, "allowed", Map.of()));
    var service =
        new NetworkMailboxService(
            jdbc, manager, source, new NetworkSigningKeys(clock, 1), clock, iam);
    var principal = mock(auriga.common.security.AurigaPrincipal.class);
    when(principal.isInternalService()).thenReturn(true);
    when(principal.subject()).thenReturn("svc:ai-gateway");
    when(principal.tenantId()).thenReturn(auriga.common.context.TenantContext.PLATFORM_TENANT);
    auriga.common.context.RequestContext.start(null, null)
        .withPrincipal(principal)
        .run(
            () ->
                auriga.common.context.TenantContext.run(
                    tenant,
                    () -> {
                      var decision =
                          service.authorizeTarget(
                              tenant,
                              "2",
                              "20",
                              Set.of(auriga.iam.api.NetworkAuthority.Operation.CONNECT));
                      reject(
                          "network.unauthorized",
                          () ->
                              service.withDecision(
                                  decision, () -> service.head(tenant, "2", "20")));
                      var other =
                          new NetworkMailboxService(
                              jdbc, manager, source, new NetworkSigningKeys(clock, 1), clock, iam);
                      reject(
                          "network.unauthorized",
                          () -> other.withDecision(decision, () -> "forbidden"));
                      when(source.currentTarget(tenant))
                          .thenReturn(
                              new auriga.collab.api.NetworkSourceAuthority.Target(
                                  "2",
                                  "20",
                                  "inc",
                                  2,
                                  "nonce-new",
                                  "request",
                                  now.get().plusSeconds(60)));
                      reject(
                          "network.unauthorized",
                          () ->
                              service.withDecision(
                                  decision, () -> service.ready(tenant, "2", "20")));
                      now.set(now.get().plusSeconds(31));
                      reject(
                          "network.unauthorized",
                          () -> service.withDecision(decision, () -> "expired"));
                    }));
    assertTrue(
        mockingDetails(jdbc).getInvocations().stream()
            .allMatch(i -> i.getMethod().getName().equals("getDataSource")));
  }

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
    registered.withTarget(
        c,
        target,
        () -> {
          var snapshot = registered.localSnapshot(tenant, "2", "20", "1");
          reject("common.unavailable", () -> registered.revalidateLocal(tenant, snapshot));
          return null;
        });
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
        missing.withArray(side).add("unknown_required_v1");
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
            mock(auriga.collab.network.local.LocalRegistry.class),
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
    assertEquals(target, NetworkReceiptProofs.target(message, null, "2", "64", "0", "system"));
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
    var frames = NetworkEnvelopeSigningTest.fixture("frames.json").path("frames");
    var sessionMessage = frames.path("delivery_session_message").path("value").path("message");
    var sessionTarget =
        NetworkReceiptProofs.target(sessionMessage, null, "2", "64", "5", "session");
    var sessionEntry =
        entry.deepCopy().put("accepted_by", "session").put("acceptance_mode", "normal");
    sessionEntry.set("target", sessionTarget);
    NetworkReceiptProofs.stage(sessionEntry, sessionMessage, sessionTarget, null);
    var first = frames.path("delivery_first_contact").path("value").path("message");
    var firstTarget = NetworkReceiptProofs.target(first, null, "2", "64", "0", "system");
    var allowed = NetworkJson.object().put("status", "allowed").put("scope", "conversation");
    NetworkReceiptProofs.stage(entry, first, firstTarget, allowed);
    reject(
        "network.unauthorized",
        () ->
            NetworkReceiptProofs.stage(
                entry, first, firstTarget, allowed.deepCopy().put("status", "pending")));
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
