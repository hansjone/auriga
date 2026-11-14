package auriga.collab.network.keys;

import auriga.collab.app.NetworkJson;
import auriga.collab.app.NetworkSigningKeys;
import java.nio.file.Path;
import java.time.Clock;
import java.util.Arrays;
import java.util.HashMap;
import java.util.Map;
import java.util.UUID;
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
    "${auriga.services.collab.enabled:true} && ${auriga.collab.network.auto-renew.enabled:false} && ('${auriga.service.schema:}' == 'collab' || ${auriga.platform.combined:false})")
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
  @org.springframework.context.annotation.Lazy
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
  @org.springframework.context.annotation.Lazy
  MsgnetPublicControl msgnetPublicControl(
      org.springframework.context.ApplicationContext context, Environment env) {
    String prefix = "auriga.collab.network.key-operations.";
    var tenant = UUID.fromString(env.getRequiredProperty(prefix + "tenant-id"));
    String realm = env.getRequiredProperty(prefix + "realm");
    Path rootFile = Path.of(env.getRequiredProperty(prefix + "root-public-file"));
    ProtectedKeyFiles.check(rootFile, false);
    var root =
        RootKeyPublication.publicKey(
            java.util.Base64.getUrlEncoder()
                .withoutPadding()
                .encodeToString(ProtectedKeyFiles.read(rootFile, 1024)));
    if (!RootKeyPublication.fingerprint(root)
        .equals(env.getRequiredProperty(prefix + "root-fingerprint")))
      throw ProtectedKeyFiles.invalid();
    return new MsgnetPublicControl(
        new JdbcTemplate(MsgnetDatabase.source(context, env)),
        MsgnetDatabase.manager(context, env),
        tenant,
        realm,
        new MsgnetV2Verifier(
            tenant.toString(), realm, env.getRequiredProperty(prefix + "root-kid"), root),
        Path.of(env.getRequiredProperty(PREFIX + "control-source")));
  }

  @Bean
  @DependsOnDatabaseInitialization
  @org.springframework.context.annotation.Lazy
  MsgnetRenewal msgnetRenewal(
      org.springframework.context.ApplicationContext context,
      MsgnetKeyEncryption ring,
      Environment env) {
    var source = MsgnetDatabase.source(context, env);
    var manager = MsgnetDatabase.manager(context, env);
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
    keys.checkRing();
    var service =
        new MsgnetRenewal(jdbc, manager, tenant, realm, trust, keys, Clock.systemUTC(), config);
    var authority =
        NetworkJson.parse(
            ProtectedKeyFiles.readText(
                Path.of(config.path("authority_source").asString()), 1048576),
            1048576);
    var control = context.getBean(MsgnetPublicControl.class).refresh();
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
  NetworkSigningKeys runtimeNetworkSigningKeys() {
    // Message signing stays unavailable until the first successful maintenance reconciliation.
    return new NetworkSigningKeys(Clock.systemUTC(), 1);
  }

  @Bean
  @org.springframework.context.annotation.Lazy
  MsgnetControlLedger msgnetControlLedger(
      MsgnetRenewal renewal,
      org.springframework.context.ApplicationContext context,
      Environment env) {
    var source = MsgnetDatabase.source(context, env);
    UUID tenant =
        UUID.fromString(env.getRequiredProperty("auriga.collab.network.key-operations.tenant-id"));
    String realm = env.getRequiredProperty("auriga.collab.network.key-operations.realm");
    String file = env.getRequiredProperty(PREFIX + "identities-file");
    var identities =
        MsgnetControlLedger.pins(
            NetworkJson.parse(ProtectedKeyFiles.readText(Path.of(file), 1048576), 1048576),
            tenant,
            realm);
    return new MsgnetControlLedger(renewal, new JdbcTemplate(source), tenant, realm, identities);
  }

  @Bean
  @org.springframework.context.annotation.Lazy
  MsgnetPki msgnetPki(
      MsgnetRenewal renewal,
      org.springframework.context.ApplicationContext context,
      MsgnetKeyEncryption ring,
      Environment env) {
    var source = MsgnetDatabase.source(context, env);
    var manager = MsgnetDatabase.manager(context, env);
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
  @org.springframework.context.annotation.Lazy
  MsgnetMaterials msgnetMaterials(
      MsgnetRenewal renewal,
      MsgnetControlLedger controls,
      MsgnetPki pki,
      org.springframework.context.ApplicationContext context,
      MsgnetKeyEncryption ring,
      Environment env) {
    var source = MsgnetDatabase.source(context, env);
    var manager = MsgnetDatabase.manager(context, env);
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
      ObjectProvider<MsgnetRenewal> renewal,
      ObjectProvider<MsgnetControlLedger> controls,
      ObjectProvider<MsgnetMaterials> materials) {
    return new MsgnetNetworkRenewal(renewal::getObject, controls::getObject, materials::getObject);
  }

  @Bean
  Maintenance msgnetMaintenance(
      ObjectProvider<MsgnetRenewal> service,
      NetworkSigningKeys keys,
      ObjectProvider<NetworkKeyPush> transport,
      ObjectProvider<MsgnetPki> pki,
      ObjectProvider<MsgnetPublicControl> control) {
    return new Maintenance(service, keys, transport, pki, control);
  }

  static final class Maintenance {
    private final ObjectProvider<MsgnetRenewal> serviceProvider;
    private final NetworkSigningKeys keys;
    private final ObjectProvider<NetworkKeyPush> transport;
    private final ObjectProvider<MsgnetPki> pkiProvider;
    private final ObjectProvider<MsgnetPublicControl> controlProvider;

    Maintenance(
        ObjectProvider<MsgnetRenewal> service,
        NetworkSigningKeys keys,
        ObjectProvider<NetworkKeyPush> transport,
        ObjectProvider<MsgnetPki> pki,
        ObjectProvider<MsgnetPublicControl> control) {
      this.serviceProvider = service;
      this.keys = keys;
      this.transport = transport;
      this.pkiProvider = pki;
      this.controlProvider = control;
    }

    @Scheduled(fixedDelayString = "${auriga.collab.network.auto-renew.reload-seconds:5}000")
    public void refresh() {
      try {
        controlProvider.getObject().refresh();
        var service = serviceProvider.getObject();
        service.reconcile();
        service.reload(keys);
        try {
          pkiProvider.getObject().reconcile();
          service.transportFailure(null);
        } catch (RuntimeException failure) {
          service.transportFailure(failure);
          throw failure;
        }
        var push = transport.getIfAvailable();
        if (push != null) service.pushPending(push);
      } catch (RuntimeException e) {
        org.slf4j.LoggerFactory.getLogger(Maintenance.class)
            .warn("Message network reconciliation failed with {}", e.getClass().getSimpleName());
      }
    }
  }
}
