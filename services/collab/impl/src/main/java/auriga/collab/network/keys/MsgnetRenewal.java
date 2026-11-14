package auriga.collab.network.keys;

import static auriga.collab.network.keys.MsgnetV2Verifier.*;

import auriga.collab.app.NetworkJson;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.persistence.TenantTransactionManager;
import auriga.common.persistence.UuidV7;
import java.math.BigDecimal;
import java.math.BigInteger;
import java.security.KeyFactory;
import java.security.KeyPairGenerator;
import java.security.PrivateKey;
import java.security.Signature;
import java.security.spec.PKCS8EncodedKeySpec;
import java.time.Clock;
import java.time.Instant;
import java.util.Arrays;
import java.util.Base64;
import java.util.List;
import java.util.UUID;
import java.util.function.Supplier;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.transaction.support.TransactionTemplate;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.json.JsonMapper;
import tools.jackson.databind.node.ObjectNode;

/**
 * All realm planning, private material, pair and outbox changes commit together under one PG lock.
 */
public final class MsgnetRenewal {
  private final JdbcTemplate jdbc;
  private final TransactionTemplate tx;
  private final UUID tenant;
  private final String realm;
  private final MsgnetV2Verifier verifier;
  private final MsgnetPrivateKeyStore secrets;
  private final Clock clock;
  private final MsgnetClock timeGuard;
  private final Supplier<Instant> databaseTime;
  private final JsonNode config;
  private static final auriga.common.events.EventSchemas EVENTS =
      new auriga.common.events.EventSchemas();
  private final auriga.common.audit.AuditLogger audit;

  public MsgnetRenewal(
      JdbcTemplate jdbc,
      TenantTransactionManager manager,
      UUID tenant,
      String realm,
      MsgnetV2Verifier verifier,
      MsgnetPrivateKeyStore secrets,
      Clock clock,
      JsonNode config) {
    this(
        jdbc,
        manager,
        tenant,
        realm,
        verifier,
        secrets,
        clock,
        new MsgnetClock(
            clock,
            System::nanoTime,
            config.path("allowed_skew_seconds").asLong(),
            config.path("forward_jump_seconds").asLong(),
            config.path("backward_jump_seconds").asLong()),
        () -> jdbc.queryForObject("SELECT clock_timestamp()", java.sql.Timestamp.class).toInstant(),
        config);
  }

  MsgnetRenewal(
      JdbcTemplate jdbc,
      TenantTransactionManager manager,
      UUID tenant,
      String realm,
      MsgnetV2Verifier verifier,
      MsgnetPrivateKeyStore secrets,
      Clock clock,
      MsgnetClock timeGuard,
      Supplier<Instant> databaseTime,
      JsonNode config) {
    MsgnetPolicy.config(config);
    this.jdbc = jdbc;
    this.tx = new TransactionTemplate(manager);
    this.tenant = tenant;
    this.realm = realm;
    this.verifier = verifier;
    this.secrets = secrets;
    this.clock = clock;
    this.timeGuard = timeGuard;
    this.databaseTime = databaseTime;
    this.config = config.deepCopy();
    this.audit =
        new auriga.common.audit.OutboxAuditLogger(
            new auriga.common.events.OutboxEventPublisher(
                jdbc, "collab", "collab", EVENTS, JsonMapper.builder().build(), clock, null));
  }

  /**
   * Trusted installation only. Root signed control/authority are validated before accepting a
   * KEK-wrapped runtime key.
   */
  public void install(JsonNode authority, JsonNode control, byte[] runtimePkcs8) {
    install(authority, control, runtimePkcs8, false);
  }

  void installForMigration(JsonNode authority, JsonNode control, byte[] runtimePkcs8) {
    install(authority, control, runtimePkcs8, true);
  }

  private void install(
      JsonNode authority, JsonNode control, byte[] runtimePkcs8, boolean migration) {
    try {
      verifier.verifyAuthority(authority, control, clock.instant());
    } catch (AurigaException e) {
      if (!"collab.msgnet_authority_revoked".equals(e.errorCode().code())) throw e;
      installControl(control);
      return;
    }
    MsgnetPolicy.authorizedConfig(config, authority);
    new MsgnetPublicControl(
            jdbc,
            (TenantTransactionManager) tx.getTransactionManager(),
            tenant,
            realm,
            verifier,
            null)
        .install(control, migration);
    run(
        () -> {
          lock();
          var old = stored();
          if (old == null && !migration)
            require(
                jdbc.queryForObject(
                        "SELECT count(*) FROM collab.network_key_publication WHERE tenant_id = ? AND realm = ?",
                        Integer.class,
                        tenant,
                        realm)
                    == 0,
                "migration_outside_commitment");
          if (old != null) {
            var previous = old.path("control");
            int order =
                number(control.path("payload"), "control_revision")
                    .compareTo(number(previous.path("payload"), "control_revision"));
            require(order >= 0, "control_rollback");
            if (order == 0) require(previous.equals(control), "same_version_conflict");
            for (String field : List.of("revoked_authority_ids", "revoked_installation_ids"))
              for (var id : previous.path("payload").path(field))
                require(
                    contains(control.path("payload").path(field), id.asString()),
                    "control_rollback");
          }
          secrets.install(
              aad(text(authority.path("payload"), "runtime_kid"), "runtime_authority"),
              text(authority.path("payload"), "public_key_spki"),
              runtimePkcs8);
          jdbc.update(
              """
          INSERT INTO collab.msgnet_realm (id, tenant_id, realm, root_control, runtime_authority, generation, keyset_version)
          VALUES (?, ?, ?, ?, ?, ?, 0)
          ON CONFLICT (tenant_id, realm) DO UPDATE SET root_control = EXCLUDED.root_control, runtime_authority = EXCLUDED.runtime_authority
          """,
              UuidV7.next(clock),
              tenant,
              realm,
              canonical(control),
              canonical(authority),
              new BigDecimal(number(control.path("payload"), "minimum_generation")));
          return null;
        });
  }

