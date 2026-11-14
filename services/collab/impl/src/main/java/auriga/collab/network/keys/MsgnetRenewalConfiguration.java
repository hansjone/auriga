package auriga.collab.network.keys;

import auriga.collab.app.NetworkJson;
import auriga.collab.app.NetworkSigningKeys;
import auriga.common.persistence.TenantTransactionManager;
import java.nio.file.Path;
import java.time.Clock;
import java.util.Arrays;
import java.util.HashMap;
import java.util.Map;
import java.util.UUID;
import javax.sql.DataSource;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.boot.autoconfigure.condition.ConditionalOnExpression;
import org.springframework.boot.context.properties.bind.Bindable;
import org.springframework.boot.context.properties.bind.Binder;
import org.springframework.boot.sql.init.dependency.DependsOnDatabaseInitialization;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.context.annotation.Import;
import org.springframework.core.env.Environment;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.scheduling.annotation.EnableScheduling;
import org.springframework.scheduling.annotation.Scheduled;
import tools.jackson.databind.JsonNode;

@Configuration(proxyBeanMethods = false)
@ConditionalOnExpression(
    "${auriga.services.collab.enabled:true} && ${auriga.collab.network.auto-renew.enabled:false} && '${auriga.service.schema:}' == 'collab'")
@EnableScheduling
@Import(MsgnetRenewalController.class)
public class MsgnetRenewalConfiguration {
  private static final String PREFIX = "auriga.collab.network.auto-renew.";

  static JsonNode config(Environment env) {
    var result = MsgnetRenewal.object().put("enabled", true);
    for (var entry :
        Map.ofEntries(
                Map.entry("keyset_seconds", 86400),
                Map.entry("refresh_seconds", 43200),
                Map.entry("check_seconds", 60),
                Map.entry("business_key_seconds", 691200),
                Map.entry("rotation_seconds", 432000),
                Map.entry("preannounce_seconds", 172800),
                Map.entry("maturity_seconds", 86400),
                Map.entry("overlap_seconds", 360),
                Map.entry("allowed_skew_seconds", 60),
                Map.entry("review_period_days", 365),
                Map.entry("reload_seconds", 5),
                Map.entry("leaf_days", 90),
                Map.entry("leaf_renew_before_days", 30),
                Map.entry("ca_years", 10),
                Map.entry("ca_renew_before_years", 2),
                Map.entry("forward_jump_seconds", 300),
                Map.entry("backward_jump_seconds", 60),
                Map.entry("clock_retry_seconds", 30),
                Map.entry("migration_margin_seconds", 21600),
                Map.entry("freeze_seconds", 600))
            .entrySet())
      result.put(
          entry.getKey(),
          env.getProperty(
              PREFIX + entry.getKey().replace('_', '-'), Integer.class, entry.getValue()));
    for (String name :
        java.util.List.of(
            "kek_secret_file",
            "kek_id",
            "authority_source",
            "control_source",
            "projector_identity_id",
            "projection_directory"))
      result.put(name, env.getRequiredProperty(PREFIX + name.replace('_', '-')));
    var reminders =
        Binder.get(env)
            .bind(PREFIX + "review-reminder-days", Bindable.listOf(Integer.class))
            .orElse(java.util.List.of(30, 7, 1));
    var array = result.putArray("review_reminder_days");
    reminders.forEach(array::add);
    MsgnetPolicy.config(result);
    return result;
  }

  @Bean(destroyMethod = "close")
  MsgnetKeyEncryption msgnetKeyEncryption(Environment env) {
    var config = config(env);
    var values = new HashMap<String, byte[]>();
    try {
      values.put(
          config.path("kek_id").asString(),
          ProtectedKeyFiles.readSecret(Path.of(config.path("kek_secret_file").asString())));
      Binder.get(env)
          .bind(PREFIX + "previous-kek-files", Bindable.mapOf(String.class, String.class))
          .orElse(Map.of())
          .forEach((id, path) -> values.put(id, ProtectedKeyFiles.readSecret(Path.of(path))));
      return new MsgnetKeyEncryption(config.path("kek_id").asString(), values);
    } finally {
      values.values().forEach(bytes -> Arrays.fill(bytes, (byte) 0));
    }
  }

  @Bean
  @DependsOnDatabaseInitialization
  MsgnetRenewal msgnetRenewal(
      DataSource source,
      TenantTransactionManager manager,
      MsgnetKeyEncryption ring,
      Environment env) {
    var config = config(env);
    String settings = "auriga.collab.network.key-operations.";
    UUID tenant = UUID.fromString(env.getRequiredProperty(settings + "tenant-id"));
    String realm = env.getRequiredProperty(settings + "realm");
    Path rootFile = Path.of(env.getRequiredProperty(settings + "root-public-file"));
    ProtectedKeyFiles.check(rootFile, false);
    var root =
        RootKeyPublication.publicKey(
            java.util.Base64.getUrlEncoder()
                .withoutPadding()
                .encodeToString(ProtectedKeyFiles.read(rootFile, 1024)));
    if (!RootKeyPublication.fingerprint(root)
        .equals(env.getRequiredProperty(settings + "root-fingerprint")))
      throw ProtectedKeyFiles.invalid();
    var trust =
        new MsgnetV2Verifier(
            tenant.toString(), realm, env.getRequiredProperty(settings + "root-kid"), root);
    var jdbc = new JdbcTemplate(source);
    var keys = new MsgnetPrivateKeyStore(jdbc, manager, tenant, realm, ring);
    var service =
        new MsgnetRenewal(jdbc, manager, tenant, realm, trust, keys, Clock.systemUTC(), config);
    var authority =
        NetworkJson.parse(
            ProtectedKeyFiles.readText(Path.of(config.path("authority_source").asString())));
    var control =
        NetworkJson.parse(
            ProtectedKeyFiles.readText(Path.of(config.path("control_source").asString())));
    String runtimeFile = env.getProperty(PREFIX + "runtime-private-file");
    byte[] secret =
        runtimeFile == null
            ? keys.read(
                service.aad(
                    authority.path("payload").path("runtime_kid").asString(), "runtime_authority"))
            : ProtectedKeyFiles.readSecret(Path.of(runtimeFile));
    try {
      service.install(authority, control, secret);
    } finally {
      Arrays.fill(secret, (byte) 0);
    }
    try {
      service.reconcile();
    } catch (auriga.common.error.AurigaException e) {
      org.slf4j.LoggerFactory.getLogger(MsgnetRenewalConfiguration.class)
          .warn("Message network starts fenced with {}", e.errorCode().code());
    }
    return service;
  }

