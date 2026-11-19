package auriga.workspace;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import auriga.common.context.TenantContext;
import auriga.contracts.node.v1.*;
import auriga.contracts.workspace.v1.*;
import auriga.workspace.app.SharedDshPlacements;
import auriga.workspace.domain.NodeCa;
import auriga.workspace.domain.WorkspaceCrypto;
import auriga.workspace.grpc.NodeGrpcServer;
import io.grpc.stub.StreamObserver;
import java.io.ByteArrayInputStream;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.time.Clock;
import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.*;
import java.util.function.Supplier;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

/**
 * Real TLS/NodeSessions/runtime/validation; the JDBC transaction commit is a controlled boundary.
 */
class SharedDshMemoryCommitTest {
  @TempDir Path directory;

  @Test
  void coldAdmitWaitsForValidatedOnNextCommitWithoutBlockingItsStream() throws Exception {
    try (var h = new Harness(directory)) {
      var pending = h.admit(h.channel, 10, TimeUnit.SECONDS);
      assertTrue(h.bindingRead.await(3, TimeUnit.SECONDS));
      assertThrows(TimeoutException.class, () -> pending.get(150, TimeUnit.MILLISECONDS));
      h.control.onNext(
          SessionRequest.newBuilder().setSharedDshPoolMemorySample(h.sample()).build());
      assertTrue(h.commitStarted.await(3, TimeUnit.SECONDS));
      assertNull(h.memory.get());
      assertThrows(TimeoutException.class, () -> pending.get(150, TimeUnit.MILLISECONDS));
      assertEquals(0, h.admitted.get());
      h.allowCommit.countDown();
      assertTrue(pending.get(3, TimeUnit.SECONDS).getAdmitted());
      assertEquals(1, h.admitted.get());
      assertNotNull(h.memory.get());
    }
  }

  @Test
  void cancellationAndDeadlineCannotBeRevivedByALaterMemoryCommit() throws Exception {
    try (var h = new Harness(directory)) {
      var context = io.grpc.Context.current().withCancellation();
      var cancelled = context.call(() -> h.admit(h.channel, 10, TimeUnit.SECONDS));
      assertTrue(h.bindingRead.await(3, TimeUnit.SECONDS));
      context.cancel(null);
      assertEquals(io.grpc.Status.Code.CANCELLED, error(cancelled));
      var expired = h.admit(h.channel, 200, TimeUnit.MILLISECONDS);
      assertEquals(io.grpc.Status.Code.DEADLINE_EXCEEDED, error(expired));
      h.allowCommit.countDown();
      h.control.onNext(
          SessionRequest.newBuilder().setSharedDshPoolMemorySample(h.sample()).build());
      assertTrue(h.committed.await(3, TimeUnit.SECONDS));
      assertTrue(h.admit(h.channel, 10, TimeUnit.SECONDS).get(3, TimeUnit.SECONDS).getAdmitted());
      assertEquals(1, h.admitted.get());
    }
  }

  @Test
  void anotherTlsConnectionCannotSatisfyOrBorrowAPendingAdmission() throws Exception {
    try (var h = new Harness(directory)) {
      var pending = h.admit(h.channel, 10, TimeUnit.SECONDS);
      assertTrue(h.bindingRead.await(3, TimeUnit.SECONDS));
      var another = h.channel();
      try {
        assertEquals(
            io.grpc.Status.Code.UNAVAILABLE, error(h.admit(another, 10, TimeUnit.SECONDS)));
      } finally {
        another.shutdownNow().awaitTermination(3, TimeUnit.SECONDS);
      }
      assertThrows(TimeoutException.class, () -> pending.get(100, TimeUnit.MILLISECONDS));
      h.allowCommit.countDown();
      h.control.onNext(
          SessionRequest.newBuilder().setSharedDshPoolMemorySample(h.sample()).build());
      assertTrue(pending.get(3, TimeUnit.SECONDS).getAdmitted());
      assertEquals(1, h.admitted.get());
    }
  }

  @Test
  void holderReplacementAndRevocationWakePendingRequestsWithoutAdmission() throws Exception {
    try (var h = new Harness(directory)) {
      var pending = h.admit(h.channel, 10, TimeUnit.SECONDS);
      assertTrue(h.bindingRead.await(3, TimeUnit.SECONDS));
      var old = h.control;
      h.control = h.openControl();
      assertEquals(io.grpc.Status.Code.UNAVAILABLE, error(pending));
      old.onNext(SessionRequest.newBuilder().setSharedDshPoolMemorySample(h.sample()).build());
      var next = h.admit(h.channel, 10, TimeUnit.SECONDS);
      assertThrows(TimeoutException.class, () -> next.get(100, TimeUnit.MILLISECONDS));
      h.f.sessions.revoke(h.f.node);
      assertEquals(io.grpc.Status.Code.UNAVAILABLE, error(next));
      assertEquals(0, h.admitted.get());
    }
  }

