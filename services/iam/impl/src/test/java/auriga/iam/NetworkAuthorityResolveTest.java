package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.context.TenantContext;
import auriga.iam.api.NetworkAuthority.*;
import java.util.*;
import org.junit.jupiter.api.Test;

/** Policy fixture only; real persisted redemption is covered separately by the PostgreSQL test. */
class NetworkAuthorityResolveTest extends NetworkAuthorityTest {
  @org.junit.jupiter.api.BeforeEach
  void backing() {
    TenantContext.run(
        tenant,
        () -> {
          String session = UUID.randomUUID().toString();
          rows.put(
              key("device", device.toString()),
              Json.object("accountId", account.toString(), "sessionId", session));
          rows.put(
              key("session", session),
              Json.object(
                  "sessionId",
                  session,
                  "tenantId",
                  tenant.toString(),
                  "accountId",
                  account.toString()));
          rows.put(key("account", account.toString()), Json.object("status", "active"));
        });
  }

  java.time.Instant resolve(IamNetworkAuthority.TargetBinding binding) {
    return new IamGrpcDevicesSpringTest()
        .as("svc:collab", TenantContext.require(), () -> network.resolveTarget(binding));
  }

  IamNetworkAuthority.TargetBinding target(Node n, IngressBinding b) {
    return new IamNetworkAuthority.TargetBinding(
        n.realm(), n.node(), b.ingress(), b.serverId(), b.clientId(), b.account());
  }

  @Test
  void exactPersistedTupleRequiredAndEveryFieldAndCredentialRechecked() {
    TenantContext.run(
        tenant,
        () -> {
          Node n = network.register("access");
          var ticket = network.prepare("access", n.node(), "server-a");
          var b = binding(ticket.ticket());
          NetworkAuthorityPostgresTest.gateway();
          network.redeem(ticket.ticket(), n.node(), device.toString(), "server-a", b);
          var target = target(n, b);
          assertEquals(ticket.connectionExpiresAt(), resolve(target));
          for (var bad :
              List.of(
                  new IamNetworkAuthority.TargetBinding(
                      "2", n.node(), b.ingress(), b.serverId(), b.clientId(), b.account()),
                  new IamNetworkAuthority.TargetBinding(
                      n.realm(), "2", b.ingress(), b.serverId(), b.clientId(), b.account()),
                  new IamNetworkAuthority.TargetBinding(
                      n.realm(), n.node(), "b".repeat(26), b.serverId(), b.clientId(), b.account()),
                  new IamNetworkAuthority.TargetBinding(
                      n.realm(),
                      n.node(),
                      b.ingress(),
                      "N" + "B".repeat(55),
                      b.clientId(),
                      b.account()),
                  new IamNetworkAuthority.TargetBinding(
                      n.realm(), n.node(), b.ingress(), b.serverId(), "43", b.account()),
                  new IamNetworkAuthority.TargetBinding(
                      n.realm(), n.node(), b.ingress(), b.serverId(), b.clientId(), "OTHER")))
            assertNull(resolve(bad));
          String key = key("network_ingress", b.ingress());
          var saved = rows.get(key).deepCopy();
          for (String field :
              List.of(
                  "device",
                  "realm",
                  "node",
                  "serverId",
                  "clientId",
                  "account",
                  "userNkey",
                  "audience")) {
            rows.get(key).put(field, "wrong");
            assertNull(resolve(target), field);
            rows.put(key, saved.deepCopy());
          }
          when(devices.credential(anyString(), anyString(), anyString())).thenReturn(null);
          assertNull(resolve(target));
          when(devices.credential(anyString(), anyString(), anyString())).thenReturn(identity);
          assertNotNull(resolve(target));
          nodes.put(
              key("node", n.node()),
              new Node(n.realm(), n.node(), UUID.randomUUID().toString(), n.device(), false));
          // Credential authority binds the current node owner, not caller supplied account data.
          when(devices.credential(
                  eq(tenant.toString()),
                  eq(nodes.get(key("node", n.node())).account()),
                  anyString()))
              .thenReturn(null);
          assertNull(resolve(target));
        });
  }

  @Test
  void missingRecordOtherTenantRevocationShortenedBackingAndCompletionDeadlineFailClosed() {
    TenantContext.run(
        tenant,
        () -> {
          Node n = network.register("access");
          var ticket = network.prepare("access", n.node(), "server-a");
          var b = binding(ticket.ticket());
          NetworkAuthorityPostgresTest.gateway();
          var target = target(n, b);
          assertNull(resolve(target));
          network.redeem(ticket.ticket(), n.node(), device.toString(), "server-a", b);
          TenantContext.run(UUID.randomUUID(), () -> assertNull(resolve(target)));
          var shortExpiry = clock.instant().plusSeconds(2);
          var shortIdentity =
              new DeviceAccessAuthority.Identity(
                  tenant.toString(), account.toString(), device.toString(), shortExpiry);
          when(devices.credential(anyString(), anyString(), anyString())).thenReturn(shortIdentity);
          assertEquals(shortExpiry, resolve(target));
          when(devices.credential(anyString(), anyString(), anyString()))
              .thenAnswer(
                  c -> {
                    clock.advance(2);
                    return shortIdentity;
                  });
          assertNull(resolve(target));
          when(devices.credential(anyString(), anyString(), anyString())).thenReturn(identity);
          rows.get(key("network_ingress", b.ingress()))
              .put("expiresAt", clock.instant().toString());
          assertNull(resolve(target));
          rows.get(key("network_ingress", b.ingress()))
              .put("expiresAt", ticket.connectionExpiresAt().toString());
          nodes.put(
              key("node", n.node()), new Node(n.realm(), n.node(), n.account(), n.device(), true));
          assertNull(resolve(target));
        });
  }

  @Test
  void mutationsDuringCredentialCheckRequireANewSnapshotEvenIfTimeDoesNotAdvance() {
    TenantContext.run(
        tenant,
        () -> {
          Node n = network.register("access");
          var ticket = network.prepare("access", n.node(), "server-a");
          var b = binding(ticket.ticket());
          NetworkAuthorityPostgresTest.gateway();
          network.redeem(ticket.ticket(), n.node(), device.toString(), "server-a", b);
          var target = target(n, b);
          for (String table : List.of("network_ingress", "device", "session", "account")) {
            String id =
                switch (table) {
                  case "network_ingress" -> b.ingress();
                  case "device" -> device.toString();
                  case "session" ->
                      rows.get(key("device", device.toString())).path("sessionId").asString();
                  default -> account.toString();
                };
            var before = rows.get(key(table, id)).deepCopy();
            doAnswer(
                    c -> {
                      rows.get(key(table, id)).put("changedAt", clock.instant().toString());
                      return identity;
                    })
                .when(devices)
                .credential(anyString(), anyString(), anyString());
            assertNull(resolve(target), table);
            rows.put(key(table, id), before);
          }
          when(devices.credential(anyString(), anyString(), anyString())).thenReturn(identity);
          assertNotNull(resolve(target));
        });
  }

  @Test
  void malformedPersistedLimitsNeverFallBackToAnAllowingDefault() {
    TenantContext.run(
        tenant,
        () -> {
          var config = Json.object("limits", Json.object("BODY_BYTES", "not-a-number"));
          rows.put(key("network_config", "organization"), config);
          new IamGrpcDevicesSpringTest()
              .as(
                  "svc:collab",
                  tenant,
                  () -> {
                    IamGrpcNetworkAuthorityTest.error(
                        "common.unavailable", () -> network.effectiveLimits());
                    return null;
                  });
        });
  }
}
