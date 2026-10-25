package auriga.iam;

import auriga.common.audit.AuditEntry;
import auriga.common.audit.AuditLogger;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.security.AuthorizationRules;
import auriga.iam.api.NetworkAuthority;
import java.math.BigDecimal;
import java.math.BigInteger;
import java.time.Clock;
import java.time.Instant;
import java.util.*;
import java.util.function.Supplier;
import org.springframework.jdbc.core.JdbcTemplate;
import tools.jackson.databind.node.ObjectNode;

/** Persistent N1 authority. No process-local authorization cache and no model session tree. */
public final class IamNetworkAuthority implements NetworkAuthority {
  private static final BigInteger MAX = new BigInteger("18446744073709551615");
  private final IamStore store;
  private final JdbcTemplate jdbc;
  private final DeviceAccessAuthority devices;
  private final AuditLogger audit;
  private final Clock clock;
  private final Set<String> servers;

  public IamNetworkAuthority(
      IamStore store,
      DeviceAccessAuthority devices,
      AuditLogger audit,
      Clock clock,
      Set<String> servers) {
    this.store = store;
    this.jdbc = store.networkJdbc();
    this.devices = devices;
    this.audit = audit;
    this.clock = clock;
    this.servers = Set.copyOf(servers);
  }

  @Override
  public Node register(String accessToken) {
    store.requireTransactionReleased();
    if (TenantContext.PLATFORM_TENANT.equals(TenantContext.require())) return denied();
    var identity = require(devices.access(accessToken));
    // No device/session locks may survive into the independent reservation transaction.
    String reserved =
        store.isolated(
            TenantContext.require(),
            () -> {
              store.lock("network-authority");
              enabled();
              require(devices.credential(identity.tenant(), identity.account(), identity.device()));
              Node existing = nodeByDevice(identity.device());
              if (existing != null) return existing.node();
              jdbc.update(
                  """
                  INSERT INTO iam.network_realm(tenant_id,realm)
                  SELECT ?,nextval('iam.network_realm_number')::numeric+9223372036854775809
                  WHERE NOT EXISTS (SELECT 1 FROM iam.network_realm WHERE tenant_id=?)
                  ON CONFLICT (tenant_id) DO NOTHING
                  """,
                  TenantContext.require(),
                  TenantContext.require());
              var values =
                  jdbc.query(
                      """
                      UPDATE iam.network_realm SET next_node=next_node+1
                      WHERE tenant_id=? AND next_node<=18446744073709551615
                      RETURNING (next_node-1)::text
                      """,
                      (rs, i) -> rs.getString(1),
                      TenantContext.require());
              if (values.isEmpty()) throw new AurigaException(CommonErrors.UNAVAILABLE);
              return values.getFirst();
            });
    return transaction(
        () -> {
          require(devices.credential(identity.tenant(), identity.account(), identity.device()));
          enabled();
          jdbc.update(
              """
              INSERT INTO iam.network_node(tenant_id,node,account_id,device_id)
              VALUES (?,?,?,?) ON CONFLICT (tenant_id,device_id) DO NOTHING
              """,
              TenantContext.require(),
              number(reserved),
              UUID.fromString(identity.account()),
              UUID.fromString(identity.device()));
          Node node = nodeByDevice(identity.device());
          if (node == null || node.revoked() || !node.account().equals(identity.account())) deny();
          record("register", node.node(), Map.of("device", node.device()));
          return node;
        });
  }

  @Override
  public Ticket prepare(String accessToken, String nodeId, String audience) {
    store.requireTransactionReleased();
    if (TenantContext.PLATFORM_TENANT.equals(TenantContext.require())) return denied();
    var identity = require(devices.access(accessToken));
    if (!validNumber(nodeId, false) || audience == null || !servers.contains(audience))
      return denied();
    return transaction(
        () -> {
          enabled();
          Node node = node(nodeId);
          if (node == null
              || node.revoked()
              || !node.device().equals(identity.device())
              || !node.account().equals(identity.account())) return denied();
          var current =
              require(devices.credential(identity.tenant(), node.account(), node.device()));
          Instant expiry =
              min(clock.instant().plusSeconds(30), min(identity.expiresAt(), current.expiresAt()));
          if (!clock.instant().isBefore(expiry)) return denied();
          // Bound outstanding tickets per device, without collecting or scanning other nodes.
          jdbc.update(
              "DELETE FROM iam.network_ticket WHERE tenant_id=? AND data->>'node'=? AND"
                  + " expires_at<=?",
              TenantContext.require(),
              nodeId,
              java.sql.Timestamp.from(clock.instant()));
          Long outstanding =
              jdbc.queryForObject(
                  """
                  SELECT count(*) FROM iam.network_ticket WHERE tenant_id=? AND data->>'node'=?
                  """,
                  Long.class,
                  TenantContext.require(),
                  nodeId);
          if (outstanding == null || outstanding >= 8) return denied();
          String raw = Secrets.random();
          store.insert(
              "network_ticket",
              Secrets.hash(raw),
              Json.object(
                  "node",
                  nodeId,
                  "device",
                  node.device(),
                  "account",
                  node.account(),
                  "realm",
                  node.realm(),
                  "audience",
                  audience,
                  "expiresAt",
                  expiry.toString()),
              expiry);
          return new Ticket(raw, node, audience, expiry);
        });
  }

