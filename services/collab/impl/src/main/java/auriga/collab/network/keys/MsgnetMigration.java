package auriga.collab.network.keys;

import static auriga.collab.network.keys.MsgnetV2Verifier.*;

import auriga.collab.app.NetworkJson;
import java.time.Clock;
import java.time.Duration;
import java.util.*;
import java.util.function.LongSupplier;
import org.springframework.jdbc.core.JdbcTemplate;
import tools.jackson.databind.JsonNode;

/** Verified queue import and irreversible barrier; no root private key enters this process. */
public final class MsgnetMigration {
  public record Member(RootKeyPublication.Release release, Map<String, byte[]> privateKeys) {}

  private final MsgnetRenewal renewal;
  private final JdbcTemplate jdbc;
  private final UUID tenant;
  private final String realm;
  private final RootKeyPublication legacy;
  private final Clock clock;
  private final LongSupplier nanos;

  public MsgnetMigration(
      MsgnetRenewal renewal,
      JdbcTemplate jdbc,
      UUID tenant,
      String realm,
      RootKeyPublication legacy,
      Clock clock) {
    this(renewal, jdbc, tenant, realm, legacy, clock, System::nanoTime);
  }

  MsgnetMigration(
      MsgnetRenewal renewal,
      JdbcTemplate jdbc,
      UUID tenant,
      String realm,
      RootKeyPublication legacy,
      Clock clock,
      LongSupplier nanos) {
    this.renewal = renewal;
    this.jdbc = jdbc;
    this.tenant = tenant;
    this.realm = realm;
    this.legacy = legacy;
    this.clock = clock;
    this.nanos = nanos;
  }

