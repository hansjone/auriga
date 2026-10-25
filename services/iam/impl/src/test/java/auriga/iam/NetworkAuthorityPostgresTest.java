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
              .thenReturn(null);
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
                          ticket.ticket(), ticket.identity().node(), device.toString(), "server-a");
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
