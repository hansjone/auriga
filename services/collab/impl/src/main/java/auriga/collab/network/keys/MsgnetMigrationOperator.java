package auriga.collab.network.keys;

import auriga.collab.app.NetworkJson;
import auriga.common.persistence.TenantTransactionManager;
import java.nio.charset.StandardCharsets;
import java.nio.file.Path;
import java.time.Clock;
import java.util.*;
import org.springframework.core.env.PropertiesPropertySource;
import org.springframework.core.env.StandardEnvironment;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.jdbc.datasource.DriverManagerDataSource;
import tools.jackson.databind.JsonNode;

/**
 * Fixed local migration/KEK commands. Reads protected files, opens no listener, never loads root
 * secrets.
 */
public final class MsgnetMigrationOperator {
  private MsgnetMigrationOperator() {}

  private static JsonNode input(String path) {
    return NetworkJson.parse(ProtectedKeyFiles.readText(Path.of(path), 1048576), 1048576);
  }

  /**
   * migrate|rewrap protected-plan.json. All secrets are referenced by file, never CLI arguments.
   */
  public static void main(String[] args) throws Exception {
    if (args.length != 2 || !Set.of("migrate", "rewrap", "pause", "resume").contains(args[0]))
      throw ProtectedKeyFiles.invalid();
    var plan = input(args[1]);
    var expected =
        "migrate".equals(args[0])
            ? Set.of(
                "config_file", "permit_file", "runtime_private_file", "ancestor_file", "members")
            : Set.of("config_file");
    if (!plan.isObject() || !new HashSet<>(plan.propertyNames()).equals(expected))
      throw ProtectedKeyFiles.invalid();
    Path configPath = Path.of(plan.path("config_file").asString());
    ProtectedKeyFiles.check(configPath, true);
    var properties = new Properties();
    properties.load(
        new java.io.StringReader(
            new String(ProtectedKeyFiles.read(configPath, 65536), StandardCharsets.UTF_8)));
    var env = new StandardEnvironment();
    env.getPropertySources().addFirst(new PropertiesPropertySource("msgnet-operator", properties));
    String prefix = "auriga.collab.network.key-operations.";
    var tenant = UUID.fromString(env.getRequiredProperty(prefix + "tenant-id"));
    String realm = env.getRequiredProperty(prefix + "realm");
    var source =
        new DriverManagerDataSource(
            env.getRequiredProperty("spring.datasource.url"),
            env.getRequiredProperty("spring.datasource.username"),
            env.getRequiredProperty("spring.datasource.password"));
    var jdbc = new JdbcTemplate(source);
    var manager = new TenantTransactionManager(source);
    if (Set.of("pause", "resume").contains(args[0])) {
      boolean paused = "pause".equals(args[0]);
      auriga.common.context.TenantContext.call(
          tenant,
          () ->
              new org.springframework.transaction.support.TransactionTemplate(manager)
                  .execute(
                      status -> {
                        jdbc.queryForObject(
                            "SELECT pg_advisory_xact_lock(hashtextextended(?, 0))",
                            Object.class,
                            tenant + ":network-keys:" + realm);
                        MsgnetV2Verifier.require(
                            jdbc.update(
                                    "UPDATE collab.msgnet_realm SET paused = ? WHERE tenant_id = ? AND realm = ?",
                                    paused,
                                    tenant,
                                    realm)
                                == 1,
                            "authority_unknown");
                        return null;
                      }));
      System.out.println(
          MsgnetRenewal.canonical(
              MsgnetRenewal.object()
                  .put("tenant_id", tenant.toString())
                  .put("realm", realm)
                  .put("paused", paused)));
      return;
    }
    var configuration = new MsgnetRenewalConfiguration();
    try (var ring = configuration.msgnetKeyEncryption(env)) {
      var store = new MsgnetPrivateKeyStore(jdbc, manager, tenant, realm, ring);
      if ("rewrap".equals(args[0])) {
        System.out.println(MsgnetRenewal.canonical(store.rewrapAll()));
        return;
      }
      var rootPath = Path.of(env.getRequiredProperty(prefix + "root-public-file"));
      ProtectedKeyFiles.check(rootPath, false);
      var root =
          java.security.KeyFactory.getInstance("Ed25519")
              .generatePublic(
                  new java.security.spec.X509EncodedKeySpec(
                      ProtectedKeyFiles.read(rootPath, 1024)));
      var clock = Clock.systemUTC();
      var legacy =
          new RootKeyPublication(
              realm,
              env.getRequiredProperty(prefix + "root-kid"),
              root,
              env.getRequiredProperty(prefix + "root-fingerprint"),
              clock);
      var verifier =
          new MsgnetV2Verifier(
              tenant.toString(), realm, env.getRequiredProperty(prefix + "root-kid"), root);
      var config = MsgnetRenewalConfiguration.config(env);
      var renewal = new MsgnetRenewal(jdbc, manager, tenant, realm, verifier, store, clock, config);
      var members = new ArrayList<MsgnetMigration.Member>();
      byte[] runtime =
          ProtectedKeyFiles.readSecret(Path.of(plan.path("runtime_private_file").asString()));
      try {
        for (var member : plan.path("members")) {
          if (!new HashSet<>(member.propertyNames())
              .equals(Set.of("signed_file", "announcement_file", "private_files")))
            throw ProtectedKeyFiles.invalid();
          var secrets = new HashMap<String, byte[]>();
          var entry =
              new MsgnetMigration.Member(
                  new RootKeyPublication.Release(
                      MsgnetRenewal.canonical(input(member.path("signed_file").asString())),
                      MsgnetRenewal.canonical(input(member.path("announcement_file").asString()))),
                  secrets);
          members.add(entry);
          for (String kid : member.path("private_files").propertyNames())
            secrets.put(
                kid,
                ProtectedKeyFiles.readSecret(
                    Path.of(member.path("private_files").path(kid).asString())));
        }
        var migration = new MsgnetMigration(renewal, jdbc, tenant, realm, legacy, clock);
        var receipt =
            migration.migrate(
                input(plan.path("permit_file").asString()),
                input(config.path("authority_source").asString()),
                input(config.path("control_source").asString()),
                runtime,
                input(plan.path("ancestor_file").asString()),
                members);
        System.out.println(MsgnetRenewal.canonical(receipt));
      } finally {
        Arrays.fill(runtime, (byte) 0);
        members.forEach(
            member -> member.privateKeys().values().forEach(bytes -> Arrays.fill(bytes, (byte) 0)));
      }
    }
  }
}
