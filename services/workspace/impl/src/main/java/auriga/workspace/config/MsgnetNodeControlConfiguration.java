package auriga.workspace.config;

import auriga.iam.api.MessageNetworkControlLedger;
import auriga.iam.api.NodeControlAuthority;
import auriga.workspace.api.MsgnetNodeControl;
import auriga.workspace.grpc.PinnedMsgnetNodeControl;
import org.springframework.boot.autoconfigure.condition.*;
import org.springframework.context.annotation.*;

@Configuration(proxyBeanMethods = false)
@ConditionalOnProperty(
    prefix = "auriga.services.workspace",
    name = "enabled",
    havingValue = "true",
    matchIfMissing = true)
public class MsgnetNodeControlConfiguration {
  @Bean
  @ConditionalOnMissingBean(MsgnetNodeControl.class)
  @ConditionalOnProperty(prefix = "auriga.iam.msgnet", name = "node-control-pins-file")
  MsgnetNodeControl msgnetNodeControl(
      NodeControlAuthority pins, MessageNetworkControlLedger ledger) {
    return new PinnedMsgnetNodeControl(pins, ledger);
  }
}
