package auriga.collab;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.collab.grpc.*;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.grpc.*;
import auriga.common.persistence.TenantTransactionManager;
import auriga.contracts.iam.v1.*;
import io.grpc.*;
import io.grpc.netty.shaded.io.grpc.netty.NettyServerBuilder;
import java.net.InetSocketAddress;
import java.time.Duration;
import java.util.concurrent.TimeUnit;
import javax.sql.DataSource;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.ValueSource;

/** Same IAM peers and reconnect tuple as the PG regression, real gRPC, no database/startup. */
class NetworkSourceReceiptReconnectTest {
  @ParameterizedTest
  @ValueSource(booleans = {false, true})
  void savedSuccessNeedsCanonicalC2AndItsVerifiedReference(boolean wrongOwner) throws Exception {
    var tenant = NetworkSourceControlPostgresTest.TENANT;
    var original =
        NetworkConnectionAnchorGrpcTest.connection(
            tenant, NetworkSourceControlPostgresTest.newIngress());
    var current = NetworkSourceControlPostgresTest.receiptReconnectConnection(original);
    var authority = new NetworkSourceControlPostgresTest.Authority();
    var peer = new NetworkSourceControlPostgresTest.SourcePeer();
    var request =
        RegisterAgentSourceRequest.newBuilder()
            .setConnection(original)
            .setOperationId("retained-success")
            .setScope(NetworkAgentSourceScope.NETWORK_AGENT_SOURCE_SCOPE_DURABLE_UNICAST_SEND)
            .setRegistration(
                NetworkAgentSourceRegistration.newBuilder()
                    .setRealm(original.getRealm())
                    .setNode(original.getNode())
                    .setRegistrationId("reg")
                    .setAllocationOpId("alloc")
                    .setAllocationFingerprint("sha256:" + "a".repeat(64))
                    .setRegistrationRevision(1)
                    .setLocal("1")
                    .setAddressType("session"))
            .build();
    var receipt =
        RegisterAgentSourceResponse.newBuilder()
            .setSource(
                NetworkAgentSource.newBuilder()
                    .setSourceBinding("retained-binding")
                    .setOriginalConnection(original)
                    .setRegistration(request.getRegistration())
                    .setScope(request.getScope())
                    .setRevision(1)
                    .setState(NetworkAgentSourceState.NETWORK_AGENT_SOURCE_STATE_ACTIVE)
                    .setOwnerUserId("01900000-0000-7000-8000-000000000001")
                    .setNotBefore("2026-10-27T00:00:00Z")
                    .setExpiresAt("2026-10-27T00:03:00Z"))
            .build();
    peer.requests.put(NetworkSourceControlPostgresTest.SourcePeer.key(request), request);
    peer.receipts.put(NetworkSourceControlPostgresTest.SourcePeer.key(request), receipt);
    var originalRef = authority.ref(original);
    authority.invalid.add(original.getIngress());
    var server =
        NettyServerBuilder.forAddress(new InetSocketAddress("127.0.0.1", 0))
            .addService(authority)
            .addService(peer)
            .intercept(
                new ServerInterceptor() {
                  @Override
                  public <Q, R> ServerCall.Listener<Q> interceptCall(
                      ServerCall<Q, R> call, Metadata headers, ServerCallHandler<Q, R> next) {
                    var context =
                        Context.current()
                            .withValue(
                                NetworkSourceControlPostgresTest.IAM_TENANT,
                                headers.get(GrpcMetadata.TENANT_ID))
                            .withValue(
                                NetworkSourceControlPostgresTest.IAM_CALLER,
                                headers.get(GrpcMetadata.AUTHORIZATION));
                    return Contexts.interceptCall(context, call, headers, next);
                  }
                })
            .build()
            .start();
    var channel =
        GrpcChannels.open(
            "127.0.0.1:" + server.getPort(),
            new AurigaClientInterceptor(() -> "collab-test", Duration.ofSeconds(2)));
    try {
      var source =
          new NetworkSourceGrpcAuthority(
              new NetworkAuthorityClient(channel, Duration.ofSeconds(2)),
              new TenantTransactionManager(mock(DataSource.class)));
      TenantContext.run(
          tenant,
          () -> {
            assertThrows(AurigaException.class, () -> source.verifiedConnection(original));
            var verified = source.verifiedConnection(current);
            assertNotNull(
                verified.anchor(),
                "legal C2 must carry a verified reference before restoring registered");
            assertEquals(current, verified.anchor().getConnection());
            authority.sameHistoryOwner = !wrongOwner;
            if (wrongOwner) {
              assertThrows(
                  AurigaException.class,
                  () -> source.readConnectionAnchor(originalRef, verified.anchor()));
              peer.receiptOwnerValid = false;
              assertThrows(AurigaException.class, () -> source.readSourceReceipt(request));
            } else {
              var proof = source.readConnectionAnchor(originalRef, verified.anchor());
              assertEquals(originalRef, proof.getOriginal());
              assertEquals(verified.anchor(), proof.getCurrent());
              assertEquals(receipt, source.readSourceReceipt(request));
              assertEquals(verified.anchor(), source.verifiedConnection(current).anchor());
              assertEquals(original, peer.lastRead.getOriginalRequest().getConnection());
            }
          });
      assertEquals(0, peer.registerCalls.get());
      assertEquals(0, peer.getCalls.get());
      assertEquals(1, peer.sources.size() + peer.receipts.size());
    } finally {
      assertTrue(channel.shutdownNow().awaitTermination(5, TimeUnit.SECONDS));
      assertTrue(server.shutdownNow().awaitTermination(5, TimeUnit.SECONDS));
    }
  }
}