  public void installControl(JsonNode control) {
    new MsgnetPublicControl(
            jdbc,
            (TenantTransactionManager) tx.getTransactionManager(),
            tenant,
            realm,
            verifier,
            null)
        .install(control, false);
  }

  public void reconcile() {
    reconcile(null);
  }

  void verifyMigration(
      JsonNode permit,
      JsonNode members,
      String currentVersion,
      String currentDigest,
      long frozenSeconds,
      long remainingSeconds,
      String firstVersion,
      boolean committed,
      boolean disabled) {
    verifier.verifyMigration(
        permit,
        members,
        currentVersion,
        currentDigest,
        frozenSeconds,
        remainingSeconds,
        firstVersion,
        committed,
        disabled);
  }

  void verifyMigrationPermit(JsonNode permit) {
    verifier.verifyMigrationPermit(permit);
  }

  void importKey(JsonNode key, byte[] privateKey) {
    secrets.install(
        aad(text(key, "kid"), text(key, "purpose")), text(key, "public_key_spki"), privateKey);
  }

  void reconcileMigration(JsonNode seed) {
    reconcile(seed);
  }

  private void reconcile(JsonNode migrationSeed) {
    try {
      run(
          () -> {
            lock();
            var state = requiredState();
            if (state.path("paused").asBoolean()) return null;
            Instant now = timeGuard.sample(databaseTime.get());
            var authority = state.path("authority");
            var control = state.path("control");
            verifier.verifyAuthority(authority, control, now);
            var previous = state.path("publication");
            var old = previous.path("keyset").path("payload");
            boolean initial = previous.isMissingNode();
            boolean authorityChanged =
                !initial
                    && !text(old, "authority_digest").equals(MsgnetV2Verifier.digest(authority));
            boolean controlChanged =
                !initial
                    && !old.path("control_revision")
                        .equals(control.path("payload").path("control_revision"));
            boolean incident =
                !initial
                    && !old.path("generation")
                        .equals(control.path("payload").path("minimum_generation"));
            if (authorityChanged
                && !incident
                && PURPOSES.stream()
                    .anyMatch(
                        p -> {
                          var key = active(old, p);
                          return key == null || !valid(key, now);
                        })) {
              // Complete recovery with the still-allowed former authority, then hand over.
              // Both publications commit under the same realm lock and transaction.
              verifier.verifyAuthority(previous.path("authority"), control, now);
              jdbc.update(
                  "UPDATE collab.msgnet_realm SET runtime_authority = ? WHERE tenant_id = ? AND realm = ?",
                  canonical(previous.path("authority")),
                  tenant,
                  realm);
              reconcile();
              jdbc.update(
                  "UPDATE collab.msgnet_realm SET runtime_authority = ? WHERE tenant_id = ? AND realm = ?",
                  canonical(authority),
                  tenant,
                  realm);
              reconcile();
              return null;
            }
            if (!initial
                && !authorityChanged
                && !controlChanged
                && !incident
                && !now.isBefore(time(old, "issued_at"))
                && now.isBefore(
                    time(old, "issued_at").plusSeconds(config.path("check_seconds").asLong()))
                && "none".equals(text(state, "failure_code"))) return null;
            var payload = initial ? object() : ((ObjectNode) old).deepCopy();
            var limits = effectiveLimits(authority);
            String generation = text(control.path("payload"), "minimum_generation");
            String version =
                initial || incident
                    ? text(control.path("payload"), "minimum_keyset_version")
                    : integer(increment(text(old, "keyset_version")))
                        .max(number(control.path("payload"), "minimum_keyset_version"))
                        .toString();
            payload
                .put("schema_version", 2)
                .put("tenant_id", tenant.toString())
                .put("realm", realm)
                .put("authority_id", text(authority.path("payload"), "authority_id"))
                .put("authority_digest", MsgnetV2Verifier.digest(authority))
                .put("control_revision", text(control.path("payload"), "control_revision"))
                .put("generation", generation)
                .put("keyset_version", version)
                .put("issued_at", now.toString())
                .put(
                    "valid_until",
                    now.plusSeconds(
                            Math.min(
                                config.path("keyset_seconds").asLong(),
                                limits.path("keyset_seconds").asLong()))
                        .toString());
            if (initial) {
              payload.putArray("keys");
              payload.putArray("first_publications");
              if (migrationSeed != null) {
                payload.set("keys", migrationSeed.path("keys").deepCopy());
                var proofs =
                    (tools.jackson.databind.node.ArrayNode) payload.path("first_publications");
                for (var key : payload.path("keys"))
                  proofs
                      .addObject()
                      .put("kid", text(key, "kid"))
                      .put("committed_version", version)
                      .put("committed_at", now.toString());
              }
            }
            String mode = initial ? "bootstrap" : "refresh";
            boolean changed = initial || authorityChanged || controlChanged || incident;
            boolean restarted = false;
            boolean activated = false;
            if (!initial && now.isBefore(time(old, "issued_at"))) {
              changed = true;
              for (var key : payload.path("keys")) {
                var proof = find(payload.path("first_publications"), "kid", text(key, "kid"));
                if ("next".equals(text(key, "state"))
                    && proof != null
                    && time(proof, "committed_at").isAfter(now))
                  ((ObjectNode) key).put("state", "retired");
              }
            }
            if (incident) {
              for (var key : payload.path("keys"))
                if (List.of("active", "next").contains(text(key, "state")))
                  ((ObjectNode) key).put("state", "revoked");
            }
            boolean recovering =
                !initial
                    && PURPOSES.stream()
                        .anyMatch(
                            p -> {
                              var key = active(payload, p);
                              return key == null || !valid(key, now);
                            });
            for (String purpose : PURPOSES) {
              if (incident) {
                generate(
                    payload,
                    purpose,
                    "active",
                    now,
                    now.plusSeconds(
                        Math.min(
                            limits.path("restart_key_seconds").asLong(),
                            limits.path("business_key_seconds").asLong())),
                    now,
                    version);
                continue;
              }
              if (authorityChanged) continue;
              var active = active(payload, purpose);
              var next = next(payload, purpose);
              boolean mature =
                  next != null && valid(next, now) && mature(payload, next, limits, now);
              if (active == null || !valid(active, now)) {
                changed = true;
                if (mature) {
                  if (active != null) active.put("state", "retired");
                  next.put("state", "active");
                  activated = true;
                } else {
                  for (var key : payload.path("keys"))
                    if (purpose.equals(text(key, "purpose"))
                        && List.of("active", "next").contains(text(key, "state")))
                      ((ObjectNode) key).put("state", "retired");
                  generate(
                      payload,
                      purpose,
                      "active",
                      now,
                      now.plusSeconds(
                          initial
                              ? Math.min(
                                  config.path("business_key_seconds").asLong(),
                                  limits.path("business_key_seconds").asLong())
                              : Math.min(
                                  limits.path("restart_key_seconds").asLong(),
                                  limits.path("business_key_seconds").asLong())),
                      now,
                      version);
                  if (!initial) restarted = true;
                }
              } else if (!recovering
                  && next != null
                  && mature
                  && !now.isBefore(
                      time(active, "not_before")
                          .plusSeconds(config.path("rotation_seconds").asLong()))) {
                require(
                    !time(active, "not_after")
                        .isBefore(now.plusSeconds(limits.path("overlap_seconds").asLong())),
                    "invalid_transition");
                active.put("state", "retired");
                next.put("state", "active");
                activated = true;
                changed = true;
              } else if (next == null
                  && !now.isBefore(
                      time(active, "not_before")
                          .plusSeconds(
                              config.path("rotation_seconds").asLong()
                                  - config.path("preannounce_seconds").asLong()))) {
                Instant before =
                    time(active, "not_before")
                        .plusSeconds(config.path("rotation_seconds").asLong());
                generate(
                    payload,
                    purpose,
                    "next",
                    before,
                    before.plusSeconds(
                        Math.min(
                            config.path("business_key_seconds").asLong(),
                            limits.path("business_key_seconds").asLong())),
                    now,
                    version);
                changed = true;
              }
            }
            if (!changed
                && !now.isBefore(time(old, "issued_at"))
                && now.isBefore(
                    time(old, "issued_at").plusSeconds(config.path("refresh_seconds").asLong()))
                && "none".equals(text(state, "failure_code"))) return null;
            if (restarted) mode = "restart_recovery";
            else if (activated) mode = "activate";
            if (authorityChanged || incident) mode = "authority_transition";
            if (migrationSeed != null) {
              require(initial, "invalid_transition");
              mode = "migration";
            }
            payload.put("mode", mode);
            timeGuard.sample(databaseTime.get());
            archive(payload, now, limits);
            var publication = object();
            var secret = runtimeKey(authority);
            publication.set(
                "keyset",
                signed(
                    payload,
                    "NetworkKeyset",
                    text(authority.path("payload"), "runtime_kid"),
                    secret));
            publication.set("authority", authority.deepCopy());
            publication.set("control", control.deepCopy());
            if (!initial) {
              var predecessor = publication.putObject("predecessor");
              predecessor.set("keyset", previous.path("keyset"));
              predecessor.set("authority", previous.path("authority"));
              predecessor.set("control", previous.path("control"));
              predecessor.put("keyset_digest", MsgnetV2Verifier.digest(previous.path("keyset")));
            }
            var seen =
                initial
                    ? Seen.empty()
                    : new Seen(
                        text(control.path("payload"), "control_revision"),
                        text(old, "generation"),
                        text(old, "keyset_version"),
                        MsgnetV2Verifier.digest(previous.path("keyset")),
                        MsgnetV2Verifier.digest(previous.path("keyset")),
                        historyProofs());
            verifier.verify(publication, control, seen, now);
            var body = object();
            for (String field :
                List.of(
                    "schema_version",
                    "tenant_id",
                    "realm",
                    "authority_id",
                    "authority_digest",
                    "control_revision",
                    "generation",
                    "keyset_version",
                    "valid_until")) body.set(field, payload.path(field));
            body.put("keyset_digest", MsgnetV2Verifier.digest(publication.path("keyset")));
            var announcement =
                signed(
                    body,
                    "NetworkKeysetAnnouncement",
                    text(authority.path("payload"), "runtime_kid"),
                    secret);
            MsgnetPolicy.announcement(announcement, publication, true);
            timeGuard.sample(databaseTime.get());
            jdbc.update(
                """
            INSERT INTO collab.msgnet_publication_history
              (id, tenant_id, realm, generation, keyset_version, publication, announcement, committed_at)
            VALUES (?, ?, ?, ?, ?, ?, ?, ?)
            """,
                UuidV7.next(clock),
                tenant,
                realm,
                new BigDecimal(generation),
                new BigDecimal(version),
                canonical(publication),
                canonical(announcement),
                java.sql.Timestamp.from(now));
            jdbc.update(
                """
            UPDATE collab.msgnet_realm SET publication = ?, announcement = ?, generation = ?, keyset_version = ?, legacy_disabled = true, failure_code = 'none'
            WHERE tenant_id = ? AND realm = ?
            """,
                canonical(publication),
                canonical(announcement),
                new BigDecimal(generation),
                new BigDecimal(version),
                tenant,
                realm);
            audit("collab.msgnet.renewed", "none", java.util.Map.of());
            return null;
          });
    } catch (AurigaException e) {
      run(
          () -> {
            var previousFailure = text(requiredState(), "failure_code");
            jdbc.update(
                "UPDATE collab.msgnet_realm SET failure_code = ? WHERE tenant_id = ? AND realm = ?",
                e.errorCode().code(),
                tenant,
                realm);
            if (!e.errorCode().code().equals(previousFailure))
              audit("collab.msgnet.renewal_failed", e.errorCode().code(), java.util.Map.of());
            return null;
          });
      throw e;
    }
  }

