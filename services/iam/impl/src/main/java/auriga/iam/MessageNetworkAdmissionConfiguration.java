package auriga.iam;

import auriga.iam.api.MessageNetworkAdmission;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.boot.autoconfigure.condition.ConditionalOnMissingBean;
import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;

/** Only C's current ledger can grant readiness. The default is message-only denial. */
@Configuration(proxyBeanMethods = false)
@ConditionalOnProperty(
    prefix = "auriga.services.iam",
    name = "enabled",
    havingValue = "true",
    matchIfMissing = true)
public class MessageNetworkAdmissionConfiguration {
  @Bean
  @ConditionalOnMissingBean(MessageNetworkAdmission.class)
  MessageNetworkAdmission msgnetAdmission(
      ObjectProvider<MessageNetworkAdmission.Ledger> ledgers,
      ObjectProvider<MessageNetworkTargets> targets,
      ObjectProvider<auriga.iam.api.MessageNetworkControlLedger> controls,
      ObjectProvider<auriga.iam.api.NodeControlAuthority> nodes,
      ObjectProvider<IamStore> stores) {
    return (tenant, realm, target) -> {
      try {
        var mapped = targets.getIfAvailable();
        if (mapped != null) {
          var control = controls.getIfAvailable();
          var store = stores.getIfAvailable();
          return control != null
              && store != null
              && mapped.ready(store, control, nodes.getIfAvailable(), tenant, realm, target);
        }
        var ledger = ledgers.getIfAvailable();
        if (ledger == null) return false;
        return MessageNetworkAdmission.fromLedger(ledger).ready(tenant, realm, target);
      } catch (RuntimeException unavailable) {
        return false;
      }
    };
  }

  @Bean
  @ConditionalOnProperty(prefix = "auriga.iam.msgnet", name = "targets-file")
  MessageNetworkTargets msgnetTargets(org.springframework.core.env.Environment env)
      throws java.io.IOException {
    return MessageNetworkTargets.load(
        java.nio.file.Path.of(env.getRequiredProperty("auriga.iam.msgnet.targets-file")));
  }
}
