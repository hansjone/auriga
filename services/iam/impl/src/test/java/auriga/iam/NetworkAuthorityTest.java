package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import auriga.common.audit.AuditLogger;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.security.*;
import auriga.iam.api.NetworkAuthority.*;
import java.math.BigInteger;
import java.time.*;
import java.util.*;
import java.util.concurrent.locks.ReentrantLock;
import java.util.function.Supplier;
import org.junit.jupiter.api.*;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.jdbc.core.RowMapper;
import org.springframework.security.core.context.SecurityContextHolder;
import tools.jackson.databind.node.ObjectNode;

/**
 * In-memory policy tests only. This replacement does not model rollback or database concurrency;
 * NetworkAuthorityPostgresTest is the only evidence for reservations and atomic redemption.
 */
class NetworkAuthorityTest {
  final UUID tenant = UUID.randomUUID(), account = UUID.randomUUID(), device = UUID.randomUUID();

  static final class Time extends Clock {
    Instant now = Instant.parse("2026-01-01T00:00:00Z");

    public ZoneId getZone() {
      return ZoneOffset.UTC;
    }

    public Clock withZone(ZoneId zone) {
      return this;
    }

    public Instant instant() {
      return now;
    }

    void advance(long seconds) {
      now = now.plusSeconds(seconds);
    }
  }

  final Time clock = new Time();
  final IamStore store = mock(IamStore.class);
  final DeviceAccessAuthority devices = mock(DeviceAccessAuthority.class);
  final AuditLogger audit = mock(AuditLogger.class);
  final Map<String, ObjectNode> rows = new HashMap<>();
  final Map<String, Node> nodes = new HashMap<>();
  final ReentrantLock lock = new ReentrantLock();
  BigInteger next = BigInteger.ONE;
  IamNetworkAuthority network;
  DeviceAccessAuthority.Identity identity;

  String key(String table, String id) {
    return TenantContext.require() + ":" + table + ":" + id;
  }

  @BeforeEach
  void setup() {
    JdbcTemplate jdbc =
        new JdbcTemplate() {
          @SuppressWarnings("unchecked")
          @Override
          public <T> List<T> query(String sql, RowMapper<T> mapper, Object... args) {
            if (sql.contains("SET next_node")) {
              String value = next.toString();
              next = next.add(BigInteger.ONE);
              return (List<T>) List.of(value);
            }
            return (List<T>)
                nodes.entrySet().stream()
                    .filter(e -> e.getKey().startsWith(TenantContext.require() + ":node:"))
                    .map(Map.Entry::getValue)
                    .filter(
                        n ->
                            sql.contains("n.device_id=?")
                                ? n.device().equals(args[1].toString())
                                : n.node().equals(args[1].toString()))
                    .toList();
          }

          @Override
          public int update(String sql, Object... args) {
            if (sql.contains("INSERT INTO iam.network_node")) {
              Node n =
                  new Node("1", args[1].toString(), args[2].toString(), args[3].toString(), false);
              if (nodes.values().stream().noneMatch(v -> v.device().equals(n.device())))
                nodes.put(key("node", n.node()), n);
            } else if (sql.contains("SET revoked=true")) {
              String k = key("node", args[1].toString());
              Node n = nodes.get(k);
              if (n != null)
                nodes.put(k, new Node(n.realm(), n.node(), n.account(), n.device(), true));
            } else if (sql.contains("DELETE FROM iam.network_ticket")) {
              rows.entrySet()
                  .removeIf(
                      e ->
                          e.getKey().startsWith(key("network_ticket", ""))
                              && e.getValue().path("node").asString().equals(args[1])
                              && !clock
                                  .instant()
                                  .isBefore(
                                      Instant.parse(e.getValue().path("expiresAt").asString())));
            }
            return 1;
          }

          @SuppressWarnings("unchecked")
          @Override
          public <T> T queryForObject(String sql, Class<T> type, Object... args) {
            return (T)
                Long.valueOf(
                    rows.entrySet().stream()
                        .filter(e -> e.getKey().startsWith(key("network_ticket", "")))
                        .filter(e -> e.getValue().path("node").asString().equals(args[1]))
                        .count());
          }
        };
    when(store.networkJdbc()).thenReturn(jdbc);
    when(store.tx(any())).thenAnswer(i -> atomic(i.getArgument(0)));
    when(store.isolated(any(), any())).thenAnswer(i -> atomic(i.getArgument(1)));
    when(store.get(anyString(), anyString(), anyBoolean()))
        .thenAnswer(
            i -> {
              var value = rows.get(key(i.getArgument(0), i.getArgument(1)));
              return value == null ? null : value.deepCopy();
            });
    when(store.insert(anyString(), anyString(), any(), nullable(Instant.class)))
        .thenAnswer(
            i -> {
              ObjectNode value = i.getArgument(2);
              value.put("id", UUID.randomUUID().toString());
              rows.put(key(i.getArgument(0), i.getArgument(1)), value.deepCopy());
              return value;
            });
    doAnswer(
            i -> {
              ObjectNode value = i.getArgument(1);
              rows.entrySet().stream()
                  .filter(e -> e.getKey().startsWith(key(i.getArgument(0), "")))
                  .filter(e -> e.getValue().path("id").equals(value.path("id")))
                  .forEach(e -> e.setValue(value.deepCopy()));
              return null;
            })
        .when(store)
        .save(anyString(), any());
    doAnswer(
            i -> {
              rows.remove(key(i.getArgument(0), i.getArgument(1)));
              return null;
            })
        .when(store)
        .delete(anyString(), anyString());
    identity =
        new DeviceAccessAuthority.Identity(
            tenant.toString(),
            account.toString(),
            device.toString(),
            clock.instant().plusSeconds(7200));
    when(devices.access("access")).thenReturn(identity);
    when(devices.credential(anyString(), anyString(), anyString()))
        .thenAnswer(
            i ->
                tenant.toString().equals(i.getArgument(0))
                    ? new DeviceAccessAuthority.Identity(
                        i.getArgument(0), i.getArgument(1), i.getArgument(2), identity.expiresAt())
                    : null);
    network = new IamNetworkAuthority(store, devices, audit, clock, Set.of("server-a", "server-b"));
  }