  private JsonNode effectiveLimits(JsonNode authority) {
    var limits = (ObjectNode) authority.path("payload").path("limits").deepCopy();
    limits.put(
        "business_key_seconds",
        Math.min(
            limits.path("business_key_seconds").asLong(),
            config.path("business_key_seconds").asLong()));
    limits.put(
        "keyset_seconds",
        Math.min(limits.path("keyset_seconds").asLong(), config.path("keyset_seconds").asLong()));
    limits.put(
        "next_maturity_seconds",
        Math.max(
            limits.path("next_maturity_seconds").asLong(),
            config.path("maturity_seconds").asLong()));
    limits.put(
        "overlap_seconds",
        Math.max(limits.path("overlap_seconds").asLong(), config.path("overlap_seconds").asLong()));
    return limits;
  }

  private void generate(
      ObjectNode payload,
      String purpose,
      String state,
      Instant before,
      Instant after,
      Instant now,
      String version) {
    try {
      var pair = KeyPairGenerator.getInstance("Ed25519").generateKeyPair();
      String kid = UUID.randomUUID().toString();
      String spki =
          Base64.getUrlEncoder().withoutPadding().encodeToString(pair.getPublic().getEncoded());
      byte[] bytes = pair.getPrivate().getEncoded();
      try {
        secrets.install(aad(kid, purpose), spki, bytes);
      } finally {
        Arrays.fill(bytes, (byte) 0);
      }
      ((tools.jackson.databind.node.ArrayNode) payload.path("keys"))
          .add(
              object()
                  .put("kid", kid)
                  .put("alg", "Ed25519")
                  .put("purpose", purpose)
                  .put("state", state)
                  .put("public_key_spki", spki)
                  .put("not_before", before.toString())
                  .put("not_after", after.toString()));
      ((tools.jackson.databind.node.ArrayNode) payload.path("first_publications"))
          .add(
              object()
                  .put("kid", kid)
                  .put("committed_version", version)
                  .put("committed_at", now.toString()));
    } catch (java.security.GeneralSecurityException e) {
      throw failure("invalid_chain");
    }
  }

