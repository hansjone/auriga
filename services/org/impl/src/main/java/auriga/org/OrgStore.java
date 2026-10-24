package auriga.org;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.persistence.TenantTransactionManager;
import java.sql.Array;
import java.sql.ResultSet;
import java.sql.SQLException;
import java.sql.Timestamp;
import java.time.Instant;
import java.util.Arrays;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.UUID;
import java.util.function.Supplier;
import org.springframework.dao.PessimisticLockingFailureException;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.jdbc.core.RowMapper;
import org.springframework.transaction.support.TransactionTemplate;
import tools.jackson.databind.node.ObjectNode;

/** Parameterized SQL with explicit tenant predicates; FORCE RLS is the backstop. */
public final class OrgStore {
  private final JdbcTemplate jdbc;
  private final TransactionTemplate transactions;

  public OrgStore(JdbcTemplate jdbc, TenantTransactionManager manager) {
    this.jdbc = jdbc;
    transactions = new TransactionTemplate(manager);
  }

  public JdbcTemplate jdbc() {
    return jdbc;
  }

  public <T> T tx(Supplier<T> work) {
    TenantContext.require();
    TenantTransactionManager.verifyTransactionTenant();
    return transactions.execute(s -> work.get());
  }

  public <T> T tenant(UUID tenant, Supplier<T> work) {
    return TenantContext.call(tenant, () -> tx(work));
  }

  public UUID tenantId() {
    return TenantContext.require();
  }

  public static UUID[] uuidArray(ResultSet rs, String column) throws SQLException {
    return asUuidArray(rs.getArray(column));
  }

  /** Unwrap JDBC {@link Array} / {@code PgArray} from {@code queryForList} maps. */
  public static UUID[] asUuidArray(Object value) throws SQLException {
    if (value == null) return new UUID[0];
    if (value instanceof UUID[] u) return u;
    Array array = value instanceof Array a ? a : null;
    if (array == null) return new UUID[0];
    Object[] raw = (Object[]) array.getArray();
    return Arrays.stream(raw).map(o -> (UUID) o).toArray(UUID[]::new);
  }

  public static String[] stringArray(ResultSet rs, String column) throws SQLException {
    Array array = rs.getArray(column);
    if (array == null) return new String[0];
    return (String[]) array.getArray();
  }

  public Array uuids(UUID... ids) throws SQLException {
    return jdbc.getDataSource()
        .getConnection()
        .createArrayOf("uuid", ids == null ? new UUID[0] : ids);
  }

  public Array texts(String... values) throws SQLException {
    return jdbc.getDataSource()
        .getConnection()
        .createArrayOf("text", values == null ? new String[0] : values);
  }

  /** Creates a PG uuid[] without leaking a connection — uses the transactional connection. */
  public Object uuidArrayParam(List<UUID> ids) {
    return ids == null ? new UUID[0] : ids.toArray(UUID[]::new);
  }

  public Object textArrayParam(List<String> values) {
    return values == null ? new String[0] : values.toArray(String[]::new);
  }

  public Optional<TenantRow> tenantById(UUID id) {
    return jdbc
        .query(
            "SELECT id, slug, display_name, locale, hosts, created_at FROM tenant WHERE tenant_id=? AND id=?",
            tenantMapper(),
            tenantId(),
            id)
        .stream()
        .findFirst();
  }

  public Optional<TenantRow> resolveByHost(String host) {
    return jdbc
        .query(
            "SELECT id, slug, display_name, locale, hosts, created_at FROM resolve_tenant_by_host(?)",
            tenantMapper(),
            host)
        .stream()
        .findFirst();
  }

  public Optional<TenantRow> defaultTenant() {
    return jdbc
        .query(
            "SELECT id, slug, display_name, locale, hosts, created_at FROM default_tenant()",
            tenantMapper())
        .stream()
        .findFirst();
  }

  public void updateTenant(UUID id, String displayName, String locale, List<String> hosts) {
    if (hosts == null) {
      jdbc.update(
          "UPDATE tenant SET display_name=COALESCE(?, display_name), locale=COALESCE(?, locale)"
              + " WHERE tenant_id=? AND id=?",
          displayName,
          locale,
          tenantId(),
          id);
    } else {
      OrgArrays.update(
          jdbc,
          "UPDATE tenant SET display_name=COALESCE(?, display_name), locale=COALESCE(?, locale),"
              + " hosts=? WHERE tenant_id=? AND id=?",
          displayName,
          locale,
          hosts.toArray(String[]::new),
          tenantId(),
          id);
    }
  }