  @org.junit.jupiter.params.ParameterizedTest
  @org.junit.jupiter.params.provider.ValueSource(booleans = {false, true})
  void invalidOrExpiredSamplesNeverSatisfyTheCommitWait(boolean expired) throws Exception {
    try (var h = new Harness(directory)) {
      var pending = h.admit(h.channel, 10, TimeUnit.SECONDS);
      assertTrue(h.bindingRead.await(3, TimeUnit.SECONDS));
      var sample = h.sample().toBuilder();
      if (expired)
        sample
            .setObservedAt(h.f.clock.instant().getEpochSecond() - 30)
            .setValidUntil(h.f.clock.instant().getEpochSecond());
      else sample.setMemoryLimitBytes(0);
      h.control.onNext(SessionRequest.newBuilder().setSharedDshPoolMemorySample(sample).build());
      assertEquals(io.grpc.Status.Code.UNAVAILABLE, error(pending));
      assertEquals(1, h.commitStarted.getCount());
      assertNull(h.memory.get());
      assertEquals(0, h.admitted.get());
    }
  }

  private static io.grpc.Status.Code error(CompletableFuture<?> response) throws Exception {
    var error = assertThrows(ExecutionException.class, () -> response.get(3, TimeUnit.SECONDS));
    return io.grpc.Status.fromThrowable(error.getCause()).getCode();
  }

  static final class Harness implements AutoCloseable {
    final SharedDshNodeRuntimeTest.Fixture f =
        new SharedDshNodeRuntimeTest.Fixture(null, Clock.systemUTC());
    final SharedDshSessionBinding binding;
    final AtomicReference<Map<String, Object>> memory = new AtomicReference<>();
    final ThreadLocal<Map<String, Object>> staged = new ThreadLocal<>();
    final CountDownLatch bindingRead = new CountDownLatch(1),
        commitStarted = new CountDownLatch(1),
        allowCommit = new CountDownLatch(1),
        committed = new CountDownLatch(1);
    final AtomicInteger admitted = new AtomicInteger();
    final AtomicReference<CountDownLatch> inventory = new AtomicReference<>();
    final NodeGrpcServer server;
    final io.grpc.ManagedChannel channel;
    StreamObserver<SessionRequest> control;

    Harness(Path directory) throws Exception {
      f.poolRow.put("state", "ready");
      f.owners.put(f.pool, f.node);
      for (var row : f.members.values()) {
        row.put("state", "ready");
        row.put("auth_generation", 1L);
      }
      var row = f.members.values().iterator().next();
      var p = SharedDshPlacements.binding(row);
      binding =
          SharedDshSessionBinding.newBuilder()
              .setTenantId(p.getTenantId())
              .setMemberId(p.getMemberId())
              .setPlacementId(p.getPlacementId())
              .setPhysicalInstanceId(p.getPhysicalInstanceId())
              .setMode(p.getMode())
              .setEpoch(p.getEpoch())
              .setRevision(p.getRevision())
              .setAuthGeneration(1)
              .setSessionId(UUID.randomUUID().toString())
              .setPlatformSessionId(UUID.randomUUID().toString())
              .build();
      doAnswer(c -> query(c.getArgument(0), (Object[]) c.getRawArguments()[1]))
          .when(f.jdbc)
          .queryForList(anyString(), any(Object[].class));
      doAnswer(c -> update(c.getArgument(0), (Object[]) c.getRawArguments()[1]))
          .when(f.jdbc)
          .update(anyString(), any(Object[].class));
      doAnswer(c -> ((String) c.getArgument(0)).contains("shared_dsh_placement") ? 2L : 0L)
          .when(f.jdbc)
          .queryForObject(anyString(), eq(Long.class), any(Object[].class));
      doAnswer(
              c ->
                  TenantContext.call(
                      c.<UUID>getArgument(0),
                      () -> {
                        var result = c.<Supplier<?>>getArgument(1).get();
                        var value = staged.get();
                        if (value != null) {
                          staged.remove();
                          commitStarted.countDown();
                          await(allowCommit);
                          memory.set(value);
                          committed.countDown();
                        }
                        return result;
                      }))
          .when(f.store)
          .in(any(), any());
      var serverKey = TestPki.key();
      var serverCert =
          TestPki.certificate(
              serverKey, f.caKey, NodeCa.parse(f.ca.bundle()), f.clock.instant(), false);
      var cert = directory.resolve("server.pem");
      var key = directory.resolve("server-key.pem");
      Files.write(cert, NodeCa.pem(serverCert.getEncoded()));
      Files.write(key, pem(serverKey.getPrivate()));
      server =
          new NodeGrpcServer("127.0.0.1", 0, cert.toFile(), key.toFile(), f.enrollment, f.sessions);
      server.start();
      channel = channel();
      control = openControl();
    }

    private static void await(CountDownLatch latch) {
      try {
        if (!latch.await(5, TimeUnit.SECONDS))
          throw new IllegalStateException("test commit timeout");
      } catch (InterruptedException e) {
        Thread.currentThread().interrupt();
        throw new IllegalStateException(e);
      }
    }

