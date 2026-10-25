package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.persistence.*;
import auriga.common.security.*;
import auriga.contracts.iam.v1.*;
import auriga.testsupport.PostgresExtension;
import java.math.BigDecimal;
import java.time.*;
import java.util.*;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.security.core.context.SecurityContextHolder;

@ExtendWith(PostgresExtension.class)
class NetworkDirectoryPostgresTest {
  static IamStore store;
  static JdbcTemplate jdbc;
  final UUID tenant = UUID.randomUUID(), account = UUID.randomUUID(), device = UUID.randomUUID();
  final UUID session = UUID.randomUUID();
  final SessionManager sessions = mock(SessionManager.class);
  final DeviceAccessAuthority devices = mock(DeviceAccessAuthority.class);
  IamNetworkDirectory directory;
  String realm;

  @BeforeAll
  static void migrate() {
    var roles =
        PostgresExtension.serviceRoles(
            NetworkDirectoryPostgresTest.class, "directory_owner", "directory_app", "iam");
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
    directory = new IamNetworkDirectory(store, sessions, devices, Clock.systemUTC());
    when(sessions.activeById(session.toString()))
        .thenReturn(Json.object("accountId", account, "audience", "portal"));
    when(devices.credential(tenant.toString(), account.toString(), device.toString()))
        .thenReturn(
            new DeviceAccessAuthority.Identity(
                tenant.toString(),
                account.toString(),
                device.toString(),
                Instant.now().plusSeconds(3600)));
    realm = createRealm(tenant);
    node(tenant, "1", account, device, false);
    user(PrincipalType.USER, TokenContext.PORTAL, tenant);
  }

  @AfterEach
  void clear() {
    SecurityContextHolder.clearContext();
  }

  void user(PrincipalType type, TokenContext context, UUID principalTenant) {
    SecurityContextHolder.getContext()
        .setAuthentication(
            new AurigaAuthentication(
                new AurigaPrincipal(
                    account.toString(),
                    principalTenant,
                    type,
                    context,
                    session.toString(),
                    device.toString(),
                    "member",
                    List.of(),
                    Set.of("auriga.connect.collab.use"),
                    null,
                    null,
                    null)));
  }

