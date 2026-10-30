package auriga.collab.network.keys;

import auriga.collab.app.NetworkJson;
import auriga.common.context.TenantContext;
import auriga.common.persistence.TenantTransactionManager;
import java.io.StringReader;
import java.math.BigInteger;
import java.nio.file.Files;
import java.nio.file.LinkOption;
import java.nio.file.Path;
import java.security.KeyFactory;
import java.security.spec.X509EncodedKeySpec;
import java.time.Clock;
import java.time.Instant;
import java.util.HashMap;
import java.util.HashSet;
import java.util.Map;
import java.util.Properties;
import java.util.Set;
import java.util.UUID;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.jdbc.datasource.DriverManagerDataSource;
import org.springframework.transaction.support.TransactionTemplate;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** Fixed local operator commands. No Spring server, arbitrary SQL, or root signing key. */
public final class NetworkKeyOperator {
  private final NetworkKeySettings settings;
  private final RootKeyPublication trust;
  private final Clock clock;
  private final JdbcTemplate jdbc;
  private final TenantTransactionManager manager;
  private final NetworkKeyOperations operations;
  private final JsonNode historyCheckpoint;
  private final String stateCheckpoint;

  NetworkKeyOperator(NetworkKeySettings settings, javax.sql.DataSource source, Clock clock) {
    this(settings, source, clock, null);
  }

  NetworkKeyOperator(
      NetworkKeySettings settings,
      javax.sql.DataSource source,
      Clock clock,
      JsonNode historyCheckpoint) {
    this(settings, source, clock, historyCheckpoint, null);
  }

  NetworkKeyOperator(
      NetworkKeySettings settings,
      javax.sql.DataSource source,
      Clock clock,
      JsonNode historyCheckpoint,
      String stateCheckpoint) {
    this.settings = settings;
    this.clock = clock;
    this.historyCheckpoint = historyCheckpoint;
    this.stateCheckpoint = stateCheckpoint;
    try {
      check(settings.rootPublicFile(), false);
      var root =
          KeyFactory.getInstance("Ed25519")
              .generatePublic(
                  new X509EncodedKeySpec(ProtectedKeyFiles.read(settings.rootPublicFile(), 1024)));
      trust =
          new RootKeyPublication(
              settings.realm(), settings.rootKid(), root, settings.rootFingerprint(), clock);
    } catch (java.security.GeneralSecurityException e) {
      throw RootKeyPublication.invalid();
    }
    jdbc = source == null ? null : new JdbcTemplate(source);
    manager = source == null ? null : new TenantTransactionManager(source);
    operations =
        source == null
            ? null
            : new NetworkKeyOperations(
                jdbc,
                manager,
                settings.tenant(),
                settings.realm(),
                trust,
                settings.minimumVersion(),
                clock);
  }

  RootKeyPublication.Verified verify(RootKeyPublication.Release release, boolean archived) {
    var verified =
        archived
            ? trust.verifyArchived(release.signedKeyset(), release.announcement())
            : trust.verify(release.signedKeyset(), release.announcement());
    var version = new BigInteger(verified.payload().path("keyset_version").asString());
    if (version.compareTo(new BigInteger(settings.minimumVersion())) < 0
        || version.compareTo(BigInteger.valueOf(Long.MAX_VALUE)) > 0)
      throw RootKeyPublication.invalid();
    return verified;
  }

  /** The expected release is an archive checkpoint, never a supplied first_published proof. */
  ObjectNode status(RootKeyPublication.Release expected) {
    return locked(() -> snapshot(expected));
  }

  ObjectNode publish(RootKeyPublication.Release expected, RootKeyPublication.Release next) {
    verify(next, false);
    return signerStopped(
        () ->
            locked(
                () -> {
                  snapshot(
                      expected); // Refuse an empty, foreign, or unexpectedly advanced database.
                  operations.publish(
                      next); // All history/time/state decisions remain production decisions.
                  return snapshot(next);
                }));
  }