  @Override
  public Connection redeem(String raw, String nodeId, String device, String audience) {
    caller("svc:ai-gateway");
    if (raw == null || !raw.matches("[A-Za-z0-9_-]{43}")) return denied();
    Connection result =
        transaction(
            () -> {
              enabled();
              String hash = Secrets.hash(raw);
              ObjectNode ticket = store.get("network_ticket", hash, true);
              if (ticket == null) return null;
              // Commit consumption even for a rejected binding. Throwing inside this transaction
              // would roll back the deletion and let a captured ticket be probed repeatedly.
              store.delete("network_ticket", hash);
              if (!validNumber(nodeId, false)
                  || audience == null
                  || !servers.contains(audience)
                  || !ticket.path("node").asString().equals(nodeId)
                  || !ticket.path("device").asString().equals(device)
                  || !ticket.path("audience").asString().equals(audience)
                  || !clock.instant().isBefore(Instant.parse(ticket.path("expiresAt").asString())))
                return null;
              Node node = node(nodeId);
              if (node == null
                  || node.revoked()
                  || !node.device().equals(device)
                  || !node.account().equals(ticket.path("account").asString())
                  || !node.realm().equals(ticket.path("realm").asString())) return null;
              var credential =
                  devices.credential(TenantContext.require().toString(), node.account(), device);
              if (credential == null
                  || !TenantContext.require().toString().equals(credential.tenant())
                  || !clock.instant().isBefore(credential.expiresAt())) return null;
              Instant expiry =
                  min(
                      credential.expiresAt(),
                      clock
                          .instant()
                          .plusSeconds(Math.min(3600, limits().get(Limit.CONNECTION_SECONDS))));
              if (!clock.instant().isBefore(expiry)) return null;
              return new Connection(node, audience, expiry);
            });
    return result == null ? denied() : result;
  }

  @Override
  public boolean active(String realm, String nodeId) {
    caller("svc:ai-gateway", "svc:collab");
    if (!validNumber(realm, false) || !validNumber(nodeId, false)) return false;
    return transaction(() -> config().path("enabled").asBoolean(true) && live(node(nodeId), realm));
  }

  @Override
  public Decision check(Check request) {
    caller("svc:ai-gateway", "svc:collab");
    return transaction(
        () -> {
          var limits = limits();
          boolean allowed = permitted(request, limits);
          return new Decision(allowed, allowed ? "allowed" : "network_denied", limits);
        });
  }

  private boolean permitted(Check r, Map<Limit, Long> limits) {
    if (r == null
        || r.operation() == null
        || r.sender() == null
        || !validNumber(r.realm(), false)
        || !validNumber(r.sourceNode(), false)
        || !validNumber(r.local(), r.sender() == Sender.HUMAN)
        || r.sender() == Sender.HUMAN && !"0".equals(r.local())
        || r.bodyBytes() < 0
        || r.targetNodes() < 0
        || r.targetLocals() < 0
        || r.bodyBytes() > limits.get(Limit.BODY_BYTES)
        || r.bodyBytes() > limits.get(Limit.PAYLOAD_BYTES)
        || !config().path("enabled").asBoolean(true)
        || !live(node(r.sourceNode()), r.realm())) return false;
    if (r.operation() == Operation.CONNECT) return true;
    if (!validNumber(r.targetNode(), false) || !live(node(r.targetNode()), r.realm())) return false;
    ObjectNode policy = store.get("network_policy", r.sourceNode() + ":" + r.targetNode(), false);
    if (policy != null && !policy.path("allowed").asBoolean()) return false;
    if (r.operation() == Operation.BROADCAST) {
      // The dispatcher asks once per target node, retaining the full immutable fanout count.
      return config().path("broadcast").asBoolean(true)
          && r.targetNodes() > 0
          && r.targetNodes() == r.targetLocals()
          && r.targetNodes() <= limits.get(Limit.BROADCAST_NODES)
          && granted(r.sourceNode(), r.sender(), r.local(), Operation.BROADCAST);
    }
    if (r.targetNodes() != 1 || r.targetLocals() != 1) return false;
    return r.operation() != Operation.FIRST_CONTACT
        || config().path("firstContact").asBoolean(true)
            && (r.sender() == Sender.HUMAN
                || granted(r.sourceNode(), r.sender(), r.local(), Operation.FIRST_CONTACT));
  }