  private void archive(ObjectNode payload, Instant now, JsonNode limits) {
    Instant before = now.minusSeconds(limits.path("archive_grace_seconds").asLong());
    persistHistory(payload);
    var keys = list(payload.path("keys"));
    var proofs = list(payload.path("first_publications"));
    var retained = payload.putArray("keys");
    var retainedProofs = payload.putArray("first_publications");
    for (var key : keys) {
      if (!time(key, "not_after").isAfter(before) && !"active".equals(text(key, "state"))) continue;
      retained.add(key);
      var proof = find(proofs, "kid", text(key, "kid"));
      if (proof != null && !time(proof, "committed_at").isAfter(now)) retainedProofs.add(proof);
    }
    var removed = object().putArray("removed");
    for (var key : keys) if (find(retained, "kid", text(key, "kid")) == null) removed.add(key);
    MsgnetPolicy.archive(
        now,
        before,
        limits.path("archive_grace_seconds").asLong(),
        removed,
        object().putArray("tombstones"),
        object().putArray("reappeared"));
    try {
      var digest = java.security.MessageDigest.getInstance("SHA-256");
      digest.update((byte) '[');
      boolean[] comma = {false};
      jdbc.query(
          "SELECT key_material, first_publication FROM collab.msgnet_key_history WHERE tenant_id = ? AND realm = ? ORDER BY kid",
          (org.springframework.jdbc.core.RowCallbackHandler)
              rs -> {
                if (comma[0]) digest.update((byte) ',');
                var entry = object();
                entry.set("key", NetworkJson.parse(rs.getString(1), 1048576));
                entry.set("first_publication", NetworkJson.parse(rs.getString(2), 1048576));
                digest.update(canonical(entry).getBytes(java.nio.charset.StandardCharsets.UTF_8));
                comma[0] = true;
              },
          tenant,
          realm);
      digest.update((byte) ']');
      payload
          .putObject("archive")
          .put("before", before.toString())
          .put("history_digest", "sha256:" + java.util.HexFormat.of().formatHex(digest.digest()));
    } catch (java.security.GeneralSecurityException e) {
      throw new IllegalStateException(e);
    }
  }

