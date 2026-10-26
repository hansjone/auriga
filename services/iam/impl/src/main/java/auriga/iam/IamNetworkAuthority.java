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
          Instant connectionExpiry =
              min(
                  current.expiresAt(),
                  clock.instant().plusSeconds(limits().get(Limit.CONNECTION_SECONDS)));
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
                  "connectionExpiresAt",
                  connectionExpiry.toString(),
                  "expiresAt",
                  expiry.toString()),
              expiry);
          return new Ticket(raw, node, audience, expiry, connectionExpiry);
        });
  }

  @Override
  public Connection redeem(
      String raw, String nodeId, String device, String audience, IngressBinding binding) {
    caller("svc:ai-gateway");
    if (raw == null || !raw.matches("[A-Za-z0-9_-]{43}")) return denied();
    // Commit consumption before calling credential(), whose participating transaction
    // can become rollback-only even when its exception is caught by the caller.
    ObjectNode ticket =
        transaction(
            () -> {
              String hash = Secrets.hash(raw);
              ObjectNode found = store.get("network_ticket", hash, true);
              if (found != null) store.delete("network_ticket", hash);
              return found;
            });
    if (ticket == null) return denied();
    Connection result =
        transaction(
            () -> {
              enabled();
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
                      min(
                          credential.expiresAt(),
                          Instant.parse(ticket.path("connectionExpiresAt").asString())),
                      clock
                          .instant()
                          .plusSeconds(Math.min(3600, limits().get(Limit.CONNECTION_SECONDS))));
              if (!clock.instant().isBefore(expiry)) return null;
              if (binding == null
                  || !Objects.toString(binding.serverId(), "").matches("N[A-Z2-7]{55}")
                  || !validNumber(binding.clientId(), false)
                  || !Objects.toString(binding.userNkey(), "").matches("U[A-Z2-7]{55}")
                  || !ingressForTicket(raw).equals(binding.ingress())
                  || binding.account() == null
                  || binding.account().isBlank()
                  || binding.account().length() > 256) return null;
              // Reclaim only expired bindings for this node; other live ingresses remain
              // distinct until READY fences the prior epoch in collab.
              jdbc.update(
                  "DELETE FROM iam.network_ingress WHERE tenant_id=? AND data->>'realm'=? AND data->>'node'=? AND expires_at<=?",
                  TenantContext.require(),
                  node.realm(),
                  nodeId,
                  java.sql.Timestamp.from(clock.instant()));
              store.insert(
                  "network_ingress",
                  binding.ingress(),
                  Json.object(
                      "realm",
                      node.realm(),
                      "node",
                      node.node(),
                      "device",
                      device,
                      "serverId",
                      binding.serverId(),
                      "clientId",
                      binding.clientId(),
                      "userNkey",
                      binding.userNkey(),
                      "account",
                      binding.account(),
                      "audience",
                      audience,
                      "expiresAt",
                      expiry.toString()),
                  expiry);
              jdbc.update(
                  "INSERT INTO iam.network_ingress_tenant(tenant_id) VALUES (?) ON CONFLICT DO NOTHING",
                  TenantContext.require());
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

  static String ingressForTicket(String raw) {
    byte[] bytes = HexFormat.of().parseHex(Secrets.hash(raw).substring(0, 32));
    String alphabet = "abcdefghijklmnopqrstuvwxyz234567";
    var result = new StringBuilder(26);
    int buffer = 0, bits = 0;
    for (byte value : bytes) {
      buffer = (buffer << 8) | (value & 255);
      bits += 8;
      while (bits >= 5) {
        bits -= 5;
        result.append(alphabet.charAt((buffer >>> bits) & 31));
      }
    }
    if (bits > 0) result.append(alphabet.charAt((buffer << (5 - bits)) & 31));
    return result.toString();
  }

  @Override
  public Map<NodeReference, Boolean> activeBatch(List<NodeReference> requested) {
    caller("svc:ai-gateway");
    if (requested == null || requested.size() > 10000)
      throw new AurigaException(CommonErrors.VALIDATION_FAILED);
    var result = new LinkedHashMap<NodeReference, Boolean>();
    for (var key : requested) {
      if (key == null || !validNumber(key.realm(), false) || !validNumber(key.node(), false))
        throw new AurigaException(CommonErrors.VALIDATION_FAILED);
      result.put(key, false);
    }
    if (result.isEmpty()) return result;
    return store.tx(
        () -> {
          long timeoutMillis = 2000;
          var deadline = io.grpc.Context.current().getDeadline();
          if (deadline != null)
            timeoutMillis =
                Math.min(
                    timeoutMillis,
                    deadline.timeRemaining(java.util.concurrent.TimeUnit.MILLISECONDS));
          if (timeoutMillis <= 0 || io.grpc.Context.current().isCancelled())
            throw new AurigaException(CommonErrors.UNAVAILABLE);
          jdbc.execute("SET LOCAL statement_timeout='" + timeoutMillis + "ms'");
          if (!config().path("enabled").asBoolean(true)) return result;
          var ids = result.keySet().stream().map(NodeReference::node).distinct().toList();
          var args = new ArrayList<Object>();
          args.add(TenantContext.require());
          args.add(java.sql.Timestamp.from(clock.instant()));
          args.add(java.sql.Timestamp.from(clock.instant()));
          for (var id : ids) args.add(number(id));
          String sql =
              """
          SELECT r.realm::text,n.node::text FROM iam.network_node n
          JOIN iam.network_realm r USING(tenant_id)
          JOIN iam.device d ON d.tenant_id=n.tenant_id AND d.id=n.device_id
          JOIN iam.session s ON s.tenant_id=n.tenant_id AND s.id=(d.data->>'sessionId')::uuid
          JOIN iam.account a ON a.tenant_id=n.tenant_id AND a.id=n.account_id
          WHERE n.tenant_id=? AND NOT n.revoked
            AND d.data->>'accountId'=n.account_id::text
            AND NOT COALESCE((d.data->>'revoked')::boolean,false)
            AND d.data->>'scope'='auriga.connect'
            AND s.data->>'tenantId'=n.tenant_id::text
            AND s.data->>'accountId'=n.account_id::text
            AND s.data->>'audience'='portal'
            AND NOT COALESCE((s.data->>'revoked')::boolean,false)
            AND (s.data->>'expiresAt')::timestamptz>?
            AND (s.data->>'idleExpiresAt')::timestamptz>?
            AND a.data->>'status'='active' AND n.node IN (
          """
                  + String.join(",", Collections.nCopies(ids.size(), "?"))
                  + ")";
          for (var key :
              jdbc.query(
                  sql,
                  (rs, i) -> new NodeReference(rs.getString(1), rs.getString(2)),
                  args.toArray())) if (result.containsKey(key)) result.put(key, true);
          return result;
        });
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

  /** Existing effective organization ceilings; no policy mutation or rate reservation. */
  Map<Limit, Long> effectiveLimits() {
    caller("svc:collab");
    return transaction(this::limits);
  }

  record TargetBinding(
      String realm,
      String node,
      String ingress,
      String serverId,
      String clientId,
      String account) {}

  private record TargetSnapshot(
      Node node, ObjectNode ingress, ObjectNode device, ObjectNode session, ObjectNode account) {}

  /**
   * Resolve only an existing redeem generation. Native device IDs are never rebound by OAuth;
   * serverId/ingress identify the broker/redeem generation. No source/local registry is inferred.
   * Check backing authority outside the network transaction, then compare its complete persisted
   * snapshots under locks. Even an expiry extension during the call requires a fresh query.
   */
  Instant resolveTarget(TargetBinding binding) {
    caller("svc:collab");
    store.requireTransactionReleased();
    if (binding == null
        || !validNumber(binding.realm(), false)
        || !validNumber(binding.node(), false)
        || binding.ingress() == null
        || !binding.ingress().matches("[a-z2-7]{26}")) return null;
    TargetSnapshot before = store.tx(() -> targetSnapshot(binding, false));
    if (before == null) return null;
    // credential() reuses the canonical owner/device/backing-session checks, without idle touch.
    var credential =
        devices.credential(
            TenantContext.require().toString(), before.node().account(), before.node().device());
    if (credential == null
        || !TenantContext.require().toString().equals(credential.tenant())
        || !before.node().account().equals(credential.account())
        || !before.node().device().equals(credential.device())) return null;
    Instant result =
        transaction(
            () -> {
              TargetSnapshot after = targetSnapshot(binding, true);
              if (!before.equals(after) || !config().path("enabled").asBoolean(true)) return null;
              Instant expiry =
                  min(
                      credential.expiresAt(),
                      Instant.parse(after.ingress().path("expiresAt").asString()));
              return clock.instant().isBefore(expiry) ? expiry : null;
            });
    // Commit and lock release consume time too; never return an already expired grant.
    return result != null && clock.instant().isBefore(result) ? result : null;
  }

  private TargetSnapshot targetSnapshot(TargetBinding b, boolean lock) {
    Node n = node(b.node());
    if (n == null || n.revoked() || !n.realm().equals(b.realm())) return null;
    // Shared identity -> ingress order also used by source transactions and device refresh.
    // Read the device without a lock only to locate the backing session, then re-read under locks.
    ObjectNode device = store.get("device", n.device(), false);
    if (device == null) return null;
    ObjectNode session = store.byId("session", device.path("sessionId").asString(), lock);
    if (lock) device = store.get("device", n.device(), true);
    ObjectNode account = store.get("account", n.account(), lock);
    if (device == null
        || session == null
        || account == null
        || !n.account().equals(device.path("accountId").asString())
        || !device.path("sessionId").asString().equals(session.path("sessionId").asString())
        || !n.account().equals(session.path("accountId").asString())) return null;
    // Fetch the complete exact ingress after identity locks; never return an earlier unlocked copy.
    ObjectNode ingress = store.get("network_ingress", b.ingress(), lock);
    if (ingress == null
        || !b.realm().equals(ingress.path("realm").asString())
        || !b.node().equals(ingress.path("node").asString())
        || !n.device().equals(ingress.path("device").asString())
        || !Objects.equals(b.serverId(), ingress.path("serverId").asString())
        || !Objects.equals(b.clientId(), ingress.path("clientId").asString())
        || !Objects.equals(b.account(), ingress.path("account").asString())
        || !ingress.path("serverId").asString().matches("N[A-Z2-7]{55}")
        || !validNumber(ingress.path("clientId").asString(), false)
        || !ingress.path("userNkey").asString().matches("U[A-Z2-7]{55}")
        || !servers.contains(ingress.path("audience").asString())
        || !clock.instant().isBefore(Instant.parse(ingress.path("expiresAt").asString())))
      return null;
    return new TargetSnapshot(n, ingress, device, session, account);
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
    if (config.has("limits") && !config.path("limits").isObject())
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    var result = new EnumMap<Limit, Long>(Limit.class);
    for (Limit limit : Limit.values()) {
      var configured = config.path("limits").path(limit.name());
      if (config.path("limits").has(limit.name())
          && (!configured.isIntegralNumber() || !configured.canConvertToLong()))
        throw new AurigaException(CommonErrors.UNAVAILABLE);
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
