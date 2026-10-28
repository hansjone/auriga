package auriga.iam;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.security.*;
import auriga.contracts.iam.v1.*;
import auriga.iam.api.NetworkDirectory;
import java.math.BigDecimal;
import java.math.BigInteger;
import java.nio.charset.StandardCharsets;
import java.time.Clock;
import java.util.*;
import org.springframework.jdbc.core.JdbcTemplate;

/** Bounded, tenant-scoped registration projection; never reads session local metadata. */
public final class IamNetworkDirectory implements NetworkDirectory {
  private final IamStore store;
  private final JdbcTemplate jdbc;
  private final SessionManager sessions;
  private final DeviceAccessAuthority devices;
  private final Clock clock;

  private static final int MAX_SCAN = 1000;
  private static final String JOINS =
      """
      JOIN iam.network_realm r ON r.tenant_id=n.tenant_id
      JOIN iam.account a ON a.tenant_id=n.tenant_id AND a.id=n.account_id
      JOIN iam.device d ON d.tenant_id=n.tenant_id AND d.id=n.device_id
      LEFT JOIN iam.session s ON s.tenant_id=n.tenant_id AND s.id=(d.data->>'sessionId')::uuid
      """;
  // Preserve offline discovery for 30 days after credential expiry; never infer online presence.
  private static final String ELIGIBLE =
      """
      NOT n.revoked AND a.data->>'status'='active'
      AND d.data->>'accountId'=n.account_id::text AND d.data->>'scope'='auriga.connect'
      AND NOT COALESCE((d.data->>'revoked')::boolean,false)
      AND s.data->>'accountId'=n.account_id::text AND s.data->>'tenantId'=n.tenant_id::text
      AND s.data->>'audience'='portal' AND NOT COALESCE((s.data->>'revoked')::boolean,false)
      AND (s.data->>'expiresAt')::timestamptz>?
      AND (s.data->>'idleExpiresAt')::timestamptz>?
      """;
  private static final String REGISTERED =
      "FROM iam.network_node n " + JOINS + " WHERE n.tenant_id=? AND " + ELIGIBLE;

  public IamNetworkDirectory(
      IamStore store, SessionManager sessions, DeviceAccessAuthority devices, Clock clock) {
    this.store = store;
    this.jdbc = store.networkJdbc();
    this.sessions = sessions;
    this.devices = devices;
    this.clock = clock;
  }

  @Override
  public ListNetworkSystemsResponse list(int limit, String cursor) {
    var principal = reader();
    if (limit < 1 || limit > 200) throw new AurigaException(CommonErrors.VALIDATION_FAILED);
    String after = position(cursor);
    return store.tx(
        () -> {
          jdbc.execute("SET LOCAL statement_timeout='2000ms'");
          // Recheck the backing authority, rather than relying on the five-minute bearer alone.
          var account = store.get("account", principal.subject(), false);
          if (account == null || !account.path("status").asString().equals("active")) deny();
          if (principal.type() == PrincipalType.DEVICE) {
            var identity =
                devices.credential(
                    principal.tenantId().toString(), principal.subject(), principal.deviceId());
            if (identity == null || !clock.instant().isBefore(identity.expiresAt())) deny();
            Integer registered =
                jdbc.queryForObject(
                    "SELECT count(*) " + REGISTERED + " AND n.device_id=? AND n.account_id=?",
                    Integer.class,
                    TenantContext.require(),
                    staleBefore(),
                    staleBefore(),
                    UUID.fromString(principal.deviceId()),
                    UUID.fromString(principal.subject()));
            if (registered == null || registered != 1) deny();
          } else {
            var session = sessions.activeById(principal.sessionId());
            if (session == null
                || !principal.subject().equals(session.path("accountId").asString())
                || !principal
                    .context()
                    .name()
                    .equalsIgnoreCase(session.path("audience").asString())) deny();
          }
          if (!enabled()) return ListNetworkSystemsResponse.getDefaultInstance();
          var args = new ArrayList<Object>();
          args.add(TenantContext.require());
          args.add(new BigDecimal(after));
          args.add(TenantContext.require());
          args.add(UUID.fromString(principal.subject()));
          String source = "";
          if (principal.type() == PrincipalType.DEVICE) {
            source = " AND source.device_id=?";
            args.add(UUID.fromString(principal.deviceId()));
          }
          args.add(staleBefore());
          args.add(staleBefore());
          // Resolve denied pairs once per page, rather than rejoining all caller sources for
          // each candidate. Retain exact pair matching even for noncanonical stored keys.
          var rows =
              jdbc.query(
                  "WITH candidates AS MATERIALIZED (SELECT * FROM iam.network_node WHERE tenant_id=? AND node>? ORDER BY node LIMIT "
                      + (MAX_SCAN + 1)
                      + """
                      ), denied_targets AS MATERIALIZED (
                        SELECT split_part(p.lookup_key,':',2) AS node
                        FROM iam.network_policy p
                        JOIN iam.network_node source ON source.tenant_id=p.tenant_id
                          AND source.node::text=split_part(p.lookup_key,':',1)
                        WHERE p.tenant_id=? AND source.account_id=? AND NOT source.revoked
                          AND NOT COALESCE((p.data->>'allowed')::boolean,false)
                          AND p.lookup_key=source.node::text||':'||split_part(p.lookup_key,':',2)
                      """
                      + source
                      + ") "
                      + "SELECT r.realm::text,n.node::text,COALESCE(d.data->>'name',''),n.account_id::text,COALESCE(a.data->>'displayName',''),COALESCE(("
                      + ELIGIBLE
                      + """
                    AND NOT EXISTS (
                      SELECT 1 FROM denied_targets denied WHERE denied.node=n.node::text
                  """
                      + ")),false) FROM candidates n "
                      + JOINS
                      + " ORDER BY n.node",
                  (rs, i) ->
                      new Row(
                          rs.getString(2),
                          !rs.getBoolean(6)
                              ? null
                              : NetworkSystemEntry.newBuilder()
                                  .setSystemAddress(
                                      "aur1:"
                                          + new BigInteger(rs.getString(1)).toString(16)
                                          + "."
                                          + new BigInteger(rs.getString(2)).toString(16)
                                          + ".0")
                                  .setDisplayName(truncate(rs.getString(3), 64))
                                  .setOwnerUserId(rs.getString(4))
                                  .setOwnerDisplayName(truncate(rs.getString(5), 128))
                                  .setOnlineState("unknown")
                                  .build()),
                  args.toArray());
          var result = ListNetworkSystemsResponse.newBuilder();
          int scanned = 0;
          while (scanned < rows.size() && scanned < MAX_SCAN && result.getItemsCount() < limit) {
            var row = rows.get(scanned++);
            if (row.entry() != null) result.addItems(row.entry());
          }
          if (scanned < rows.size()) result.setNextCursor(cursor(rows.get(scanned - 1).node()));
          return result.build();
        });
  }