  private void persistHistory(JsonNode payload) {
    for (var key : payload.path("keys")) {
      var proof = find(payload.path("first_publications"), "kid", text(key, "kid"));
      var known =
          jdbc.query(
              "SELECT key_material, first_publication FROM collab.msgnet_key_history WHERE tenant_id = ? AND realm = ? AND kid = ?",
              (rs, n) ->
                  List.of(
                      NetworkJson.parse(rs.getString(1), 1048576),
                      NetworkJson.parse(rs.getString(2), 1048576)),
              tenant,
              realm,
              text(key, "kid"));
      if (!known.isEmpty()) {
        var old = known.getFirst().getFirst();
        if (proof == null) proof = known.getFirst().get(1);
        require(known.getFirst().get(1).equals(proof), "invalid_transition");
        for (String field :
            List.of("alg", "kid", "purpose", "public_key_spki", "not_before", "not_after"))
          require(old.path(field).equals(key.path(field)), "invalid_transition");
        if (List.of("retired", "revoked").contains(text(old, "state")))
          require(old.path("state").equals(key.path("state")), "invalid_transition");
      }
      require(proof != null, "invalid_transition");
      jdbc.update(
          """
          INSERT INTO collab.msgnet_key_history (id, tenant_id, realm, kid, public_key_spki, key_material, first_publication)
          VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT (tenant_id, realm, kid) DO UPDATE SET key_material = EXCLUDED.key_material
          """,
          UuidV7.next(clock),
          tenant,
          realm,
          text(key, "kid"),
          text(key, "public_key_spki"),
          canonical(key),
          canonical(proof));
    }
  }

  private List<JsonNode> historyProofs() {
    return jdbc.query(
        "SELECT first_publication FROM collab.msgnet_key_history WHERE tenant_id = ? AND realm = ?",
        (rs, n) -> NetworkJson.parse(rs.getString(1), 1048576),
        tenant,
        realm);
  }

  private boolean mature(JsonNode payload, JsonNode next, JsonNode limits, Instant now) {
    var proof = find(payload.path("first_publications"), "kid", text(next, "kid"));
    return proof != null
        && !time(proof, "committed_at")
            .isAfter(now.minusSeconds(limits.path("next_maturity_seconds").asLong()));
  }

  private static ObjectNode active(JsonNode payload, String purpose) {
    return stateKey(payload, purpose, "active");
  }

  private static ObjectNode next(JsonNode payload, String purpose) {
    return stateKey(payload, purpose, "next");
  }

  private static ObjectNode stateKey(JsonNode payload, String purpose, String state) {
    for (var key : payload.path("keys"))
      if (purpose.equals(text(key, "purpose")) && state.equals(text(key, "state")))
        return (ObjectNode) key;
    return null;
  }

  ObjectNode aad(String kid, String purpose) {
    return object()
        .put("tenant_id", tenant.toString())
        .put("realm", realm)
        .put("kid", kid)
        .put("alg", "Ed25519")
        .put("purpose", purpose)
        .put("component", "protocol_response".equals(purpose) ? "network" : "collab");
  }

  private PrivateKey runtimeKey(JsonNode authority) {
    byte[] bytes =
        secrets.read(aad(text(authority.path("payload"), "runtime_kid"), "runtime_authority"));
    try {
      return KeyFactory.getInstance("Ed25519").generatePrivate(new PKCS8EncodedKeySpec(bytes));
    } catch (java.security.GeneralSecurityException e) {
      throw failure("invalid_chain");
    } finally {
      Arrays.fill(bytes, (byte) 0);
    }
  }

