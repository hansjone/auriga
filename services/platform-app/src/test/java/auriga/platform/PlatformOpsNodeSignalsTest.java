package auriga.platform;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.persistence.*;
import auriga.testsupport.PostgresExtension;
import auriga.workspace.app.WorkspaceService;
import auriga.workspace.persistence.WorkspaceStore;
import java.time.*;
import java.util.*;
import org.flywaydb.core.Flyway;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.beans.factory.support.DefaultListableBeanFactory;
import org.springframework.jdbc.core.JdbcTemplate;

@ExtendWith(PostgresExtension.class)
class PlatformOpsNodeSignalsTest {
  @Test
  void heartbeatExpiryRecoveryAndRevocationUseWorkspaceBoundary() {
    var roles =
        PostgresExtension.serviceRoles(
            PlatformOpsNodeSignalsTest.class, "signal_ws_owner", "signal_ws_app", "workspace");
    CommonMigrations.migrate(roles.owner().dataSource(), "workspace");
    CommonMigrations.serviceFlyway(
            Flyway.configure()
                .dataSource(roles.owner().dataSource())
                .schemas("workspace")
                .locations("classpath:db/workspace")
                .load())
        .migrate();
    var store =
        new WorkspaceStore(
            new JdbcTemplate(roles.runtime().dataSource()),
            new TenantTransactionManager(roles.runtime().dataSource()));
    UUID tenant = UUID.randomUUID(), node = UUID.randomUUID();
    store.register(tenant, "tenant", tenant);
    store.in(
        tenant,
        () ->
            store.jdbc.update(
                "INSERT INTO node(id,tenant_id,name,hostname,os,arch,agent_version,sealing_key,certificate_serial,certificate_expires_at,joined_at) VALUES(?,?,'node','host','linux','amd64','test',?,'serial',now()+interval '1 day',now())",
                node,
                tenant,
                new byte[32]));
    var service =
        new WorkspaceService(
            store,
            null,
            null,
            null,
            (type, subject, data, version) -> UUID.randomUUID(),
            Clock.systemUTC());
    var beans = new DefaultListableBeanFactory();
    beans.registerSingleton("workspace", service);
    var observations = new ArrayList<Boolean>();
    var poller =
        new PlatformOpsNodeSignals(
            beans.getBeanProvider(WorkspaceService.class),
            (t, source, subject, severity, active) -> {
              assertEquals(tenant, t);
              assertEquals("node_offline", source);
              assertEquals(node.toString(), subject);
              assertEquals("critical", severity);
              assertFalse(
                  org.springframework.transaction.support.TransactionSynchronizationManager
                      .isActualTransactionActive());
              observations.add(active);
            });
    poller.poll();
    store.in(
        tenant, () -> store.jdbc.update("UPDATE node SET last_seen_at=now() WHERE id=?", node));
    poller.poll();
    store.in(
        tenant,
        () ->
            store.jdbc.update(
                "UPDATE node SET last_seen_at=now()-interval '61 seconds' WHERE id=?", node));
    poller.poll();
    store.in(tenant, () -> store.jdbc.update("UPDATE node SET revoked=true WHERE id=?", node));
    poller.poll();
    assertEquals(List.of(true, false, true, false), observations);
  }
}
