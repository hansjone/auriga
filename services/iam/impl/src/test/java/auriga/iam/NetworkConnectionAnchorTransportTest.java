package auriga.iam;

import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.mockito.Mockito.*;

import auriga.common.context.TenantContext;
import auriga.contracts.iam.v1.NetworkAuthorityActiveRequest;
import com.nimbusds.jose.jwk.Curve;
import com.nimbusds.jose.jwk.gen.ECKeyGenerator;
import java.util.Map;
import java.util.Set;
import java.util.concurrent.TimeUnit;
import org.junit.jupiter.api.Test;

/** Real JWT/gRPC only: reuse the PG fixture transport without running its database extension. */
class NetworkConnectionAnchorTransportTest {
  @Test
  void fixtureReauthenticatesEveryRpcAfterHistoricalClockAdvances() throws Exception {
    var fixture = new NetworkConnectionAnchorPostgresTest();
    fixture.key = new ECKeyGenerator(Curve.P_256).keyID("transport-test").generate();
    var signing = mock(SigningKeys.class);
    when(signing.current()).thenReturn(fixture.key);
    fixture.tokens =
        new TokenService(
            signing,
            new ContractSchemas(),
            (t, s, c) -> Set.of(),
            fixture.clock,
            "https://portal.example.com",
            Map.of());
    var authority = mock(IamNetworkAuthority.class);
    when(authority.active("2", "1")).thenReturn(true);
    fixture.rpc =
        new IamGrpcNetworkAuthority(
            authority, fixture.clock, mock(IamNetworkSourceAuthority.class));
    fixture.withRpc(
        stub -> {
          var request =
              NetworkAuthorityActiveRequest.newBuilder().setRealm("2").setNode("1").build();
          Runnable call =
              () ->
                  TenantContext.run(
                      fixture.tenant,
                      () ->
                          assertTrue(
                              stub.withDeadlineAfter(5, TimeUnit.SECONDS)
                                  .networkAuthorityActive(request)
                                  .getActive()));
          call.run();
          fixture.clock.advance(3500);
          call.run();
          fixture.clock.advance(101);
          call.run();
          fixture.clock.advance(7201);
          call.run();
        });
  }
}
