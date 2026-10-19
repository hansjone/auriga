package io.github.hansjone.auriga.common.persistence;

import static org.junit.jupiter.api.Assertions.*;

import io.github.hansjone.auriga.common.context.TenantContext;
import io.github.hansjone.auriga.testsupport.PostgresExtension;
import java.sql.Connection;
import java.sql.SQLException;
import java.util.UUID;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.transaction.support.TransactionTemplate;

/**
 * Proves the database-level guard: connected as the (non-superuser) service role with plain JDBC,
 * i.e. bypassing MyBatis-Plus entirely, a transaction only ever sees and writes its own tenant.
 */
@ExtendWith(PostgresExtension.class)
class RowLevelSecurityTest {
  private static final UUID A = UUID.randomUUID();
  private static final UUID B = UUID.randomUUID();
  private static PostgresExtension.ServiceRole role;

  @BeforeAll
  static void schema() throws Exception {
    role = PostgresExtension.serviceRole(RowLevelSecurityTest.class, "rls_app", "rls");
    CommonMigrations.migrate(role.dataSource(), "rls");
    // idempotent
    CommonMigrations.migrate(role.dataSource(), "rls");
    try (Connection c = role.dataSource().getConnection();
        var s = c.createStatement()) {
      s.execute(
          "CREATE TABLE rls.note (id uuid PRIMARY KEY, tenant_id uuid NOT NULL, body text NOT NULL)");
      s.execute(CommonMigrations.enableTenantRls("rls", "note"));
    }
    insert(A, "a1");
    insert(A, "a2");
    insert(B, "b1");
  }

  private static void insert(UUID tenant, String body) throws SQLException {
    try (Connection c = role.dataSource().getConnection()) {
      c.setAutoCommit(false);
      TenantTransactionManager.applyTenant(c, tenant.toString());
      try (var ps = c.prepareStatement("INSERT INTO rls.note VALUES (?, ?, ?)")) {
        ps.setObject(1, UUID.randomUUID());
        ps.setObject(2, tenant);
        ps.setString(3, body);
        ps.executeUpdate();
      }
      c.commit();
    }
  }

  private static long count(Connection c, String sql) throws SQLException {
    try (var s = c.createStatement();
        var rs = s.executeQuery(sql)) {
      rs.next();
      return rs.getLong(1);
    }
  }

  @Test
  void withoutTenantNothingIsVisible() throws Exception {
    try (Connection c = role.dataSource().getConnection()) {
      assertEquals(0, count(c, "SELECT count(*) FROM rls.note"));
      c.setAutoCommit(false);
      TenantTransactionManager.applyTenant(c, "");
      assertEquals(0, count(c, "SELECT count(*) FROM rls.note"));
      c.rollback();
    }
  }

  @Test
  void rawJdbcSeesOnlyItsTenantEvenWhenAskingForAnother() throws Exception {
    try (Connection c = role.dataSource().getConnection()) {
      c.setAutoCommit(false);
      TenantTransactionManager.applyTenant(c, A.toString());
      assertEquals(2, count(c, "SELECT count(*) FROM rls.note"));
      assertEquals(0, count(c, "SELECT count(*) FROM rls.note WHERE tenant_id = '" + B + "'"));
      assertEquals(0, count(c, "SELECT count(*) FROM rls.note WHERE body = 'b1'"));
      c.rollback();
    }
  }

  @Test
  void cannotWriteOrMoveRowsAcrossTenants() throws Exception {
    try (Connection c = role.dataSource().getConnection()) {
      c.setAutoCommit(false);
      TenantTransactionManager.applyTenant(c, A.toString());
      try (var s = c.createStatement()) {
        assertEquals(
            0, s.executeUpdate("UPDATE rls.note SET body = 'x' WHERE tenant_id = '" + B + "'"));
        assertEquals(0, s.executeUpdate("DELETE FROM rls.note WHERE body = 'b1'"));
      }
      SQLException insert =
          assertThrows(
              SQLException.class,
              () -> {
                try (var ps = c.prepareStatement("INSERT INTO rls.note VALUES (?, ?, 'evil')")) {
                  ps.setObject(1, UUID.randomUUID());
                  ps.setObject(2, B);
                  ps.executeUpdate();
                }
              });
      assertEquals("42501", insert.getSQLState());
      c.rollback();
      TenantTransactionManager.applyTenant(c, A.toString());
      SQLException move =
          assertThrows(
              SQLException.class,
              () -> {
                try (var s = c.createStatement()) {
                  s.executeUpdate("UPDATE rls.note SET tenant_id = '" + B + "' WHERE body = 'a1'");
                }
              });
      assertEquals("42501", move.getSQLState());
      c.rollback();
    }
  }

  @Test
  void tenantSettingEndsWithTheTransaction() throws Exception {
    try (Connection c = role.dataSource().getConnection()) {
      c.setAutoCommit(false);
      TenantTransactionManager.applyTenant(c, A.toString());
      assertEquals(2, count(c, "SELECT count(*) FROM rls.note"));
      c.commit();
      // same pooled connection, next transaction: no tenant leaks over
      assertEquals(0, count(c, "SELECT count(*) FROM rls.note"));
      c.rollback();
    }
  }

  @Test
  void invalidTenantValueFailsClosed() throws Exception {
    try (Connection c = role.dataSource().getConnection()) {
      c.setAutoCommit(false);
      TenantTransactionManager.applyTenant(c, "' OR true --");
      assertThrows(SQLException.class, () -> count(c, "SELECT count(*) FROM rls.note"));
      c.rollback();
    }
  }

  @Test
  void transactionManagerStampsTheBoundTenant() {
    TenantTransactionManager manager = new TenantTransactionManager(role.dataSource());
    TransactionTemplate tx = new TransactionTemplate(manager);
    JdbcTemplate jdbc = new JdbcTemplate(role.dataSource());
    String sql = "SELECT count(*) FROM rls.note";
    assertEquals(
        2L,
        TenantContext.<Long, RuntimeException>call(
            A, () -> tx.execute(s -> jdbc.queryForObject(sql, Long.class))));
    assertEquals(
        1L,
        TenantContext.<Long, RuntimeException>call(
            B, () -> tx.execute(s -> jdbc.queryForObject(sql, Long.class))));
    Long unbound = tx.execute(s -> jdbc.queryForObject(sql, Long.class));
    assertEquals(0L, unbound);
    assertEquals(
        0L,
        TenantContext.<Long, RuntimeException>callAsSystem(
            () -> tx.execute(s -> jdbc.queryForObject(sql, Long.class))));
  }

  @Test
  void superuserBypassesRlsSoServicesMustNotUseIt() throws Exception {
    try (Connection c = PostgresExtension.database(getClass()).dataSource().getConnection()) {
      assertEquals(3, count(c, "SELECT count(*) FROM rls.note"));
    }
  }
}
