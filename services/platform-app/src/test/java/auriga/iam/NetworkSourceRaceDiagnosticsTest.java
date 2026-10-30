package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;

import auriga.collab.grpc.NetworkAuthorityClient;
import auriga.common.context.TenantContext;
import auriga.contracts.iam.v1.*;
import io.grpc.*;
import io.grpc.netty.shaded.io.grpc.netty.NettyServerBuilder;
import io.grpc.stub.StreamObserver;
import java.net.InetSocketAddress;
import java.nio.file.*;
import java.time.Duration;
import java.util.UUID;
import java.util.concurrent.TimeUnit;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.EnumSource;

/** Real transport and production client; verifies diagnostics only, not IAM/PG behavior. */
class NetworkSourceRaceDiagnosticsTest {
  @ParameterizedTest
  @EnumSource(
      value = Status.Code.class,
      names = {"OK", "DEADLINE_EXCEEDED", "UNAVAILABLE"})
  void actualRpcCodeAndPhasesSurviveSuccessOrFailure(Status.Code code) throws Exception {
    var tenant = UUID.randomUUID();
    var request =
        RegisterAgentSourceRequest.newBuilder()
            .setConnection(NetworkAuthorityConnection.newBuilder().setTenantId(tenant.toString()))
            .setOperationId("diagnostic-operation")
            .build();
    var receipt =
        RegisterAgentSourceResponse.newBuilder()
            .setDefinitivelyUncommitted(true)
            .setOperationId(request.getOperationId())
            .setRequestDigest("sha256:" + "a".repeat(64))
            .setRejectionReason("original_connection_invalid_uncommitted")
            .build();
    var server =
        NettyServerBuilder.forAddress(new InetSocketAddress("127.0.0.1", 0))
            .addService(
                new NetworkSourceAuthorityServiceGrpc.NetworkSourceAuthorityServiceImplBase() {
                  @Override
                  public void registerAgentSource(
                      RegisterAgentSourceRequest received,
                      StreamObserver<RegisterAgentSourceResponse> observer) {
                    assertEquals(request, received);
                    if (code == Status.Code.OK) {
                      observer.onNext(receipt);
                      observer.onCompleted();
                    } else observer.onError(code.toStatus().asRuntimeException());
                  }
                })
            .build()
            .start();
    ManagedChannel channel = null;
    try {
      channel =
          ManagedChannelBuilder.forAddress("127.0.0.1", server.getPort()).usePlaintext().build();
      var client = new NetworkAuthorityClient(channel, Duration.ofSeconds(5));
      var diagnostics =
          new NetworkLocalSourceHistoryIntegrationTest.RaceDiagnostics(
              Path.of("target", "network-source-history-diagnostics-test"));
      if (code == Status.Code.OK) {
        try (diagnostics) {
          assertEquals(
              receipt,
              diagnostics.register(
                  () -> TenantContext.call(tenant, () -> client.registerSource(request))));
        }
      } else {
        var failure =
            assertThrows(
                NetworkAuthorityClient.SourceRpcFailure.class,
                () -> {
                  try (diagnostics) {
                    diagnostics.register(
                        () -> TenantContext.call(tenant, () -> client.registerSource(request)));
                  }
                });
        assertEquals(code, failure.grpcCode(), "diagnostics must rethrow the actual RPC failure");
      }
      String retained = Files.readString(diagnostics.path);
      assertTrue(retained.contains("late_register_grpc_code=" + code.name() + "\n"));
      assertTrue(retained.contains("original_rpc_started="));
      assertTrue(retained.contains("original_rpc_finished="));
      assertFalse(retained.contains(tenant.toString()));
      assertFalse(retained.contains(request.getOperationId()));
      assertFalse(retained.contains(receipt.getRequestDigest()));
    } finally {
      if (channel != null) {
        channel.shutdownNow();
        assertTrue(channel.awaitTermination(5, TimeUnit.SECONDS));
      }
      server.shutdownNow();
      assertTrue(server.awaitTermination(5, TimeUnit.SECONDS));
    }
  }
}
