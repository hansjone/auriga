package auriga.common.grpc;

import io.grpc.BindableService;
import io.grpc.Server;
import io.grpc.ServerInterceptors;
import io.grpc.netty.shaded.io.grpc.netty.GrpcSslContexts;
import io.grpc.netty.shaded.io.grpc.netty.NettyServerBuilder;
import io.grpc.netty.shaded.io.netty.handler.ssl.ClientAuth;
import io.grpc.netty.shaded.io.netty.handler.ssl.SslContextBuilder;
import java.io.File;
import java.io.IOException;
import java.io.UncheckedIOException;
import java.net.InetAddress;
import java.net.InetSocketAddress;
import java.net.UnknownHostException;
import java.util.List;
import java.util.Set;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.TimeUnit;
import org.springframework.context.SmartLifecycle;

/**
 * Embedded gRPC server for split deployments: serves every {@link BindableService} bean through
 * {@link AurigaServerInterceptor}, handlers on virtual threads. Port 0 picks a free port.
 *
 * <p>Binding: loopback by default. Without TLS it refuses to bind a non-loopback address unless
 * {@code allowInsecureNonLoopback} is set explicitly; with {@link Tls} it serves mTLS (client
 * certificates required when a trust collection is configured), as contracts/README.md requires for
 * split deployments. Every RPC method must carry an authorization annotation ({@link
 * GrpcAuthorizationCheck}), otherwise the server does not start.
 */
public final class GrpcServer implements SmartLifecycle {
  /**
   * PEM files.
   *
   * @param certChain server certificate chain
   * @param privateKey server private key
   * @param trustCertCollection CA bundle for client certificates; non-null means mTLS required
   */
  public record Tls(File certChain, File privateKey, File trustCertCollection) {}

  private final String host;
  private final int port;
  private final List<BindableService> services;
  private final AurigaServerInterceptor interceptor;
  private final Tls tls;
  private final boolean allowInsecureNonLoopback;
  private final Set<String> publicMethods;
  private volatile Server server;
  private ExecutorService executor;

  public GrpcServer(
      String host,
      int port,
      List<BindableService> services,
      AurigaServerInterceptor interceptor,
      Tls tls,
      boolean allowInsecureNonLoopback,
      Set<String> publicMethods) {
    this.host = host == null ? "127.0.0.1" : host;
    this.port = port;
    this.services = List.copyOf(services);
    this.interceptor = interceptor;
    this.tls = tls;
    this.allowInsecureNonLoopback = allowInsecureNonLoopback;
    this.publicMethods = publicMethods == null ? Set.of() : Set.copyOf(publicMethods);
  }

  @Override
  public synchronized void start() {
    if (server != null) return;
    GrpcAuthorizationCheck.verify(services, publicMethods);
    InetAddress address;
    try {
      address = InetAddress.getByName(host);
    } catch (UnknownHostException e) {
      throw new IllegalStateException("Cannot resolve gRPC bind host " + host, e);
    }
    if (tls == null && !address.isLoopbackAddress() && !allowInsecureNonLoopback) {
      throw new IllegalStateException(
          "Refusing to serve internal gRPC without TLS on non-loopback address "
              + host
              + "; configure auriga.grpc.server.tls or bind 127.0.0.1");
    }
    NettyServerBuilder builder =
        NettyServerBuilder.forAddress(new InetSocketAddress(address, port))
            .executor(executor = Executors.newVirtualThreadPerTaskExecutor());
    if (tls != null) {
      try {
        SslContextBuilder ssl = SslContextBuilder.forServer(tls.certChain(), tls.privateKey());
        if (tls.trustCertCollection() != null) {
          ssl.trustManager(tls.trustCertCollection()).clientAuth(ClientAuth.REQUIRE);
        }
        builder.sslContext(GrpcSslContexts.configure(ssl).build());
      } catch (IOException e) {
        throw new UncheckedIOException("Invalid gRPC TLS configuration", e);
      }
    }
    services.forEach(s -> builder.addService(ServerInterceptors.intercept(s, interceptor)));
    try {
      server = builder.build().start();
    } catch (IOException e) {
      executor.close();
      throw new UncheckedIOException("Cannot start gRPC server on " + host + ":" + port, e);
    }
  }

  @Override
  public synchronized void stop() {
    Server s = server;
    if (s == null) return;
    s.shutdown();
    try {
      if (!s.awaitTermination(10, TimeUnit.SECONDS)) s.shutdownNow();
    } catch (InterruptedException e) {
      s.shutdownNow();
      Thread.currentThread().interrupt();
    }
    server = null;
    executor.close();
  }

  @Override
  public boolean isRunning() {
    return server != null;
  }

  /** The bound port (useful with port 0). */
  public int port() {
    Server s = server;
    if (s == null) throw new IllegalStateException("gRPC server not started");
    return s.getPort();
  }
}
