package auriga.iam;

import auriga.iam.api.InternalTokenIssuer;
import java.nio.file.Path;
import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.core.env.Environment;

@Configuration(proxyBeanMethods = false)
@ConditionalOnProperty(
    prefix = "auriga.iam.edge-token-export",
    name = "enabled",
    havingValue = "true")
@org.springframework.boot.autoconfigure.condition.ConditionalOnExpression(
    "${auriga.services.iam.enabled:true}")
public class EdgeTokenFileConfiguration {
  @Bean(destroyMethod = "close")
  EdgeTokenFilePublisher iamEdgeTokenFilePublisher(InternalTokenIssuer issuer, Environment env) {
    if (!env.getProperty("auriga.platform.combined", Boolean.class, false))
      throw new IllegalStateException(
          "Edge token export requires a same-host combined installation");
    return new EdgeTokenFilePublisher(
        issuer,
        Path.of(env.getRequiredProperty("auriga.iam.edge-token-export.installation-secret-file")),
        Path.of(env.getRequiredProperty("auriga.iam.edge-token-export.token-file")),
        env.getProperty("auriga.iam.edge-token-export.reader-group", Integer.class, 65532),
        env.getProperty("auriga.iam.edge-token-export.installation-owner", Integer.class, -1));
  }
}
