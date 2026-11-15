package auriga.workspace.config;

import auriga.iam.api.*;
import auriga.workspace.app.NodeEnrollment;
import auriga.workspace.grpc.*;
import java.net.InetSocketAddress;
import java.nio.file.Path;
import org.springframework.boot.autoconfigure.condition.*;
import org.springframework.context.annotation.*;
import org.springframework.core.env.Environment;

@Configuration(proxyBeanMethods = false)
@ConditionalOnProperty(prefix = "auriga.workspace.msgnet-proof", name = "port")
@ConditionalOnExpression("${auriga.services.workspace.enabled:true}")
public class MsgnetProofRelayConfiguration {
  @Bean(destroyMethod = "close")
  MsgnetProofRelay msgnetProofRelay(
      Environment env,
      NodeEnrollment enrollment,
      NodeControlAuthority pins,
      MessageNetworkControlLedger ledger,
      MessageNetworkIsolation isolation)
      throws Exception {
    return new MsgnetProofRelay(
        new InetSocketAddress(
            env.getProperty("auriga.workspace.msgnet-proof.host", "127.0.0.1"),
            env.getRequiredProperty("auriga.workspace.msgnet-proof.port", Integer.class)),
        MsgnetRelayTls.context(
            Path.of(env.getRequiredProperty("auriga.workspace.tls-certificate-file")),
            Path.of(env.getRequiredProperty("auriga.workspace.tls-private-key-file")),
            enrollment.ca.bundle()),
        enrollment,
        pins,
        ledger,
        isolation);
  }
}
