package auriga.collab;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.collab.app.NetworkMailboxWireService;
import auriga.collab.grpc.NetworkMailboxGrpcService;
import auriga.common.context.TenantContext;
import auriga.common.error.*;
import auriga.common.grpc.*;
import auriga.common.security.*;
import auriga.contracts.collab.v1.*;
import io.grpc.*;
import java.time.Duration;
import java.util.*;
import java.util.concurrent.TimeUnit;
import org.junit.jupiter.api.Test;

class NetworkMailboxGrpcTest {
  @Test
  void realCommonInterceptorGuardsAllFourMethodsAndMapsErrors() throws Exception {
    var wire = mock(NetworkMailboxWireService.class);
    when(wire.ready(any(), any())).thenReturn("ready-result");
    when(wire.head(any())).thenReturn("head-result");
    when(wire.fetch(any(), any())).thenReturn("fetch-result");
    when(wire.receipt(any(), any())).thenReturn("receipt-result");
    var verifier = mock(AccessTokenVerifier.class);
    when(verifier.verify(anyString())).thenAnswer(i -> principal(i.getArgument(0)));
    var server =
        new GrpcServer(
            "127.0.0.1",
            0,
            List.of(new NetworkMailboxGrpcService(wire)),
            new AurigaServerInterceptor(verifier, Set.of(), Set.of("svc:ai-gateway", "svc:other")),
            null,
            false,
            Set.of());
    server.start();
    var channels = new ArrayList<ManagedChannel>();
    UUID tenant = UUID.randomUUID();
    var connection =
        NetworkMailboxConnection.newBuilder()
            .setTenantId(tenant.toString())
            .setRealm("2")
            .setNode("20")
            .setIngress("ingress-test")
            .setServerId("broker-test")
            .setClientId("12")
            .setClientAccount("clients-test")
            .build();
    try {
      for (String caller : List.of("svc:ai-gateway", "svc:other", "user")) {
        ManagedChannel channel =
            GrpcChannels.open(
                "127.0.0.1:" + server.port(),
                new AurigaClientInterceptor(() -> caller, Duration.ofSeconds(2)));
        channels.add(channel);
        var rpc = NetworkMailboxServiceGrpc.newBlockingStub(channel);
        TenantContext.run(
            tenant,
            () -> {
              List<Runnable> calls =
                  List.of(
                      () ->
                          assertEquals(
                              "ready-result",
                              rpc.ready(
                                      NetworkMailboxServiceReadyRequest.newBuilder()
                                          .setConnection(connection)
                                          .build())
                                  .getFrameJson()
                                  .toStringUtf8()),
                      () ->
                          assertEquals(
                              "head-result",
                              rpc.head(
                                      NetworkMailboxServiceHeadRequest.newBuilder()
                                          .setConnection(connection)
                                          .build())
                                  .getFrameJson()
                                  .toStringUtf8()),
                      () ->
                          assertEquals(
                              "fetch-result",
                              rpc.fetch(
                                      NetworkMailboxServiceFetchRequest.newBuilder()
                                          .setConnection(connection)
                                          .build())
                                  .getFrameJson()
                                  .toStringUtf8()),
                      () ->
                          assertEquals(
                              "receipt-result",
                              rpc.receipt(
                                      NetworkMailboxServiceReceiptRequest.newBuilder()
                                          .setConnection(connection)
                                          .build())
                                  .getFrameJson()
                                  .toStringUtf8()));
              for (Runnable call : calls) {
                if (caller.equals("svc:ai-gateway")) call.run();
                else rejected(call, Status.Code.PERMISSION_DENIED, "common.forbidden");
              }
            });
        if (caller.equals("svc:ai-gateway")) {
          rejected(
              () ->
                  rpc.head(
                      NetworkMailboxServiceHeadRequest.newBuilder()
                          .setConnection(connection)
                          .build()),
              Status.Code.PERMISSION_DENIED,
              "common.forbidden");
          TenantContext.run(
              UUID.randomUUID(),
              () ->
                  rejected(
                      () ->
                          rpc.head(
                              NetworkMailboxServiceHeadRequest.newBuilder()
                                  .setConnection(connection)
                                  .build()),
                      Status.Code.PERMISSION_DENIED,
                      "common.forbidden"));
          when(wire.head(any())).thenThrow(new IllegalStateException("private database details"));
          TenantContext.run(
              tenant,
              () ->
                  rejected(
                      () ->
                          rpc.head(
                              NetworkMailboxServiceHeadRequest.newBuilder()
                                  .setConnection(connection)
                                  .build()),
                      Status.Code.INTERNAL,
                      "common.internal"));
          doThrow(new AurigaException(auriga.collab.app.NetworkErrors.STALE_EPOCH))
              .when(wire)
              .head(any());
          TenantContext.run(
              tenant,
              () ->
                  rejected(
                      () ->
                          rpc.head(
                              NetworkMailboxServiceHeadRequest.newBuilder()
                                  .setConnection(connection)
                                  .build()),
                      Status.Code.FAILED_PRECONDITION,
                      "network.stale_epoch"));
        }
      }
      ManagedChannel anonymous = GrpcChannels.open("127.0.0.1:" + server.port());
      channels.add(anonymous);
      rejected(
          () ->
              NetworkMailboxServiceGrpc.newBlockingStub(anonymous)
                  .head(NetworkMailboxServiceHeadRequest.getDefaultInstance()),
          Status.Code.UNAUTHENTICATED,
          "common.unauthenticated");
      verify(wire, times(1)).ready(any(), any());
      verify(wire, times(1)).fetch(any(), any());
      verify(wire, times(1)).receipt(any(), any());
    } finally {
      for (var channel : channels) channel.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
      server.stop();
    }
  }

  static AurigaPrincipal principal(String subject) {
    return new AurigaPrincipal(
        subject,
        TenantContext.PLATFORM_TENANT,
        subject.equals("user") ? PrincipalType.USER : PrincipalType.INTERNAL_SERVICE,
        TokenContext.SERVICE,
        null,
        null,
        null,
        List.of(),
        Set.of(),
        null,
        null,
        "test");
  }

  static void rejected(Runnable call, Status.Code status, String code) {
    var e = assertThrows(StatusRuntimeException.class, call::run);
    assertEquals(status, e.getStatus().getCode());
    assertEquals(code, e.getTrailers().get(GrpcMetadata.ERROR_CODE));
    assertFalse(e.getStatus().toString().contains("private database details"));
  }
}
