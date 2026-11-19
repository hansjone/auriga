package auriga.workspace;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import auriga.contracts.node.v1.*;
import auriga.contracts.workspace.v1.*;
import auriga.workspace.app.SharedDshNodeRuntime;
import auriga.workspace.grpc.NodeSessions;
import io.grpc.Context;
import io.grpc.stub.StreamObserver;
import java.util.*;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

/** Real certificate/NodeSessions/lease; runtime SQL policy has a separate PG suite. */
class SharedDshNodeAdmissionTest {
  @TempDir java.nio.file.Path directory;

  @Test
  void actualNodeTlsListenerMountsOnlyTheFencedWritesAndRequiresACertificate() throws Exception {
    var runtime = mock(SharedDshNodeRuntime.class);
    var inventoryReady = new java.util.concurrent.CountDownLatch(1);
    when(runtime.pending(any(), any())).thenReturn(new SharedDshNodeRuntime.Batch(List.of(), null));
    when(runtime.views(any(), any()))
        .thenAnswer(
            call -> {
              inventoryReady.countDown();
              return new SharedDshNodeRuntime.Views(List.of(), null);
            });
    try (var f = new SharedDshNodeRuntimeTest.Fixture(runtime, java.time.Clock.systemUTC())) {
      var serverKey = TestPki.key();
      var serverCert =
          TestPki.certificate(
              serverKey,
              f.caKey,
              auriga.workspace.domain.NodeCa.parse(f.ca.bundle()),
              f.clock.instant(),
              false);
      var cert = directory.resolve("server.pem");
      var key = directory.resolve("server-key.pem");
      java.nio.file.Files.write(cert, auriga.workspace.domain.NodeCa.pem(serverCert.getEncoded()));
      java.nio.file.Files.write(key, privatePem(serverKey.getPrivate()));
      var server =
          new auriga.workspace.grpc.NodeGrpcServer(
              "127.0.0.1", 0, cert.toFile(), key.toFile(), f.enrollment, f.sessions);
      server.start();
      io.grpc.ManagedChannel channel = null, anonymous = null, anotherSocket = null;
      try {
        channel = nodeChannel(server.port(), f, true);
        var stream =
            NodeControlServiceGrpc.newStub(channel)
                .session(
                    new StreamObserver<SessionResponse>() {
                      public void onNext(SessionResponse value) {}

                      public void onError(Throwable error) {}

                      public void onCompleted() {}
                    });
        stream.onNext(
            SessionRequest.newBuilder()
                .setHello(
                    Hello.newBuilder()
                        .setNodeId(f.node.toString())
                        .setProtocolVersion(1)
                        .setOs("linux")
                        .addDrivers(RuntimeKind.RUNTIME_KIND_DOCKER)
                        .setSealingKeyId(
                            HexFormat.of()
                                .formatHex(
                                    auriga.workspace.domain.WorkspaceCrypto.hash(new byte[32]))
                                .substring(0, 16))
                        .putLabels(
                            SharedDshNodeRuntime.CAPABILITY, SharedDshNodeRuntime.PREPARATION)
                        .putLabels(
                            SharedDshNodeRuntime.PROCESS_CAPABILITY, SharedDshNodeRuntime.PROCESS))
                .build());
        stream.onNext(
            SessionRequest.newBuilder().setInventory(Inventory.getDefaultInstance()).build());
        assertTrue(inventoryReady.await(10, java.util.concurrent.TimeUnit.SECONDS));
        stream.onNext(SessionRequest.newBuilder().setSharedDshPoolMemorySample(memory(f)).build());
        var request =
            AdmitSharedDshTaskRequest.newBuilder()
                .setBinding(binding(f))
                .setTaskId(UUID.randomUUID().toString())
                .build();
        var rpc =
            SharedDshServiceGrpc.newBlockingStub(channel)
                .withDeadlineAfter(10, java.util.concurrent.TimeUnit.SECONDS);
        assertTrue(rpc.admitSharedDshTask(request).getAdmitted());
        anotherSocket = nodeChannel(server.port(), f, true);
        var anotherSocketRpc =
            SharedDshServiceGrpc.newBlockingStub(anotherSocket)
                .withDeadlineAfter(10, java.util.concurrent.TimeUnit.SECONDS);
        assertEquals(
            io.grpc.Status.Code.UNAVAILABLE,
            assertThrows(
                    io.grpc.StatusRuntimeException.class,
                    () -> anotherSocketRpc.admitSharedDshTask(request))
                .getStatus()
                .getCode());
        assertEquals(
            io.grpc.Status.Code.UNAVAILABLE,
            assertThrows(
                    io.grpc.StatusRuntimeException.class,
                    () ->
                        anotherSocketRpc.releaseSharedDshTask(
                            ReleaseSharedDshTaskRequest.newBuilder()
                                .setBinding(request.getBinding())
                                .setTaskId(request.getTaskId())
                                .build()))
                .getStatus()
                .getCode());
        assertTrue(
            rpc.releaseSharedDshTask(
                    ReleaseSharedDshTaskRequest.newBuilder()
                        .setBinding(request.getBinding())
                        .setTaskId(request.getTaskId())
                        .build())
                .getFenced());
        assertEquals(
            io.grpc.Status.Code.UNIMPLEMENTED,
            assertThrows(
                    io.grpc.StatusRuntimeException.class,
                    () -> rpc.getSharedDshPool(GetSharedDshPoolRequest.getDefaultInstance()))
                .getStatus()
                .getCode());
        anonymous = nodeChannel(server.port(), f, false);
        var anonymousRpc =
            SharedDshServiceGrpc.newBlockingStub(anonymous)
                .withDeadlineAfter(10, java.util.concurrent.TimeUnit.SECONDS);
        assertEquals(
            io.grpc.Status.Code.UNAUTHENTICATED,
            assertThrows(
                    io.grpc.StatusRuntimeException.class,
                    () -> anonymousRpc.admitSharedDshTask(request))
                .getStatus()
                .getCode());
        verify(runtime, times(1))
            .admit(eq(f.node), any(), eq(request.getBinding()), eq(request.getTaskId()));
        stream.onCompleted();
      } finally {
        if (channel != null)
          channel.shutdownNow().awaitTermination(5, java.util.concurrent.TimeUnit.SECONDS);
        if (anonymous != null)
          anonymous.shutdownNow().awaitTermination(5, java.util.concurrent.TimeUnit.SECONDS);
        if (anotherSocket != null)
          anotherSocket.shutdownNow().awaitTermination(5, java.util.concurrent.TimeUnit.SECONDS);
        server.close();
      }
    }
  }

