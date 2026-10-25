package auriga.collab.network.keys;

import auriga.collab.app.NetworkJson;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.persistence.TenantTransactionManager;
import auriga.common.persistence.UuidV7;
import java.math.BigDecimal;
import java.math.BigInteger;
import java.time.Clock;
import java.time.Instant;
import java.util.HashMap;
import java.util.Map;
import java.util.UUID;
import java.util.function.Supplier;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.transaction.support.TransactionTemplate;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.json.JsonMapper;
import tools.jackson.databind.node.ObjectNode;

/** Durable monotonic public state, independent of the local business private-key mount. */
public final class NetworkKeyOperations {
  private final JdbcTemplate jdbc;
  private final TransactionTemplate tx;
  private final UUID tenant;
  private final String realm;
  private final RootKeyPublication trust;
  private final BigInteger floor;
  private final Clock clock;

  public NetworkKeyOperations(
      JdbcTemplate jdbc,
      TenantTransactionManager manager,
      UUID tenant,
      String realm,
      RootKeyPublication trust,
      String minimumVersion,
      Clock clock) {
    this.jdbc = jdbc;
    this.tx = new TransactionTemplate(manager);
    this.tenant = java.util.Objects.requireNonNull(tenant);
    this.realm = java.util.Objects.requireNonNull(realm);
    this.trust = java.util.Objects.requireNonNull(trust);
    this.floor = new BigInteger(minimumVersion);
    if (floor.signum() <= 0 || floor.bitLength() > 64) throw RootKeyPublication.invalid();
    this.clock = clock;
  }

  public void publish(RootKeyPublication.Release release) {
    var next = trust.verify(release.signedKeyset(), release.announcement());
    var p = next.payload();
    var version = new BigInteger(p.path("keyset_version").asString());
    if (!realm.equals(p.path("realm").asString()) || version.compareTo(floor) < 0)
      throw RootKeyPublication.invalid();
    run(
        () -> {
          // Also serializes the first INSERT; there is no absent-row FOR UPDATE race.
          jdbc.queryForObject(
              "SELECT pg_advisory_xact_lock(hashtextextended(?, 0))",
              Object.class,
              tenant + ":network-keys:" + realm);
          Stored old = stored();
          ObjectNode history = JsonMapper.builder().build().createObjectNode();
          if (old != null) {
            var previous = archived(old);
            int order = version.compareTo(old.version());
            if (order < 0) throw RootKeyPublication.invalid();
            if (order == 0) {
              if (!old.release().equals(next.release())) throw RootKeyPublication.invalid();
              return null;
            }
            history = (ObjectNode) NetworkJson.parse(old.history());
            transition(previous.payload(), p, history);
          }
          for (var key : p.path("keys")) {
            if (!history.has(key.path("kid").asString()))
              history.put(key.path("kid").asString(), clock.instant().toString());
          }
          jdbc.update(
              """
          INSERT INTO collab.network_key_publication
            (id, tenant_id, realm, keyset_version, signed_keyset, announcement, first_published, pending_push, updated_at)
          VALUES (?, ?, ?, ?, ?, ?, ?::jsonb, true, ?)
          ON CONFLICT (tenant_id, realm) DO UPDATE SET
            keyset_version = EXCLUDED.keyset_version, signed_keyset = EXCLUDED.signed_keyset,
            announcement = EXCLUDED.announcement, first_published = EXCLUDED.first_published,
            pending_push = true, updated_at = EXCLUDED.updated_at
          """,
              UuidV7.next(clock),
              tenant,
              realm,
              new BigDecimal(version),
              next.release().signedKeyset(),
              next.release().announcement(),
              NetworkJson.canonical(history),
              java.sql.Timestamp.from(clock.instant()));
          return null;
        });
  }

  /** Fixed endpoint and response adapters share the same complete, currently valid release. */
  public RootKeyPublication.Release current() {
    return run(
        () -> {
          Stored row = stored();
          if (row == null || row.version().compareTo(floor) < 0) throw unavailable();
          try {
            var verified = trust.verify(row.release().signedKeyset(), row.release().announcement());
            if (!row.version()
                .toString()
                .equals(verified.payload().path("keyset_version").asString())) throw unavailable();
            return verified.release();
          } catch (RuntimeException e) {
            throw unavailable();
          }
        });
  }

  /** Called under the signer's rotation monitor before every signature; PG failures fail closed. */
  public void assertApproved(String publicKeysJson) {
    try {
      var approved = NetworkJson.parse(current().signedKeyset()).path("keyset");
      if (!NetworkJson.canonical(approved).equals(publicKeysJson)) throw unavailable();
    } catch (RuntimeException e) {
      throw unavailable();
    }
  }