  private record Row(String node, NetworkSystemEntry entry) {}

  private static String truncate(String value, int maximum) {
    int count = value.codePointCount(0, value.length());
    return count <= maximum ? value : value.substring(0, value.offsetByCodePoints(0, maximum));
  }

  @Override
  public ResolveNetworkNodeResponse resolve(String realm, String node) {
    collabCaller();
    if (!IamNetworkAuthority.validNumber(realm, false)
        || !IamNetworkAuthority.validNumber(node, false))
      throw new AurigaException(CommonErrors.VALIDATION_FAILED);
    return store.tx(
        () -> {
          if (!enabled()) return ResolveNetworkNodeResponse.getDefaultInstance();
          Integer count =
              jdbc.queryForObject(
                  "SELECT count(*) " + REGISTERED + " AND r.realm=? AND n.node=?",
                  Integer.class,
                  TenantContext.require(),
                  staleBefore(),
                  staleBefore(),
                  new BigDecimal(realm),
                  new BigDecimal(node));
          return count != null && count == 1
              ? ResolveNetworkNodeResponse.newBuilder()
                  .setExists(true)
                  .setType(NetworkDirectoryNodeType.NETWORK_DIRECTORY_NODE_TYPE_DSH)
                  .build()
              : ResolveNetworkNodeResponse.getDefaultInstance();
        });
  }

  private java.sql.Timestamp staleBefore() {
    return java.sql.Timestamp.from(clock.instant().minus(java.time.Duration.ofDays(30)));
  }

  private boolean enabled() {
    var config = store.get("network_config", "organization", false);
    return config == null || config.path("enabled").asBoolean(true);
  }

  private static AurigaPrincipal reader() {
    var p = principal();
    if (!p.tenantId().equals(TenantContext.require())
        || TenantContext.PLATFORM_TENANT.equals(p.tenantId())
        || !(p.type() == PrincipalType.USER
                && Set.of(TokenContext.PORTAL, TokenContext.ADMIN).contains(p.context())
            || p.type() == PrincipalType.DEVICE
                && p.context() == TokenContext.DEVICE
                && p.hasPermission("auriga.connect.collab.use"))) deny();
    return p;
  }

  static void collabCaller() {
    var p = principal();
    if (!p.isInternalService()
        || !p.subject().equals("svc:collab")
        || !TenantContext.PLATFORM_TENANT.equals(p.tenantId())
        || TenantContext.current().filter(t -> !TenantContext.PLATFORM_TENANT.equals(t)).isEmpty())
      deny();
  }

  private static AurigaPrincipal principal() {
    return AuthorizationRules.currentPrincipal()
        .orElseThrow(() -> new AurigaException(CommonErrors.UNAUTHENTICATED));
  }

  private static void deny() {
    throw new AurigaException(CommonErrors.FORBIDDEN);
  }

  private static String cursor(String node) {
    return Base64.getUrlEncoder()
        .withoutPadding()
        .encodeToString((TenantContext.require() + ":" + node).getBytes(StandardCharsets.US_ASCII));
  }

  private static String position(String cursor) {
    if (cursor == null || cursor.isEmpty()) return "0";
    if (cursor.length() > 128) throw new AurigaException(CommonErrors.VALIDATION_FAILED);
    try {
      String decoded = new String(Base64.getUrlDecoder().decode(cursor), StandardCharsets.US_ASCII);
      String prefix = TenantContext.require() + ":";
      String node = decoded.startsWith(prefix) ? decoded.substring(prefix.length()) : "";
      if (!IamNetworkAuthority.validNumber(node, false) || !cursor(node).equals(cursor))
        throw new IllegalArgumentException();
      return node;
    } catch (IllegalArgumentException invalid) {
      throw new AurigaException(CommonErrors.VALIDATION_FAILED);
    }
  }
}
