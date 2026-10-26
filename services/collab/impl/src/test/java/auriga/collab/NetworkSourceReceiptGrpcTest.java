package auriga.collab;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.collab.grpc.NetworkAuthorityClient;
import auriga.collab.grpc.NetworkSourceGrpcAuthority;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.grpc.AurigaClientInterceptor;
import auriga.common.grpc.GrpcChannels;
import auriga.common.grpc.GrpcMetadata;
import auriga.common.persistence.TenantTransactionManager;
import auriga.contracts.iam.v1.*;
import io.grpc.*;
import io.grpc.netty.shaded.io.grpc.netty.NettyServerBuilder;
import io.grpc.stub.StreamObserver;
import java.net.InetSocketAddress;
import java.time.Duration;
import java.util.UUID;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;
import javax.sql.DataSource;
import org.junit.jupiter.api.Test;
import org.springframework.transaction.support.TransactionSynchronizationManager;

/** Transport fixture, not real IAM: exact old request, service token, delegation and budget. */
class NetworkSourceReceiptGrpcTest {
  @Test
  void generatedReadRpcPreservesTheFrozenBodyAndDelegatedTenant() throws Exception {
    UUID tenant = UUID.randomUUID();
    var original = original(tenant);
    var receipt =
        RegisterAgentSourceResponse.newBuilder()
            .setDefinitivelyUncommitted(true)
            .setOperationId(original.getOperationId())
            .setRequestDigest("sha256:" + "a".repeat(64))
            .setRejectionReason("lineage_stopped")
            .build();
    var reads = new AtomicInteger();
    var mutations = new AtomicInteger();
    var peer =
        new NetworkSourceAuthorityServiceGrpc.NetworkSourceAuthorityServiceImplBase() {
          @Override
          public void readAgentSourceReceipt(
              ReadAgentSourceReceiptRequest r, StreamObserver<RegisterAgentSourceResponse> o) {
            assertEquals(original, r.getOriginalRequest());
            assertNotNull(Context.current().getDeadline());
            assertTrue(
                Context.current().getDeadline().timeRemaining(TimeUnit.MILLISECONDS) <= 2000);
            reads.incrementAndGet();
            o.onNext(receipt);
            o.onCompleted();
          }

          @Override
          public void registerAgentSource(
              RegisterAgentSourceRequest r, StreamObserver<RegisterAgentSourceResponse> o) {
            mutations.incrementAndGet();
            o.onError(Status.INTERNAL.asRuntimeException());
          }
        };
    var server =
        NettyServerBuilder.forAddress(new InetSocketAddress("127.0.0.1", 0))
            .addService(
                ServerInterceptors.intercept(
                    peer,
                    new ServerInterceptor() {
                      @Override
                      public <Q, R> ServerCall.Listener<Q> interceptCall(
                          ServerCall<Q, R> call, Metadata headers, ServerCallHandler<Q, R> next) {
                        assertEquals(
                            "Bearer source-receipt-service-test",
                            headers.get(GrpcMetadata.AUTHORIZATION));
                        assertEquals(tenant.toString(), headers.get(GrpcMetadata.TENANT_ID));
                        return next.startCall(call, headers);
                      }
                    }))
            .build()
            .start();
    var channel =
        GrpcChannels.open(
            "127.0.0.1:" + server.getPort(),
            new AurigaClientInterceptor(
                () -> "source-receipt-service-test", Duration.ofSeconds(2)));
    try {
      var client = new NetworkAuthorityClient(channel, Duration.ofSeconds(2));
      var source =
          new NetworkSourceGrpcAuthority(
              client, new TenantTransactionManager(mock(DataSource.class)));
      TenantContext.run(
          tenant,
          () ->
              assertEquals(
                  receipt, source.sourceRequest(() -> source.readSourceReceipt(original))));
      TenantContext.run(
          UUID.randomUUID(),
          () -> denied("common.forbidden", () -> source.readSourceReceipt(original)));
      assertEquals(1, reads.get());
      assertEquals(0, mutations.get());
      TransactionSynchronizationManager.setActualTransactionActive(true);
      try {
        TenantContext.run(
            tenant, () -> denied("common.unavailable", () -> source.readSourceReceipt(original)));
      } finally {
        TransactionSynchronizationManager.setActualTransactionActive(false);
      }
      assertEquals(1, reads.get());
    } finally {
      channel.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
      server.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
    }
  }

  @Test
  void deadlineAndCancellationNeverCallRegister() throws Exception {
    var mutations = new AtomicInteger();
    var server =
        NettyServerBuilder.forAddress(new InetSocketAddress("127.0.0.1", 0))
            .addService(
                new NetworkSourceAuthorityServiceGrpc.NetworkSourceAuthorityServiceImplBase() {
                  @Override
                  public void readAgentSourceReceipt(
                      ReadAgentSourceReceiptRequest r,
                      StreamObserver<RegisterAgentSourceResponse> o) {
                    // Deliberately no response: the bounded production client must stop waiting.
                  }

                  @Override
                  public void registerAgentSource(
                      RegisterAgentSourceRequest r, StreamObserver<RegisterAgentSourceResponse> o) {
                    mutations.incrementAndGet();
                    o.onError(Status.INTERNAL.asRuntimeException());
                  }
                })
            .build()
            .start();
    var channel = GrpcChannels.open("127.0.0.1:" + server.getPort());
    UUID tenant = UUID.randomUUID();
    try {
      var client = new NetworkAuthorityClient(channel, Duration.ofMillis(100));
      TenantContext.run(
          tenant,
          () -> denied("network.overloaded", () -> client.readSourceReceipt(original(tenant))));
      try (var cancelled = Context.current().withCancellation()) {
        cancelled.cancel(null);
        cancelled.run(
            () ->
                TenantContext.run(
                    tenant,
                    () ->
                        denied(
                            "network.overloaded",
                            () ->
                                client.sourceRequest(
                                    () -> client.readSourceReceipt(original(tenant))))));
      }
      assertEquals(0, mutations.get());
    } finally {
      channel.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
      server.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
    }
  }

  private static RegisterAgentSourceRequest original(UUID tenant) {
    return RegisterAgentSourceRequest.newBuilder()
        .setConnection(NetworkAuthorityGrpcTest.connection(tenant))
        .setOperationId("old-source-operation")
        .setScope(NetworkAgentSourceScope.NETWORK_AGENT_SOURCE_SCOPE_DURABLE_UNICAST_SEND)
        .setPredecessorBinding("expired-predecessor")
        .setExpectedRevision(Long.parseUnsignedLong("18446744073709551615"))
        .setRegistration(
            NetworkAgentSourceRegistration.newBuilder()
                .setRegistrationId("registration")
                .setAllocationOpId("allocation")
                .setAllocationFingerprint("sha256:" + "a".repeat(64))
                .setRegistrationRevision(3)
                .setRealm("2")
                .setNode("20")
                .setLocal("7")
                .setAddressType("session"))
        .build();
  }

  private static void denied(String code, Runnable work) {
    assertEquals(code, assertThrows(AurigaException.class, work::run).errorCode().code());
  }
}