  private ObjectNode signerStopped(java.util.function.Supplier<ObjectNode> operation) {
    Path original =
        settings.stateFile().resolveSibling(settings.stateFile().getFileName() + ".lock");
    check(original, false);
    try (var channel =
            java.nio.channels.FileChannel.open(
                original, java.nio.file.StandardOpenOption.WRITE, LinkOption.NOFOLLOW_LINKS);
        var lock = channel.tryLock()) {
      if (lock == null) throw ProtectedKeyFiles.invalid();
      return operation.get();
    } catch (java.io.IOException e) {
      throw ProtectedKeyFiles.invalid();
    }
  }

  void preflight(RootKeyPublication.Release expected, RootKeyPublication.Release next) {
    verify(next, false);
    locked(
        () -> {
          snapshot(expected);
          operations.publish(next);
          return snapshot(next);
        },
        true);
  }

  /** Used for preflight and install, with the real loader, including its private/public proof. */
  void validatePrivate(
      RootKeyPublication.Release release, Path temporaryState, Map<String, String> secrets)
      throws java.io.IOException {
    var payload = verify(release, false).payload();
    checkSecrets(secrets);
    if (Files.exists(temporaryState, LinkOption.NOFOLLOW_LINKS)) throw ProtectedKeyFiles.invalid();
    SigningKeyLoader.installState(payload, temporaryState);
    try (var keys =
        SigningKeyLoader.load(
            payload,
            temporaryState,
            secrets,
            clock,
            (ignored, signature) -> {
              throw ProtectedKeyFiles.invalid();
            })) {
      // Loading proves the complete snapshot and every active/next private key. No signature
      // issued.
    }
  }

  ObjectNode install(RootKeyPublication.Release release, Path state, Map<String, String> secrets) {
    var payload = verify(release, false).payload();
    checkSecrets(secrets);
    try {
      Path directory =
          Files.createTempDirectory(state.toAbsolutePath().getParent(), "operator-check-");
      Path temporary = directory.resolve("state.json");
      try {
        validatePrivate(release, temporary, secrets);
      } finally {
        Files.deleteIfExists(temporary);
        Files.deleteIfExists(directory.resolve("state.json.lock"));
        Files.deleteIfExists(directory);
      }
    } catch (java.io.IOException e) {
      throw ProtectedKeyFiles.invalid();
    }
    return locked(
        () -> {
          var result = snapshot(release);
          try {
            // The persistent lock is at the original state path; never copied or unlinked.
            SigningKeyLoader.installState(payload, state);
            try (var keys =
                SigningKeyLoader.load(
                    payload,
                    state,
                    secrets,
                    clock,
                    (publicKeys, signature) ->
                        operations.signApproved(
                            publicKeys.publicKeysJson(settings.realm()), signature))) {
              // Restarts use exactly the same loader and signing guard.
            }
          } catch (java.io.IOException e) {
            throw ProtectedKeyFiles.invalid();
          }
          return result;
        });
  }

  private void checkSecrets(Map<String, String> secrets) {
    for (var path : secrets.values()) check(Path.of(path), true);
  }

  private ObjectNode locked(java.util.function.Supplier<ObjectNode> operation) {
    return locked(operation, false);
  }

  private ObjectNode locked(java.util.function.Supplier<ObjectNode> operation, boolean rollback) {
    if (jdbc == null) throw ProtectedKeyFiles.invalid();
    return TenantContext.call(
        settings.tenant(),
        () ->
            new TransactionTemplate(manager)
                .execute(
                    transaction -> {
                      jdbc.queryForObject(
                          "SELECT pg_advisory_xact_lock(hashtextextended(?, 0))",
                          Object.class,
                          settings.tenant() + ":network-keys:" + settings.realm());
                      var result = operation.get();
                      if (rollback) transaction.setRollbackOnly();
                      return result;
                    }));
  }