  /**
   * Loads only committed material. Each business signature independently checks the authoritative
   * PG row.
   */
  public void reload(auriga.collab.app.NetworkSigningKeys keys) {
    var state =
        run(
            () -> {
              var current = requiredState();
              require(!current.path("paused").asBoolean(), "projection_pending");
              var publication = current.path("publication");
              var k = publication.path("keyset").path("payload");
              verifier.verify(
                  publication,
                  current.path("control"),
                  new Seen(
                      text(current.path("control").path("payload"), "control_revision"),
                      text(k, "generation"),
                      text(k, "keyset_version"),
                      MsgnetV2Verifier.digest(publication.path("keyset")),
                      "",
                      historyProofs()),
                  clock.instant());
              return current;
            });
    var payload = (ObjectNode) state.path("publication").path("keyset").path("payload");
    var material = new java.util.LinkedHashMap<String, PrivateKey>();
    for (var key : payload.path("keys")) {
      if (!List.of("active", "next").contains(text(key, "state"))) continue;
      byte[] bytes = secrets.read(aad(text(key, "kid"), text(key, "purpose")));
      try {
        material.put(
            text(key, "kid"),
            KeyFactory.getInstance("Ed25519").generatePrivate(new PKCS8EncodedKeySpec(bytes)));
      } catch (java.security.GeneralSecurityException e) {
        throw failure("invalid_chain");
      } finally {
        Arrays.fill(bytes, (byte) 0);
      }
    }
    keys.installRuntimeSnapshot(
        payload,
        material,
        (snapshot, signature) ->
            run(
                () -> {
                  jdbc.queryForObject(
                      "SELECT id FROM collab.msgnet_realm WHERE tenant_id = ? AND realm = ? FOR SHARE",
                      UUID.class,
                      tenant,
                      realm);
                  var current = requiredState();
                  require(!current.path("paused").asBoolean(), "projection_pending");
                  var signedPayload = current.path("publication").path("keyset").path("payload");
                  var rootPolicy = current.path("control").path("payload");
                  require(
                      signedPayload.path("generation").equals(rootPolicy.path("minimum_generation"))
                          && number(signedPayload, "keyset_version")
                                  .compareTo(number(rootPolicy, "minimum_keyset_version"))
                              >= 0,
                      "version_rollback");
                  timeGuard.sample(databaseTime.get());
                  verifier.verifyAuthority(
                      current.path("publication").path("authority"),
                      current.path("control"),
                      clock.instant());
                  require(
                      canonical(current.path("publication").path("keyset").path("payload"))
                          .equals(snapshot.publicKeysJson(realm)),
                      "version_rollback");
                  require(
                      time(
                              current.path("publication").path("keyset").path("payload"),
                              "valid_until")
                          .isAfter(clock.instant()),
                      "invalid_transition");
                  return signature.get();
                }));
  }

  public void pushPending(NetworkKeyPush transport) {
    var pending =
        run(
            () ->
                jdbc.query(
                    "SELECT id, publication, announcement FROM collab.msgnet_publication_history WHERE tenant_id = ? AND realm = ? AND pending_push = true ORDER BY generation DESC, keyset_version DESC LIMIT 1",
                    (rs, n) -> List.of(rs.getString(1), rs.getString(2), rs.getString(3)),
                    tenant,
                    realm));
    for (var row : pending) {
      var publication = NetworkJson.parse(row.get(1), 1048576);
      require(canonical(publication).equals(publication()), "version_rollback");
      transport.broadcast(realm, canonical(publication), row.get(2));
      run(
          () -> {
            jdbc.update(
                "UPDATE collab.msgnet_publication_history SET pending_push = false WHERE tenant_id = ? AND realm = ? AND id = ? AND publication = ?",
                tenant,
                realm,
                UUID.fromString(row.get(0)),
                row.get(1));
            return null;
          });
    }
  }

  public void paused(boolean value) {
    run(
        () -> {
          jdbc.update(
              "UPDATE collab.msgnet_realm SET paused = ? WHERE tenant_id = ? AND realm = ?",
              value,
              tenant,
              realm);
          return null;
        });
  }

  static ObjectNode signed(JsonNode payload, String type, String kid, PrivateKey secret) {
    try {
      require(
          List.of("NetworkKeyset", "NetworkKeysetAnnouncement", "NetworkTransportTrustBundle")
              .contains(type),
          "scope_denied");
      var signer = Signature.getInstance("Ed25519");
      signer.initSign(secret);
      signer.update(
          ("Auriga." + type + ".v2\0" + canonical(payload))
              .getBytes(java.nio.charset.StandardCharsets.UTF_8));
      var result = object();
      result.set("payload", payload.deepCopy());
      result
          .putObject("runtime_auth")
          .put("alg", "Ed25519")
          .put("kid", kid)
          .put("signature", Base64.getUrlEncoder().withoutPadding().encodeToString(signer.sign()));
      return result;
    } catch (java.security.GeneralSecurityException e) {
      throw failure("invalid_chain");
    }
  }

  static ObjectNode object() {
    return JsonMapper.builder().build().createObjectNode();
  }

  static String canonical(JsonNode node) {
    return NetworkJson.canonical(node, 1048576);
  }

  private static String increment(String value) {
    var next = integer(value).add(BigInteger.ONE);
    require(next.bitLength() <= 64, "version_rollback");
    return next.toString();
  }

