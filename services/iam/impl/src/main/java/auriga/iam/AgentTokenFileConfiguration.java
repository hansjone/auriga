package auriga.iam;

import auriga.iam.api.InternalTokenIssuer;
import java.nio.file.Path;
import org.springframework.boot.autoconfigure.condition.ConditionalOnExpression;
import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.core.env.Environment;

@Configuration(proxyBeanMethods = false)
@ConditionalOnProperty(
    prefix = "auriga.iam.agent-token-export",
    name = "enabled",
    havingValue = "true")
@ConditionalOnExpression("${auriga.services.iam.enabled:true}")
public class AgentTokenFileConfiguration {
  @Bean(destroyMethod = "close")
  AgentTokenFilePublisher iamAgentTokenFilePublisher(InternalTokenIssuer issuer, Environment env) {
    ServiceTokenExportIsolation.verify(env);
    if (!env.getProperty("auriga.platform.combined", Boolean.class, false))
      throw new IllegalStateException(
          "Agent token export requires a same-host combined installation");
    return new AgentTokenFilePublisher(
        issuer,
        Path.of(env.getRequiredProperty("auriga.iam.agent-token-export.installation-secret-file")),
        Path.of(env.getRequiredProperty("auriga.iam.agent-token-export.token-file")),
        env.getProperty("auriga.iam.agent-token-export.installation-owner", Integer.class, -1));
  }
}
