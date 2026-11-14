package auriga.iam;

import auriga.iam.api.NodeControlAuthority;
import java.nio.file.Path;
import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.core.env.Environment;

@Configuration(proxyBeanMethods = false)
@ConditionalOnProperty(prefix = "auriga.iam.msgnet", name = "node-control-pins-file")
@org.springframework.boot.autoconfigure.condition.ConditionalOnExpression(
    "${auriga.services.iam.enabled:true}")
public class NodeControlPinConfiguration {
  @Bean
  NodeControlAuthority nodeControlAuthority(Environment env) throws java.io.IOException {
    return PinnedNodeControls.load(
        Path.of(env.getRequiredProperty("auriga.iam.msgnet.node-control-pins-file")));
  }
}
