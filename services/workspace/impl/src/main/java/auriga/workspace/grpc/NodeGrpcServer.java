package auriga.workspace.grpc;

import auriga.common.error.*;
import auriga.common.grpc.GrpcErrors;
import auriga.contracts.node.v1.*;
import auriga.workspace.app.NodeEnrollment;
import io.grpc.*;
import io.grpc.netty.shaded.io.grpc.netty.*;
import io.grpc.netty.shaded.io.netty.handler.ssl.ClientAuth;
import io.grpc.stub.StreamObserver;
import java.io.ByteArrayInputStream;
import java.io.File;
import java.net.InetSocketAddress;
import java.security.cert.X509Certificate;
import java.util.concurrent.*;
import javax.net.ssl.SSLSession;
import org.springframework.context.SmartLifecycle;

/**
 * TLS listener: Join is token-authenticated; every other method requires a valid node certificate.
 */
public final class NodeGrpcServer implements SmartLifecycle, AutoCloseable {
  private final String host;
  private final int port;
  private final File certificate, key;
  private final NodeEnrollment enrollment;
  private final NodeSessions sessions;
  private Server server;
  private ExecutorService executor;

  public NodeGrpcServer(
      String host,
      int port,
      File certificate,
      File key,
      NodeEnrollment enrollment,
      NodeSessions sessions) {
    this.host = host;
    this.port = port;
    this.certificate = certificate;
    this.key = key;
    this.enrollment = enrollment;
    this.sessions = sessions;
  }

  @Override
  public synchronized void start() {
    if (server != null) return;
    try {
      var tls =
          GrpcSslContexts.forServer(certificate, key)
              .trustManager(new ByteArrayInputStream(enrollment.ca.bundle()))
              .clientAuth(ClientAuth.OPTIONAL)
              .build();
      executor = Executors.newVirtualThreadPerTaskExecutor();
      ServerInterceptor auth =
          new ServerInterceptor() {
            @Override
            public <Q, S> ServerCall.Listener<Q> interceptCall(
                ServerCall<Q, S> call, Metadata headers, ServerCallHandler<Q, S> next) {
              try {
                if (call.getMethodDescriptor()
                    .getFullMethodName()
                    .equals(NodeEnrollmentServiceGrpc.getJoinMethod().getFullMethodName()))
                  return next.startCall(call, headers);
                SSLSession tlsSession = call.getAttributes().get(Grpc.TRANSPORT_ATTR_SSL_SESSION);
                if (tlsSession == null) throw new AurigaException(CommonErrors.UNAUTHENTICATED);
                X509Certificate leaf = (X509Certificate) tlsSession.getPeerCertificates()[0];
                enrollment.identity(leaf);
                return Contexts.interceptCall(
                    Context.current().withValue(NodeSessions.PEER, leaf), call, headers, next);
              } catch (Exception e) {
                var status = GrpcErrors.toStatus(new AurigaException(CommonErrors.UNAUTHENTICATED));
                call.close(status.getStatus(), status.getTrailers());
                return new ServerCall.Listener<>() {};
              }
            }
          };
      var join =
          new NodeEnrollmentServiceGrpc.NodeEnrollmentServiceImplBase() {
            @Override
            public void join(JoinRequest request, StreamObserver<JoinResponse> out) {
              try {
                out.onNext(enrollment.join(request));
                out.onCompleted();
              } catch (RuntimeException e) {
                out.onError(GrpcErrors.toStatus(e));
              }
            }

            @Override
            public void renewCertificate(
                RenewCertificateRequest request, StreamObserver<RenewCertificateResponse> out) {
              try {
                var leaf = NodeSessions.PEER.get();
                out.onNext(enrollment.renew(enrollment.identity(leaf), leaf, request));
                out.onCompleted();
              } catch (RuntimeException e) {
                out.onError(GrpcErrors.toStatus(e));
              }
            }
          };
      server =
          NettyServerBuilder.forAddress(new InetSocketAddress(host, port))
              .executor(executor)
              .sslContext(tls)
              .maxInboundMessageSize(1024 * 1024)
              .addService(ServerInterceptors.intercept(join, auth))
              .addService(ServerInterceptors.intercept(sessions.service(), auth))
              .build()
              .start();
    } catch (Exception e) {
      if (executor != null) executor.close();
      throw new IllegalStateException("Cannot start workspace node TLS listener", e);
    }
  }

  public synchronized int port() {
    if (server == null) throw new IllegalStateException("Node listener is stopped");
    return server.getPort();
  }

  @Override
  public synchronized boolean isRunning() {
    return server != null;
  }

  @Override
  public synchronized void stop() {
    if (server == null) return;
    sessions.close();
    server.shutdown();
    try {
      if (!server.awaitTermination(5, TimeUnit.SECONDS)) server.shutdownNow();
    } catch (InterruptedException e) {
      server.shutdownNow();
      Thread.currentThread().interrupt();
    }
    server = null;
    executor.close();
  }

  @Override
  public void close() {
    stop();
  }
}