  private ObjectNode snapshot(RootKeyPublication.Release expected) {
    var archived = verify(expected, true);
    Boolean restricted =
        jdbc.queryForObject(
            """
            SELECT NOT r.rolsuper AND NOT r.rolbypassrls AND NOT pg_has_role(current_user, c.relowner, 'USAGE')
          FROM pg_roles r JOIN pg_class c ON c.relname = 'network_key_publication'
          JOIN pg_namespace n ON n.oid = c.relnamespace AND n.nspname = 'collab'
         WHERE r.rolname = current_user
        """,
            Boolean.class);
    if (!Boolean.TRUE.equals(restricted)) throw ProtectedKeyFiles.invalid();
    var rows =
        jdbc.query(
            """
            SELECT keyset_version, signed_keyset, announcement, first_published::text,
                   pending_push, clock_timestamp()
              FROM collab.network_key_publication WHERE tenant_id = ? AND realm = ? FOR SHARE
            """,
            (rs, index) -> {
              var actual =
                  verify(new RootKeyPublication.Release(rs.getString(2), rs.getString(3)), true);
              if (!actual.release().equals(archived.release())
                  || !rs.getBigDecimal(1)
                      .toBigIntegerExact()
                      .toString()
                      .equals(actual.payload().path("keyset_version").asString()))
                throw RootKeyPublication.invalid();
              check(settings.stateFile(), false);
              byte[] localBytes = ProtectedKeyFiles.read(settings.stateFile(), 65536);
              if (stateCheckpoint != null) {
                try {
                  String actualHash =
                      java.util.HexFormat.of()
                          .formatHex(
                              java.security.MessageDigest.getInstance("SHA-256")
                                  .digest(localBytes));
                  if (!actualHash.equals(stateCheckpoint)) throw ProtectedKeyFiles.invalid();
                } catch (java.security.GeneralSecurityException e) {
                  throw ProtectedKeyFiles.invalid();
                }
              }
              var local = NetworkJson.parse(localBytes);
              if (new BigInteger(local.path("version").asString())
                      .compareTo(rs.getBigDecimal(1).toBigIntegerExact())
                  > 0) throw RootKeyPublication.invalid();
              var history = NetworkJson.parse(rs.getString(4));
              Set<String> kids = new HashSet<>();
              for (var key : actual.payload().path("keys")) kids.add(key.path("kid").asString());
              var names = new HashSet<String>();
              history.propertyNames().forEach(names::add);
              if (!history.isObject() || !kids.equals(names)) throw RootKeyPublication.invalid();
              if (historyCheckpoint != null) {
                if (!historyCheckpoint.isObject()) throw RootKeyPublication.invalid();
                for (String kid : historyCheckpoint.propertyNames()) {
                  if (!historyCheckpoint.path(kid).equals(history.path(kid)))
                    throw RootKeyPublication.invalid();
                }
              }
              Instant databaseTime = rs.getTimestamp(6).toInstant();
              for (var name : names) {
                if (Instant.parse(history.path(name).asString()).isAfter(databaseTime))
                  throw RootKeyPublication.invalid();
              }
              var result =
                  tools.jackson.databind.json.JsonMapper.builder().build().createObjectNode();
              result
                  .put("version", rs.getBigDecimal(1).toBigIntegerExact().toString())
                  .put("databaseTime", databaseTime.toString())
                  .put("pendingPush", rs.getBoolean(5))
                  .put("manifestDigest", NetworkJson.digest(actual.payload()));
              result.set("firstPublished", history);
              return result;
            },
            settings.tenant(),
            settings.realm());
    if (rows.size() != 1) throw RootKeyPublication.invalid();
    return rows.getFirst();
  }

  private static void check(Path path, boolean secret) {
    ProtectedKeyFiles.check(path, secret);
    try {
      if (Files.getFileStore(path).supportsFileAttributeView("unix")
          && ((Number) Files.getAttribute(path, "unix:nlink", LinkOption.NOFOLLOW_LINKS))
                  .longValue()
              != 1) throw ProtectedKeyFiles.invalid();
    } catch (java.io.IOException e) {
      throw ProtectedKeyFiles.invalid();
    }
  }

  private static String text(Path path) {
    check(path, false);
    return ProtectedKeyFiles.readText(path);
  }

