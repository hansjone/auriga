package auriga.collab.config;

import auriga.collab.grpc.NetworkAuthorityClient;
import auriga.collab.grpc.NetworkSourceGrpcAuthority;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.grpc.AurigaClientInterceptor;
import auriga.common.grpc.GrpcChannels;
import auriga.common.persistence.TenantTransactionManager;
import io.grpc.ManagedChannel;
import java.io.File;
import java.nio.file.*;
import java.time.Duration;
import java.util.Set;
import org.springframework.boot.autoconfigure.condition.ConditionalOnExpression;
import org.springframework.context.annotation.*;
import org.springframework.core.env.Environment;

/** Standalone network authority is mandatory; missing settings fail startup. */
@Configuration(proxyBeanMethods = false)
@ConditionalOnExpression(
    "${auriga.services.collab.enabled:true} && '${auriga.service.schema:}' == 'collab'")
public class CollabNetworkConfiguration {
  private static final String PREFIX = "auriga.collab.network.";

  @Bean(destroyMethod = "shutdownNow")
  ManagedChannel collabNetworkIamChannel(Environment env) {
    String target = env.getRequiredProperty(PREFIX + "iam-target");
    Path file = Path.of(env.getRequiredProperty(PREFIX + "service-token-file"));
    token(file); // Validate readability and content before opening a channel.
    var interceptor = new AurigaClientInterceptor(() -> token(file), timeout(env));
    String certificate = env.getProperty(PREFIX + "tls-cert-chain", "");
    if (!certificate.isBlank())
      return GrpcChannels.openTls(
          target,
          new File(certificate),
          new File(env.getRequiredProperty(PREFIX + "tls-private-key")),
          new File(env.getRequiredProperty(PREFIX + "tls-trust-cert-collection")),
          interceptor);
    int colon = target.lastIndexOf(':');
    String host = colon < 0 ? "" : target.substring(0, colon);
    if (!Set.of("127.0.0.1", "[::1]", "[0:0:0:0:0:0:0:1]").contains(host)
        || !target.substring(colon + 1).matches("[1-9][0-9]{0,4}")
        || Integer.parseInt(target.substring(colon + 1)) > 65535)
      throw new IllegalStateException("Collab network IAM requires numeric loopback or mTLS");
    return GrpcChannels.open(target, interceptor);
  }

  private static String token(Path file) {
    try {
      String token = Files.readString(file).trim();
      if (token.isEmpty() || token.contains("\n") || token.contains("\r"))
        throw new AurigaException(CommonErrors.UNAVAILABLE);
      return token;
    } catch (java.io.IOException e) {
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    }
  }

  private static Duration timeout(Environment env) {
    long millis = env.getProperty(PREFIX + "iam-timeout-ms", Long.class, 5000L);
    if (millis < 1 || millis > 30000)
      throw new IllegalArgumentException("IAM timeout must be in [1,30000] ms");
    return Duration.ofMillis(millis);
  }

  @Bean
  NetworkAuthorityClient networkAuthorityClient(
      @org.springframework.beans.factory.annotation.Qualifier("collabNetworkIamChannel")
          ManagedChannel channel,
      Environment env) {
    return new NetworkAuthorityClient(channel, timeout(env));
  }

  @Bean
  NetworkSourceGrpcAuthority networkSourceGrpcAuthority(
      NetworkAuthorityClient client, TenantTransactionManager manager) {
    return new NetworkSourceGrpcAuthority(client, manager);
  }
}