    List<Map<String, Object>> query(String sql, Object[] args) {
      if (sql.startsWith("SELECT binding FROM shared_dsh_session")) {
        bindingRead.countDown();
        return List.of(Map.of("binding", binding.toByteArray()));
      }
      if (sql.startsWith("SELECT * FROM shared_dsh_placement") && sql.contains("member_id"))
        return f.members.values().stream().filter(r -> r.get("member_id").equals(args[1])).toList();
      if (sql.startsWith("SELECT generation,observed_at")) {
        var m = memory.get();
        return m == null ? List.of() : List.of(m);
      }
      if (sql.startsWith("SELECT p.id,p.mode,m.*")) {
        var row = new HashMap<String, Object>();
        row.put("id", f.pool);
        row.put("mode", "shared");
        row.put("current_holder", f.holders.get(f.node));
        row.put("lease_live", true);
        var m = memory.get();
        if (m != null) row.putAll(m);
        return List.of(row);
      }
      if (sql.startsWith("SELECT task_id")
          || sql.startsWith("SELECT binding FROM shared_dsh_task_end")) return List.of();
      if (sql.startsWith("SELECT * FROM instance")) {
        var ready = inventory.get();
        if (ready != null) ready.countDown();
      }
      return f.query(sql, args);
    }

    int update(String sql, Object[] args) {
      if (sql.startsWith("INSERT INTO shared_dsh_pool_memory")) {
        staged.set(
            Map.of(
                "node_id",
                args[2],
                "holder",
                args[3],
                "generation",
                args[4],
                "observed_at",
                args[5],
                "valid_until",
                args[6],
                "used_bytes",
                args[7],
                "limit_bytes",
                args[8]));
        return 1;
      }
      if (sql.startsWith("INSERT INTO shared_dsh_active")) {
        admitted.incrementAndGet();
        return 1;
      }
      return f.update(sql, args);
    }

    StreamObserver<SessionRequest> openControl() throws Exception {
      var ready = new CountDownLatch(1);
      inventory.set(ready);
      var stream =
          NodeControlServiceGrpc.newStub(channel)
              .session(
                  new StreamObserver<>() {
                    public void onNext(SessionResponse response) {}

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
                              .formatHex(WorkspaceCrypto.hash(new byte[32]))
                              .substring(0, 16))
                      .putLabels(
                          auriga.workspace.app.SharedDshNodeRuntime.CAPABILITY,
                          auriga.workspace.app.SharedDshNodeRuntime.PREPARATION)
                      .putLabels(
                          auriga.workspace.app.SharedDshNodeRuntime.PROCESS_CAPABILITY,
                          auriga.workspace.app.SharedDshNodeRuntime.PROCESS))
              .build());
      stream.onNext(
          SessionRequest.newBuilder().setInventory(Inventory.getDefaultInstance()).build());
      assertTrue(ready.await(3, TimeUnit.SECONDS));
      return stream;
    }

    SharedDshPoolMemorySample sample() {
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

    CompletableFuture<AdmitSharedDshTaskResponse> admit(
        io.grpc.ManagedChannel conn, long timeout, TimeUnit unit) {
      var result = new CompletableFuture<AdmitSharedDshTaskResponse>();
      SharedDshServiceGrpc.newStub(conn)
          .withDeadlineAfter(timeout, unit)
          .admitSharedDshTask(
              AdmitSharedDshTaskRequest.newBuilder()
                  .setBinding(binding)
                  .setTaskId(UUID.randomUUID().toString())
                  .build(),
              new StreamObserver<>() {
                public void onNext(AdmitSharedDshTaskResponse response) {
                  result.complete(response);
                }

                public void onError(Throwable error) {
                  result.completeExceptionally(error);
                }

                public void onCompleted() {}
              });
      return result;
    }

    io.grpc.ManagedChannel channel() throws Exception {
      var tls =
          io.grpc.netty.shaded.io.grpc.netty.GrpcSslContexts.forClient()
              .trustManager(new ByteArrayInputStream(f.ca.bundle()))
              .keyManager(
                  new ByteArrayInputStream(NodeCa.pem(f.certificates.get(f.node).getEncoded())),
                  new ByteArrayInputStream(pem(f.certificateKeys.get(f.node).getPrivate())))
              .build();
      return io.grpc.netty.shaded.io.grpc.netty.NettyChannelBuilder.forAddress(
              "127.0.0.1", server.port())
          .sslContext(tls)
          .build();
    }

    static byte[] pem(java.security.PrivateKey key) {
      return ("-----BEGIN PRIVATE KEY-----\n"
              + Base64.getMimeEncoder(64, new byte[] {'\n'}).encodeToString(key.getEncoded())
              + "\n-----END PRIVATE KEY-----\n")
          .getBytes(StandardCharsets.US_ASCII);
    }

    public void close() throws Exception {
      allowCommit.countDown();
      if (control != null) control.onCompleted();
      channel.shutdownNow().awaitTermination(3, TimeUnit.SECONDS);
      server.close();
      f.close();
    }
  }
}
