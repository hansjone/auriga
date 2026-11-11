package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.audit.AuditLogger;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.persistence.*;
import auriga.testsupport.PostgresExtension;
import java.time.Clock;
import java.util.UUID;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;

@ExtendWith(PostgresExtension.class)
class AdminDevicesPostgresTest {
  static IamStore store;

  @BeforeAll
  static void migrate() {
    var roles =
        PostgresExtension.serviceRoles(
            AdminDevicesPostgresTest.class, "devices_owner", "devices_app", "iam");
    CommonMigrations.migrate(roles.owner().dataSource(), "iam");
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas("iam")
                .locations("classpath:db/iam")
                .load())
        .migrate();
    store =
        new IamStore(
            new JdbcTemplate(roles.runtime().dataSource()),
            new TenantTransactionManager(roles.runtime().dataSource()));
  }

  @Test
  void issuedAccessAndRefreshAreInvalidAfterRevocation() {
    NativeConnectPostgresTest.migrate();
    var nativeLogin = new NativeConnectPostgresTest();
    nativeLogin.setup();
    TenantContext.run(
        nativeLogin.tenant,
        () -> {
          var token = nativeLogin.connected();
          var authority =
              new DeviceAccessAuthority(
                  NativeConnectPostgresTest.store, nativeLogin.sessions, nativeLogin.tokens);
          String access = token.path("access_token").asString();
          var identity = authority.access(access);
          assertNotNull(identity);
          var devices =
              new IamAdminDevices(
                  NativeConnectPostgresTest.store, nativeLogin.clock, mock(AuditLogger.class));
          devices.revoke(identity.device());
          assertNull(authority.access(access));
          assertEquals("invalid_grant", nativeLogin.rotate(token).path("error").asString());
        });
  }

  @Test
  void tenantIsolationProjectionAndRevocation() {
    UUID tenant = UUID.randomUUID();
    UUID account = UUID.randomUUID();
    String id = UUID.randomUUID().toString();
    var devices = new IamAdminDevices(store, Clock.systemUTC(), mock(AuditLogger.class));
    TenantContext.run(
        tenant,
        () -> {
          store.tx(
              () -> {
                store.insert(
                    "account", account.toString(), Json.object("displayName", "Alice"), null);
                return null;
              });
          store.tx(
              () ->
                  store.insert(
                      "device",
                      id,
                      Json.object(
                          "accountId",
                          account.toString(),
                          "name",
                          "Desktop",
                          "kind",
                          "dsh_connect",
                          "scope",
                          "auriga.connect",
                          "refreshHash",
                          "secret",
                          "revoked",
                          false),
                      null));
          store.tx(
              () -> {
                store
                    .networkJdbc()
                    .update("INSERT INTO iam.network_realm(tenant_id,realm) VALUES (?,1)", tenant);
                store
                    .networkJdbc()
                    .update(
                        "INSERT INTO iam.network_node(tenant_id,node,account_id,device_id) VALUES (?,1,?,?)",
                        tenant,
                        account,
                        UUID.fromString(id));
                return null;
              });
          assertEquals(1, devices.list(null, "unknown").path("items").size());
          assertEquals(1, devices.list("alice", null).path("items").size());
          assertEquals(0, devices.list(null, "online").path("items").size());
          assertEquals("1", devices.get(id).path("networkNode").asString());
          assertFalse(devices.get(id).has("refreshHash"));
          assertEquals("active", devices.get(id).path("status").asString());
          assertEquals("revoked", devices.revoke(id).path("status").asString());
          assertTrue(store.tx(() -> store.get("device", id, false)).path("revoked").asBoolean());
          assertTrue(
              store.tx(
                  () ->
                      store
                          .networkJdbc()
                          .queryForObject(
                              "SELECT revoked FROM iam.network_node WHERE tenant_id=? AND device_id=?",
                              Boolean.class,
                              tenant,
                              UUID.fromString(id))));
          assertEquals("revoked", devices.revoke(id).path("status").asString());
          assertEquals(0, devices.list("other", null).path("items").size());
          String managed = UUID.randomUUID().toString();
          store.tx(
              () ->
                  store.insert(
                      "device",
                      managed,
                      Json.object(
                          "accountId",
                          account.toString(),
                          "kind",
                          "dsh_connect",
                          "deviceType",
                          "managed",
                          "scope",
                          "auriga.connect"),
                      null));
          assertEquals(1, devices.list(null, null).path("items").size());
          assertEquals(
              "common.not_found",
              assertThrows(AurigaException.class, () -> devices.revoke(managed))
                  .errorCode()
                  .code());
        });
    TenantContext.run(
        UUID.randomUUID(),
        () -> {
          assertEquals(0, devices.list(null, null).path("items").size());
          var e = assertThrows(AurigaException.class, () -> devices.revoke(id));
          assertEquals("common.not_found", e.errorCode().code());
        });
  }
}