  Object atomic(Supplier<?> work) {
    lock.lock();
    try {
      return work.get();
    } finally {
      lock.unlock();
    }
  }

  @AfterEach
  void clear() {
    SecurityContextHolder.clearContext();
  }

  void service(String subject) {
    principal(
        new AurigaPrincipal(
            subject,
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
            null));
  }

  void admin() {
    principal(
        new AurigaPrincipal(
            account.toString(),
            tenant,
            PrincipalType.USER,
            TokenContext.ADMIN,
            null,
            null,
            null,
            List.of(),
            Set.of("iam.network.manage"),
            null,
            null,
            null));
  }

  void principal(AurigaPrincipal value) {
    SecurityContextHolder.getContext().setAuthentication(new AurigaAuthentication(value));
  }

  void business(Runnable work) {
    TenantContext.run(tenant, work);
  }

  Node register() {
    return network.register("access");
  }

  void forbidden(Runnable work) {
    assertEquals(
        "common.forbidden", assertThrows(AurigaException.class, work::run).errorCode().code());
  }

  Check request(Sender sender, String local, Operation operation) {
    return new Check("1", "1", "2", local, sender, operation, 10, 1, 1);
  }

  void twoNodes() {
    register();
    String second = UUID.randomUUID().toString();
    when(devices.access("second"))
        .thenReturn(
            new DeviceAccessAuthority.Identity(
                tenant.toString(), account.toString(), second, identity.expiresAt()));
    network.register("second");
  }

  @Test
  void registrationIsIdempotentAndReinstallAndRevocationNeverReuseNode() {
    business(
        () -> {
          Node first = register();
          assertEquals(first, register());
          assertEquals("aur1:1.1.0", first.systemAddress());
          admin();
          network.revoke("1");
          forbidden(this::register);
          String newDevice = UUID.randomUUID().toString();
          when(devices.access("reinstalled"))
              .thenReturn(
                  new DeviceAccessAuthority.Identity(
                      tenant.toString(), account.toString(), newDevice, identity.expiresAt()));
          assertEquals("2", network.register("reinstalled").node());
        });
  }

