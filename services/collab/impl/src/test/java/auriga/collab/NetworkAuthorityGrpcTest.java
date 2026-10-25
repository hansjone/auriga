package auriga.collab;

import static org.junit.jupiter.api.Assertions.*;

import auriga.collab.grpc.NetworkAuthorityClient;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.grpc.*;
import auriga.contracts.iam.v1.*;
import auriga.iam.api.NetworkAuthority.*;
import io.grpc.*;
import io.grpc.netty.shaded.io.grpc.netty.NettyServerBuilder;
import io.grpc.stub.StreamObserver;
import java.net.InetSocketAddress;
import java.time.Duration;
import java.time.Instant;
import java.util.*;
import java.util.concurrent.TimeUnit;
import org.junit.jupiter.api.Test;

class NetworkAuthorityGrpcTest {
  @Test
  void delegatesTenantAndMapsAllChecksWithoutCaching() throws Exception {
    UUID tenant = UUID.randomUUID();
    var fake = new Authority();
    var seen = new ArrayList<String>();
    Server server =
        NettyServerBuilder.forAddress(new InetSocketAddress("127.0.0.1", 0))
            .intercept(
                new ServerInterceptor() {
                  @Override
                  public <Q, R> ServerCall.Listener<Q> interceptCall(
                      ServerCall<Q, R> call, Metadata headers, ServerCallHandler<Q, R> next) {
                    seen.add(headers.get(GrpcMetadata.AUTHORIZATION));
                    assertEquals(tenant.toString(), headers.get(GrpcMetadata.TENANT_ID));
                    return next.startCall(call, headers);
                  }
                })
            .addService(fake)
            .build()
            .start();
    ManagedChannel channel =
        GrpcChannels.open(
            "127.0.0.1:" + server.getPort(),
            new AurigaClientInterceptor(() -> "collab-test-token", Duration.ofSeconds(2)));
    try {
      var client = new NetworkAuthorityClient(channel, Duration.ofSeconds(2));
      TenantContext.run(
          tenant,
          () -> {
            assertTrue(client.active("2", "20"));
            var check = new Check("2", "10", "20", "1", Sender.AGENT, Operation.SEND, 12, 1, 1);
            assertEquals(23, client.check(check).limits().get(Limit.BODY_BYTES));
            assertEquals(
                NetworkAuthoritySender.NETWORK_AUTHORITY_SENDER_AGENT, fake.last.getSender());
            assertEquals(
                NetworkAuthorityOperation.NETWORK_AUTHORITY_OPERATION_SEND,
                fake.last.getOperation());
            fake.allowed = false;
            assertFalse(client.check(check).allowed());
            fake.allowed = true;
            var binding = connection(tenant);
            assertEquals(binding, client.resolve(binding, "0").getConnection());
            assertTrue(client.authorize(binding, "{}", "{}").isAfter(Instant.now()));
            fake.mismatch = true;
            denied(() -> client.resolve(binding, "0"), "common.unavailable");
            denied(() -> client.authorize(binding, "{}", "{}"), "common.unavailable");
            fake.mismatch = false;
            fake.expired = true;
            denied(() -> client.resolve(binding, "0"), "network.unauthorized");
            fake.expired = false;
            fake.incomplete = true;
            denied(() -> client.check(check), "common.unavailable");
          });
      assertTrue(seen.size() >= 9);
      assertTrue(seen.stream().allMatch("Bearer collab-test-token"::equals));
      assertThrows(AurigaException.class, () -> client.active("2", "20"));
    } finally {
      channel.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
      server.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
    }
  }

  @Test
  void deadlineAndUnavailableNeverBecomeAllows() throws Exception {
    Server server =
        NettyServerBuilder.forAddress(new InetSocketAddress("127.0.0.1", 0))
            .addService(
                new NetworkAuthorityServiceGrpc.NetworkAuthorityServiceImplBase() {
                  @Override
                  public void networkAuthorityActive(
                      NetworkAuthorityActiveRequest r,
                      StreamObserver<NetworkAuthorityActiveResponse> o) {
                    // Intentionally never complete: the client must enforce its own deadline.
                  }
                })
            .build()
            .start();
    ManagedChannel channel = GrpcChannels.open("127.0.0.1:" + server.getPort());
    try {
      var client = new NetworkAuthorityClient(channel, Duration.ofMillis(100));
      TenantContext.run(
          UUID.randomUUID(), () -> denied(() -> client.active("2", "20"), "common.unavailable"));
      server.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
      TenantContext.run(
          UUID.randomUUID(), () -> denied(() -> client.active("2", "20"), "common.unavailable"));
    } finally {
      channel.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
      server.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
    }
  }

  static NetworkAuthorityConnection connection(UUID tenant) {
    return NetworkAuthorityConnection.newBuilder()
        .setTenantId(tenant.toString())
        .setRealm("2")
        .setNode("20")
        .setIngress("ingress-test")
        .setServerId("broker-test")
        .setClientId("12")
        .setClientAccount("clients-test")
        .build();
  }

  static void denied(Runnable action, String code) {
    assertEquals(code, assertThrows(AurigaException.class, action::run).errorCode().code());
  }

  static class Authority extends NetworkAuthorityServiceGrpc.NetworkAuthorityServiceImplBase {
    boolean allowed = true, mismatch, expired, incomplete;
    CheckNetworkRequest last;

    @Override
    public void checkNetwork(CheckNetworkRequest r, StreamObserver<CheckNetworkResponse> o) {
      last = r;
      reply(o, CheckNetworkResponse.newBuilder().setAllowed(allowed).setReason("test").build());
    }

    @Override
    public void networkAuthorityActive(
        NetworkAuthorityActiveRequest r, StreamObserver<NetworkAuthorityActiveResponse> o) {
      reply(o, NetworkAuthorityActiveResponse.newBuilder().setActive(allowed).build());
    }

    @Override
    public void networkLimits(NetworkLimitsRequest r, StreamObserver<NetworkLimitsResponse> o) {
      var b = NetworkLimitsResponse.newBuilder();
      for (var l : Limit.values())
        if (!incomplete || l != Limit.BODY_BYTES)
          b.putLimits(l.name(), l == Limit.BODY_BYTES ? 23 : l.defaultValue());
      reply(o, b.build());
    }

    @Override
    public void resolveNetworkTarget(
        ResolveNetworkTargetRequest r, StreamObserver<ResolveNetworkTargetResponse> o) {
      reply(
          o,
          ResolveNetworkTargetResponse.newBuilder()
              .setAllowed(allowed)
              .setConnection(echo(r.getConnection()))
              .setLocal(r.getLocal())
              .setTargetType("system")
              .setValidUntil(expiry())
              .build());
    }

    @Override
    public void authorizeNetworkDelivery(
        AuthorizeNetworkDeliveryRequest r, StreamObserver<AuthorizeNetworkDeliveryResponse> o) {
      reply(
          o,
          AuthorizeNetworkDeliveryResponse.newBuilder()
              .setAllowed(allowed)
              .setConnection(echo(r.getConnection()))
              .setValidUntil(expiry())
              .build());
    }

    NetworkAuthorityConnection echo(NetworkAuthorityConnection c) {
      return mismatch ? c.toBuilder().setNode("21").build() : c;
    }

    String expiry() {
      return Instant.now().plusSeconds(expired ? -60 : 60).toString();
    }

    static <T> void reply(StreamObserver<T> o, T result) {
      o.onNext(result);
      o.onCompleted();
    }
  }
}
