package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.audit.AuditLogger;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.persistence.*;
import auriga.common.security.*;
import auriga.iam.api.NetworkAuthority.Node;
import auriga.testsupport.PostgresExtension;
import java.time.*;
import java.util.*;
import java.util.concurrent.*;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.security.core.context.SecurityContextHolder;

/** Real FORCE RLS, migrations, rollback reservations and concurrent redemption. */
@ExtendWith(PostgresExtension.class)
class NetworkAuthorityPostgresTest {
  static auriga.iam.api.NetworkAuthority.IngressBinding binding(String ticket) {
    return NetworkAuthorityTest.binding(ticket);
  }

  static IamStore store;
  static JdbcTemplate jdbc;
  final UUID tenant = UUID.randomUUID(), account = UUID.randomUUID(), device = UUID.randomUUID();
  final Clock clock = Clock.systemUTC();
  final DeviceAccessAuthority devices = mock(DeviceAccessAuthority.class);
  IamNetworkAuthority network;
  DeviceAccessAuthority.Identity identity;

  @BeforeAll
  static void migrate() {
    var roles =
        PostgresExtension.serviceRoles(
            NetworkAuthorityPostgresTest.class, "netn1_owner", "netn1_app", "iam");
    CommonMigrations.migrate(roles.owner().dataSource(), "iam");
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas("iam")
                .locations("classpath:db/iam")
                .load())
        .migrate();
    jdbc = new JdbcTemplate(roles.runtime().dataSource());
    store = new IamStore(jdbc, new TenantTransactionManager(roles.runtime().dataSource()));
  }

  @BeforeEach
  void setup() {
    identity =
        new DeviceAccessAuthority.Identity(
            tenant.toString(),
            account.toString(),
            device.toString(),
            clock.instant().plusSeconds(3600));
    when(devices.access("access")).thenReturn(identity);
    when(devices.credential(tenant.toString(), account.toString(), device.toString()))
        .thenReturn(identity);
    TenantContext.run(
        tenant,
        () ->
            store.tx(
                () -> {
                  store.insert(
                      "account", account.toString(), Json.object("status", "active"), null);
                  store.insert(
                      "device",
                      device.toString(),
                      Json.object("accountId", account.toString()),
                      null);
                  return null;
                }));
    network =
        new IamNetworkAuthority(store, devices, mock(AuditLogger.class), clock, Set.of("server-a"));
  }

  @AfterEach
  void clear() {
    SecurityContextHolder.clearContext();
  }

  static void gateway() {
    var p =
        new AurigaPrincipal(
            "svc:ai-gateway",
            TenantContext.PLATFORM_TENANT,
            PrincipalType.INTERNAL_SERVICE,
            TokenContext.SERVICE,
            null,
            null,
            null,
            List.of(),
            Set.of(),
            null,
            null,
            null);
    SecurityContextHolder.getContext().setAuthentication(new AurigaAuthentication(p));
  }

  @Test
  void migrationReservationsSurviveFailedBindingAndRlsHidesOtherTenant() {
    TenantContext.run(
        tenant,
        () -> {
          when(devices.credential(tenant.toString(), account.toString(), device.toString()))
              .thenReturn(identity, (DeviceAccessAuthority.Identity) null);
          assertThrows(AurigaException.class, () -> network.register("access"));
          when(devices.credential(tenant.toString(), account.toString(), device.toString()))
              .thenReturn(identity);
          Node node = network.register("access");
          assertEquals("2", node.node());
          assertEquals(node, network.register("access"));
          gateway();
          assertTrue(network.active(node.realm(), node.node()));
          TenantContext.run(
              UUID.randomUUID(),
              () -> {
                assertFalse(network.active(node.realm(), node.node()));
                assertEquals(
                    0,
                    store.tx(
                        () ->
                            jdbc.queryForObject(
                                "SELECT count(*) FROM iam.network_node", Integer.class)));
              });
        });
  }

  @Test
  void disabledOrganizationOrInvalidCredentialDoesNotAllocateRealmOrNode() {
    TenantContext.run(
        tenant,
        () -> {
          store.tx(
              () -> {
                store.insert("network_config", "organization", Json.object("enabled", false), null);
                return null;
              });
          assertThrows(AurigaException.class, () -> network.register("access"));
          assertEquals(
              0,
              store.tx(
                  () ->
                      jdbc.queryForObject(
                          "SELECT count(*) FROM iam.network_realm WHERE tenant_id=?",
                          Integer.class,
                          tenant)));
          store.tx(
              () -> {
                store.delete("network_config", "organization");
                return null;
              });
          when(devices.credential(tenant.toString(), account.toString(), device.toString()))
              .thenReturn(null);
          assertThrows(AurigaException.class, () -> network.register("access"));
          assertEquals(
              0,
              store.tx(
                  () ->
                      jdbc.queryForObject(
                          "SELECT count(*) FROM iam.network_realm WHERE tenant_id=?",
                          Integer.class,
                          tenant)));
          when(devices.credential(tenant.toString(), account.toString(), device.toString()))
              .thenReturn(identity);
          assertEquals("1", network.register("access").node());
        });
  }

  @Test
  void successfulGrantPersistsExactBindingAndRlsHidesItFromOtherTenant() {
    TenantContext.run(
        tenant,
        () -> {
          Node node = network.register("access");
          var ticket = network.prepare("access", node.node(), "server-a");
          gateway();
          var binding = binding(ticket.ticket());
          network.redeem(ticket.ticket(), node.node(), device.toString(), "server-a", binding);
          var stored = store.tx(() -> store.get("network_ingress", binding.ingress(), false));
          assertEquals(node.realm(), stored.path("realm").asString());
          assertEquals(binding.serverId(), stored.path("serverId").asString());
          assertEquals(binding.clientId(), stored.path("clientId").asString());
          assertEquals(binding.userNkey(), stored.path("userNkey").asString());
          assertFalse(stored.toString().contains(ticket.ticket()));
          TenantContext.run(
              UUID.randomUUID(),
              () ->
                  assertNull(
                      store.tx(() -> store.get("network_ingress", binding.ingress(), false))));
          var duplicate = network.prepare("access", node.node(), "server-a");
          assertThrows(
              RuntimeException.class,
              () ->
                  network.redeem(
                      duplicate.ticket(),
                      node.node(),
                      device.toString(),
                      "server-a",
                      binding(duplicate.ticket())));
          assertNull(
              store.tx(() -> store.get("network_ticket", Secrets.hash(duplicate.ticket()), false)));
        });
  }

  @Test
  void batchActiveUsesLiveSessionDeviceAccountAndRlsAndOfflineBindingsExpire() {
    TenantContext.run(
        tenant,
        () -> {
          Node node = network.register("access");
          String sid = UUID.randomUUID().toString();
          UUID provider = UUID.randomUUID();
          store.tx(
              () -> {
                store.insert(
                    "idp_provider", provider.toString(), Json.object("providerId", provider), null);
                store.insert(
                    "session",
                    sid,
                    Json.object(
                        "tenantId",
                        tenant.toString(),
                        "accountId",
                        account.toString(),
                        "providerId",
                        provider,
                        "audience",
                        "portal",
                        "expiresAt",
                        clock.instant().plusSeconds(3600).toString(),
                        "idleExpiresAt",
                        clock.instant().plusSeconds(1800).toString()),
                    clock.instant().plusSeconds(3600));
                var d = store.get("device", device.toString(), true);
                d.put("sessionId", sid);
                d.put("scope", "auriga.connect");
                store.save("device", d);
                return null;
              });
          var ticket = network.prepare("access", node.node(), "server-a");
          gateway();
          network.redeem(
              ticket.ticket(),
              node.node(),
              device.toString(),
              "server-a",
              binding(ticket.ticket()));
          var ref = new auriga.iam.api.NetworkAuthority.NodeReference(node.realm(), node.node());
          var wrongRealm = new auriga.iam.api.NetworkAuthority.NodeReference("1", node.node());
          assertEquals(
              Map.of(ref, true, wrongRealm, false), network.activeBatch(List.of(ref, wrongRealm)));
          TenantContext.run(
              UUID.randomUUID(), () -> assertFalse(network.activeBatch(List.of(ref)).get(ref)));
          for (String type : List.of("device", "session", "account")) {
            store.tx(
                () -> {
                  var row =
                      store.get(
                          type,
                          type.equals("device")
                              ? device.toString()
                              : type.equals("session") ? sid : account.toString(),
                          true);
                  if (type.equals("account")) row.put("status", "disabled");
                  else row.put("revoked", true);
                  store.save(type, row);
                  return null;
                });
            assertFalse(network.activeBatch(List.of(ref)).get(ref), type);
            store.tx(
                () -> {
                  var row =
                      store.get(
                          type,
                          type.equals("device")
                              ? device.toString()
                              : type.equals("session") ? sid : account.toString(),
                          true);
                  if (type.equals("account")) row.put("status", "active");
                  else row.put("revoked", false);
                  store.save(type, row);
                  return null;
                });
          }
          store.tx(
              () -> {
                var s = store.get("session", sid, true);
                s.put("idleExpiresAt", clock.instant().minusSeconds(1).toString());
                store.save("session", s);
                jdbc.update(
                    "UPDATE iam.network_ingress SET expires_at=? WHERE tenant_id=?",
                    java.sql.Timestamp.from(clock.instant().minusSeconds(1)),
                    tenant);
                return null;
              });
          assertFalse(network.activeBatch(List.of(ref)).get(ref));
          // The node never reconnects. The production retention worker still discovers it.
          assertTrue(new NetworkIngressCleanup(store, clock).sweepOnce() > 0);
          assertNull(
              store.tx(
                  () -> store.get("network_ingress", binding(ticket.ticket()).ingress(), false)));
          store.tx(
              () -> {
                jdbc.execute("SET LOCAL enable_seqscan=off");
                var plan =
                    jdbc.queryForList(
                        "EXPLAIN SELECT id FROM iam.network_ingress WHERE tenant_id=? AND expires_at<=? ORDER BY expires_at,id LIMIT 256",
                        String.class,
                        tenant,
                        java.sql.Timestamp.from(clock.instant()));
                assertTrue(
                    plan.stream().anyMatch(line -> line.contains("network_ingress_expiry")),
                    plan.toString());
                return null;
              });
        });
  }

  @Test
  void credentialExceptionDurablyBurnsTicketBeforeRejecting() {
    TenantContext.run(
        tenant,
        () -> {
          Node node = network.register("access");
          var ticket = network.prepare("access", node.node(), "server-a");
          gateway();
          when(devices.credential(tenant.toString(), account.toString(), device.toString()))
              .thenAnswer(
                  invocation ->
                      store.tx(
                          () -> {
                            throw new IllegalStateException("credential unavailable");
                          }));
          assertThrows(
              RuntimeException.class,
              () ->
                  network.redeem(
                      ticket.ticket(),
                      node.node(),
                      device.toString(),
                      "server-a",
                      binding(ticket.ticket())));
          assertNull(
              store.tx(() -> store.get("network_ticket", Secrets.hash(ticket.ticket()), false)));
          doReturn(identity)
              .when(devices)
              .credential(tenant.toString(), account.toString(), device.toString());
          assertEquals(
              "common.forbidden",
              assertThrows(
                      AurigaException.class,
                      () ->
                          network.redeem(
                              ticket.ticket(),
                              node.node(),
                              device.toString(),
                              "server-a",
                              binding(ticket.ticket())))
                  .errorCode()
                  .code());
        });
  }

  @Test
  void wrongBindingDurablyInvalidatesTicketBeforeReturningForbidden() {
    TenantContext.run(
        tenant,
        () -> {
          Node node = network.register("access");
          gateway();
          for (String[] wrong :
              List.of(
                  new String[] {"2", device.toString(), "server-a"},
                  new String[] {"0", device.toString(), "server-a"},
                  new String[] {node.node(), UUID.randomUUID().toString(), "server-a"},
                  new String[] {node.node(), device.toString(), "server-other"},
                  new String[] {node.node(), device.toString(), null})) {
            var ticket = network.prepare("access", node.node(), "server-a");
            assertEquals(
                "common.forbidden",
                assertThrows(
                        AurigaException.class,
                        () ->
                            network.redeem(
                                ticket.ticket(),
                                wrong[0],
                                wrong[1],
                                wrong[2],
                                binding(ticket.ticket())))
                    .errorCode()
                    .code());
            assertNull(
                store.tx(() -> store.get("network_ticket", Secrets.hash(ticket.ticket()), false)));
            assertEquals(
                "common.forbidden",
                assertThrows(
                        AurigaException.class,
                        () ->
                            network.redeem(
                                ticket.ticket(),
                                node.node(),
                                device.toString(),
                                "server-a",
                                binding(ticket.ticket())))
                    .errorCode()
                    .code());
          }
        });
  }

  @Test
  void realmSequenceBurnsRolledBackValueAndSupportsUnsignedRange() {
    TenantContext.run(
        tenant,
        () -> {
          var first = new java.util.concurrent.atomic.AtomicReference<java.math.BigDecimal>();
          assertThrows(
              IllegalStateException.class,
              () ->
                  store.tx(
                      () -> {
                        first.set(
                            jdbc.queryForObject(
                                "SELECT"
                                    + " nextval('iam.network_realm_number')::numeric+9223372036854775809",
                                java.math.BigDecimal.class));
                        throw new IllegalStateException("rollback after reservation");
                      }));
          var second =
              store.tx(
                  () ->
                      jdbc.queryForObject(
                          "SELECT nextval('iam.network_realm_number')::numeric+9223372036854775809",
                          java.math.BigDecimal.class));
          assertTrue(second.compareTo(first.get()) > 0);
          network.register("access");
          store.tx(
              () -> {
                jdbc.update(
                    "UPDATE iam.network_realm SET next_node=18446744073709551615 WHERE tenant_id=?",
                    tenant);
                return null;
              });
          UUID another = UUID.randomUUID();
          store.tx(
              () -> {
                store.insert("device", another.toString(), Json.object("accountId", account), null);
                return null;
              });
          var otherIdentity =
              new DeviceAccessAuthority.Identity(
                  tenant.toString(), account.toString(), another.toString(), identity.expiresAt());
          when(devices.access("other")).thenReturn(otherIdentity);
          when(devices.credential(tenant.toString(), account.toString(), another.toString()))
              .thenReturn(otherIdentity);
          assertEquals("18446744073709551615", network.register("other").node());
        });
  }

  @Test
  void concurrentTicketRedemptionCommitsExactlyOnce() throws Exception {
    var ticket =
        TenantContext.call(
            tenant,
            () -> {
              Node node = network.register("access");
              return network.prepare("access", node.node(), "server-a");
            });
    try (var executor = Executors.newFixedThreadPool(2)) {
      Callable<Boolean> redeem =
          () -> {
            try {
              gateway();
              return TenantContext.call(
                  tenant,
                  () -> {
                    try {
                      network.redeem(
                          ticket.ticket(),
                          ticket.identity().node(),
                          device.toString(),
                          "server-a",
                          binding(ticket.ticket()));
                      return true;
                    } catch (AurigaException e) {
                      assertEquals("common.forbidden", e.errorCode().code());
                      return false;
                    }
                  });
            } finally {
              SecurityContextHolder.clearContext();
            }
          };
      var results = executor.invokeAll(List.of(redeem, redeem));
      assertNotEquals(results.get(0).get(), results.get(1).get());
      TenantContext.run(
          tenant,
          () ->
              assertNull(
                  store.tx(
                      () -> store.get("network_ticket", Secrets.hash(ticket.ticket()), false))));
    }
  }
}