  @Override
  public void configure(Configuration configuration) {
    admin();
    Objects.requireNonNull(configuration);
    for (var entry : configuration.limits().entrySet())
      if (entry.getValue() <= 0 || entry.getValue() > entry.getKey().defaultValue())
        throw new AurigaException(CommonErrors.VALIDATION_FAILED);
    transaction(
        () -> {
          ObjectNode value =
              Json.object(
                  "enabled",
                  configuration.enabled(),
                  "firstContact",
                  configuration.firstContact(),
                  "broadcast",
                  configuration.broadcast(),
                  "limits",
                  configuration.limits());
          put("network_config", "organization", value, null);
          record("configure", "organization", Map.of("configuration", value));
          return null;
        });
  }

  @Override
  public void nodePolicy(String source, String target, boolean allowed) {
    admin();
    validateNode(source);
    validateNode(target);
    transaction(
        () -> {
          if (node(source) == null || node(target) == null) return denied();
          put("network_policy", source + ":" + target, Json.object("allowed", allowed), null);
          record("policy", source, Map.of("targetNode", target, "allowed", allowed));
          return null;
        });
  }

  @Override
  public void grant(
      String nodeId, Sender sender, String local, Operation operation, Instant expiry) {
    admin();
    grantKey(nodeId, sender, local, operation);
    if (expiry == null
        || !clock.instant().isBefore(expiry)
        || expiry.isAfter(clock.instant().plusSeconds(86400)))
      throw new AurigaException(CommonErrors.VALIDATION_FAILED);
    transaction(
        () -> {
          Node node = node(nodeId);
          if (node == null || node.revoked()) return denied();
          put(
              "network_grant",
              grantKey(nodeId, sender, local, operation),
              Json.object("expiresAt", expiry.toString()),
              expiry);
          record(
              "grant",
              nodeId,
              Map.of(
                  "sender",
                  sender.name(),
                  "local",
                  local,
                  "operation",
                  operation.name(),
                  "expiresAt",
                  expiry.toString()));
          return null;
        });
  }

  @Override
  public void withdraw(String nodeId, Sender sender, String local, Operation operation) {
    admin();
    String key = grantKey(nodeId, sender, local, operation);
    transaction(
        () -> {
          store.delete("network_grant", key);
          record(
              "withdraw",
              nodeId,
              Map.of("sender", sender.name(), "local", local, "operation", operation.name()));
          return null;
        });
  }

  @Override
  public void revoke(String nodeId) {
    admin();
    validateNode(nodeId);
    transaction(
        () -> {
          jdbc.update(
              "UPDATE iam.network_node SET revoked=true WHERE tenant_id=? AND node=?",
              TenantContext.require(),
              number(nodeId));
          record("revoke", nodeId, Map.of());
          return null;
        });
  }

  private boolean granted(String node, Sender sender, String local, Operation operation) {
    ObjectNode grant = store.get("network_grant", grantKey(node, sender, local, operation), false);
    return grant != null
        && clock.instant().isBefore(Instant.parse(grant.path("expiresAt").asString()));
  }

  private static String grantKey(String node, Sender sender, String local, Operation operation) {
    validateNode(node);
    if (sender == null
        || !validNumber(local, sender == Sender.HUMAN)
        || sender == Sender.HUMAN && !"0".equals(local)
        || operation != Operation.FIRST_CONTACT && operation != Operation.BROADCAST)
      throw new AurigaException(CommonErrors.VALIDATION_FAILED);
    return node + ":" + sender.name() + ":" + local + ":" + operation.name();
  }

  private Map<Limit, Long> limits() {
    ObjectNode config = config();
    var result = new EnumMap<Limit, Long>(Limit.class);
    for (Limit limit : Limit.values()) {
      long value = config.path("limits").path(limit.name()).asLong(limit.defaultValue());
      if (value <= 0 || value > 9007199254740991L)
        throw new AurigaException(CommonErrors.UNAVAILABLE);
      // Previously persisted settings must not relax the platform ceiling either.
      result.put(limit, Math.min(value, limit.defaultValue()));
    }
    return result;
  }

