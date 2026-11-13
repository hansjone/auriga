package auriga.iam;

import auriga.iam.api.InternalTokenIssuer;
import java.nio.file.Path;
import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.core.env.Environment;

/** Independent, fixed component identities. Material scope is checked again by collab. */
@Configuration(proxyBeanMethods = false)
@ConditionalOnProperty(
    prefix = "auriga.services.iam",
    name = "enabled",
    havingValue = "true",
    matchIfMissing = true)
public class MsgnetProjectorTokenConfiguration {
  @Bean(destroyMethod = "close")
  @ConditionalOnProperty(
      prefix = "auriga.iam.msgnet-projector-token-export.network",
      name = "enabled",
      havingValue = "true")
  ServiceTokenFilePublisher msgnetNetworkProjectorToken(
      InternalTokenIssuer issuer, Environment env) {
    return publisher("network", issuer, env);
  }

  @Bean(destroyMethod = "close")
  @ConditionalOnProperty(
      prefix = "auriga.iam.msgnet-projector-token-export.edge",
      name = "enabled",
      havingValue = "true")
  ServiceTokenFilePublisher msgnetEdgeProjectorToken(InternalTokenIssuer issuer, Environment env) {
    return publisher("edge", issuer, env);
  }

  @Bean(destroyMethod = "close")
  @ConditionalOnProperty(
      prefix = "auriga.iam.msgnet-projector-token-export.broker",
      name = "enabled",
      havingValue = "true")
  ServiceTokenFilePublisher msgnetBrokerProjectorToken(
      InternalTokenIssuer issuer, Environment env) {
    return publisher("broker", issuer, env);
  }

  private static ServiceTokenFilePublisher publisher(
      String component, InternalTokenIssuer issuer, Environment env) {
    ServiceTokenExportIsolation.verify(env);
    if (!env.getProperty("auriga.platform.combined", Boolean.class, false))
      throw new IllegalStateException(
          "Projector token export requires a same-host combined installation");
    String prefix = "auriga.iam.msgnet-projector-token-export." + component + ".";
    return new ServiceTokenFilePublisher(
        "msgnet-projector-" + component,
        issuer,
        Path.of(env.getRequiredProperty(prefix + "installation-secret-file")),
        Path.of(env.getRequiredProperty(prefix + "token-file")),
        env.getRequiredProperty(prefix + "reader-group", Integer.class),
        env.getProperty(prefix + "installation-owner", Integer.class, -1));
  }
}
