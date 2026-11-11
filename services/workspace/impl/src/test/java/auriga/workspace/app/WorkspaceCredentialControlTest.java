package auriga.workspace.app;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.events.EventPublisher;
import auriga.common.persistence.*;
import auriga.contracts.node.v1.*;
import auriga.testsupport.PostgresExtension;
import auriga.workspace.api.WorkspaceInputs;
import auriga.workspace.domain.WorkspaceCrypto;
import auriga.workspace.persistence.WorkspaceStore;
import java.time.*;
import java.util.*;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;

@ExtendWith(PostgresExtension.class)
class WorkspaceCredentialControlTest {
  static WorkspaceStore store;
  UUID tenant = UUID.randomUUID(), other = UUID.randomUUID(), instance = UUID.randomUUID();
  WorkspaceCredentialControl control;

  @BeforeAll
  static void database() {
    var roles =
        PostgresExtension.serviceRoles(
            WorkspaceCredentialControlTest.class, "cred_ws_owner", "cred_ws_app", "workspace");
    CommonMigrations.migrate(roles.owner().dataSource(), "workspace");
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas("workspace")
                .defaultSchema("workspace")
                .locations("classpath:db/workspace")
                .load())
        .migrate();
    store =
        new WorkspaceStore(
            new JdbcTemplate(roles.runtime().dataSource()),
            new TenantTransactionManager(roles.runtime().dataSource()));
  }

  @BeforeEach
  void seed() {
    var service =
        new WorkspaceService(
            store,
            mock(WorkspaceInputs.class),
            new WorkspaceTemplates(List.of()),
            new WorkspaceCrypto(WorkspaceCrypto.random(32)),
            mock(EventPublisher.class),
            Clock.systemUTC());
    control = new WorkspaceCredentialControl(service);
    TenantContext.run(
        tenant,
        () ->
            store.tx(
                () ->
                    store.jdbc.update(
                        "INSERT INTO instance(id,tenant_id,owner_id,name,spec,generation,token_hash,token_encrypted,last_activity_at,created_at,updated_at,managed_credential,managed_model_credential) VALUES(?,?,?,'test',?,1,?,?,now(),now(),now(),?,?)",
                        instance,
                        tenant,
                        UUID.randomUUID(),
                        InstanceSpec.newBuilder()
                            .setGeneration(1)
                            .setDesired(DesiredState.DESIRED_STATE_STOPPED)
                            .build()
                            .toByteArray(),
                        new byte[32],
                        new byte[32],
                        new byte[32],
                        new byte[32])));
  }

  @Test
  void rotatesDurablyClearsOldMaterialAndDoesNotAdvanceAgainOnRetry() {
    TenantContext.run(
        tenant,
        () -> {
          assertEquals(2, control.rotate(tenant, instance, 1));
          assertEquals(2, control.rotate(tenant, instance, 1));
          assertEquals(2, control.currentVersion(tenant, instance));
          var row = store.tx(() -> store.instance(instance));
          assertNull(row.get("managed_credential"));
          assertNull(row.get("managed_model_credential"));
          assertEquals("pending", row.get("managed_network_state"));
          assertTrue((boolean) row.get("managed_restart_required"));
          assertEquals(2, ((Number) row.get("generation")).longValue());
        });
  }

  @Test
  void rejectsForeignTenantForeignInstanceAndFutureVersion() {
    assertEquals(
        "common.forbidden",
        assertThrows(
                AurigaException.class,
                () -> TenantContext.call(other, () -> control.rotate(tenant, instance, 1)))
            .errorCode()
            .code());
    assertEquals(
        "common.not_found",
        assertThrows(
                AurigaException.class,
                () -> TenantContext.call(other, () -> control.rotate(other, instance, 1)))
            .errorCode()
            .code());
    assertEquals(
        "common.conflict",
        assertThrows(
                AurigaException.class,
                () -> TenantContext.call(tenant, () -> control.rotate(tenant, instance, 9)))
            .errorCode()
            .code());
  }

  @Test
  void nodeMetadataNeverIncludesSealingMaterialAndIsTenantScoped() {
    TenantContext.run(
        tenant,
        () ->
            store.tx(
                () ->
                    store.jdbc.update(
                        "INSERT INTO node(id,tenant_id,name,hostname,os,arch,agent_version,sealing_key,certificate_serial,certificate_expires_at,joined_at) VALUES(?,?,'test','example.com','linux','amd64','test',?,'serial-2',now()+interval '1 day',now())",
                        UUID.randomUUID(),
                        tenant,
                        new byte[32])));
    var own = TenantContext.call(tenant, control::snapshot);
    assertEquals(1, own.entries().size());
    assertEquals("serial-2", own.entries().getFirst().versionLabel());
    assertNull(own.entries().getFirst().issuedAt());
    assertFalse(own.toString().contains("sealing"));
    assertEquals(0, TenantContext.call(other, control::snapshot).entries().size());
  }

  @Test
  void deletedAndForeignInstancesAreAbsentFromBatchWithoutLocks() throws Exception {
    // A separate transaction retains an update lock: a metadata read must not wait for it.
    var ready = new java.util.concurrent.CountDownLatch(1);
    var release = new java.util.concurrent.CountDownLatch(1);
    try (var executor = java.util.concurrent.Executors.newSingleThreadExecutor()) {
      var writer =
          executor.submit(
              () ->
                  TenantContext.run(
                      tenant,
                      () ->
                          store.tx(
                              () -> {
                                store.instance(instance);
                                ready.countDown();
                                try {
                                  if (!release.await(10, java.util.concurrent.TimeUnit.SECONDS))
                                    throw new IllegalStateException();
                                } catch (InterruptedException e) {
                                  Thread.currentThread().interrupt();
                                  throw new IllegalStateException(e);
                                }
                                return null;
                              })));
      try {
        assertTrue(ready.await(5, java.util.concurrent.TimeUnit.SECONDS));
        assertTimeoutPreemptively(
            Duration.ofSeconds(3),
            () ->
                assertEquals(
                    Map.of(instance, 1L),
                    TenantContext.call(
                        tenant,
                        () ->
                            control.currentVersions(
                                tenant, List.of(instance, UUID.randomUUID())))));
      } finally {
        release.countDown();
        writer.get(5, java.util.concurrent.TimeUnit.SECONDS);
      }
    }
    TenantContext.run(
        tenant,
        () ->
            store.tx(
                () ->
                    store.jdbc.update(
                        "UPDATE instance SET deleted=true WHERE tenant_id=? AND id=?",
                        tenant,
                        instance)));
    assertTrue(
        TenantContext.call(tenant, () -> control.currentVersions(tenant, List.of(instance)))
            .isEmpty());
    assertTrue(
        TenantContext.call(other, () -> control.currentVersions(other, List.of(instance)))
            .isEmpty());
  }
}