  @Test
  void supportsFullUnsignedDisplay() {
    assertTrue(IamNetworkAuthority.validNumber("18446744073709551615", false));
    assertFalse(IamNetworkAuthority.validNumber("18446744073709551616", false));
    for (String bad : List.of("0", "01", "-1", "1.0", "ff"))
      assertFalse(IamNetworkAuthority.validNumber(bad, false));
    assertEquals(
        "aur1:ffffffffffffffff.ffffffffffffffff.0",
        new Node("18446744073709551615", "18446744073709551615", "a", "d", false).systemAddress());
  }

  @Test
  void ticketIsHashedSingleUseAndBoundToDeviceNodeAndVerifiedServer() {
    business(
        () -> {
          register();
          Ticket ticket = network.prepare("access", "1", "server-a");
          assertEquals(clock.instant().plusSeconds(30), ticket.expiresAt());
          assertFalse(rows.toString().contains(ticket.ticket()));
          service("svc:ai-gateway");
          Connection connection =
              network.redeem(ticket.ticket(), "1", device.toString(), "server-a");
          assertEquals(clock.instant().plusSeconds(3600), connection.expiresAt());
          forbidden(() -> network.redeem(ticket.ticket(), "1", device.toString(), "server-a"));
          forbidden(() -> network.prepare("access", "1", "not-configured"));
        });
  }

  @Test
  void expiryDeviceRevocationAndBackingCredentialExpiryRejectAndKickQueryFailsClosed() {
    business(
        () -> {
          register();
          Ticket expired = network.prepare("access", "1", "server-a");
          clock.advance(30);
          service("svc:ai-gateway");
          forbidden(() -> network.redeem(expired.ticket(), "1", device.toString(), "server-a"));
          Ticket revoked = network.prepare("access", "1", "server-a");
          when(devices.credential(anyString(), anyString(), anyString())).thenReturn(null);
          forbidden(() -> network.redeem(revoked.ticket(), "1", device.toString(), "server-a"));
          assertFalse(network.active("1", "1"));
          when(devices.credential(anyString(), anyString(), anyString()))
              .thenReturn(
                  new DeviceAccessAuthority.Identity(
                      tenant.toString(),
                      account.toString(),
                      device.toString(),
                      clock.instant().plusSeconds(9)));
          Ticket shortTicket = network.prepare("access", "1", "server-a");
          Connection connection =
              network.redeem(shortTicket.ticket(), "1", device.toString(), "server-a");
          assertEquals(clock.instant().plusSeconds(9), connection.expiresAt());
          admin();
          network.revoke("1");
          service("svc:ai-gateway");
          assertFalse(network.active("1", "1"));
        });
  }

  @Test
  void agentsHaveNoFirstContactOrBroadcastAndNeverInheritHumanGrants() {
    business(
        () -> {
          twoNodes();
          service("svc:collab");
          assertFalse(network.check(request(Sender.AGENT, "9", Operation.FIRST_CONTACT)).allowed());
          assertTrue(network.check(request(Sender.HUMAN, "0", Operation.FIRST_CONTACT)).allowed());
          assertFalse(network.check(request(Sender.HUMAN, "0", Operation.BROADCAST)).allowed());
          admin();
          network.grant(
              "1", Sender.HUMAN, "0", Operation.BROADCAST, clock.instant().plusSeconds(3600));
          service("svc:collab");
          assertTrue(network.check(request(Sender.HUMAN, "0", Operation.BROADCAST)).allowed());
          assertFalse(network.check(request(Sender.AGENT, "9", Operation.BROADCAST)).allowed());
          admin();
          network.grant(
              "1", Sender.AGENT, "9", Operation.FIRST_CONTACT, clock.instant().plusSeconds(30));
          service("svc:collab");
          assertTrue(network.check(request(Sender.AGENT, "9", Operation.FIRST_CONTACT)).allowed());
          assertFalse(
              network.check(request(Sender.AGENT, "10", Operation.FIRST_CONTACT)).allowed());
          clock.advance(30);
          assertFalse(network.check(request(Sender.AGENT, "9", Operation.FIRST_CONTACT)).allowed());
          admin();
          network.withdraw("1", Sender.HUMAN, "0", Operation.BROADCAST);
          service("svc:collab");
          assertFalse(network.check(request(Sender.HUMAN, "0", Operation.BROADCAST)).allowed());
          verify(audit, atLeast(4)).record(any());
        });
  }