  /** Serialize new signatures with publication/revocation commit, including concurrent workers. */
  public <T> T signApproved(String publicKeysJson, Supplier<T> signature) {
    try {
      return run(
          () -> {
            jdbc.queryForObject(
                "SELECT id FROM collab.network_key_publication WHERE tenant_id = ? AND realm = ? FOR SHARE",
                UUID.class,
                tenant,
                realm);
            assertApproved(publicKeysJson);
            return signature.get();
          });
    } catch (RuntimeException e) {
      throw unavailable();
    }
  }

  /** Network calls stay outside transactions. CAS acknowledgment cannot clear a newer release. */
  public void pushPending(NetworkKeyPush transport) {
    Stored row = run(this::stored);
    if (row == null || !row.pending()) return;
    var release =
        trust.verify(row.release().signedKeyset(), row.release().announcement()).release();
    transport.broadcast(realm, release.signedKeyset(), release.announcement());
    run(
        () -> {
          jdbc.update(
              "UPDATE collab.network_key_publication SET pending_push = false WHERE tenant_id = ? AND realm = ? AND keyset_version = ? AND signed_keyset = ?",
              tenant,
              realm,
              new BigDecimal(row.version()),
              release.signedKeyset());
          return null;
        });
  }

  private RootKeyPublication.Verified archived(Stored row) {
    var verified = trust.verifyArchived(row.release().signedKeyset(), row.release().announcement());
    if (!row.version().toString().equals(verified.payload().path("keyset_version").asString()))
      throw RootKeyPublication.invalid();
    return verified;
  }

  private void transition(JsonNode old, JsonNode next, ObjectNode history) {
    Instant issued = Instant.parse(next.path("issued_at").asString());
    if (issued.isBefore(Instant.parse(old.path("issued_at").asString())))
      throw RootKeyPublication.invalid();
    Map<String, JsonNode> entries = new HashMap<>();
    for (var key : next.path("keys")) entries.put(key.path("kid").asString(), key);
    for (var key : old.path("keys")) {
      var replacement = entries.get(key.path("kid").asString());
      if (replacement == null) throw RootKeyPublication.invalid();
      for (String field :
          new String[] {"kid", "alg", "purpose", "public_key_spki", "not_before", "not_after"}) {
        if (!key.path(field).equals(replacement.path(field))) throw RootKeyPublication.invalid();
      }
      String state = key.path("state").asString();
      String updated = replacement.path("state").asString();
      boolean allowed =
          state.equals(updated)
              || "revoked".equals(updated)
              || ("next".equals(state) && "active".equals(updated))
              || ("active".equals(state) && "retired".equals(updated));
      if (!allowed) throw RootKeyPublication.invalid();
      if ("next".equals(state) && "active".equals(updated)) {
        Instant first = Instant.parse(history.path(key.path("kid").asString()).asString());
        if (issued.isBefore(first.plusSeconds(86400))) throw RootKeyPublication.invalid();
        for (var previous : old.path("keys")) {
          if (previous.path("purpose").equals(key.path("purpose"))
              && "active".equals(previous.path("state").asString())
              && Instant.parse(previous.path("not_after").asString())
                  .isBefore(issued.plusSeconds(360))) throw RootKeyPublication.invalid();
        }
      }
    }
    for (var key : next.path("keys")) {
      if (!history.has(key.path("kid").asString()) && !"next".equals(key.path("state").asString()))
        throw RootKeyPublication.invalid();
    }
  }

  private Stored stored() {
    var rows =
        jdbc.query(
            "SELECT keyset_version, signed_keyset, announcement, first_published::text, pending_push FROM collab.network_key_publication WHERE tenant_id = ? AND realm = ?",
            (rs, index) ->
                new Stored(
                    rs.getBigDecimal(1).toBigIntegerExact(),
                    new RootKeyPublication.Release(rs.getString(2), rs.getString(3)),
                    rs.getString(4),
                    rs.getBoolean(5)),
            tenant,
            realm);
    return rows.isEmpty() ? null : rows.getFirst();
  }

  private <T> T run(Supplier<T> operation) {
    return TenantContext.call(
        tenant,
        () ->
            tx.execute(
                status -> {
                  TenantTransactionManager.verifyTransactionTenant();
                  return operation.get();
                }));
  }

  private record Stored(
      BigInteger version, RootKeyPublication.Release release, String history, boolean pending) {}

  private static AurigaException unavailable() {
    return new AurigaException(CommonErrors.UNAVAILABLE);
  }
}