  private static RowMapper<TenantRow> tenantMapper() {
    return (rs, i) ->
        new TenantRow(
            (UUID) rs.getObject("id"),
            rs.getString("slug"),
            rs.getString("display_name"),
            rs.getString("locale"),
            List.of(stringArray(rs, "hosts")),
            rs.getTimestamp("created_at").toInstant());
  }

  public record TenantRow(
      UUID id,
      String slug,
      String displayName,
      String locale,
      List<String> hosts,
      Instant createdAt) {}

  public record MemberRow(
      UUID id,
      String displayName,
      String email,
      String memberType,
      String status,
      long version,
      Instant createdAt,
      Instant disabledAt,
      Instant offboardedAt) {}

  public Optional<MemberRow> member(UUID id) {
    return jdbc
        .query(
            "SELECT id, display_name, email, member_type, status, version, created_at, disabled_at, offboarded_at"
                + " FROM member WHERE tenant_id=? AND id=?",
            memberMapper(),
            tenantId(),
            id)
        .stream()
        .findFirst();
  }

  public void insertMember(MemberRow m) {
    jdbc.update(
        "INSERT INTO member (id, tenant_id, display_name, email, member_type, status, version, created_at,"
            + " disabled_at, offboarded_at) VALUES (?,?,?,?,?,?,?,?,?,?)",
        m.id(),
        tenantId(),
        m.displayName(),
        m.email(),
        m.memberType(),
        m.status(),
        m.version(),
        Timestamp.from(m.createdAt()),
        m.disabledAt() == null ? null : Timestamp.from(m.disabledAt()),
        m.offboardedAt() == null ? null : Timestamp.from(m.offboardedAt()));
  }

  public void saveMember(MemberRow m) {
    jdbc.update(
        "UPDATE member SET display_name=?, email=?, member_type=?, status=?, version=?, disabled_at=?,"
            + " offboarded_at=? WHERE tenant_id=? AND id=?",
        m.displayName(),
        m.email(),
        m.memberType(),
        m.status(),
        m.version(),
        m.disabledAt() == null ? null : Timestamp.from(m.disabledAt()),
        m.offboardedAt() == null ? null : Timestamp.from(m.offboardedAt()),
        tenantId(),
        m.id());
  }

  private static RowMapper<MemberRow> memberMapper() {
    return (rs, i) ->
        new MemberRow(
            (UUID) rs.getObject("id"),
            rs.getString("display_name"),
            rs.getString("email"),
            rs.getString("member_type"),
            rs.getString("status"),
            rs.getLong("version"),
            rs.getTimestamp("created_at").toInstant(),
            optionalInstant(rs, "disabled_at"),
            optionalInstant(rs, "offboarded_at"));
  }

  private static Instant optionalInstant(ResultSet rs, String col) throws SQLException {
    Timestamp t = rs.getTimestamp(col);
    return t == null ? null : t.toInstant();
  }

  public List<UUID> groupIdsOf(UUID accountId) {
    return jdbc.query(
        "SELECT group_id FROM group_member WHERE tenant_id=? AND account_id=?",
        (rs, i) -> (UUID) rs.getObject(1),
        tenantId(),
        accountId);
  }

  public List<String> directRoleKeys(UUID accountId) {
    return jdbc.query(
        "SELECT r.key FROM role_binding b JOIN role r ON r.id=b.role_id AND r.tenant_id=b.tenant_id"
            + " WHERE b.tenant_id=? AND b.subject_type='member' AND b.subject_id=?",
        (rs, i) -> rs.getString(1),
        tenantId(),
        accountId);
  }

  public List<String> groupRoleKeys(UUID accountId) {
    return jdbc.query(
        "SELECT DISTINCT r.key FROM group_member gm"
            + " JOIN role_binding b ON b.tenant_id=gm.tenant_id AND b.subject_type='group' AND b.subject_id=gm.group_id"
            + " JOIN role r ON r.id=b.role_id AND r.tenant_id=b.tenant_id"
            + " WHERE gm.tenant_id=? AND gm.account_id=?",
        (rs, i) -> rs.getString(1),
        tenantId(),
        accountId);
  }

  public List<String> permissionsForRoles(List<String> roleKeys) {
    if (roleKeys == null || roleKeys.isEmpty()) return List.of();
    return OrgArrays.query(
        jdbc,
        "SELECT DISTINCT unnest(permissions) AS p FROM role WHERE tenant_id=? AND key = ANY (?)"
            + " ORDER BY p",
        (rs, i) -> rs.getString(1),
        tenantId(),
        roleKeys.toArray(String[]::new));
  }