  private static byte[] privatePem(java.security.PrivateKey key) {
    return ("-----BEGIN "
            + "PRIVATE KEY-----\n"
            + Base64.getMimeEncoder(64, new byte[] {'\n'}).encodeToString(key.getEncoded())
            + "\n-----END "
            + "PRIVATE KEY-----\n")
        .getBytes(java.nio.charset.StandardCharsets.US_ASCII);
  }

  private static io.grpc.ManagedChannel nodeChannel(
      int port, SharedDshNodeRuntimeTest.Fixture f, boolean authenticated) throws Exception {
    var tls =
        io.grpc.netty.shaded.io.grpc.netty.GrpcSslContexts.forClient()
            .trustManager(new java.io.ByteArrayInputStream(f.ca.bundle()));
    if (authenticated)
      tls.keyManager(
          new java.io.ByteArrayInputStream(
              auriga.workspace.domain.NodeCa.pem(f.certificates.get(f.node).getEncoded())),
          new java.io.ByteArrayInputStream(privatePem(f.certificateKeys.get(f.node).getPrivate())));
    return io.grpc.netty.shaded.io.grpc.netty.NettyChannelBuilder.forAddress("127.0.0.1", port)
        .sslContext(tls.build())
        .build();
  }

  @Test
  void onlyAnEnrolledCurrentCapableSessionCanAdmitAndRelease() throws Exception {
    var runtime = mock(SharedDshNodeRuntime.class);
    when(runtime.pending(any(), any())).thenReturn(new SharedDshNodeRuntime.Batch(List.of(), null));
    when(runtime.views(any(), any())).thenReturn(new SharedDshNodeRuntime.Views(List.of(), null));
    try (var f = new SharedDshNodeRuntimeTest.Fixture(runtime)) {
      var service = (SharedDshServiceGrpc.SharedDshServiceImplBase) f.sessions.admissionService();
      var request =
          AdmitSharedDshTaskRequest.newBuilder()
              .setBinding(binding(f))
              .setTaskId(UUID.randomUUID().toString())
              .build();
      var missing = new Reply<AdmitSharedDshTaskResponse>();
      service.admitSharedDshTask(request, missing);
      assertNotNull(missing.error);
      verify(runtime, never()).admit(any(), any(), any(), anyString());
      var context = Context.current().withValue(NodeSessions.PEER, f.certificates.get(f.node));
      var noStream = new Reply<AdmitSharedDshTaskResponse>();
      context.run(() -> service.admitSharedDshTask(request, noStream));
      assertNotNull(noStream.error);
      var peer = f.connect(f.node, true);
      peer.input.onNext(
          SessionRequest.newBuilder().setSharedDshPoolMemorySample(memory(f)).build());
      var currentContext =
          Context.current()
              .withValues(
                  NodeSessions.PEER,
                  f.certificates.get(f.node),
                  NodeSessions.CONNECTION,
                  peer.connection);
      var admitted = new Reply<AdmitSharedDshTaskResponse>();
      currentContext.run(() -> service.admitSharedDshTask(request, admitted));
      assertNull(admitted.error);
      assertTrue(admitted.result.getAdmitted());
      assertEquals(request.getBinding(), admitted.result.getBinding());
      assertEquals(request.getTaskId(), admitted.result.getTaskId());
      UUID firstHolder = f.holders.get(f.node);
      verify(runtime).admit(f.node, firstHolder, request.getBinding(), request.getTaskId());
      // Even an identical/resumed SSLSession must not authorize a different live socket.
      var resumed =
          new NodeSessions.Connection(
              peer.connection.tls(), new java.net.InetSocketAddress("127.0.0.1", 1));
      var resumedReply = new Reply<AdmitSharedDshTaskResponse>();
      Context.current()
          .withValues(
              NodeSessions.PEER, f.certificates.get(f.node), NodeSessions.CONNECTION, resumed)
          .run(() -> service.admitSharedDshTask(request, resumedReply));
      assertEquals(
          io.grpc.Status.Code.UNAVAILABLE,
          io.grpc.Status.fromThrowable(resumedReply.error).getCode());
      verify(runtime, times(1)).admit(any(), any(), any(), anyString());
      var released = new Reply<ReleaseSharedDshTaskResponse>();
      currentContext.run(
          () ->
              service.releaseSharedDshTask(
                  ReleaseSharedDshTaskRequest.newBuilder()
                      .setBinding(request.getBinding())
                      .setTaskId(request.getTaskId())
                      .build(),
                  released));
      assertNull(released.error);
      assertTrue(released.result.getFenced());
      verify(runtime).release(f.node, request.getBinding(), request.getTaskId());
      f.holders.put(f.node, UUID.randomUUID());
      var stale = new Reply<AdmitSharedDshTaskResponse>();
      currentContext.run(() -> service.admitSharedDshTask(request, stale));
      assertNotNull(stale.error);
      verify(runtime, times(1)).admit(any(), any(), any(), anyString());
      peer.input.onCompleted();
    }
  }

