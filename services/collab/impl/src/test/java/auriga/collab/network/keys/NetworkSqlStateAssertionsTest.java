package auriga.collab.network.keys;

import static org.junit.jupiter.api.Assertions.*;

import java.sql.SQLException;
import org.junit.jupiter.api.Test;
import org.springframework.dao.DataAccessResourceFailureException;
import org.springframework.jdbc.UncategorizedSQLException;

class NetworkSqlStateAssertionsTest {
  @Test
  void currentAbortDoesNotBecomeThePreviousLockFailure() {
    var previous = new SQLException("previous publication", "55P03");
    var current = new SQLException("current SELECT 1", "25P02", previous);
    current.setNextException(previous);
    var failure = new UncategorizedSQLException("statement", "SELECT 1", current);
    assertSame(previous, failure.getMostSpecificCause());
    assertSame(current, NetworkKeyOperationsPostgresTest.currentSqlException(failure));
    assertEquals(
        "25P02", NetworkKeyOperationsPostgresTest.currentSqlException(failure).getSQLState());
  }

  @Test
  void currentLockFailureDoesNotBecomeAnOlderAbort() {
    var previous = new SQLException("previous transaction", "25P02");
    var current = new SQLException("current publication", "55P03", previous);
    var failure = new UncategorizedSQLException("publication", "INSERT", current);
    assertSame(current, NetworkKeyOperationsPostgresTest.currentSqlException(failure));
    assertEquals(
        "55P03", NetworkKeyOperationsPostgresTest.currentSqlException(failure).getSQLState());
  }

  @Test
  void missingCurrentStateDoesNotBorrowAnEarlierState() {
    var current =
        new SQLException(
            "current operation", (String) null, new SQLException("previous publication", "55P03"));
    var failure = new UncategorizedSQLException("statement", "SELECT 1", current);
    assertSame(current, NetworkKeyOperationsPostgresTest.currentSqlException(failure));
    assertNull(NetworkKeyOperationsPostgresTest.currentSqlException(failure).getSQLState());
  }

  @Test
  void ambiguousNonJdbcWrapperCannotBorrowAnEarlierSqlException() {
    var failure =
        new DataAccessResourceFailureException(
            "current connection failure",
            new IllegalStateException(
                "non-JDBC operation", new SQLException("previous publication", "55P03")));
    assertThrows(
        org.opentest4j.AssertionFailedError.class,
        () -> NetworkKeyOperationsPostgresTest.currentSqlException(failure));
  }
}
