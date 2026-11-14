package auriga.iam;

import auriga.iam.api.NetworkInstallationAuthority;
import java.nio.file.Path;
import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.core.env.Environment;

/** C must supply its durable revocation and nonce ports; there is no permissive fallback. */
@Configuration(proxyBeanMethods = false)
@ConditionalOnProperty(prefix = "auriga.iam.msgnet", name = "installation-pins-file")
@org.springframework.boot.autoconfigure.condition.ConditionalOnExpression(
    "${auriga.services.iam.enabled:true}")
public class MsgnetInstallationConfiguration {
  @Bean
  NetworkInstallationAuthority msgnetInstallationAuthority(
      Environment env,
      NetworkInstallationAuthority.Access access,
      NetworkInstallationAuthority.Nonces nonces)
      throws java.io.IOException {
    return PinnedNetworkInstallations.load(
        Path.of(env.getRequiredProperty("auriga.iam.msgnet.installation-pins-file")),
        access,
        nonces);
  }
}