  /**
   * Archive is an immutable public array of {signed_keyset,announcement}; every pair is verified.
   */
  public JsonNode migrate(
      JsonNode permit,
      JsonNode authority,
      JsonNode control,
      byte[] runtime,
      JsonNode ancestorArchive,
      List<Member> members) {
    renewal.verifyMigrationPermit(permit);
    var p = permit.path("payload");
    require(
        tenant.toString().equals(text(p, "tenant_id")) && realm.equals(text(p, "realm")),
        "scope_denied");
    var completed =
        renewal.run(
            () ->
                jdbc.queryForList(
                    "SELECT permit, receipt FROM collab.msgnet_migration WHERE tenant_id = ? AND realm = ? AND permit_id = ? AND committed",
                    tenant,
                    realm,
                    text(p, "permit_id")));
    if (!completed.isEmpty()) {
      require(
          NetworkJson.parse(completed.getFirst().get("permit").toString(), 1048576).equals(permit),
          "same_version_conflict");
      return NetworkJson.parse(completed.getFirst().get("receipt").toString(), 1048576);
    }
    require(
        ancestorArchive.isArray() && !ancestorArchive.isEmpty(), "migration_outside_commitment");
    for (var entry : ancestorArchive) {
      require(
          new HashSet<>(entry.propertyNames()).equals(Set.of("signed_keyset", "announcement")),
          "invalid_chain");
      legacy.verifyArchived(
          MsgnetRenewal.canonical(entry.path("signed_keyset")),
          MsgnetRenewal.canonical(entry.path("announcement")));
    }
    require(
        digest(ancestorArchive).equals(text(p, "ancestor_digest")), "migration_outside_commitment");
    var verified = new TreeMap<java.math.BigInteger, Member>();
    var commitment = MsgnetRenewal.object().putArray("members");
    for (var member : members) {
      var release =
          legacy.verifyArchived(member.release().signedKeyset(), member.release().announcement());
      var k = release.payload();
      require(
          verified.putIfAbsent(number(k, "keyset_version"), member) == null,
          "migration_outside_commitment");
      var expectedIds = new HashSet<String>();
      for (var key : k.path("keys")) {
        String kid = text(key, "kid");
        expectedIds.add(kid);
        MsgnetPrivateKeyStore.match(
            text(key, "public_key_spki"), member.privateKeys().get(kid), "Ed25519");
      }
      require(expectedIds.equals(member.privateKeys().keySet()), "migration_outside_commitment");
    }
    require(!verified.isEmpty(), "migration_outside_commitment");
    for (var entry : verified.entrySet()) {
      var signed = NetworkJson.parse(entry.getValue().release().signedKeyset(), 1048576);
      var materials = MsgnetRenewal.object().put("schema_version", 2);
      var keys = materials.putArray("keys");
      list(signed.path("keyset").path("keys")).stream()
          .sorted(Comparator.comparing(k -> text(k, "kid")))
          .forEach(
              k -> {
                var material = MsgnetRenewal.object();
                for (String field :
                    List.of("kid", "purpose", "public_key_spki", "not_before", "not_after"))
                  material.set(field, k.path(field));
                keys.add(material);
              });
      schema("MigrationMaterialCommitment", materials, "invalid_chain");
      commitment
          .addObject()
          .put("version", entry.getKey().toString())
          .put("pair_digest", digest(signed))
          .put("material_digest", digest(materials));
    }
    require(
        digest(commitment).equals(text(p, "queue_commitment_digest"))
            && verified.lastKey().equals(number(p, "maximum_committed_v1_version"))
            && number(p, "minimum_v2_version").compareTo(verified.lastKey()) > 0
            && number(control.path("payload"), "minimum_keyset_version")
                    .compareTo(number(p, "minimum_v2_version"))
                >= 0
            && number(control.path("payload"), "minimum_generation")
                    .compareTo(number(p, "minimum_generation"))
                >= 0,
        "migration_outside_commitment");
    // Verify current coverage before taking the freeze lock. Repeat after acquiring it.
    renewal.run(
        () -> {
          observed(verified);
          return null;
        });
    long[] started = {-1};
    try {
      return renewal.run(
          () -> {
            renewal.lock();
            started[0] = nanos.getAsLong();
            jdbc.execute("SET LOCAL statement_timeout = '600s'");
            jdbc.execute("SET LOCAL idle_in_transaction_session_timeout = '600s'");
            var priorReceipts =
                jdbc.queryForList(
                    "SELECT receipt FROM collab.msgnet_migration WHERE tenant_id = ? AND realm = ? AND permit_id = ? AND committed",
                    String.class,
                    tenant,
                    realm,
                    text(p, "permit_id"));
            if (!priorReceipts.isEmpty())
              return NetworkJson.parse(priorReceipts.getFirst(), 1048576);
            var observed = observed(verified);
            var signed = NetworkJson.parse(observed.get("signed_keyset").toString(), 1048576);
            long remainingAtFreeze =
                Duration.between(clock.instant(), time(signed.path("keyset"), "valid_until"))
                    .getSeconds();
            var selected = verified.get(number(signed.path("keyset"), "keyset_version"));
            // The immutable v1 row and its real first_published are archived unchanged below.
            renewal.installForMigration(authority, control, runtime);
            var payload = signed.path("keyset");
            for (var key : payload.path("keys"))
              renewal.importKey(key, selected.privateKeys().get(text(key, "kid")));
            renewal.reconcileMigration(payload);
            long elapsed = elapsed(started[0]);

            var publication = NetworkJson.parse(renewal.publication(), 1048576);
            var k = publication.path("keyset").path("payload");
            renewal.verifyMigration(
                permit,
                commitment,
                text(signed.path("keyset"), "keyset_version"),
                digest(signed),
                elapsed,
                remainingAtFreeze,
                text(k, "keyset_version"),
                true,
                jdbc.queryForObject(
                    "SELECT legacy_disabled FROM collab.msgnet_realm WHERE tenant_id = ? AND realm = ?",
                    Boolean.class,
                    tenant,
                    realm));
            var receipt =
                MsgnetRenewal.object()
                    .put("permit_id", text(p, "permit_id"))
                    .put("observed_v1_version", text(payload, "keyset_version"))
                    .put("observed_v1_digest", digest(signed))
                    .put("v2_generation", text(k, "generation"))
                    .put("v2_keyset_version", text(k, "keyset_version"))
                    .put("publication_digest", digest(publication.path("keyset")))
                    .put("barrier_committed", true)
                    .put("legacy_disabled", true)
                    .put("frozen_elapsed_seconds", elapsed);
            schema("MigrationReceipt", receipt, "invalid_chain");
            var archive = MsgnetRenewal.object();
            archive.set("ancestor", ancestorArchive);
            archive.set("members", commitment);
            archive.set("observed_signed_keyset", signed);
            archive.set(
                "observed_announcement",
                NetworkJson.parse(observed.get("announcement").toString(), 1048576));
            archive.set(
                "first_published",
                NetworkJson.parse(observed.get("first_published").toString(), 1048576));
            jdbc.update(
                "INSERT INTO collab.msgnet_migration (id, tenant_id, realm, permit_id, permit, archive, receipt, committed) VALUES (?, ?, ?, ?, ?, ?, ?, true)",
                auriga.common.persistence.UuidV7.next(clock),
                tenant,
                realm,
                text(p, "permit_id"),
                MsgnetRenewal.canonical(permit),
                MsgnetRenewal.canonical(archive),
                MsgnetRenewal.canonical(receipt));
            require(elapsed(started[0]) <= 600, "migration_expired");
            return receipt;
          });
    } catch (RuntimeException failure) {
      // Original legacy row is unchanged by rollback and the xact lock automatically unfreezes it.
      renewal.run(
          () -> {
            var receipt =
                MsgnetRenewal.object()
                    .put("barrier_committed", false)
                    .put(
                        "failure_code",
                        failure instanceof auriga.common.error.AurigaException a
                            ? a.errorCode().code()
                            : "collab.msgnet_invalid_transition")
                    .put("frozen_elapsed_seconds", started[0] < 0 ? 0 : elapsed(started[0]));
            jdbc.update(
                "INSERT INTO collab.msgnet_migration (id, tenant_id, realm, permit_id, permit, archive, receipt, committed) VALUES (?, ?, ?, ?, ?, ?, ?, false)",
                auriga.common.persistence.UuidV7.next(clock),
                tenant,
                realm,
                text(p, "permit_id"),
                MsgnetRenewal.canonical(permit),
                MsgnetRenewal.canonical(ancestorArchive),
                MsgnetRenewal.canonical(receipt));
            return null;
          });
      throw failure;
    }
  }

