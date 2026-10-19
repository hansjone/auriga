package io.github.hansjone.auriga.common.autoconfigure;

import io.github.hansjone.auriga.common.grpc.AurigaClientInterceptor;
import io.github.hansjone.auriga.common.grpc.AurigaServerInterceptor;
import io.github.hansjone.auriga.common.grpc.GrpcServer;
import io.github.hansjone.auriga.common.grpc.ServiceTokenProvider;
import io.github.hansjone.auriga.common.security.InternalTokenVerifier;
import io.grpc.BindableService;
import java.io.File;
import java.util.Set;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.boot.autoconfigure.AutoConfiguration;
import org.springframework.boot.autoconfigure.condition.ConditionalOnBean;
import org.springframework.boot.autoconfigure.condition.ConditionalOnMissingBean;
import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty;
import org.springframework.context.annotation.Bean;

/** Internal gRPC: server interceptor/server for split deployments, client interceptor. */
@AutoConfiguration(after = AurigaSecurityAutoConfiguration.class)
public class AurigaGrpcAutoConfiguration {
  @Bean
  @ConditionalOnMissingBean
  @ConditionalOnBean(InternalTokenVerifier.class)
  AurigaServerInterceptor aurigaServerInterceptor(
      InternalTokenVerifier verifier, AurigaProperties properties) {
    return new AurigaServerInterceptor(
        verifier.verifier(),
        Set.copyOf(properties.getGrpc().getServer().getPublicMethods()),
        Set.copyOf(properties.getGrpc().getServer().getTenantDelegates()));
  }

  @Bean
  @ConditionalOnMissingBean
  @ConditionalOnProperty(prefix = "auriga.grpc.server", name = "enabled", havingValue = "true")
  GrpcServer aurigaGrpcServer(
      ObjectProvider<BindableService> services,
      AurigaServerInterceptor interceptor,
      AurigaProperties properties) {
    AurigaProperties.Server server = properties.getGrpc().getServer();
    GrpcServer.Tls tls =
        server.getTlsCertChain() == null
            ? null
            : new GrpcServer.Tls(
                new File(server.getTlsCertChain()),
                new File(server.getTlsPrivateKey()),
                server.getTlsTrustCertCollection() == null
                    ? null
                    : new File(server.getTlsTrustCertCollection()));
    return new GrpcServer(
        server.getHost(),
        server.getPort(),
        services.orderedStream().toList(),
        interceptor,
        tls,
        server.isAllowInsecureNonLoopback(),
        Set.copyOf(server.getPublicMethods()));
  }

  @Bean
  @ConditionalOnMissingBean
  @ConditionalOnBean(ServiceTokenProvider.class)
  AurigaClientInterceptor aurigaClientInterceptor(
      ServiceTokenProvider tokens, AurigaProperties properties) {
    return new AurigaClientInterceptor(tokens, properties.getGrpc().getClientTimeout());
  }
}