  public List<String> permissionsForRoleIds(List<UUID> roleIds) {
    if (roleIds == null || roleIds.isEmpty()) return List.of();
    return OrgArrays.query(
        jdbc,
        "SELECT DISTINCT unnest(permissions) AS p FROM role WHERE tenant_id=? AND id = ANY (?)"
            + " ORDER BY p",
        (rs, i) -> rs.getString(1),
        tenantId(),
        roleIds.toArray(UUID[]::new));
  }

  public List<String> groupEffectivePermissions(UUID groupId) {
    List<UUID> chain = groupAncestorIds(groupId);
    if (chain.isEmpty()) return List.of();
    return OrgArrays.query(
        jdbc,
        "SELECT DISTINCT unnest(r.permissions) AS p FROM role_binding b"
            + " JOIN role r ON r.id=b.role_id AND r.tenant_id=b.tenant_id"
            + " WHERE b.tenant_id=? AND b.subject_type='group' AND b.subject_id = ANY (?)"
            + " ORDER BY p",
        (rs, i) -> rs.getString(1),
        tenantId(),
        chain.toArray(UUID[]::new));
  }

  public List<UUID> groupAncestorIds(UUID groupId) {
    // UNION deduplicates the finite (id,parent_id) set, including on pre-existing cyclic data.
    List<GroupParent> chain =
        jdbc.query(
            """
        WITH RECURSIVE chain AS (
          SELECT id, parent_id FROM "group" WHERE tenant_id=? AND id=?
          UNION
          SELECT g.id, g.parent_id FROM "group" g
          JOIN chain c ON g.id = c.parent_id AND g.tenant_id=?
        )
        SELECT id, parent_id FROM chain
        """,
            (rs, i) -> new GroupParent((UUID) rs.getObject("id"), (UUID) rs.getObject("parent_id")),
            tenantId(),
            groupId,
            tenantId());
    if (chain.isEmpty()) return List.of();
    var ids = chain.stream().map(GroupParent::id).collect(java.util.stream.Collectors.toSet());
    if (chain.stream().anyMatch(g -> g.parentId() != null && !ids.contains(g.parentId()))) {
      throw new AurigaException(CommonErrors.NOT_FOUND);
    }
    // A complete single-parent chain must end at a root. No root means a cycle.
    if (chain.stream().noneMatch(g -> g.parentId() == null)) {
      throw new AurigaException(CommonErrors.CONFLICT);
    }
    return chain.stream().map(GroupParent::id).toList();
  }

  private record GroupParent(UUID id, UUID parentId) {}

  /**
   * Shared tenant structural lock: acquire before group target or last direct owner checks and
   * member updates. These paths share the hierarchy/quota lock and hold it until commit.
   */
  public void lockGroupHierarchy() {
    try {
      // A same-value write also rejects stale concurrent REPEATABLE READ snapshots. The tenant
      // table has no business triggers; no tenant fields or tenant update events are changed.
      int rows =
          jdbc.update("UPDATE tenant SET id=id WHERE tenant_id=? AND id=?", tenantId(), tenantId());
      if (rows == 0) throw new AurigaException(CommonErrors.NOT_FOUND);
    } catch (PessimisticLockingFailureException e) {
      // Let the surrounding transaction roll back. Do not retry against its stale snapshot.
      throw new AurigaException(CommonErrors.CONFLICT, Map.of(), List.of(), e);
    }
  }

  public long countOwners() {
    Long n =
        jdbc.queryForObject(
            "SELECT count(DISTINCT b.subject_id) FROM role_binding b"
                + " JOIN role r ON r.id=b.role_id AND r.tenant_id=b.tenant_id"
                + " JOIN member m ON m.id=b.subject_id AND m.tenant_id=b.tenant_id"
                + " WHERE b.tenant_id=? AND b.subject_type='member' AND r.key='owner' AND m.status='active'",
            Long.class,
            tenantId());
    return n == null ? 0 : n;
  }

  public boolean isOwner(UUID accountId) {
    Integer n =
        jdbc.queryForObject(
            "SELECT count(*) FROM role_binding b JOIN role r ON r.id=b.role_id AND r.tenant_id=b.tenant_id"
                + " WHERE b.tenant_id=? AND b.subject_type='member' AND b.subject_id=? AND r.key='owner'",
            Integer.class,
            tenantId(),
            accountId);
    return n != null && n > 0;
  }

  public ObjectNode readJson(String sql, Object... args) {
    List<String> rows = jdbc.query(sql, (rs, i) -> rs.getString(1), args);
    return rows.isEmpty() ? null : OrgJson.read(rows.getFirst());
  }
}
