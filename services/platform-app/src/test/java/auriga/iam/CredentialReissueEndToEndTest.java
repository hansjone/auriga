package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.audit.AuditLogger;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.events.EventPublisher;
import auriga.common.persistence.*;
import auriga.contracts.iam.v1.*;
import auriga.contracts.org.v1.*;
import auriga.contracts.workspace.v1.*;
import auriga.iam.api.InstanceAiAuthorization;
import auriga.testsupport.PostgresExtension;
import auriga.workspace.api.ManagedInstanceBindings;
import com.nimbusds.jose.jwk.Curve;
import com.nimbusds.jose.jwk.gen.ECKeyGenerator;
import java.time.*;
import java.util.*;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;

@ExtendWith(PostgresExtension.class)
class CredentialReissueEndToEndTest {
  static IamStore store;
  static auriga.workspace.persistence.WorkspaceStore workspace;
  auriga.workspace.app.WorkspaceCredentialControl rotation;
  final UUID tenant = UUID.randomUUID(), owner = UUID.randomUUID(), instance = UUID.randomUUID();
  final Clock clock = Clock.systemUTC();
  final ManagedInstanceBindings bindings = mock(ManagedInstanceBindings.class);
  final InstanceAiAuthorization permissions = mock(InstanceAiAuthorization.class);
  IamManagedDevices managed;