  void lock() {
    jdbc.queryForObject(
        "SELECT pg_advisory_xact_lock(hashtextextended(?, 0))",
        Object.class,
        tenant + ":network-keys:" + realm);
  }

  Instant checkedTime() {
    Instant now = timeGuard.sample(databaseTime.get());
    var state = requiredState();
    verifier.verifyAuthority(state.path("authority"), state.path("control"), now);
    return now;
  }

  ObjectNode signTransport(JsonNode payload) {
    var authority = requiredState().path("authority");
    capability(authority.path("payload"), "network_transport");
    return signed(
        payload,
        "NetworkTransportTrustBundle",
        text(authority.path("payload"), "runtime_kid"),
        runtimeKey(authority));
  }

  <T> T run(Supplier<T> work) {
    require(TenantContext.current().map(tenant::equals).orElse(true), "scope_denied");
    return TenantContext.call(
        tenant,
        () ->
            tx.execute(
                s -> {
                  TenantTransactionManager.verifyTransactionTenant();
                  return work.get();
                }));
  }

  ObjectNode requiredState() {
    var state = stored();
    require(state != null, "authority_unknown");
    return state;
  }

  private ObjectNode stored() {
    var rows =
        jdbc.query(
            "SELECT root_control, runtime_authority, publication, announcement, paused, failure_code, transport_failure_code FROM collab.msgnet_realm WHERE tenant_id = ? AND realm = ?",
            (rs, n) -> {
              var state = object();
              state.set("control", NetworkJson.parse(rs.getString(1), 1048576));
              state.set("authority", NetworkJson.parse(rs.getString(2), 1048576));
              if (rs.getString(3) != null)
                state.set("publication", NetworkJson.parse(rs.getString(3), 1048576));
              if (rs.getString(4) != null)
                state.set("announcement", NetworkJson.parse(rs.getString(4), 1048576));
              state
                  .put("paused", rs.getBoolean(5))
                  .put("failure_code", rs.getString(6))
                  .put("transport_failure_code", rs.getString(7));
              return state;
            },
            tenant,
            realm);
    return rows.isEmpty() ? null : rows.getFirst();
  }

  public String publication() {
    return run(
        () -> {
          var s = requiredState();
          var p = s.path("publication");
          verifier.verifyAuthority(p.path("authority"), s.path("control"), clock.instant());
          require(
              time(p.path("keyset").path("payload"), "valid_until").isAfter(clock.instant()),
              "invalid_transition");
          return canonical(p);
        });
  }

  public String announcement() {
    return run(
        () -> {
          publication();
          return canonical(requiredState().path("announcement"));
        });
  }

  public String isolation(String targetId) {
    return run(
        () -> {
          var rows =
              jdbc.queryForList(
                  "SELECT isolation_record FROM collab.msgnet_control_target WHERE tenant_id = ? AND realm = ? AND target_id = ?",
                  String.class,
                  tenant,
                  realm,
                  targetId);
          require(!rows.isEmpty(), "identity_quarantined");
          return rows.getFirst();
        });
  }

  void audit(String action, String failure, java.util.Map<String, Object> details) {
    var state = requiredState();
    var authority = state.path("authority").path("payload");
    var entry =
        auriga.common.audit.AuditEntry.builder(action)
            .target("message_network", realm)
            .actor(auriga.common.audit.AuditEntry.Actor.SYSTEM)
            .detail("tenant_id", tenant.toString())
            .detail("realm", realm)
            .detail("authority_id", text(authority, "authority_id"))
            .detail(
                "review_due_at",
                time(authority, "not_before").plusSeconds(365L * 86400).toString());
    details.forEach(entry::detail);
    if (!"none".equals(failure)) entry.failure(failure);
    audit.record(entry.build());
  }

  void transportFailure(RuntimeException failure) {
    run(
        () -> {
          lock();
          String code =
              failure == null
                  ? "none"
                  : failure instanceof AurigaException a
                      ? a.errorCode().code()
                      : "collab.msgnet_projection_pending";
          String previous = text(requiredState(), "transport_failure_code");
          jdbc.update(
              "UPDATE collab.msgnet_realm SET transport_failure_code = ? WHERE tenant_id = ? AND realm = ?",
              code,
              tenant,
              realm);
          if (!code.equals(previous))
            audit(
                failure == null
                    ? "collab.msgnet.transport_recovered"
                    : "collab.msgnet.transport_failed",
                code,
                java.util.Map.of());
          return null;
        });
  }

  void requireCallerTenant() {
    auriga.common.context.TenantContext.current()
        .ifPresent(current -> require(tenant.equals(current), "scope_denied"));
    auriga.common.security.AuthorizationRules.currentPrincipal()
        .ifPresent(principal -> require(tenant.equals(principal.tenantId()), "scope_denied"));
  }

  public String status() {
    return run(() -> status(requiredState()));
  }

  public String publicControl() {
    return run(
        () -> {
          var control = requiredState().path("control");
          verifier.verifyControl(control);
          return canonical(control);
        });
  }

