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
  MessageNetworkAdmission msgnetAdmission(ObjectProvider<MessageNetworkAdmission.Ledger> ledgers) {
    return (tenant, realm, target) -> {
      var ledger = ledgers.getIfAvailable();
      if (ledger == null) return false;
      try {
        return MessageNetworkAdmission.fromLedger(ledger).ready(tenant, realm, target);
      } catch (RuntimeException unavailable) {
        return false;
      }
    };
  }
}
