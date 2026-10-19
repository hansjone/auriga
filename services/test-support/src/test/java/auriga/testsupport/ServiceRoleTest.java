package auriga.testsupport;

import static org.junit.jupiter.api.Assertions.*;

import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;

@ExtendWith(PostgresExtension.class)
class ServiceRoleTest {
  @Test
  void serviceRoleIsUnprivilegedAndOwnsItsSchema() throws Exception {
    var role = PostgresExtension.serviceRole(getClass(), "svc_test", "svc");
    // idempotent
    PostgresExtension.serviceRole(getClass(), "svc_test", "svc");
    try (var connection = role.dataSource().getConnection();
        var statement = connection.createStatement();
        var rows =
            statement.executeQuery(
                "SELECT current_user, rolsuper, rolbypassrls, current_schema()"
                    + " FROM pg_roles WHERE rolname = current_user")) {
      assertTrue(rows.next());
      assertEquals("svc_test", rows.getString(1));
      assertFalse(rows.getBoolean(2));
      assertFalse(rows.getBoolean(3));
      assertEquals("svc", rows.getString(4));
    }
  }

  @Test
  void runtimeRoleOnlyReadsAndWritesRows() throws Exception {
    var roles = PostgresExtension.serviceRoles(getClass(), "own_test", "run_test", "split");
    try (var owner = roles.owner().dataSource().getConnection();
        var statement = owner.createStatement()) {
      statement.execute("CREATE TABLE split.item (id int PRIMARY KEY, name text)");
    }
    try (var runtime = roles.runtime().dataSource().getConnection();
        var statement = runtime.createStatement()) {
      statement.execute("INSERT INTO item VALUES (1, 'a')");
      statement.execute("UPDATE item SET name = 'b' WHERE id = 1");
      try (var rows = statement.executeQuery("SELECT name FROM item")) {
        assertTrue(rows.next());
        assertEquals("b", rows.getString(1));
      }
      statement.execute("DELETE FROM item");
      assertThrows(
          java.sql.SQLException.class, () -> statement.execute("CREATE TABLE split.x (id int)"));
      assertThrows(java.sql.SQLException.class, () -> statement.execute("DROP TABLE item"));
      assertThrows(
          java.sql.SQLException.class,
          () -> statement.execute("ALTER TABLE item DISABLE ROW LEVEL SECURITY"));
    }
  }
}