  @Test
  void fieldTwelveRunsOnTheCurrentCertificateLeaseAndReconnectChangesTheHolder() throws Exception {
    var runtime = mock(SharedDshNodeRuntime.class);
    when(runtime.pending(any(), any())).thenReturn(new SharedDshNodeRuntime.Batch(List.of(), null));
    when(runtime.views(any(), any())).thenReturn(new SharedDshNodeRuntime.Views(List.of(), null));
    try (var f = new SharedDshNodeRuntimeTest.Fixture(runtime)) {
      var sample =
          SharedDshPoolMemorySample.newBuilder()
              .setSchemaVersion(1)
              .setTenantId(f.tenant.toString())
              .setPhysicalInstanceId(f.pool.toString())
              .build();
      var first = f.connect(f.node, true);
      UUID firstHolder = f.holders.get(f.node);
      first.input.onNext(SessionRequest.newBuilder().setSharedDshPoolMemorySample(sample).build());
      assertNull(first.error);
      verify(runtime).memory(f.node, firstHolder, f.clock.instant().getEpochSecond(), sample);
      first.input.onCompleted();
      var second = f.connect(f.node, true);
      UUID secondHolder = f.holders.get(f.node);
      assertNotEquals(firstHolder, secondHolder);
      first.input.onNext(SessionRequest.newBuilder().setSharedDshPoolMemorySample(sample).build());
      verify(runtime, times(1)).memory(any(), any(), anyLong(), any());
      second.input.onNext(SessionRequest.newBuilder().setSharedDshPoolMemorySample(sample).build());
      assertNull(second.error);
      verify(runtime).memory(f.node, secondHolder, f.clock.instant().getEpochSecond(), sample);
    }
  }