  private Map<String, Object> observed(Map<java.math.BigInteger, Member> members) {
    var rows =
        jdbc.queryForList(
            "SELECT keyset_version, signed_keyset, announcement, first_published::text FROM collab.network_key_publication WHERE tenant_id = ? AND realm = ?",
            tenant,
            realm);
    require(rows.size() == 1, "migration_outside_commitment");
    var row = rows.getFirst();
    var release =
        legacy.verify(row.get("signed_keyset").toString(), row.get("announcement").toString());
    var signed = NetworkJson.parse(release.release().signedKeyset(), 1048576);
    var member = members.get(number(release.payload(), "keyset_version"));
    require(
        member != null
            && NetworkJson.parse(member.release().signedKeyset(), 1048576).equals(signed)
            && NetworkJson.parse(member.release().announcement(), 1048576)
                .equals(NetworkJson.parse(release.release().announcement(), 1048576)),
        "migration_outside_commitment");
    require(
        Duration.between(clock.instant(), time(release.payload(), "valid_until")).getSeconds()
            >= 21600,
        "migration_expired");
    return row;
  }

  private long elapsed(long started) {
    long delta = nanos.getAsLong() - started;
    require(delta >= 0, "clock_error");
    return delta / 1_000_000_000L + (delta % 1_000_000_000L == 0 ? 0 : 1);
  }
}
