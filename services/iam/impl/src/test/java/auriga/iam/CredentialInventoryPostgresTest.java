package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.context.TenantContext;
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
  auriga.workspace.api.ManagedCredentialRotation rotation =
      mock(auriga.workspace.api.ManagedCredentialRotation.class);
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
    when(rotation.currentVersions(any(), anyCollection()))
        .thenAnswer(c -> tenant.equals(c.getArgument(0)) ? Map.of(instance, 2L) : Map.of());
    when(rotation.rotate(any(), any(), anyLong())).thenReturn(3L);
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
    var inventory = new CredentialInventory(store, managed, clock, audit, List::of, () -> rotation);
    var own = store.tenant(tenant, () -> inventory.list("managed", "active", true));
    assertEquals(1, own.path("items").size());
    assertFalse(own.toString().contains("must-not-leak"));
    assertFalse(own.path("items").get(0).has("credential"));
    assertEquals(
        0, store.tenant(other, () -> inventory.list("managed", null, false)).path("items").size());
    var denied =
        assertThrows(
            AurigaException.class,
            () -> TenantContext.call(other, () -> inventory.revoke(instance.toString(), 2)));
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
            SigningKeys.PLATFORM,
            () ->
                new CredentialInventory(store, managed, clock, audit, List::of, () -> rotation)
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
    TenantContext.call(
        tenant,
        () -> {
          var result =
              new CredentialInventory(store, real, clock, audit, List::of, () -> rotation)
                  .revoke(instance.toString(), 2);
          assertEquals("revoked", result.path("status").asString());
          assertTrue(
              store.tx(() -> store.byId("session", sessionId, false).path("revoked").asBoolean()));
          var lineage = store.tx(() -> store.get("managed_device", instance.toString(), false));
          assertTrue(
              store.tx(
                  () ->
                      store
                          .get("device", lineage.path("device").asString(), false)
                          .path("revoked")
                          .asBoolean()));
          return null;
        });
  }

  @Test
  void staleVersionCannotRevokeReplacement() {
    var inventory = new CredentialInventory(store, managed, clock, audit, List::of, () -> rotation);
    assertEquals(
        409,
        assertThrows(
                AurigaException.class,
                () -> TenantContext.call(tenant, () -> inventory.revoke(instance.toString(), 1)))
            .errorCode()
            .httpStatus());
    verifyNoInteractions(managed);
    TenantContext.call(tenant, () -> inventory.revoke(instance.toString(), 2));
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

  @Test
  void rotationRunsAfterIamCommitAndReadbackShowsReplacement() {
    var inventory = new CredentialInventory(store, managed, clock, audit, List::of, () -> rotation);
    when(rotation.rotate(any(), any(), anyLong()))
        .thenAnswer(
            call -> {
              assertFalse(
                  org.springframework.transaction.support.TransactionSynchronizationManager
                      .isActualTransactionActive());
              return 3L;
            });
    var result = TenantContext.call(tenant, () -> inventory.revoke(instance.toString(), 2));
    assertEquals(3, result.path("replacementVersion").asLong());
    when(rotation.currentVersions(any(), anyCollection())).thenReturn(Map.of(instance, 3L));
    assertEquals(
        3,
        TenantContext.call(tenant, () -> inventory.list("managed", null, false))
            .path("items")
            .get(0)
            .path("replacementVersion")
            .asLong());
    verify(rotation).rotate(tenant, instance, 2);
  }

  @Test
  void metadataSourcesCannotSmuggleSecretValuesAndMissingSourcesAreExplicit() {
    var source = mock(auriga.iam.api.CredentialMetadataSource.class);
    when(source.name()).thenReturn("fixture");
    when(source.snapshot())
        .thenReturn(
            new auriga.iam.api.CredentialMetadataSource.Snapshot(
                List.of(
                    new auriga.iam.api.CredentialMetadataSource.Entry(
                        "service:edge",
                        "service",
                        "iam-service-issuance",
                        "v1",
                        clock.instant(),
                        clock.instant().plusSeconds(60),
                        "active")),
                List.of(
                    new auriga.iam.api.CredentialMetadataSource.Gap(
                        "identity-header", "not_reported"))));
    var inventory =
        new CredentialInventory(
            store, managed, clock, audit, () -> List.of(source), () -> rotation);
    var result =
        TenantContext.call(SigningKeys.PLATFORM, () -> inventory.list("service", null, true));
    assertEquals(1, result.path("items").size());
    assertEquals("v1", result.path("items").get(0).path("versionLabel").asString());
    assertEquals("identity-header", result.path("sourceGaps").get(0).path("source").asString());
    assertFalse(result.toString().contains("private"));
  }

  @Test
  void workspaceFailureCanRetryAfterCommittedRevocationWithoutHoldingIamResources() {
    var real =
        new IamManagedDevices(
            store,
            mock(SessionManager.class),
            mock(TokenService.class),
            mock(Secrets.class),
            mock(auriga.workspace.api.ManagedInstanceBindings.class),
            mock(auriga.iam.api.InstanceAiAuthorization.class),
            clock);
    var inventory = new CredentialInventory(store, real, clock, audit, List::of, () -> rotation);
    when(rotation.rotate(any(), any(), anyLong()))
        .thenThrow(new AurigaException(auriga.common.error.CommonErrors.UNAVAILABLE))
        .thenReturn(3L);
    assertThrows(
        AurigaException.class,
        () -> TenantContext.call(tenant, () -> inventory.revoke(instance.toString(), 2)));
    assertTrue(
        store.tenant(
            tenant,
            () ->
                store
                    .get("managed_device", instance.toString(), false)
                    .path("revoked")
                    .asBoolean()));
    assertEquals(
        3,
        TenantContext.call(tenant, () -> inventory.revoke(instance.toString(), 2))
            .path("replacementVersion")
            .asLong());
  }

  @Test
  void deletedInstanceIsTerminalAndNeverRevokesOrAuditsOnRetry() {
    when(rotation.currentVersions(any(), anyCollection())).thenReturn(Map.of());
    var inventory = new CredentialInventory(store, managed, clock, audit, List::of, () -> rotation);
    var list = TenantContext.call(tenant, () -> inventory.list("managed", null, false));
    assertEquals("instance_deleted", list.path("items").get(0).path("status").asString());
    assertFalse(list.path("sourceGaps").toString().contains("workspace-rotation"));
    for (int i = 0; i < 2; i++)
      assertEquals(
          "instance_deleted",
          TenantContext.call(tenant, () -> inventory.revoke(instance.toString(), 2))
              .path("status")
              .asString());
    verifyNoInteractions(managed, audit);
    verify(rotation, never()).rotate(any(), any(), anyLong());
    verify(rotation, times(3)).currentVersions(eq(tenant), anyCollection());
  }

  @Test
  void nonPlatformTenantCannotReadInstallationSources() {
    String kid = UUID.randomUUID().toString();
    store.isolated(
        SigningKeys.PLATFORM,
        () ->
            store.insert(
                "signing_key",
                kid,
                Json.object(
                    "kid", kid, "state", "current", "publishedAt", clock.instant().toString()),
                null));
    var source = mock(auriga.iam.api.CredentialMetadataSource.class);
    when(source.name()).thenReturn("iam-service-issuance");
    var inventory =
        new CredentialInventory(
            store, managed, clock, audit, () -> List.of(source), () -> rotation);
    var result = TenantContext.call(tenant, () -> inventory.list("signing", null, false));
    assertTrue(result.path("items").isEmpty());
    verify(source, never()).snapshot();
  }

  @Test
  void concurrentDeletionAfterPreflightReturnsTerminalState() {
    var inventory = new CredentialInventory(store, managed, clock, audit, List::of, () -> rotation);
    when(rotation.rotate(any(), any(), anyLong()))
        .thenThrow(new AurigaException(auriga.common.error.CommonErrors.NOT_FOUND));
    assertEquals(
        "instance_deleted",
        TenantContext.call(tenant, () -> inventory.revoke(instance.toString(), 2))
            .path("status")
            .asString());
  }
}
