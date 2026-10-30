package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.context.TenantContext;
import auriga.contracts.iam.v1.*;
import com.google.protobuf.UnknownFieldSet;
import com.nimbusds.jose.jwk.Curve;
import com.nimbusds.jose.jwk.gen.ECKeyGenerator;
import java.time.Clock;
import java.util.*;
import java.util.concurrent.TimeUnit;
import org.junit.jupiter.api.Test;

/** Real TCP/gRPC/JWT and strict input boundary only; persistence is covered by the PG suite. */
class NetworkSourceFenceTransportTest {
  @Test
  void malformedWrappersAndOriginalsNeverReachTheDatabaseOrRemoteAuthority() {
    var store = mock(IamStore.class);
    var network = mock(IamNetworkAuthority.class);
    var devices = mock(DeviceAccessAuthority.class);
    var org = mock(auriga.iam.api.InstanceAiAuthorization.class);
    var authority =
        new IamNetworkSourceAuthority(
            store,
            network,
            devices,
            org,
            Clock.systemUTC(),
            new IamNetworkSourceAuthority.Policy(0, 0));
    clearInvocations(store);
    var r = new NetworkSourceBoundaryTest().request();
    var unknown =
        UnknownFieldSet.newBuilder()
            .addField(100, UnknownFieldSet.Field.newBuilder().addVarint(1).build())
            .build();
    TenantContext.run(
        UUID.fromString(r.getConnection().getTenantId()),
        () -> {
          for (var bad :
              List.of(
                  FenceAgentSourceReceiptRequest.getDefaultInstance(),
                  NetworkSourceFencePostgresTest.fence(r).toBuilder()
                      .setUnknownFields(unknown)
                      .build(),
                  NetworkSourceFencePostgresTest.fence(
                      r.toBuilder().setUnknownFields(unknown).build()),
                  NetworkSourceFencePostgresTest.fence(
                      r.toBuilder()
                          .setConnection(r.getConnection().toBuilder().setUnknownFields(unknown))
                          .build()),
                  NetworkSourceFencePostgresTest.fence(
                      r.toBuilder()
                          .setRegistration(
                              r.getRegistration().toBuilder().setUnknownFields(unknown))
                          .build()),
                  NetworkSourceFencePostgresTest.fence(r.toBuilder().setScopeValue(999).build()),
                  NetworkSourceFencePostgresTest.fence(
                      r.toBuilder().setExpectedRevision(1).build())))
            IamGrpcDevicesSpringTest.error(
                "common.validation_failed", () -> authority.fenceReceipt(bad));
          IamGrpcDevicesSpringTest.error(
              "common.validation_failed", () -> authority.fenceReceipt(null));
        });
    verifyNoInteractions(store, network, devices, org);
  }

  @Test
  void originalBodyAndCompleteReceiptCrossRealGrpcAfterEachClockAdvance() throws Exception {
    var f = new NetworkSourceAuthorityPostgresTest();
    f.key = new ECKeyGenerator(Curve.P_256).keyID("fence-transport").generate();
    var signing = mock(SigningKeys.class);
    when(signing.current()).thenReturn(f.key);
    f.tokens =
        new TokenService(
            signing,
            new ContractSchemas(),
            (t, s, c) -> Set.of(),
            f.clock,
            "https://portal.example.com",
            Map.of());
    f.sources = mock(IamNetworkSourceAuthority.class);
    var r =
        new NetworkSourceBoundaryTest()
            .request().toBuilder()
                .setConnection(
                    new NetworkSourceBoundaryTest()
                        .request().getConnection().toBuilder().setTenantId(f.tenant.toString()))
                .build();
    var request = NetworkSourceFencePostgresTest.fence(r);
    var response =
        RegisterAgentSourceResponse.newBuilder()
            .setDefinitivelyUncommitted(true)
            .setOperationId(r.getOperationId())
            .setRequestDigest(IamNetworkSources.registerDigest(r))
            .setRejectionReason("original_connection_invalid_uncommitted")
            .build();
    when(f.sources.fenceReceipt(request)).thenReturn(response);
    f.withSpringRpc(
        stub ->
            TenantContext.run(
                f.tenant,
                () -> {
                  for (long advance : new long[] {0, 3500, 101, 7201}) {
                    f.clock.advance(advance);
                    assertEquals(
                        response,
                        stub.withDeadlineAfter(5, TimeUnit.SECONDS)
                            .fenceAgentSourceReceipt(request));
                  }
                }));
    verify(f.sources, times(4)).fenceReceipt(request);
    verifyNoMoreInteractions(f.sources);
  }

  @Test
  void protocolHasOneFrozenOriginalFieldAndReusesRegisterResponse() {
    var fields = FenceAgentSourceReceiptRequest.getDescriptor().getFields();
    assertEquals(1, fields.size());
    assertEquals("original_request", fields.getFirst().getName());
    assertEquals(1, fields.getFirst().getNumber());
    assertEquals(RegisterAgentSourceRequest.getDescriptor(), fields.getFirst().getMessageType());
    var service = NetworkSource.getDescriptor().findServiceByName("NetworkSourceAuthorityService");
    assertEquals(
        RegisterAgentSourceResponse.getDescriptor(),
        service.findMethodByName("FenceAgentSourceReceipt").getOutputType());
  }
}