  @Test
  void organizationPolicyAndLimitsOverrideIndividualGrantsAndHideTargetExistence() {
    business(
        () -> {
          twoNodes();
          admin();
          network.grant(
              "1", Sender.AGENT, "9", Operation.FIRST_CONTACT, clock.instant().plusSeconds(30));
          network.nodePolicy("1", "2", false);
          service("svc:collab");
          Decision policyDenied =
              network.check(request(Sender.AGENT, "9", Operation.FIRST_CONTACT));
          Decision unknown =
              network.check(
                  new Check("1", "1", "999", "9", Sender.AGENT, Operation.SEND, 10, 1, 1));
          assertEquals(policyDenied, unknown);
          admin();
          network.nodePolicy("1", "2", true);
          network.configure(new Configuration(true, false, true, Map.of(Limit.BODY_BYTES, 5L)));
          service("svc:collab");
          assertFalse(network.check(request(Sender.AGENT, "9", Operation.SEND)).allowed());
          Decision first =
              network.check(
                  new Check("1", "1", "2", "0", Sender.HUMAN, Operation.FIRST_CONTACT, 1, 1, 1));
          assertFalse(first.allowed());
          assertEquals(10L, first.limits().get(Limit.FIRST_NODE_PER_MINUTE));
          admin();
          network.configure(new Configuration(false, true, true, Map.of()));
          service("svc:ai-gateway");
          assertFalse(network.active("1", "1"));
          forbidden(() -> network.prepare("access", "1", "server-a"));
        });
  }

  @Test
  void crossTenantAndRealmAndCallerSpoofingAreDenied() {
    business(
        () -> {
          twoNodes();
          service("svc:collab");
          assertFalse(
              network
                  .check(new Check("2", "1", "2", "0", Sender.HUMAN, Operation.SEND, 1, 1, 1))
                  .allowed());
          Ticket ticket = network.prepare("access", "1", "server-a");
          forbidden(() -> network.redeem(ticket.ticket(), "1", device.toString(), "server-a"));
          service("svc:workspace");
          forbidden(() -> network.active("1", "1"));
          service("svc:ai-gateway");
          TenantContext.run(
              UUID.randomUUID(),
              () -> {
                assertFalse(network.active("1", "1"));
                forbidden(
                    () -> network.redeem(ticket.ticket(), "1", device.toString(), "server-a"));
                forbidden(this::register);
              });
          forbidden(
              () ->
                  network.grant(
                      "1",
                      Sender.AGENT,
                      "9",
                      Operation.BROADCAST,
                      clock.instant().plusSeconds(30)));
          admin();
          forbiddenInOtherTenant();
          assertThrows(
              AurigaException.class,
              () ->
                  network.grant(
                      "1",
                      Sender.HUMAN,
                      "9",
                      Operation.BROADCAST,
                      clock.instant().plusSeconds(30)));
          assertThrows(
              AurigaException.class,
              () ->
                  network.grant(
                      "1",
                      Sender.HUMAN,
                      "0",
                      Operation.BROADCAST,
                      clock.instant().plusSeconds(86401)));
        });
  }

  void forbiddenInOtherTenant() {
    TenantContext.run(UUID.randomUUID(), () -> forbidden(() -> network.revoke("1")));
  }

  @Test
  void broadcastCannotBypassPairPolicyOrTargetRevocationAndPendingTicketsAreBounded() {
    business(
        () -> {
          twoNodes();
          admin();
          network.grant(
              "1", Sender.HUMAN, "0", Operation.BROADCAST, clock.instant().plusSeconds(3600));
          network.nodePolicy("1", "2", false);
          service("svc:collab");
          assertFalse(network.check(request(Sender.HUMAN, "0", Operation.BROADCAST)).allowed());
          admin();
          network.nodePolicy("1", "2", true);
          network.revoke("2");
          service("svc:collab");
          assertFalse(network.check(request(Sender.HUMAN, "0", Operation.BROADCAST)).allowed());
          for (int i = 0; i < 8; i++) network.prepare("access", "1", "server-a");
          forbidden(() -> network.prepare("access", "1", "server-a"));
          clock.advance(30);
          assertNotNull(network.prepare("access", "1", "server-a"));
        });
  }