  @BeforeAll
  static void migrate() {
    var roles =
        PostgresExtension.serviceRoles(
            CredentialReissueEndToEndTest.class, "managed_owner", "managed_app", "iam");
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
    var wr =
        PostgresExtension.serviceRoles(
            CredentialReissueEndToEndTest.class, "e2e_ws_owner", "e2e_ws_app", "workspace");
    CommonMigrations.migrate(wr.owner().dataSource(), "workspace");
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(wr.owner().dataSource())
                .schemas("workspace")
                .defaultSchema("workspace")
                .locations("classpath:db/workspace")
                .load())
        .migrate();
    workspace =
        new auriga.workspace.persistence.WorkspaceStore(
            new JdbcTemplate(wr.runtime().dataSource()),
            new TenantTransactionManager(wr.runtime().dataSource()));
  }

  @BeforeEach
  void setup() throws Exception {

    var key = new ECKeyGenerator(Curve.P_256).keyID(UUID.randomUUID().toString()).generate();
    var keys = mock(SigningKeys.class);
    when(keys.current()).thenReturn(key);
    when(keys.jwks())
        .thenReturn(Json.object("keys", List.of(Json.read(key.toPublicJWK().toJSONString()))));
    var tokens =
        new TokenService(
            keys,
            new ContractSchemas(),
            (t, s, c) -> Set.of(),
            clock,
            "https://portal.example.com",
            Map.of(),
            List.of("https://managed.example.com"));
    var sessions =
        new SessionManager(
            store,
            mock(SessionKv.class),
            tokens,
            clock,
            mock(EventPublisher.class),
            mock(AuditLogger.class));
    when(permissions.getEffectivePermissions(any()))
        .thenReturn(
            GetEffectivePermissionsResponse.newBuilder()
                .setStatus(MemberStatus.MEMBER_STATUS_ACTIVE)
                .setMemberType(MemberType.MEMBER_TYPE_MEMBER)
                .addAllPermissions(
                    List.of(
                        "auriga.connect.collab.use",
                        "auriga.connect.models.invoke",
                        "iam.accounts.admin"))
                .build());
    byte[] encryptionKey = new byte[32];
    new java.security.SecureRandom().nextBytes(encryptionKey);
    managed =
        new IamManagedDevices(
            store,
            sessions,
            tokens,
            new Secrets(Base64.getEncoder().encodeToString(encryptionKey)),
            bindings,
            permissions,
            clock);
    sessions.managedValidator(managed::validSession);
    TenantContext.run(
        tenant,
        () ->
            store.tx(
                () ->
                    store.insert(
                        "account",
                        owner.toString(),
                        Json.object("accountId", owner, "status", "active"),
                        null)));
  }

  @Test
  void revokedCredentialCannotExchangeAndUpgradedWorkspaceVersionIssuesNewMaterial() {
    prepareWorkspace();
    TenantContext.run(
        tenant,
        () -> {
          var first = managed.issue(request(1));
          var inventory =
              new CredentialInventory(
                  store, managed, clock, mock(AuditLogger.class), List::of, () -> rotation);
          assertEquals(
              2, inventory.revoke(instance.toString(), 1).path("replacementVersion").asLong());
          var error = assertThrows(AurigaException.class, () -> exchange(first));
          assertEquals("network.revoked", error.errorCode().code());
          assertEquals(2, rotation.currentVersion(tenant, instance));
          var next = managed.issue(request(2));
          assertNotEquals(first.getCredential(), next.getCredential());
          assertNotNull(exchange(next).get("access_token"));
        });
  }

  void prepareWorkspace() {
    var inputs = mock(auriga.workspace.api.WorkspaceInputs.class);
    when(inputs.member(any(), any()))
        .thenReturn(
            Member.newBuilder()
                .setAccountId(owner.toString())
                .setTenantId(tenant.toString())
                .setStatus(MemberStatus.MEMBER_STATUS_ACTIVE)
                .build());
    var service =
        new auriga.workspace.app.WorkspaceService(
            workspace,
            inputs,
            new auriga.workspace.app.WorkspaceTemplates(List.of()),
            new auriga.workspace.domain.WorkspaceCrypto(
                auriga.workspace.domain.WorkspaceCrypto.random(32)),
            mock(EventPublisher.class),
            clock);
    rotation = new auriga.workspace.app.WorkspaceCredentialControl(service);
    var realBindings = new auriga.workspace.app.WorkspaceManagedBindings(workspace, inputs);
    when(bindings.get(any())).thenAnswer(c -> realBindings.get(c.getArgument(0)));
    TenantContext.run(
        tenant,
        () -> {
          workspace.tx(
              () ->
                  workspace.jdbc.update(
                      "INSERT INTO instance(id,tenant_id,owner_id,name,spec,generation,token_hash,token_encrypted,last_activity_at,created_at,updated_at) VALUES(?,?,?,'test',?,1,?,?,now(),now(),now())",
                      instance,
                      tenant,
                      owner,
                      auriga.contracts.node.v1.InstanceSpec.newBuilder()
                          .setGeneration(1)
                          .setDesired(auriga.contracts.node.v1.DesiredState.DESIRED_STATE_RUNNING)
                          .build()
                          .toByteArray(),
                      new byte[32],
                      new byte[32]));
        });
  }

  @Test
  void deletedInstanceCredentialRemainsVisibleAndCanBeRevokedWithoutReissue() {
    prepareWorkspace();
    TenantContext.run(
        tenant,
        () -> {
          var first = managed.issue(request(1));
          assertNotNull(exchange(first).get("access_token"));
          workspace.tx(
              () ->
                  workspace.jdbc.update(
                      "UPDATE instance SET deleted=true WHERE tenant_id=? AND id=?",
                      tenant,
                      instance));
          var audit = mock(AuditLogger.class);
          var inventory =
              new CredentialInventory(store, managed, clock, audit, List::of, () -> rotation);
          var row = inventory.list("managed", null, false).path("items").get(0);
          assertTrue(row.path("instanceDeleted").asBoolean());
          assertEquals("active", row.path("status").asString());
          var revoked = inventory.revoke(instance.toString(), 1);
          assertTrue(revoked.path("instanceDeleted").asBoolean());
          assertEquals("revoked", revoked.path("status").asString());
          assertFalse(revoked.has("replacementVersion"));
          assertEquals(
              "network.revoked",
              assertThrows(AurigaException.class, () -> exchange(first)).errorCode().code());
          inventory.revoke(instance.toString(), 1);
          verify(audit, times(1)).record(any());
        });
  }

  IssueManagedDeviceRequest request(long version) {
    return IssueManagedDeviceRequest.newBuilder()
        .setTenantId(tenant.toString())
        .setOwnerId(owner.toString())
        .setInstanceId(instance.toString())
        .setCredentialVersion(version)
        .build();
  }

  tools.jackson.databind.node.ObjectNode exchange(IssueManagedDeviceResponse value) {
    return managed.exchange(
        Map.of(
            "instance_id",
            instance.toString(),
            "owner_id",
            owner.toString(),
            "credential",
            value.getCredential()));
  }
}
