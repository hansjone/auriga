package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.audit.AuditLogger;
import auriga.common.context.TenantContext;
import auriga.common.events.EventPublisher;
import auriga.common.persistence.*;
import auriga.common.security.*;
import auriga.testsupport.PostgresExtension;
import com.nimbusds.jose.jwk.Curve;
import com.nimbusds.jose.jwk.gen.ECKeyGenerator;
import java.time.*;
import java.util.*;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.security.core.context.SecurityContextHolder;

/**
 * Real signatures, online device/session/account checks, transactions, FORCE RLS and rate counters.
 */
@ExtendWith(PostgresExtension.class)
class NetworkClientPostgresTest {
  static IamStore store;
  static JdbcTemplate jdbc;
  final UUID tenant = UUID.randomUUID(), account = UUID.randomUUID(), device = UUID.randomUUID();
  final AdminCsrfPostgresTest.Time clock = new AdminCsrfPostgresTest.Time();
  SessionManager sessions;
  SessionManager.Issued session;
  TokenService tokens;
  IamNetworkAuthority authority;
  IamNetworkClient client;
  String access;

  @BeforeAll
  static void migrate() {
    var roles =
        PostgresExtension.serviceRoles(
            NetworkClientPostgresTest.class, "netclient_owner", "netclient_app", "iam");
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
  void setup() throws Exception {
    var key = new ECKeyGenerator(Curve.P_256).keyID(UUID.randomUUID().toString()).generate();
    var keys = mock(SigningKeys.class);
    when(keys.current()).thenReturn(key);
    when(keys.jwks()).thenReturn(Json.object("keys", List.of(key.toPublicJWK().toJSONObject())));
    tokens =
        new TokenService(
            keys,
            new ContractSchemas(),
            (t, s, c) -> Set.of("auriga.connect", "auriga.connect.collab.use"),
            clock,
            "https://portal.example.com",
            Map.of());
    sessions =
        new SessionManager(
            store,
            mock(SessionKv.class),
            tokens,
            clock,
            mock(EventPublisher.class),
            mock(AuditLogger.class));
    TenantContext.run(
        tenant,
        () -> {
          session =
              store.tx(
                  () -> {
                    var provider = UUID.randomUUID();
                    store.insert(
                        "account", account.toString(), Json.object("status", "active"), null);
                    store.insert("idp_provider", provider.toString(), Json.object(), null);
                    var issued =
                        sessions.create(
                            account,
                            provider,
                            "portal",
                            true,
                            Duration.ofHours(2),
                            Duration.ofHours(1));
                    store.insert(
                        "device",
                        device.toString(),
                        Json.object(
                            "accountId",
                            account,
                            "sessionId",
                            issued.session().path("sessionId").asString(),
                            "scope",
                            "auriga.connect",
                            "revoked",
                            false),
                        null);
                    return issued;
                  });
          access = token("auriga", "device", "device", null, device, "auriga.connect");
        });
    authority =
        new IamNetworkAuthority(
            store,
            new DeviceAccessAuthority(store, sessions, tokens),
            mock(AuditLogger.class),
            clock,
            Set.of("server-a"));
    client = new IamNetworkClient(authority, store, clock, "server-a", Set.of("server-a"));
    devicePrincipal();
  }

  String token(
      String audience, String type, String context, UUID sessionId, UUID deviceId, String scope) {
    return tokens.access(
        tenant,
        account.toString(),
        audience,
        type,
        context,
        sessionId,
        deviceId,
        clock.instant(),
        clock.instant(),
        scope);
  }

  void devicePrincipal() {
    SecurityContextHolder.getContext()
        .setAuthentication(
            new AurigaAuthentication(
                new AurigaPrincipal(
                    account.toString(),
                    tenant,
                    PrincipalType.DEVICE,
                    TokenContext.DEVICE,
                    null,
                    device.toString(),
                    "member",
                    List.of(),
                    Set.of("auriga.connect.collab.use"),
                    null,
                    null,
                    null)));
  }

  @AfterEach
  void clear() {
    SecurityContextHolder.clearContext();
  }

  void mutate(String table, String id, String field, Object value) {
    store.tx(
        () -> {
          var row = store.byId(table, id, true);
          row.set(field, Json.MAPPER.valueToTree(value));
          store.save(table, row);
          return null;
        });
  }

  @Test
  void nativeDeviceRegistersIdempotentlyAndRedeemsOnlyOnceWithinReturnedDeadline() {
    TenantContext.run(
        tenant,
        () -> {
          var node = client.register(access);
          assertEquals(node, client.register(access));
          var ticket = client.prepare(access, node.node());
          assertEquals(clock.instant().plusSeconds(30), ticket.expiresAt());
          assertEquals(clock.instant().plusSeconds(3600), ticket.connectionExpiresAt());
          var stored =
              store.tx(() -> store.get("network_ticket", Secrets.hash(ticket.ticket()), false));
          assertFalse(stored.toString().contains(ticket.ticket()));
          assertFalse(stored.toString().contains(access));
          clock.now = clock.instant().plusSeconds(20);
          NetworkAuthorityPostgresTest.gateway();
          var connection =
              authority.redeem(
                  ticket.ticket(),
                  node.node(),
                  device.toString(),
                  "server-a",
                  NetworkAuthorityTest.binding(ticket.ticket()));
          assertEquals(ticket.connectionExpiresAt(), connection.expiresAt());
          NetworkClientBoundaryTest.error(
              "common.forbidden",
              () ->
                  authority.redeem(
                      ticket.ticket(),
                      node.node(),
                      device.toString(),
                      "server-a",
                      NetworkAuthorityTest.binding(ticket.ticket())));
        });
  }

  @Test
  void revokedDeviceSessionAccountAndDisabledOrganizationAllDenyBothOperations() {
    TenantContext.run(
        tenant,
        () -> {
          var node = client.register(access);
          String sessionId = session.session().path("sessionId").asString();
          for (String kind : List.of("device", "session", "account", "organization")) {
            switch (kind) {
              case "device" -> mutate("device", device.toString(), "revoked", true);
              case "session" -> mutate("session", sessionId, "revoked", true);
              case "account" -> mutate("account", account.toString(), "status", "disabled");
              case "organization" ->
                  store.tx(
                      () ->
                          store.insert(
                              "network_config",
                              "organization",
                              Json.object("enabled", false),
                              null));
            }
            NetworkClientBoundaryTest.error("common.forbidden", () -> client.register(access));
            NetworkClientBoundaryTest.error(
                "common.forbidden", () -> client.prepare(access, node.node()));
            switch (kind) {
              case "device" -> mutate("device", device.toString(), "revoked", false);
              case "session" -> mutate("session", sessionId, "revoked", false);
              case "account" -> mutate("account", account.toString(), "status", "active");
              case "organization" ->
                  store.tx(
                      () -> {
                        store.delete("network_config", "organization");
                        return null;
                      });
            }
          }
        });
  }

  @Test
  void expiredSessionAndCrossTenantTokenAreDeniedWithoutIdentityAllocation() {
    TenantContext.run(
        tenant,
        () -> {
          mutate(
              "session",
              session.session().path("sessionId").asString(),
              "idleExpiresAt",
              clock.instant().toString());
          NetworkClientBoundaryTest.error("common.forbidden", () -> client.register(access));
          assertEquals(
              0,
              store.tx(
                  () ->
                      jdbc.queryForObject("SELECT count(*) FROM iam.network_node", Integer.class)));
        });
    TenantContext.run(
        UUID.randomUUID(),
        () -> NetworkClientBoundaryTest.error("common.forbidden", () -> client.register(access)));
  }

  @Test
  void portalAdminServiceWrongAudienceScopeAndForeignDeviceCredentialsAreDenied() {
    TenantContext.run(
        tenant,
        () -> {
          UUID sessionId = UUID.fromString(session.session().path("sessionId").asString());
          for (String invalid :
              List.of(
                  token("auriga", "user", "portal", sessionId, null, ""),
                  token("auriga", "user", "admin", sessionId, null, ""),
                  token("auriga", "service_account", "service", null, null, ""),
                  token("auriga.ai-gateway", "device", "device", null, device, "auriga.connect"),
                  token("auriga", "device", "device", null, device, "openid"),
                  token("auriga", "device", "device", null, UUID.randomUUID(), "auriga.connect"),
                  "invalid-refresh-or-cookie")) {
            NetworkClientBoundaryTest.error("common.forbidden", () -> client.register(invalid));
          }
        });
  }

  @Test
  void rateLimitSurvivesNewClientAndRejectedRequestsThenResetsAtNextWindow() {
    TenantContext.run(
        tenant,
        () -> {
          for (int i = 0; i < 30; i++)
            NetworkClientBoundaryTest.error("common.forbidden", () -> client.register("invalid"));
          var restarted =
              new IamNetworkClient(authority, store, clock, "server-a", Set.of("server-a"));
          NetworkClientBoundaryTest.error("common.rate_limited", () -> restarted.register(access));
          NetworkClientBoundaryTest.error(
              "common.rate_limited", () -> restarted.prepare(access, "1"));
          clock.now = clock.instant().plusSeconds(300);
          access = token("auriga", "device", "device", null, device, "auriga.connect");
          assertNotNull(restarted.register(access));
        });
  }
}
