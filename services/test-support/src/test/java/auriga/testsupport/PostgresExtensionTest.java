package auriga.testsupport;

import static org.junit.jupiter.api.Assertions.*;

import javax.sql.DataSource;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;

@ExtendWith(PostgresExtension.class)
class PostgresExtensionTest {
  private static final class OtherTestClass {}

  @Test
  void usesPostgres17AndReusesTheClassDatabase(DataSource dataSource) throws Exception {
    try (var connection = dataSource.getConnection();
        var statement = connection.createStatement();
        var rows = statement.executeQuery("SHOW server_version_num")) {
      assertTrue(rows.next());
      assertEquals(17, rows.getInt(1) / 10000);
    }
    assertSame(PostgresExtension.database(getClass()), PostgresExtension.database(getClass()));
  }

  @Test
  void isolatesClassesOnTheSameServer() throws Exception {
    var first = PostgresExtension.database(getClass());
    var second = PostgresExtension.database(OtherTestClass.class);
    assertNotEquals(first.name(), second.name());
    try (var a = first.dataSource().getConnection();
        var b = second.dataSource().getConnection();
        var create = a.createStatement();
        var query = b.createStatement()) {
      create.execute("CREATE TABLE isolation_probe (id integer)");
      try (var rows = query.executeQuery("SELECT to_regclass('public.isolation_probe')")) {
        assertTrue(rows.next());
        assertNull(rows.getString(1));
      }
      assertEquals(
          a.getMetaData().getURL().replace(first.name(), ""),
          b.getMetaData().getURL().replace(second.name(), ""));
    }
  }
}
