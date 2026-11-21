package auriga.collab.config;

import auriga.collab.app.NetworkMailboxService;
import auriga.collab.app.NetworkMailboxWireService;
import auriga.collab.app.NetworkSigningKeys;
import auriga.collab.app.SharedMessageService;
import auriga.collab.app.SharedMessageSource;
import auriga.collab.grpc.SharedMessageGrpcService;
import auriga.collab.grpc.SharedMessageIamClient;
import auriga.collab.network.keys.MsgnetRenewal;
import auriga.collab.network.local.LocalRegistry;
import auriga.collab.network.local.LocalTargets;
import auriga.common.persistence.TenantTransactionManager;
import io.grpc.ManagedChannel;
import java.time.Clock;
import javax.sql.DataSource;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.beans.factory.annotation.Qualifier;
import org.springframework.boot.autoconfigure.condition.ConditionalOnExpression;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.jdbc.core.JdbcTemplate;

/** Shared-only adapter for the existing standalone collab schema and signing authority. */
@Configuration(proxyBeanMethods = false)
@ConditionalOnExpression(
    "!${auriga.platform.combined:false} && ${auriga.services.collab.enabled:true} && '${auriga.service.schema:}' == 'collab' && ${auriga.collab.shared-messages.enabled:false}")
public class SharedMessageConfiguration {
  @Bean
  SharedMessageGrpcService standaloneSharedMessageGrpcService(
      @Qualifier("collabNetworkIamChannel") ManagedChannel channel,
      DataSource data,
      TenantTransactionManager manager,
      NetworkSigningKeys keys,
      ObjectProvider<MsgnetRenewal> renewal) {
    var iam = new SharedMessageIamClient(channel, manager);
    var targets = new LocalTargets(new JdbcTemplate(data), manager);
    var source = new SharedMessageSource(iam, manager, targets);
    var mailbox =
        new NetworkMailboxService(
            new JdbcTemplate(data), manager, source, keys, Clock.systemUTC(), iam);
    var locals = new LocalRegistry(new JdbcTemplate(data), manager, source, iam, Clock.systemUTC());
    var wire =
        new NetworkMailboxWireService(
            mailbox, locals, new JdbcTemplate(data), manager, source, keys, Clock.systemUTC());
    return new SharedMessageGrpcService(
        new SharedMessageService(
            iam,
            source,
            mailbox,
            wire,
            locals,
            new JdbcTemplate(data),
            manager,
            keys,
            renewal::getObject,
            Clock.systemUTC()));
  }
}
