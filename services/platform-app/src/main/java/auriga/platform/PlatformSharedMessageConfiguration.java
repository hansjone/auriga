package auriga.platform;

import auriga.collab.app.*;
import auriga.collab.grpc.*;
import auriga.collab.network.keys.MsgnetRenewal;
import auriga.collab.network.local.*;
import auriga.common.persistence.TenantTransactionManager;
import io.grpc.ManagedChannel;
import java.time.Clock;
import javax.sql.DataSource;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.beans.factory.annotation.Qualifier;
import org.springframework.boot.autoconfigure.condition.ConditionalOnExpression;
import org.springframework.context.annotation.*;
import org.springframework.jdbc.core.JdbcTemplate;

/** Shared-only mailbox adapter reuses the official durable and signing engines. */
@Configuration(proxyBeanMethods = false)
@ConditionalOnExpression(
    "${auriga.platform.combined:false} && ${auriga.services.collab.enabled:false} && ${auriga.iam.shared-tasks.enabled:false} && ${auriga.collab.shared-messages.enabled:false}")
public class PlatformSharedMessageConfiguration {
  @Bean
  SharedMessageGrpcService sharedMessageGrpcService(
      @Qualifier("collabNetworkIamChannel") ManagedChannel channel,
      @Qualifier("collabDataSource") DataSource data,
      @Qualifier("collabTransactionManager") TenantTransactionManager manager,
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
