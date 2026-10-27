package auriga.iam;

import static auriga.common.error.CommonErrors.FORBIDDEN;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.persistence.UuidV7;
import auriga.contracts.iam.v1.*;
import java.math.BigDecimal;
import java.time.Instant;
import java.util.*;
import org.springframework.jdbc.core.JdbcTemplate;

/** Permanent redeem provenance; historical reads check associations, never online authority. */
final class IamNetworkConnectionAnchors {
  private final IamStore store;
  private final JdbcTemplate jdbc;

  IamNetworkConnectionAnchors(IamStore store) {
    this.store = store;
    this.jdbc = store.networkJdbc();
  }

  private record Anchor(
      UUID id,
      UUID ingress,
      String key,
      NetworkAuthorityConnection connection,
      String realm,
      String node,
      UUID owner,
      UUID device,
      UUID session) {
    NetworkConnectionAnchor reference() {
      return NetworkConnectionAnchor.newBuilder()
          .setAnchorId(id.toString())
          .setConnection(connection)
          .build();
    }
  }

  /** Caller owns network-authority -> session -> device -> account locks, in the redeem tx. */
  void retain(
      NetworkAuthorityConnection c,
      UUID ingress,
      UUID owner,
      UUID device,
      UUID session,
      Instant redeemedAt) {
    validate(c);
    var row =
        new Anchor(
            UuidV7.next(),
            ingress,
            c.getIngress(),
            c,
            c.getRealm(),
            c.getNode(),
            owner,
            device,
            session);
    ownership(List.of(row));
    checkIngress(row);
    jdbc.update(
        """
        INSERT INTO iam.network_connection_anchor
        (id,tenant_id,ingress_id,ingress_key,connection,realm,node,owner_id,device_id,session_id,redeemed_at)
        VALUES (?,?,?,?,?,?,?,?,?,?,?)
        """,
        row.id(),
        TenantContext.require(),
        ingress,
        c.getIngress(),
        c.toByteArray(),
        new BigDecimal(c.getRealm()),
        new BigDecimal(c.getNode()),
        owner,
        device,
        session,
        java.sql.Timestamp.from(redeemedAt));
  }

  /** Already locked live ingress/identity only; never backfill a pre-upgrade connection. */
  NetworkConnectionAnchor existing(
      NetworkAuthorityConnection c, UUID ingress, UUID owner, UUID device, UUID session) {
    var rows = load("ingress_key", c.getIngress());
    if (rows.isEmpty()) return null;
    check(rows.size() == 1);
    var a = rows.getFirst();
    check(
        a.connection().equals(c)
            && a.ingress().equals(ingress)
            && a.owner().equals(owner)
            && a.device().equals(device)
            && a.session().equals(session));
    checkIngress(a);
    ownership(List.of(a));
    return a.reference();
  }

  ReadNetworkConnectionAnchorResponse read(ReadNetworkConnectionAnchorRequest request) {
    check(
        request.getUnknownFields().asMap().isEmpty()
            && request.hasOriginal()
            && request.hasCurrent());
    validate(request.getOriginal());
    validate(request.getCurrent());
    store.requireTransactionReleased();
    return store.tx(
        () -> {
          jdbc.execute("SET LOCAL lock_timeout='5s'");
          // Advisory transaction lock only: no same-value UPDATE or business writes.
          store.lock("network-authority");
          Anchor original = find(request.getOriginal());
          Anchor current = find(request.getCurrent());
          check(
              original.connection().getTenantId().equals(current.connection().getTenantId())
                  && original.realm().equals(current.realm())
                  && original.node().equals(current.node())
                  && original.owner().equals(current.owner())
                  && original.device().equals(current.device())
                  && original.session().equals(current.session()));
          ownership(List.of(original, current));
          return ReadNetworkConnectionAnchorResponse.newBuilder()
              .setOriginal(original.reference())
              .setCurrent(current.reference())
              .build();
        });
  }

  private Anchor find(NetworkConnectionAnchor reference) {
    var rows = load("id", UUID.fromString(reference.getAnchorId()));
    check(rows.size() == 1);
    var row = rows.getFirst();
    check(row.reference().equals(reference));
    return row;
  }

