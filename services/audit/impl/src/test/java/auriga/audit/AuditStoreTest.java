package auriga.audit;

import static org.assertj.core.api.Assertions.*;

import auriga.common.context.*;
import auriga.common.events.*;
import auriga.common.security.*;
import auriga.testsupport.PostgresExtension;
import java.time.*;
import java.util.*;
import javax.sql.DataSource;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import tools.jackson.databind.json.JsonMapper;

@ExtendWith(PostgresExtension.class)
class AuditStoreTest {
  @Test
  void filtersPagesRedactsAndAudits(DataSource ds) {
    ds = PostgresExtension.serviceRole(AuditStoreTest.class, "audit_owner", "audit").dataSource();
    var store = new AuditStore(ds);
    var mapper = JsonMapper.builder().build();
    var tenant = UUID.randomUUID();
    var now = Instant.parse("2026-11-01T00:00:00Z");
    for (int i = 0; i < 3; i++)
      store.accept(
          new ReceivedEvent(
              UUID.randomUUID(),
              "audit.recorded",
              "auriga/org",
              null,
              now.plusSeconds(i),
              tenant,
              "1",
              null,
              mapper.readTree(
                  "{\"actor\":{\"type\":\"user\",\"id\":\"alice\"},\"action\":\"org.member.update\",\"result\":\"success\",\"details\":{\"password\":\"hidden\",\"nested\":{\"token\":\"hidden\"}}}")));
    assertThat(
            new org.springframework.jdbc.core.JdbcTemplate(ds)
                .queryForObject("SELECT count(*) FROM audit.event", Long.class))
        .isZero();
    var principal =
        new AurigaPrincipal(
            "filter_admin",
            tenant,
            PrincipalType.USER,
            TokenContext.ADMIN,
            null,
            null,
            null,
            List.of(),
            Set.of("audit.events.read"),
            now,
            null,
            "jti");
    RequestContext.start("req", null)
        .withPrincipal(principal)
        .run(
            () -> {
              var page =
                  store.query(
                      null, null, "alice", "org.member.update", "success", "", null, 2, false);
              assertThat(page.items()).hasSize(2);
              assertThat(page.nextCursor()).isNotNull();
              assertThat(page.items().toString()).doesNotContain("hidden");
              assertThat(
                      store
                          .query(
                              null,
                              null,
                              "alice",
                              "org.member.update",
                              "success",
                              "",
                              page.nextCursor(),
                              2,
                              false)
                          .items())
                  .hasSize(1);
              assertThat(
                      store
                          .query(
                              null,
                              null,
                              "filter_admin",
                              "audit.events.query",
                              "",
                              "",
                              null,
                              10,
                              false)
                          .items())
                  .hasSize(2);
              assertThat(
                      store
                          .query(
                              now.plusSeconds(1),
                              now.plusSeconds(2),
                              "alice",
                              "",
                              "success",
                              "member",
                              null,
                              10,
                              false)
                          .items())
                  .hasSize(1);
              assertThatThrownBy(
                      () -> store.query(now, now.minusSeconds(1), "", "", "", "", null, 1, false))
                  .isInstanceOf(IllegalArgumentException.class);
            });
  }

  @Test
  void deduplicatesAndRejectsOversizedExport(DataSource ds) {
    var source =
        PostgresExtension.serviceRole(AuditStoreTest.class, "audit_owner", "audit").dataSource();
    var store = new AuditStore(source);
    var tenant = UUID.randomUUID();
    var mapper = JsonMapper.builder().build();
    var data =
        mapper.readTree(
            "{\"actor\":{\"type\":\"user\",\"id\":\"export_actor\"},\"action\":\"org.member.update\",\"result\":\"success\"}");
    var event =
        new ReceivedEvent(
            UUID.randomUUID(),
            "audit.recorded",
            "auriga/org",
            null,
            Instant.now(),
            tenant,
            "1",
            null,
            data);
    store.accept(event);
    store.accept(event);
    var p =
        new AurigaPrincipal(
            "admin",
            tenant,
            PrincipalType.USER,
            TokenContext.ADMIN,
            null,
            null,
            null,
            List.of(),
            Set.of("audit.events.read"),
            Instant.now(),
            null,
            "jti");
    RequestContext.start("export", null)
        .withPrincipal(p)
        .run(
            () -> {
              assertThat(
                      store.query(null, null, "export_actor", "", "", "", null, 50, false).items())
                  .hasSize(1);
              new org.springframework.transaction.support.TransactionTemplate(
                      new org.springframework.jdbc.support.JdbcTransactionManager(source))
                  .executeWithoutResult(
                      status -> {
                        for (int i = 0; i < 1000; i++)
                          store.accept(
                              new ReceivedEvent(
                                  UUID.randomUUID(),
                                  "audit.recorded",
                                  "auriga/org",
                                  null,
                                  Instant.now(),
                                  tenant,
                                  "1",
                                  null,
                                  data));
                      });
              assertThatThrownBy(
                      () -> store.query(null, null, "export_actor", "", "", "", null, 1000, true))
                  .isInstanceOf(IllegalArgumentException.class);
              assertThat(
                      store.query(null, null, "export_actor", "", "", "", null, 1, false).items())
                  .hasSize(1);
              assertThat(store.detail(event.id())).isNotNull();
            });
  }

  @Test
  void permissionIsExactAndAdminOnly() {
    var factory = new org.springframework.aop.framework.ProxyFactory(new AuditController(null));
    factory.setProxyTargetClass(true);
    factory.addAdvisor(new AuthorizationAdvisor(Clock.systemUTC()));
    var controller = (AuditController) factory.getProxy();
    assertThatThrownBy(() -> controller.list(null, null, "", "", "", "", null, 50))
        .isInstanceOf(auriga.common.error.AurigaException.class);
    for (var context : List.of(TokenContext.ADMIN, TokenContext.PORTAL)) {
      var permissions =
          context == TokenContext.ADMIN ? Set.of("audit.*") : Set.of("audit.events.read");
      var principal =
          new AurigaPrincipal(
              "admin",
              UUID.randomUUID(),
              PrincipalType.USER,
              context,
              null,
              null,
              null,
              List.of(),
              permissions,
              Instant.now(),
              null,
              "jti");
      RequestContext.start("denied", null)
          .withPrincipal(principal)
          .run(
              () ->
                  assertThatThrownBy(() -> controller.list(null, null, "", "", "", "", null, 50))
                      .isInstanceOf(auriga.common.error.AurigaException.class));
    }
  }

  @Test
  void csvNeutralizesFormulaAndQuotes() {
    assertThat(AuditController.cell("=SUM(1,2)")).contains("'=SUM");
    assertThat(AuditController.cell(" =1\n+2")).startsWith("\"'");
  }
}