  @Test
  void anIncapableNodeCannotUseNewWritesOrSendPoolSamples() throws Exception {
    var runtime = mock(SharedDshNodeRuntime.class);
    try (var f = new SharedDshNodeRuntimeTest.Fixture(runtime)) {
      var peer = f.connect(f.node, false);
      var service = (SharedDshServiceGrpc.SharedDshServiceImplBase) f.sessions.admissionService();
      var reply = new Reply<AdmitSharedDshTaskResponse>();
      Context.current()
          .withValue(NodeSessions.PEER, f.certificates.get(f.node))
          .run(
              () ->
                  service.admitSharedDshTask(
                      AdmitSharedDshTaskRequest.newBuilder()
                          .setBinding(binding(f))
                          .setTaskId(UUID.randomUUID().toString())
                          .build(),
                      reply));
      assertNotNull(reply.error);
      peer.input.onNext(
          SessionRequest.newBuilder()
              .setSharedDshPoolMemorySample(SharedDshPoolMemorySample.getDefaultInstance())
              .build());
      assertNotNull(peer.error);
      verify(runtime, never()).memory(any(), any(), anyLong(), any());
      verify(runtime, never()).admit(any(), any(), any(), anyString());
    }
  }

  private static SharedDshSessionBinding binding(SharedDshNodeRuntimeTest.Fixture f) {
    return SharedDshSessionBinding.newBuilder()
        .setTenantId(f.tenant.toString())
        .setPhysicalInstanceId(f.pool.toString())
        .build();
  }

  private static SharedDshPoolMemorySample memory(SharedDshNodeRuntimeTest.Fixture f) {
    long now = f.clock.instant().getEpochSecond();
    return SharedDshPoolMemorySample.newBuilder()
        .setSchemaVersion(1)
        .setTenantId(f.tenant.toString())
        .setPhysicalInstanceId(f.pool.toString())
        .setObservedGeneration(1)
        .setObservedAt(now)
        .setValidUntil(now + 30)
        .setMemoryUsedBytes(79)
        .setMemoryLimitBytes(100)
        .build();
  }

  static final class Reply<T> implements StreamObserver<T> {
    T result;
    Throwable error;

    public void onNext(T value) {
      result = value;
    }

    public void onError(Throwable error) {
      this.error = error;
    }

    public void onCompleted() {}
  }
}