  /** action plan-file; only paths enter argv. Plan fields are a private local deployment format. */
  public static void main(String[] args) {
    try {
      if (args.length != 2
          || !Set.of("verify", "status", "preflight", "publish", "check-private", "install")
              .contains(args[0])) throw ProtectedKeyFiles.invalid();
      var plan = NetworkJson.parse(text(Path.of(args[1])));
      Set<String> expected =
          Set.of(
              "configFile",
              "rootFile",
              "expectedSignedFile",
              "expectedAnnouncementFile",
              "signedFile",
              "announcementFile",
              "stateFile",
              "privateFiles",
              "expectedHistoryFile",
              "expectedStateSha256");
      if (!plan.isObject() || !new HashSet<>(plan.propertyNames()).equals(expected))
        throw ProtectedKeyFiles.invalid();
      var properties = new Properties();
      Path config = Path.of(plan.path("configFile").asString());
      check(config, true);
      properties.load(
          new StringReader(
              new String(
                  ProtectedKeyFiles.read(config, 65536), java.nio.charset.StandardCharsets.UTF_8)));
      String prefix = "auriga.collab.network.key-operations.";
      var settings =
          new NetworkKeySettings(
              UUID.fromString(properties.getProperty(prefix + "tenant-id")),
              properties.getProperty(prefix + "realm"),
              properties.getProperty(prefix + "root-kid"),
              Path.of(plan.path("rootFile").asString()),
              properties.getProperty(prefix + "root-fingerprint"),
              Path.of(plan.path("signedFile").asString()),
              Path.of(plan.path("announcementFile").asString()),
              Path.of(plan.path("stateFile").asString()),
              properties.getProperty(prefix + "minimum-version"),
              Map.of());
      boolean database = Set.of("status", "preflight", "publish", "install").contains(args[0]);
      var source =
          database
              ? new DriverManagerDataSource(
                  properties.getProperty("spring.datasource.url"),
                  properties.getProperty("spring.datasource.username"),
                  properties.getProperty("spring.datasource.password"))
              : null;
      JsonNode checkpoint =
          plan.path("expectedHistoryFile").asString().isEmpty()
              ? null
              : NetworkJson.parse(text(Path.of(plan.path("expectedHistoryFile").asString())))
                  .path("firstPublished");
      var operator =
          new NetworkKeyOperator(
              settings,
              source,
              Clock.systemUTC(),
              checkpoint,
              plan.path("expectedStateSha256").asString());
      var release =
          new RootKeyPublication.Release(
              text(settings.signedFile()), text(settings.announcementFile()));
      ObjectNode result =
          tools.jackson.databind.json.JsonMapper.builder().build().createObjectNode();
      Map<String, String> secrets = new HashMap<>();
      if (!plan.path("privateFiles").isObject()) throw ProtectedKeyFiles.invalid();
      for (String kid : plan.path("privateFiles").propertyNames()) {
        if (!kid.matches("[A-Za-z0-9._-]{1,128}")) throw ProtectedKeyFiles.invalid();
        secrets.put(kid, plan.path("privateFiles").path(kid).asString());
      }
      switch (args[0]) {
        case "verify" -> operator.verify(release, false);
        case "check-private" -> operator.validatePrivate(release, settings.stateFile(), secrets);
        case "status", "preflight", "publish" -> {
          var previous =
              new RootKeyPublication.Release(
                  text(Path.of(plan.path("expectedSignedFile").asString())),
                  text(Path.of(plan.path("expectedAnnouncementFile").asString())));
          if ("preflight".equals(args[0])) operator.preflight(previous, release);
          else
            result =
                "status".equals(args[0])
                    ? operator.status(previous)
                    : operator.publish(previous, release);
        }
        case "install" -> result = operator.install(release, settings.stateFile(), secrets);
        default -> throw ProtectedKeyFiles.invalid();
      }
      System.out.println(NetworkJson.canonical(result));
    } catch (Exception e) {
      // JDBC/config/key exceptions can contain credentials or paths. Keep the CLI failure bounded.
      System.err.println("Network key operator rejected the operation; no automatic repair");
      System.exit(1);
    }
  }
}