  private String status(JsonNode state) {
    var k = state.path("publication").path("keyset").path("payload");
    var a = state.path("authority").path("payload");
    Instant due = time(a, "not_before").plusSeconds(365L * 86400);
    String failureCode = text(state, "failure_code");
    if ("none".equals(failureCode) && !text(state, "transport_failure_code").isEmpty())
      failureCode = text(state, "transport_failure_code");
    if (contains(
        state.path("control").path("payload").path("revoked_authority_ids"),
        text(a, "authority_id"))) failureCode = "collab.msgnet_authority_revoked";
    if (state.path("publication").isNull() || state.path("publication").isMissingNode()) {
      var unpublished =
          object()
              .put("tenant_id", tenant.toString())
              .put("realm", realm)
              .put("authority_id", text(a, "authority_id"))
              .put("expiry_policy", "no_hard_expiry")
              .put(
                  "control_revision",
                  text(state.path("control").path("payload"), "control_revision"))
              .put("review_due_at", due.toString())
              .put("review_required", !clock.instant().isBefore(due))
              .put("announcement_confirmed", false)
              .put("has_publication", false)
              .put("failure_code", failureCode)
              .put(
                  "state",
                  state.path("paused").asBoolean()
                      ? "paused"
                      : (timeGuard.clockError() || "collab.msgnet_clock_error".equals(failureCode))
                          ? "clock_error"
                          : "collab.msgnet_authority_revoked".equals(failureCode)
                              ? "authority_revoked"
                              : "none".equals(failureCode) ? "renewing" : "degraded");
      schema("RenewalStatusOutput", unpublished, "invalid_chain");
      return canonical(unpublished);
    }
    if ("none".equals(failureCode) && !clock.instant().isBefore(time(k, "valid_until")))
      failureCode = "collab.msgnet_invalid_transition";
    boolean announced =
        Boolean.TRUE.equals(
            jdbc.queryForObject(
                "SELECT NOT pending_push FROM collab.msgnet_publication_history WHERE tenant_id = ? AND realm = ? AND generation = ? AND keyset_version = ?",
                Boolean.class,
                tenant,
                realm,
                new BigDecimal(text(k, "generation")),
                new BigDecimal(text(k, "keyset_version"))));
    boolean projected = false;
    var bundles =
        jdbc.queryForList(
            "SELECT bundle FROM collab.msgnet_transport WHERE tenant_id = ? AND realm = ?",
            String.class,
            tenant,
            realm);
    if (!bundles.isEmpty()) {
      var bundle = NetworkJson.parse(bundles.getFirst(), 1048576);
      var body = bundle.path("payload");
      projected = "retire_old".equals(text(body, "phase"));
      for (var reader : body.path("required_reader_ids")) {
        var isolated =
            jdbc.queryForList(
                "SELECT isolation_record FROM collab.msgnet_control_target WHERE tenant_id = ? AND realm = ? AND identity_id = ? AND enforcement_confirmed = true",
                String.class,
                tenant,
                realm,
                reader.asString());
        if (!isolated.isEmpty()
            && "quarantined".equals(text(NetworkJson.parse(isolated.getFirst(), 1048576), "state")))
          continue;
        var receipts =
            jdbc.queryForList(
                "SELECT receipt FROM collab.msgnet_projection_receipt WHERE tenant_id = ? AND realm = ? AND installation_id = ? AND bundle_version = ?",
                String.class,
                tenant,
                realm,
                reader.asString(),
                new BigDecimal(text(body, "bundle_version")));
        if (receipts.isEmpty()) {
          projected = false;
          continue;
        }
        var receipt = NetworkJson.parse(receipts.getFirst(), 1048576);
        if (!List.of("loaded", "handshake_verified").contains(text(receipt, "phase"))
            || !text(receipt, "publication_digest")
                .equals(MsgnetV2Verifier.digest(state.path("publication").path("keyset"))))
          projected = false;
      }
    }
    var out =
        object()
            .put("tenant_id", tenant.toString())
            .put("realm", realm)
            .put("authority_id", text(a, "authority_id"))
            .put("expiry_policy", "no_hard_expiry")
            .put("generation", text(k, "generation"))
            .put("keyset_version", text(k, "keyset_version"))
            .put(
                "control_revision", text(state.path("control").path("payload"), "control_revision"))
            .put("review_due_at", due.toString())
            .put("review_required", !clock.instant().isBefore(due))
            .put(
                "next_renewal_at",
                time(k, "issued_at")
                    .plusSeconds(config.path("refresh_seconds").asLong())
                    .toString())
            .put("last_success_at", text(k, "issued_at"))
            .put(
                "business_valid_until",
                PURPOSES.stream()
                    .map(p -> time(active(k, p), "not_after"))
                    .min(Instant::compareTo)
                    .orElseThrow()
                    .toString())
            .put("announcement_confirmed", announced)
            .put("failure_code", failureCode)
            .put(
                "state",
                state.path("paused").asBoolean()
                    ? "paused"
                    : (timeGuard.clockError() || "collab.msgnet_clock_error".equals(failureCode))
                        ? "clock_error"
                        : "collab.msgnet_authority_revoked".equals(failureCode)
                            ? "authority_revoked"
                            : !"none".equals(failureCode)
                                ? "degraded"
                                : announced && projected ? "ready" : "projection_pending");
    schema("RenewalStatus", out, "invalid_chain");
    return canonical(out);
  }
}
