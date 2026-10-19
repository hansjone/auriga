package auriga.org;

import java.sql.Array;
import java.sql.Connection;
import java.sql.PreparedStatement;
import java.sql.ResultSet;
import java.sql.SQLException;
import java.util.ArrayList;
import java.util.List;
import java.util.UUID;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.jdbc.core.RowMapper;

/** PostgreSQL array bind helpers that use the statement's connection (no leak). */
final class OrgArrays {
  static <T> List<T> query(
      JdbcTemplate jdbc, String sql, RowMapper<T> mapper, Object... argsWithArrays) {
    return jdbc.execute(
        (Connection con) -> {
          try (PreparedStatement ps = con.prepareStatement(sql)) {
            bind(con, ps, argsWithArrays);
            try (ResultSet rs = ps.executeQuery()) {
              List<T> out = new ArrayList<>();
              int i = 0;
              while (rs.next()) {
                try {
                  out.add(mapper.mapRow(rs, i++));
                } catch (SQLException e) {
                  throw e;
                } catch (Exception e) {
                  throw new SQLException(e);
                }
              }
              return out;
            }
          }
        });
  }

  static int update(JdbcTemplate jdbc, String sql, Object... argsWithArrays) {
    Integer n =
        jdbc.execute(
            (Connection con) -> {
              try (PreparedStatement ps = con.prepareStatement(sql)) {
                bind(con, ps, argsWithArrays);
                return ps.executeUpdate();
              }
            });
    return n == null ? 0 : n;
  }

  private static void bind(Connection con, PreparedStatement ps, Object[] args)
      throws SQLException {
    for (int i = 0; i < args.length; i++) {
      Object a = args[i];
      if (a instanceof UUID[] u) ps.setArray(i + 1, con.createArrayOf("uuid", u));
      else if (a instanceof String[] s) ps.setArray(i + 1, con.createArrayOf("text", s));
      else if (a instanceof List<?> list) {
        if (list.isEmpty()) ps.setArray(i + 1, con.createArrayOf("text", new String[0]));
        else if (list.getFirst() instanceof UUID)
          ps.setArray(i + 1, con.createArrayOf("uuid", list.toArray()));
        else ps.setArray(i + 1, con.createArrayOf("text", list.toArray()));
      } else if (a instanceof Array arr) ps.setArray(i + 1, arr);
      else ps.setObject(i + 1, a);
    }
  }

  private OrgArrays() {}
}