  private ObjectNode config() {
    ObjectNode value = store.get("network_config", "organization", false);
    return value == null ? Json.object() : value;
  }

  private void enabled() {
    if (!config().path("enabled").asBoolean(true)) deny();
  }

  private boolean live(Node node, String realm) {
    if (node == null || node.revoked() || !node.realm().equals(realm)) return false;
    var credential =
        devices.credential(TenantContext.require().toString(), node.account(), node.device());
    return credential != null && clock.instant().isBefore(credential.expiresAt());
  }

  private Node node(String id) {
    if (!validNumber(id, false)) return null;
    return loadNode("n.node=?", number(id));
  }

  private Node nodeByDevice(String device) {
    return loadNode("n.device_id=?", UUID.fromString(device));
  }

  private Node loadNode(String predicate, Object argument) {
    var rows =
        jdbc.query(
            """
            SELECT r.realm::text,n.node::text,n.account_id::text,n.device_id::text,n.revoked
            FROM iam.network_node n JOIN iam.network_realm r USING (tenant_id)
            WHERE n.tenant_id=? AND
            """
                + predicate,
            (rs, i) ->
                new Node(
                    rs.getString(1),
                    rs.getString(2),
                    rs.getString(3),
                    rs.getString(4),
                    rs.getBoolean(5)),
            TenantContext.require(),
            argument);
    return rows.isEmpty() ? null : rows.getFirst();
  }

  private void put(String table, String key, ObjectNode value, Instant expiry) {
    ObjectNode existing = store.get(table, key, true);
    if (existing == null) store.insert(table, key, value, expiry);
    else {
      value.put("id", existing.path("id").asString());
      store.save(table, value);
      // IamStore.save intentionally does not update expiry. Grants use their canonical data TTL;
      // keep the indexed expiry synchronized for future bounded retention jobs.
      if (table.equals("network_grant"))
        jdbc.update(
            "UPDATE iam.network_grant SET expires_at=? WHERE tenant_id=? AND lookup_key=?",
            java.sql.Timestamp.from(expiry),
            TenantContext.require(),
            key);
    }
  }

  private <T> T transaction(Supplier<T> work) {
    store.requireTransactionReleased();
    return store.tx(
        () -> {
          // Serialize N1 authority decisions/mutations per tenant; credential locks always follow.
          store.lock("network-authority");
          return work.get();
        });
  }

  private void record(String action, String node, Map<String, ?> details) {
    var entry = AuditEntry.builder("iam.network." + action).target("node", node);
    details.forEach(entry::detail);
    audit.record(entry.build());
  }

  private static void caller(String... subjects) {
    var principal =
        AuthorizationRules.currentPrincipal()
            .orElseThrow(() -> new AurigaException(CommonErrors.UNAUTHENTICATED));
    if (!principal.isInternalService()
        || !TenantContext.PLATFORM_TENANT.equals(principal.tenantId())
        || !Set.of(subjects).contains(principal.subject())
        || TenantContext.PLATFORM_TENANT.equals(TenantContext.require())) deny();
  }

  private static void admin() {
    var principal =
        AuthorizationRules.currentPrincipal()
            .orElseThrow(() -> new AurigaException(CommonErrors.UNAUTHENTICATED));
    if (!principal.isAdminContext()
        || !principal.hasPermission("iam.network.manage")
        || !principal.tenantId().equals(TenantContext.require())
        || TenantContext.PLATFORM_TENANT.equals(principal.tenantId())) deny();
  }

  private DeviceAccessAuthority.Identity require(DeviceAccessAuthority.Identity identity) {
    if (identity == null
        || !TenantContext.require().toString().equals(identity.tenant())
        || !clock.instant().isBefore(identity.expiresAt())) return denied();
    return identity;
  }

  static boolean validNumber(String value, boolean zero) {
    return value != null
        && value.matches(zero ? "0|[1-9][0-9]{0,19}" : "[1-9][0-9]{0,19}")
        && new BigInteger(value).compareTo(MAX) <= 0;
  }

  private static BigDecimal number(String value) {
    return new BigDecimal(value);
  }

  private static void validateNode(String value) {
    if (!validNumber(value, false)) throw new AurigaException(CommonErrors.VALIDATION_FAILED);
  }

  private static Instant min(Instant a, Instant b) {
    return a.isBefore(b) ? a : b;
  }

  private static void deny() {
    throw new AurigaException(CommonErrors.FORBIDDEN);
  }

  private static <T> T denied() {
    deny();
    return null;
  }
}