  void service(String subject) {
    SecurityContextHolder.getContext()
        .setAuthentication(
            new AurigaAuthentication(
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
                    null)));
  }

  static String createRealm(UUID tenantId) {
    return TenantContext.call(
        tenantId,
        () ->
            store.tx(
                () ->
                    jdbc.queryForObject(
                        "INSERT INTO iam.network_realm(tenant_id,realm) VALUES (?,nextval('iam.network_realm_number')::numeric+9223372036854775809) RETURNING realm::text",
                        String.class,
                        tenantId)));
  }

  String address(String node) {
    return "aur1:" + new java.math.BigInteger(realm).toString(16) + "." + node + ".0";
  }

  void node(UUID targetTenant, String number, UUID owner, UUID nativeDevice, boolean revoked) {
    TenantContext.run(
        targetTenant,
        () ->
            store.tx(
                () -> {
                  if (store.get("account", owner.toString(), false) == null)
                    store.insert(
                        "account",
                        owner.toString(),
                        Json.object("status", "active", "displayName", "Alice"),
                        null);
                  UUID providerId = UUID.randomUUID(), sessionId = UUID.randomUUID();
                  store.insert(
                      "idp_provider",
                      providerId.toString(),
                      Json.object("providerId", providerId),
                      null);
                  store.insert(
                      "session",
                      sessionId.toString(),
                      Json.object(
                          "tenantId",
                          targetTenant,
                          "accountId",
                          owner,
                          "providerId",
                          providerId,
                          "audience",
                          "portal",
                          "expiresAt",
                          Instant.now().plusSeconds(3600).toString(),
                          "idleExpiresAt",
                          Instant.now().plusSeconds(1800).toString()),
                      Instant.now().plusSeconds(3600));
                  store.insert(
                      "device",
                      nativeDevice.toString(),
                      Json.object(
                          "accountId",
                          owner,
                          "name",
                          "DSH " + number,
                          "sessionId",
                          sessionId,
                          "scope",
                          "auriga.connect",
                          "sessionLocal",
                          "secret-session-local"),
                      null);
                  jdbc.update(
                      "INSERT INTO iam.network_node(tenant_id,node,account_id,device_id,revoked) VALUES (?,?,?,?,?)",
                      targetTenant,
                      new BigDecimal(number),
                      owner,
                      nativeDevice,
                      revoked);
                  return null;
                }));
  }

  void config(boolean enabled) {
    store.tx(
        () -> {
          store.delete("network_config", "organization");
          store.insert("network_config", "organization", Json.object("enabled", enabled), null);
          return null;
        });
  }

  void policy(String source, String target, boolean allowed) {
    store.tx(
        () -> {
          store.delete("network_policy", source + ":" + target);
          store.insert(
              "network_policy", source + ":" + target, Json.object("allowed", allowed), null);
          return null;
        });
  }

  static void error(String code, Runnable action) {
    assertEquals(code, assertThrows(AurigaException.class, action::run).errorCode().code());
  }

  @Test
  void onlyCurrentTenantRegisteredUnrevokedSystemsAreVisibleWithoutSessionLocals() {
    UUID foreign = UUID.randomUUID();
    createRealm(foreign);
    node(foreign, "2", UUID.randomUUID(), UUID.randomUUID(), false);
    node(tenant, "2", UUID.randomUUID(), UUID.randomUUID(), false);
    node(tenant, "3", UUID.randomUUID(), UUID.randomUUID(), true);
    TenantContext.run(
        tenant,
        () -> {
          var page = directory.list(50, "");
          assertEquals(2, page.getItemsCount());
          assertEquals(address("2"), page.getItems(1).getSystemAddress());
          assertEquals("Alice", page.getItems(1).getOwnerDisplayName());
          assertEquals("DSH 2", page.getItems(1).getDisplayName());
          var controller = new IamNetworkDirectoryController(directory);
          var json = controller.systems(50, "");
          new ContractSchemas().validate("network/system-directory.schema.json", json);
          assertFalse(json.toString().contains("secret-session-local"));
          assertFalse(json.has("nextCursor"));
          assertEquals(
              Set.of(
                  "systemAddress",
                  "displayName",
                  "ownerUserId",
                  "ownerDisplayName",
                  "onlineState",
                  "statusObservedAt"),
              json.path("items").get(0).propertyNames());
          user(PrincipalType.USER, TokenContext.PORTAL, foreign);
          error("common.forbidden", () -> directory.list(50, ""));
        });
  }

  @Test
  void organizationAndPairPolicyAreRecheckedOnEveryPageForPortalAndDevice() {
    node(tenant, "2", UUID.randomUUID(), UUID.randomUUID(), false);
    node(tenant, "3", account, UUID.randomUUID(), false);
    TenantContext.run(
        tenant,
        () -> {
          policy("3", "2", false);
          assertEquals(
              List.of(address("1"), address("3")),
              directory.list(50, "").getItemsList().stream()
                  .map(NetworkSystemEntry::getSystemAddress)
                  .toList());
          user(PrincipalType.DEVICE, TokenContext.DEVICE, tenant);
          assertEquals(3, directory.list(50, "").getItemsCount());
          policy("1", "2", false);
          assertEquals(2, directory.list(50, "").getItemsCount());
          config(false);
          assertEquals(0, directory.list(50, "").getItemsCount());
          config(true);
          assertEquals(2, directory.list(50, "").getItemsCount());
          when(devices.credential(tenant.toString(), account.toString(), device.toString()))
              .thenReturn(null);
          error("common.forbidden", () -> directory.list(50, ""));
        });
  }

  @Test
  void paginationIsBoundedOrderedAndDoesNotSkipUnsignedIdsOrTrustCursorForAuthorization() {
    for (int i = 2; i <= 202; i++)
      node(tenant, Integer.toString(i), UUID.randomUUID(), UUID.randomUUID(), false);
    node(tenant, "18446744073709551615", UUID.randomUUID(), UUID.randomUUID(), false);
    TenantContext.run(
        tenant,
        () -> {
          var first = directory.list(200, "");
          assertEquals(200, first.getItemsCount());
          assertFalse(first.getNextCursor().isEmpty());
          policy("1", "201", false);
          var second = directory.list(200, first.getNextCursor());
          assertEquals(2, second.getItemsCount());
          assertEquals(address("ffffffffffffffff"), second.getItems(1).getSystemAddress());
          assertEquals("", second.getNextCursor());
          assertEquals(1, directory.list(1, "").getItemsCount());
          for (int limit : new int[] {0, -1, 201, Integer.MAX_VALUE})
            error("common.validation_failed", () -> directory.list(limit, ""));
          for (String cursor : List.of("x".repeat(129), "bad!", "01", "-1"))
            error("common.validation_failed", () -> directory.list(50, cursor));
          config(false);
          assertEquals(0, directory.list(50, first.getNextCursor()).getItemsCount());
        });
  }

  @Test
  void revokedDevicesAndDisabledOwnersAreHiddenAndServicesCannotReadUserDirectory() {
    UUID owner = UUID.randomUUID(), targetDevice = UUID.randomUUID();
    node(tenant, "2", owner, targetDevice, false);
    TenantContext.run(
        tenant,
        () -> {
          for (String table : List.of("device", "account")) {
            store.tx(
                () -> {
                  var row =
                      store.get(
                          table,
                          table.equals("device") ? targetDevice.toString() : owner.toString(),
                          true);
                  if (table.equals("device")) row.put("revoked", true);
                  else row.put("status", "disabled");
                  store.save(table, row);
                  return null;
                });
            assertEquals(1, directory.list(50, "").getItemsCount());
          }
          store.tx(
              () -> {
                var disabled = store.get("account", account.toString(), true);
                disabled.put("status", "disabled");
                store.save("account", disabled);
                return null;
              });
          error("common.forbidden", () -> directory.list(50, ""));
          for (String caller : List.of("svc:collab", "svc:ai-gateway", "svc:edge")) {
            service(caller);
            error("common.forbidden", () -> directory.list(50, ""));
          }
          user(PrincipalType.SERVICE_ACCOUNT, TokenContext.SERVICE, tenant);
          error("common.forbidden", () -> directory.list(50, ""));
          SecurityContextHolder.clearContext();
          error("common.unauthenticated", () -> directory.list(50, ""));
        });
  }

  @Test
  void collabLookupOnlyReportsRegisteredDshAndCannotProbeOtherTenantOrLocals() {
    TenantContext.run(
        tenant,
        () -> {
          service("svc:collab");
          var found = directory.resolve(realm, "1");
          assertTrue(found.getExists());
          assertEquals(NetworkDirectoryNodeType.NETWORK_DIRECTORY_NODE_TYPE_DSH, found.getType());
          assertFalse(directory.resolve("1", "1").getExists());
          assertFalse(directory.resolve(realm, "999").getExists());
          TenantContext.run(
              UUID.randomUUID(), () -> assertFalse(directory.resolve(realm, "1").getExists()));
          for (String bad : List.of("0", "01", "aur1:1.1.0", "1.2", "18446744073709551616"))
            error("common.validation_failed", () -> directory.resolve(realm, bad));
          config(false);
          assertEquals(
              ResolveNetworkNodeResponse.getDefaultInstance(), directory.resolve(realm, "1"));
          config(true);
          store.tx(
              () -> {
                jdbc.update("UPDATE iam.network_node SET revoked=true WHERE tenant_id=?", tenant);
                return null;
              });
          assertFalse(directory.resolve(realm, "1").getExists());
          service("svc:ai-gateway");
          error("common.forbidden", () -> directory.resolve(realm, "1"));
        });
  }

  @Test
  void staleCredentialsAreHiddenButRecentOfflineDevicesKeepUnknownStatus() {
    UUID targetDevice = UUID.randomUUID();
    node(tenant, "2", UUID.randomUUID(), targetDevice, false);
    TenantContext.run(
        tenant,
        () -> {
          store.tx(
              () -> {
                var d = store.get("device", targetDevice.toString(), false);
                var backing = store.get("session", d.path("sessionId").asString(), true);
                backing.put("expiresAt", Instant.now().minusSeconds(31L * 86400).toString());
                backing.put("idleExpiresAt", Instant.now().minusSeconds(31L * 86400).toString());
                store.save("session", backing);
                return null;
              });
          assertEquals(1, directory.list(50, "").getItemsCount());
          service("svc:collab");
          assertEquals(
              ResolveNetworkNodeResponse.getDefaultInstance(), directory.resolve(realm, "2"));
          store.tx(
              () -> {
                var d = store.get("device", targetDevice.toString(), false);
                var backing = store.get("session", d.path("sessionId").asString(), true);
                backing.put("expiresAt", Instant.now().minusSeconds(86400).toString());
                backing.put("idleExpiresAt", Instant.now().minusSeconds(86400).toString());
                store.save("session", backing);
                return null;
              });
          assertTrue(directory.resolve(realm, "2").getExists());
          user(PrincipalType.USER, TokenContext.PORTAL, tenant);
          var offline = directory.list(50, "").getItems(1);
          assertEquals("unknown", offline.getOnlineState());
          assertFalse(offline.hasStatusObservedAt());
        });
  }

  @Test
  void massivePolicyFilteringReturnsBoundedPartialPagesWithProgressingCursor() {
    // More than one scan window is denied by node policy, without scanning to tenant end.
    TenantContext.run(
        tenant,
        () ->
            store.tx(
                () -> {
                  jdbc.update(
                      "INSERT INTO iam.device(id,tenant_id,lookup_key,data) "
                          + "SELECT gen_random_uuid(),?,'scan-'||g,(SELECT data FROM iam.device WHERE tenant_id=? AND id=?)||jsonb_build_object('scanNode',g) FROM generate_series(2,1001) g",
                      tenant,
                      tenant,
                      device);
                  jdbc.update(
                      "INSERT INTO iam.network_node(tenant_id,node,account_id,device_id,revoked) "
                          + "SELECT tenant_id,(data->>'scanNode')::numeric,account_id,id,false FROM iam.device WHERE tenant_id=? AND data->>'scanNode' IS NOT NULL",
                      tenant);
                  return null;
                }));
    node(tenant, "1002", UUID.randomUUID(), UUID.randomUUID(), false);
    TenantContext.run(
        tenant,
        () -> {
          store.tx(
              () -> {
                jdbc.update(
                    "INSERT INTO iam.network_policy(id,tenant_id,lookup_key,data) SELECT gen_random_uuid(),?,'1:'||g,jsonb_build_object('allowed',false) FROM generate_series(1,1001) g",
                    tenant);
                return null;
              });
          var first = directory.list(200, "");
          assertEquals(0, first.getItemsCount());
          assertFalse(first.getNextCursor().isEmpty());
          var second = directory.list(200, first.getNextCursor());
          assertEquals(1, second.getItemsCount());
          assertEquals(address("3ea"), second.getItems(0).getSystemAddress());
          assertTrue(second.getNextCursor().isEmpty());
        });
  }
}
