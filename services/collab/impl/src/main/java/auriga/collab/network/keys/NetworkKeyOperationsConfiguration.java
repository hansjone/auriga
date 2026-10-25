package auriga.collab.network.keys;

import auriga.collab.app.NetworkJson;
import auriga.collab.app.NetworkSigningKeys;
import auriga.common.persistence.TenantTransactionManager;
import java.security.KeyFactory;
import java.security.spec.X509EncodedKeySpec;
import java.time.Clock;
import javax.sql.DataSource;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.boot.autoconfigure.condition.ConditionalOnExpression;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.context.annotation.Import;
import org.springframework.core.env.Environment;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.scheduling.annotation.EnableScheduling;

@Configuration(proxyBeanMethods = false)
@ConditionalOnExpression(
    "${auriga.services.collab.enabled:true} && ${auriga.collab.network.key-operations.enabled:false} && '${auriga.service.schema:}' == 'collab'")
@Import(NetworkKeysController.class)
@EnableScheduling
public class NetworkKeyOperationsConfiguration {
  @Bean
  NetworkKeySettings networkKeySettings(Environment environment) {
    return NetworkKeySettings.from(environment);
  }

  @Bean
  RootKeyPublication networkRootTrust(NetworkKeySettings settings)
      throws java.security.GeneralSecurityException {
    ProtectedKeyFiles.check(settings.rootPublicFile(), false);
    var root =
        KeyFactory.getInstance("Ed25519")
            .generatePublic(
                new X509EncodedKeySpec(ProtectedKeyFiles.read(settings.rootPublicFile(), 1024)));
    return new RootKeyPublication(
        settings.realm(), settings.rootKid(), root, settings.rootFingerprint(), Clock.systemUTC());
  }

  @Bean
  NetworkKeyOperations networkKeyOperations(
      DataSource source,
      TenantTransactionManager manager,
      NetworkKeySettings settings,
      RootKeyPublication trust) {
    var operations =
        new NetworkKeyOperations(
            new JdbcTemplate(source),
            manager,
            settings.tenant(),
            settings.realm(),
            trust,
            settings.minimumVersion(),
            Clock.systemUTC());
    operations.publish(settings.mountedRelease());
    return operations;
  }

  @Bean(destroyMethod = "close")
  NetworkSigningKeys approvedNetworkSigningKeys(
      NetworkKeySettings settings, NetworkKeyOperations operations) {
    var payload = NetworkJson.parse(operations.current().signedKeyset()).path("keyset");
    return SigningKeyLoader.load(
        payload,
        settings.stateFile(),
        settings.privateFiles(),
        Clock.systemUTC(),
        (keys, signature) ->
            operations.signApproved(keys.publicKeysJson(settings.realm()), signature));
  }

  @Bean
  NetworkKeyMaintenance networkKeyMaintenance(
      NetworkKeySettings settings,
      NetworkKeyOperations operations,
      ObjectProvider<NetworkKeyPush> transport) {
    return new NetworkKeyMaintenance(settings, operations, transport);
  }
}
