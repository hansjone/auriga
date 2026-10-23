package auriga.resource.persistence;

import auriga.common.context.TenantContext;
import auriga.common.persistence.TenantTransactionManager;
import auriga.resource.app.ResourceAccess.Reader;
import auriga.resource.web.model.ModelPackage;
import auriga.resource.web.model.Version;
import java.sql.ResultSet;
import java.sql.SQLException;
import java.time.OffsetDateTime;
import java.util.*;
import java.util.function.Supplier;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.jdbc.core.namedparam.NamedParameterJdbcTemplate;
import org.springframework.transaction.support.TransactionTemplate;

/** Parameterized resource-only SQL, always inside a tenant-stamped runtime-role transaction. */
public final class ResourceStore {
  private final NamedParameterJdbcTemplate jdbc;
  private final TransactionTemplate tx;

  public ResourceStore(JdbcTemplate jdbc, TenantTransactionManager manager) {
    this.jdbc = new NamedParameterJdbcTemplate(jdbc);
    tx = new TransactionTemplate(manager);
    tx.setReadOnly(true);
    tx.setIsolationLevel(
        org.springframework.transaction.TransactionDefinition.ISOLATION_REPEATABLE_READ);
  }

  public <T> T read(Supplier<T> action) {
    TenantContext.require();
    TenantTransactionManager.verifyTransactionTenant();
    return tx.execute(status -> action.get());
  }

  private Map<String, Object> params(Reader r) {
    return new HashMap<>(
        Map.of(
            "tenant",
            r.tenant(),
            "account",
            r.account(),
            "external",
            r.external(),
            "personal",
            r.personalPlugins(),
            "groups",
            r.groups()));
  }

  private String visible(Reader r) {
    String group =
        r.groups().isEmpty()
            ? "FALSE"
            : "EXISTS (SELECT 1 FROM resource.resource_package_group g WHERE g.tenant_id=p.tenant_id AND g.package_id=p.id AND g.group_id IN (:groups))";
    return "p.tenant_id=:tenant AND (p.level <> 'l4' OR (:personal AND p.owner_id=:account)) AND ("
        + "p.visibility='tenant' OR (p.visibility='members' AND NOT :external) OR "
        + "(p.visibility='private' AND p.owner_id=:account) OR (p.visibility='groups' AND "
        + group
        + "))";
  }

  private static final String VERSION_VISIBLE =
      "(v.author_id=:account OR v.state IN ('published','deprecated','withdrawn'))";
  private static final String PACKAGE_COLUMNS =
      "p.*, ARRAY(SELECT g.group_id FROM resource.resource_package_group g WHERE g.tenant_id=p.tenant_id AND g.package_id=p.id ORDER BY g.group_id) AS group_ids, "
          + "(SELECT v.id FROM resource.resource_version v WHERE v.tenant_id=p.tenant_id AND v.package_id=p.id AND v.id=p.default_version_id AND "
          + VERSION_VISIBLE
          + ") AS visible_default";

  public Optional<ModelPackage> packageById(Reader r, UUID id) {
    var args = params(r);
    args.put("id", id);
    return jdbc
        .query(
            "SELECT "
                + PACKAGE_COLUMNS
                + " FROM resource.resource_package p WHERE "
                + visible(r)
                + " AND p.id=:id",
            args,
            this::packageRow)
        .stream()
        .findFirst();
  }

  public UUID ceiling(Reader r, UUID packageId) {
    var args = params(r);
    String sql;
    if (packageId == null)
      sql =
          "SELECT p.id FROM resource.resource_package p WHERE "
              + visible(r)
              + " ORDER BY p.id DESC LIMIT 1";
    else {
      args.put("package", packageId);
      sql =
          "SELECT v.id FROM resource.resource_version v WHERE v.tenant_id=:tenant AND v.package_id=:package AND "
              + VERSION_VISIBLE
              + " ORDER BY v.id DESC LIMIT 1";
    }
    return jdbc.query(sql, args, (rs, n) -> rs.getObject("id", UUID.class)).stream()
        .findFirst()
        .orElse(new UUID(0, 0));
  }

  public List<ModelPackage> packages(Reader r, UUID after, UUID ceiling, int limit) {
    var args = params(r);
    args.put("after", after);
    args.put("ceiling", ceiling);
    args.put("limit", limit);
    return jdbc.query(
        "SELECT "
            + PACKAGE_COLUMNS
            + " FROM resource.resource_package p WHERE "
            + visible(r)
            + " AND p.id > :after AND p.id <= :ceiling ORDER BY p.id LIMIT :limit",
        args,
        this::packageRow);
  }

  public List<Version> versions(Reader r, UUID packageId, UUID after, UUID ceiling, int limit) {
    var args = params(r);
    args.put("package", packageId);
    args.put("after", after);
    args.put("ceiling", ceiling);
    args.put("limit", limit);
    return jdbc.query(
        "SELECT v.* FROM resource.resource_version v WHERE v.tenant_id=:tenant AND v.package_id=:package AND "
            + VERSION_VISIBLE
            + " AND v.id > :after AND v.id <= :ceiling ORDER BY v.id LIMIT :limit",
        args,
        this::versionRow);
  }

  public Optional<Version> versionById(Reader r, UUID packageId, UUID versionId) {
    var args = params(r);
    args.put("package", packageId);
    args.put("version", versionId);
    return jdbc
        .query(
            "SELECT v.* FROM resource.resource_version v WHERE v.tenant_id=:tenant AND v.package_id=:package AND v.id=:version AND "
                + VERSION_VISIBLE,
            args,
            this::versionRow)
        .stream()
        .findFirst();
  }

  private ModelPackage packageRow(ResultSet rs, int row) throws SQLException {
    var groups = new LinkedHashSet<UUID>();
    var array = rs.getArray("group_ids");
    try {
      for (var group : (Object[]) array.getArray()) groups.add((UUID) group);
    } finally {
      array.free();
    }
    return new ModelPackage()
        .id(rs.getObject("id", UUID.class))
        .tenantId(rs.getObject("tenant_id", UUID.class))
        .name(rs.getString("name"))
        .kind(ModelPackage.KindEnum.PLUGIN)
        .ownerId(rs.getObject("owner_id", UUID.class))
        .level(ModelPackage.LevelEnum.fromValue(rs.getString("level")))
        .visibility(ModelPackage.VisibilityEnum.fromValue(rs.getString("visibility")))
        .groupIds(groups)
        .revision(rs.getLong("revision"))
        .createdAt(rs.getObject("created_at", OffsetDateTime.class))
        .enabled(rs.getBoolean("enabled"))
        .defaultVersionId(rs.getObject("visible_default", UUID.class));
  }

  private Version versionRow(ResultSet rs, int row) throws SQLException {
    var result =
        new Version()
            .id(rs.getObject("id", UUID.class))
            .packageId(rs.getObject("package_id", UUID.class))
            .version(rs.getString("version"))
            .digest(rs.getString("digest"))
            .sizeBytes(rs.getLong("size_bytes"))
            .mediaType(Version.MediaTypeEnum.APPLICATION_GZIP)
            .state(Version.StateEnum.fromValue(rs.getString("state")))
            .revision(rs.getLong("revision"))
            .createdAt(rs.getObject("created_at", OffsetDateTime.class));
    Long sequence = rs.getObject("release_sequence", Long.class);
    if (sequence != null) result.releaseSequence(sequence);
    return result;
  }
}
