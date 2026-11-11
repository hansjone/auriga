package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.error.AurigaException;
import auriga.common.persistence.*;
import auriga.testsupport.PostgresExtension;
import java.time.*;
import java.util.*;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;

@ExtendWith(PostgresExtension.class)
class CredentialInventoryPostgresTest {
  static IamStore store;
  UUID tenant = UUID.randomUUID(), other = UUID.randomUUID(), instance = UUID.randomUUID();
  Clock clock = Clock.fixed(Instant.parse("2026-01-01T00:00:00Z"), ZoneOffset.UTC);
  auriga.common.audit.AuditLogger audit = mock(auriga.common.audit.AuditLogger.class);
  IamManagedDevices managed = mock(IamManagedDevices.class);

  @BeforeAll
  static void migrate() {
    var roles =
        PostgresExtension.serviceRoles(
            CredentialInventoryPostgresTest.class, "creds_owner", "creds_app", "iam");
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

  @BeforeEach
  void seed() {
    store.tenant(
        tenant,
        () ->
            store.insert(
                "managed_device",
                instance.toString(),
                Json.object(
                    "device",
                    UUID.randomUUID().toString(),
                    "version",
                    2,
                    "expiresAt",
                    "2026-01-05T00:00:00Z",
                    "credential",
                    "must-not-leak",
                    "modelCredential",
                    "must-not-leak"),
                null));
  }

  @Test
  void tenantIsolationAndSecretProjection() {
    var inventory = new CredentialInventory(store, managed, clock, audit);
    var own = store.tenant(tenant, () -> inventory.list("managed", "active", true));
    assertEquals(1, own.path("items").size());
    assertFalse(own.toString().contains("must-not-leak"));
    assertFalse(own.path("items").get(0).has("credential"));
    assertEquals(
        0, store.tenant(other, () -> inventory.list("managed", null, false)).path("items").size());
    var denied =
        assertThrows(
            AurigaException.class,
            () -> store.tenant(other, () -> inventory.revoke(instance.toString(), 2)));
    assertEquals("common.not_found", denied.errorCode().code());
    verifyNoInteractions(managed);
  }

  @Test
  void authorizationRequiresAdminPermissionAndRecentVerification() throws Exception {
    var list =
        auriga.common.security.AuthorizationRules.of(
            CredentialAdminController.class.getMethod(
                "list", String.class, String.class, boolean.class),
            CredentialAdminController.class);
    var revoke =
        auriga.common.security.AuthorizationRules.of(
            CredentialAdminController.class.getMethod(
                "revoke", UUID.class, auriga.iam.web.model.CredentialRevoke.class),
            CredentialAdminController.class);
    assertEquals(
        "common.unauthenticated",
        assertThrows(AurigaException.class, () -> list.check(null, clock)).errorCode().code());
    var denied =
        new auriga.common.security.AurigaPrincipal(
            UUID.randomUUID().toString(),
            tenant,
            auriga.common.security.PrincipalType.USER,
            auriga.common.security.TokenContext.ADMIN,
            null,
            null,
            null,
            List.of(),
            Set.of(),
            clock.instant(),
            null,
            "test");
    assertEquals(
        "common.forbidden",
        assertThrows(AurigaException.class, () -> list.check(denied, clock)).errorCode().code());
    var admin =
        new auriga.common.security.AurigaPrincipal(
            denied.subject(),
            tenant,
            denied.type(),
            denied.context(),
            null,
            null,
            null,
            List.of(),
            Set.of("iam.keys.write"),
            clock.instant(),
            null,
            "test");
    list.check(admin, clock);
    assertEquals(
        "iam.step_up_required",
        assertThrows(AurigaException.class, () -> revoke.check(admin, clock)).errorCode().code());
    var portal =
        new auriga.common.security.AurigaPrincipal(
            admin.subject(),
            tenant,
            admin.type(),
            auriga.common.security.TokenContext.PORTAL,
            null,
            null,
            null,
            List.of(),
            admin.permissions(),
            clock.instant(),
            clock.instant(),
            "test");
    assertEquals(
        "common.forbidden",
        assertThrows(AurigaException.class, () -> list.check(portal, clock)).errorCode().code());
  }

  @Test
  void signingProjectionContainsOnlyMetadata() {
    String kid = UUID.randomUUID().toString();
    store.isolated(
        SigningKeys.PLATFORM,
        () ->
            store.insert(
                "signing_key",
                kid,
                Json.object(
                    "kid",
                    kid,
                    "state",
                    "retiring",
                    "publishedAt",
                    "2025-12-01T00:00:00Z",
                    "retainUntil",
                    "2026-01-02T00:00:00Z",
                    "private",
                    "must-not-leak",
                    "public",
                    Json.object("x", "must-not-leak")),
                null));
    var result =
        store.tenant(
            tenant,
            () ->
                new CredentialInventory(store, managed, clock, audit)
                    .list("signing", "retiring", true));
    assertTrue(result.path("items").size() >= 1);
    assertFalse(result.toString().contains("must-not-leak"));
  }

  @Test
  void realRevocationInvalidatesDeviceAndSession() {
    String sessionId = UUID.randomUUID().toString();
    store.tenant(
        tenant,
        () -> {
          var lineage = store.get("managed_device", instance.toString(), false);
          String owner = UUID.randomUUID().toString();
          store.insert("account", owner, Json.object("status", "active"), null);
          store.insert(
              "session",
              sessionId,
              Json.object(
                  "accountId",
                  owner,
                  "sessionId",
                  sessionId,
                  "kind",
                  "managed_device",
                  "audience",
                  "portal",
                  "managedInstance",
                  instance.toString(),
                  "managedVersion",
                  2,
                  "revoked",
                  false),
              null);
          store.insert(
              "device",
              lineage.path("device").asString(),
              Json.object(
                  "accountId",
                  owner,
                  "sessionId",
                  sessionId,
                  "revoked",
                  false,
                  "createdAt",
                  clock.instant().toString()),
              null);
          return null;
        });
    var real =
        new IamManagedDevices(
            store,
            mock(SessionManager.class),
            mock(TokenService.class),
            mock(Secrets.class),
            mock(auriga.workspace.api.ManagedInstanceBindings.class),
            mock(auriga.iam.api.InstanceAiAuthorization.class),
            clock);
    store.tenant(
        tenant,
        () -> {
          var result =
              new CredentialInventory(store, real, clock, audit).revoke(instance.toString(), 2);
          assertEquals("revoked", result.path("status").asString());
          assertTrue(store.byId("session", sessionId, false).path("revoked").asBoolean());
          var lineage = store.get("managed_device", instance.toString(), false);
          assertTrue(
              store
                  .get("device", lineage.path("device").asString(), false)
                  .path("revoked")
                  .asBoolean());
          return null;
        });
  }

  @Test
  void staleVersionCannotRevokeReplacement() {
    var inventory = new CredentialInventory(store, managed, clock, audit);
    assertEquals(
        409,
        assertThrows(
                AurigaException.class,
                () -> store.tenant(tenant, () -> inventory.revoke(instance.toString(), 1)))
            .errorCode()
            .httpStatus());
    verifyNoInteractions(managed);
    store.tenant(tenant, () -> inventory.revoke(instance.toString(), 2));
    verify(audit)
        .record(
            argThat(
                e ->
                    e.action().equals("iam.credential.revoke")
                        && e.target().id().equals(instance.toString())));
    verify(managed)
        .revoke(
            argThat(
                r ->
                    r.getTenantId().equals(tenant.toString())
                        && r.getInstanceId().equals(instance.toString())
                        && r.getCredentialVersion() == 2));
  }
}