  private List<Anchor> load(String column, Object value) {
    // Only private constant column names, never RPC input.
    return jdbc.query(
        """
        SELECT id,ingress_id,ingress_key,connection,realm::text,node::text,owner_id,device_id,session_id
        FROM iam.network_connection_anchor WHERE tenant_id=? AND
        """
            + column
            + "=?",
        (rs, i) -> {
          NetworkAuthorityConnection c;
          try {
            c = NetworkAuthorityConnection.parseFrom(rs.getBytes(4));
          } catch (com.google.protobuf.InvalidProtocolBufferException e) {
            throw new AurigaException(FORBIDDEN);
          }
          validate(c);
          var a =
              new Anchor(
                  rs.getObject(1, UUID.class),
                  rs.getObject(2, UUID.class),
                  rs.getString(3),
                  c,
                  rs.getString(5),
                  rs.getString(6),
                  rs.getObject(7, UUID.class),
                  rs.getObject(8, UUID.class),
                  rs.getObject(9, UUID.class));
          check(
              a.key().equals(c.getIngress())
                  && a.realm().equals(c.getRealm())
                  && a.node().equals(c.getNode()));
          return a;
        },
        TenantContext.require(),
        value);
  }

  /** Uses actual PK plus lookup key and every C/identity field before retaining or returning it. */
  private void checkIngress(Anchor a) {
    var rows =
        jdbc.query(
            "SELECT id,data::text FROM iam.network_ingress WHERE tenant_id=? AND lookup_key=? FOR SHARE",
            (rs, i) -> {
              var data = Json.read(rs.getString(2));
              var c = a.connection();
              return a.ingress().equals(rs.getObject(1, UUID.class))
                  && a.realm().equals(data.path("realm").asString())
                  && a.node().equals(data.path("node").asString())
                  && a.device().toString().equals(data.path("device").asString())
                  && a.session().toString().equals(data.path("sessionId").asString())
                  && c.getServerId().equals(data.path("serverId").asString())
                  && c.getClientId().equals(data.path("clientId").asString())
                  && c.getClientAccount().equals(data.path("account").asString());
            },
            TenantContext.require(),
            a.key());
    check(rows.size() == 1 && rows.getFirst());
  }

  /** Shared lock order with source/refresh: session -> device -> account -> node/realm. */
  private void ownership(List<Anchor> anchors) {
    var sessions = new TreeSet<UUID>();
    var devices = new TreeSet<UUID>();
    var owners = new TreeSet<UUID>();
    for (var a : anchors) {
      sessions.add(a.session());
      devices.add(a.device());
      owners.add(a.owner());
    }
    for (UUID id : sessions) check(store.byId("session", id.toString(), true) != null);
    for (UUID id : devices) check(store.byId("device", id.toString(), true) != null);
    for (UUID id : owners) check(store.byId("account", id.toString(), true) != null);
    for (var a : anchors) {
      var s = store.byId("session", a.session().toString(), false);
      var d = store.byId("device", a.device().toString(), false);
      check(
          s != null
              && d != null
              && a.owner().toString().equals(s.path("accountId").asString())
              && a.owner().toString().equals(d.path("accountId").asString())
              && a.session().toString().equals(s.path("sessionId").asString())
              && a.session().toString().equals(d.path("sessionId").asString())
              && TenantContext.require().toString().equals(s.path("tenantId").asString()));
    }
    var ordered = new TreeMap<String, Anchor>();
    for (var a : anchors) ordered.put(a.realm() + ":" + a.node(), a);
    for (var a : ordered.values()) {
      var rows =
          jdbc.query(
              """
          SELECT n.account_id,n.device_id FROM iam.network_node n
          JOIN iam.network_realm r USING(tenant_id)
          WHERE n.tenant_id=? AND n.node=? AND r.realm=? FOR SHARE OF n,r
          """,
              (rs, i) ->
                  a.owner().equals(rs.getObject(1, UUID.class))
                      && a.device().equals(rs.getObject(2, UUID.class)),
              TenantContext.require(),
              new BigDecimal(a.node()),
              new BigDecimal(a.realm()));
      check(rows.size() == 1 && rows.getFirst());
    }
  }

  static void validate(NetworkConnectionAnchor a) {
    check(
        a.getUnknownFields().asMap().isEmpty()
            && a.hasConnection()
            && a.getAnchorId()
                .matches("[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}"));
    validate(a.getConnection());
  }

  private static void validate(NetworkAuthorityConnection c) {
    check(
        c.getUnknownFields().asMap().isEmpty()
            && TenantContext.require().toString().equals(c.getTenantId())
            && !TenantContext.PLATFORM_TENANT.equals(TenantContext.require())
            && IamNetworkAuthority.validNumber(c.getRealm(), false)
            && IamNetworkAuthority.validNumber(c.getNode(), false)
            && c.getIngress().matches("[a-z2-7]{26}")
            && c.getServerId().matches("N[A-Z2-7]{55}")
            && IamNetworkAuthority.validNumber(c.getClientId(), false)
            && !c.getClientAccount().isBlank()
            && c.getClientAccount().length() <= 256);
  }

  private static void check(boolean condition) {
    if (!condition) throw new AurigaException(FORBIDDEN);
  }
}