  @Test
  void rejectedRegistrationDoesNotReachAllocation() {
    business(
        () -> {
          admin();
          network.configure(new Configuration(false, true, true, Map.of()));
          forbidden(this::register);
          assertEquals(BigInteger.ONE, next);
          network.configure(new Configuration(true, true, true, Map.of()));
          when(devices.credential(anyString(), anyString(), anyString())).thenReturn(null);
          forbidden(this::register);
          assertEquals(BigInteger.ONE, next);
        });
  }

  @Test
  void mismatchedBindingInvalidatesTicketForCorrectCaller() {
    business(
        () -> {
          register();
          service("svc:ai-gateway");
          for (String[] wrong :
              List.of(
                  new String[] {"2", device.toString(), "server-a"},
                  new String[] {"0", device.toString(), "server-a"},
                  new String[] {"1", UUID.randomUUID().toString(), "server-a"},
                  new String[] {"1", device.toString(), "server-b"},
                  new String[] {"1", device.toString(), "server-other"},
                  new String[] {"1", device.toString(), null})) {
            var ticket = network.prepare("access", "1", "server-a");
            forbidden(() -> network.redeem(ticket.ticket(), wrong[0], wrong[1], wrong[2]));
            forbidden(() -> network.redeem(ticket.ticket(), "1", device.toString(), "server-a"));
          }
        });
  }

  @Test
  void organizationCanOnlyTightenPlatformLimits() {
    business(
        () -> {
          admin();
          for (Limit limit : Limit.values()) {
            network.configure(
                new Configuration(true, true, true, Map.of(limit, limit.defaultValue())));
            network.configure(new Configuration(true, true, true, Map.of(limit, 1L)));
            for (long invalid : new long[] {0, -1, limit.defaultValue() + 1, 9007199254740991L}) {
              assertEquals(
                  "common.validation_failed",
                  assertThrows(
                          AurigaException.class,
                          () ->
                              network.configure(
                                  new Configuration(true, true, true, Map.of(limit, invalid))))
                      .errorCode()
                      .code());
            }
          }
        });
  }

  @Test
  void storedOverlargeLimitsCannotRelaxPlatformCeiling() {
    business(
        () -> {
          var configured = Json.object();
          for (Limit limit : Limit.values()) configured.put(limit.name(), 9007199254740991L);
          rows.put(key("network_config", "organization"), Json.object("limits", configured));
          service("svc:collab");
          var decision = network.check(null);
          for (Limit limit : Limit.values())
            assertEquals(limit.defaultValue(), decision.limits().get(limit));
          configured.put(Limit.BODY_BYTES.name(), 1);
          rows.put(key("network_config", "organization"), Json.object("limits", configured));
          assertEquals(1L, network.check(null).limits().get(Limit.BODY_BYTES));
        });
  }

  @Test
  void realCredentialAuthorityRechecksDeviceAndBackingSessionWithoutCache() {
    business(
        () -> {
          String sessionId = UUID.randomUUID().toString();
          var sessions = mock(SessionManager.class);
          when(sessions.activeById(sessionId))
              .thenReturn(
                  Json.object(
                      "sessionId",
                      sessionId,
                      "accountId",
                      account.toString(),
                      "audience",
                      "portal",
                      "expiresAt",
                      clock.instant().plusSeconds(7200).toString(),
                      "idleExpiresAt",
                      clock.instant().plusSeconds(60).toString()));
          ObjectNode deviceRow =
              Json.object(
                  "accountId",
                  account.toString(),
                  "sessionId",
                  sessionId,
                  "scope",
                  "auriga.connect",
                  "revoked",
                  false);
          rows.put(key("device", device.toString()), deviceRow);
          var real = new DeviceAccessAuthority(store, sessions, mock(TokenService.class));
          var valid = real.credential(tenant.toString(), account.toString(), device.toString());
          assertNotNull(valid);
          assertEquals(clock.instant().plusSeconds(60), valid.expiresAt());
          deviceRow.put("revoked", true);
          assertNull(real.credential(tenant.toString(), account.toString(), device.toString()));
          deviceRow.put("revoked", false);
          when(sessions.activeById(sessionId)).thenReturn(null);
          assertNull(real.credential(tenant.toString(), account.toString(), device.toString()));
        });
  }
}