  @Bean(destroyMethod = "close")
  NetworkSigningKeys runtimeNetworkSigningKeys(MsgnetRenewal service) {
    var keys = new NetworkSigningKeys(Clock.systemUTC(), 1);
    try {
      service.reload(keys);
      return keys;
    } catch (auriga.common.error.AurigaException e) {
      return keys;
    }
  }

  @Bean
  MsgnetControlLedger msgnetControlLedger(
      MsgnetRenewal renewal, DataSource source, Environment env) {
    UUID tenant =
        UUID.fromString(env.getRequiredProperty("auriga.collab.network.key-operations.tenant-id"));
    String realm = env.getRequiredProperty("auriga.collab.network.key-operations.realm");
    String file = env.getRequiredProperty(PREFIX + "identities-file");
    var identities =
        MsgnetControlLedger.pins(
            NetworkJson.parse(ProtectedKeyFiles.readText(Path.of(file)), 1048576), tenant, realm);
    return new MsgnetControlLedger(renewal, new JdbcTemplate(source), tenant, realm, identities);
  }

  @Bean
  MsgnetPki msgnetPki(
      MsgnetRenewal renewal,
      DataSource source,
      TenantTransactionManager manager,
      MsgnetKeyEncryption ring,
      Environment env) {
    UUID tenant =
        UUID.fromString(env.getRequiredProperty("auriga.collab.network.key-operations.tenant-id"));
    String realm = env.getRequiredProperty("auriga.collab.network.key-operations.realm");
    var jdbc = new JdbcTemplate(source);
    var pki =
        new MsgnetPki(
            renewal,
            new MsgnetPrivateKeyStore(jdbc, manager, tenant, realm, ring),
            jdbc,
            tenant,
            realm,
            config(env));
    try {
      pki.reconcile();
    } catch (auriga.common.error.AurigaException e) {
      org.slf4j.LoggerFactory.getLogger(MsgnetRenewalConfiguration.class)
          .warn("Message network transport starts fenced with {}", e.errorCode().code());
    }
    return pki;
  }

  @Bean
  MsgnetMaterials msgnetMaterials(
      MsgnetRenewal renewal,
      MsgnetControlLedger controls,
      MsgnetPki pki,
      DataSource source,
      TenantTransactionManager manager,
      MsgnetKeyEncryption ring,
      Environment env) {
    UUID tenant =
        UUID.fromString(env.getRequiredProperty("auriga.collab.network.key-operations.tenant-id"));
    String realm = env.getRequiredProperty("auriga.collab.network.key-operations.realm");
    var jdbc = new JdbcTemplate(source);
    return new MsgnetMaterials(
        renewal,
        controls,
        new MsgnetPrivateKeyStore(jdbc, manager, tenant, realm, ring),
        jdbc,
        tenant,
        realm,
        pki);
  }

  @Bean
  auriga.collab.api.NetworkRenewal networkRenewal(
      MsgnetRenewal renewal, MsgnetControlLedger controls, MsgnetMaterials materials) {
    return new MsgnetNetworkRenewal(renewal, controls, materials);
  }

  @Bean
  Maintenance msgnetMaintenance(
      MsgnetRenewal service,
      NetworkSigningKeys keys,
      ObjectProvider<NetworkKeyPush> transport,
      MsgnetPki pki) {
    return new Maintenance(service, keys, transport, pki);
  }

  static final class Maintenance {
    private final MsgnetRenewal service;
    private final NetworkSigningKeys keys;
    private final ObjectProvider<NetworkKeyPush> transport;
    private final MsgnetPki pki;

    Maintenance(
        MsgnetRenewal service,
        NetworkSigningKeys keys,
        ObjectProvider<NetworkKeyPush> transport,
        MsgnetPki pki) {
      this.service = service;
      this.keys = keys;
      this.transport = transport;
      this.pki = pki;
    }

    @Scheduled(fixedDelayString = "${auriga.collab.network.auto-renew.reload-seconds:5}000")
    public void refresh() {
      try {
        service.reconcile();
        service.reload(keys);
        pki.reconcile();
        var push = transport.getIfAvailable();
        if (push != null) service.pushPending(push);
      } catch (RuntimeException e) {
        org.slf4j.LoggerFactory.getLogger(Maintenance.class)
            .warn("Message network reconciliation failed with {}", e.getClass().getSimpleName());
      }
    }
  }
}
