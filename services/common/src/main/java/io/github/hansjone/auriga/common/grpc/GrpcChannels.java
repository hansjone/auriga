package io.github.hansjone.auriga.common.grpc;

import io.grpc.ClientInterceptor;
import io.grpc.ManagedChannel;
import io.grpc.netty.shaded.io.grpc.netty.NettyChannelBuilder;
import java.util.Objects;
import org.springframework.core.env.Environment;

/** Channels to other services in split deployments. */
public final class GrpcChannels {
  private GrpcChannels() {}

  /** {@code auriga.remote.<service>.target}, e.g. {@code org.internal.example.com:9090}. */
  public static String target(Environment environment, String service) {
    String target = environment.getProperty("auriga.remote." + service + ".target");
    if (target == null || target.isBlank()) {
      throw new IllegalStateException(
          "auriga.remote." + service + ".target is required when the mode is grpc");
    }
    return target;
  }

  /**
   * mTLS channel for split deployments (contracts/README.md): client certificate + key, CA bundle
   * for the server certificate (PEM files).
   */
  public static ManagedChannel openTls(
      String target,
      java.io.File clientCertChain,
      java.io.File clientKey,
      java.io.File trustCertCollection,
      ClientInterceptor... interceptors) {
    try {
      var ssl =
          io.grpc.netty.shaded.io.grpc.netty.GrpcSslContexts.forClient()
              .trustManager(trustCertCollection)
              .keyManager(clientCertChain, clientKey)
              .build();
      return NettyChannelBuilder.forTarget(target).sslContext(ssl).intercept(interceptors).build();
    } catch (javax.net.ssl.SSLException e) {
      throw new IllegalStateException("Invalid gRPC client TLS configuration", e);
    }
  }

  /**
   * Plaintext channel with the common client interceptor; close it on shutdown. Only for loopback
   * targets (single host) — split deployments must use {@link #openTls}.
   */
  public static ManagedChannel open(String target, ClientInterceptor... interceptors) {
    Objects.requireNonNull(target, "target");
    return NettyChannelBuilder.forTarget(target).usePlaintext().intercept(interceptors).build();
  }
}
