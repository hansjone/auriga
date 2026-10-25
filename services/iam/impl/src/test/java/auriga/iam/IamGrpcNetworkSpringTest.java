package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.autoconfigure.AurigaCoreAutoConfiguration;
import auriga.common.context.*;
import auriga.common.security.*;
import auriga.contracts.iam.v1.*;
import auriga.iam.api.NetworkAuthority;
import java.time.Instant;
import java.util.*;
import org.junit.jupiter.api.Test;
import org.springframework.aop.support.AopUtils;
import org.springframework.boot.autoconfigure.AutoConfigurations;
import org.springframework.boot.autoconfigure.aop.AopAutoConfiguration;
import org.springframework.boot.test.context.runner.ApplicationContextRunner;
import org.springframework.context.annotation.*;

class IamGrpcNetworkSpringTest {
  @Configuration(proxyBeanMethods = false)
  static class Beans {
    @Bean
    NetworkAuthority authority() {
      return mock(NetworkAuthority.class);
    }

    @Bean
    IamGrpcNetwork service(NetworkAuthority authority) {
      return new IamRuntimeConfiguration().iamGrpcNetwork(authority, mock(IamGrpcSessions.class));
    }
  }

  @Test
  void productionBeanHasSecurityProxyAndOnlyGatewayCanRedeemOrQueryActive() {
    new ApplicationContextRunner()
        .withConfiguration(
            AutoConfigurations.of(AopAutoConfiguration.class, AurigaCoreAutoConfiguration.class))
        .withUserConfiguration(Beans.class)
        .run(
            context -> {
              assertNull(context.getStartupFailure());
              var service = context.getBean(IamGrpcNetwork.class);
              var authority = context.getBean(NetworkAuthority.class);
              assertTrue(AopUtils.isCglibProxy(service));
              auriga.common.grpc.GrpcAuthorizationCheck.verify(List.of(service), Set.of());
              var request =
                  RedeemNetworkTicketRequest.newBuilder()
                      .setTicket("secret")
                      .setNode("1")
                      .setDevice("device")
                      .setServerAudience("server-a")
                      .build();
              var active = NetworkNodeActiveRequest.newBuilder().setRealm("2").setNode("1").build();
              var batch =
                  BatchNetworkNodesActiveRequest.newBuilder()
                      .addNodes(
                          NetworkNodeIdentity.newBuilder()
                              .setTenantId(UUID.randomUUID().toString())
                              .setRealm("2")
                              .setNode("1"))
                      .build();
              var identities = new IamGrpcDevicesSpringTest();
              IamGrpcDevicesSpringTest.error(
                  "common.unauthenticated",
                  () ->
                      service.redeemNetworkTicket(
                          request, new IamGrpcDevicesSpringTest.Capture<>()));
              for (String caller : List.of("svc:edge", "svc:collab", "svc:workspace")) {
                identities.as(
                    caller,
                    identities.tenant,
                    () -> {
                      IamGrpcDevicesSpringTest.error(
                          "common.forbidden",
                          () ->
                              service.redeemNetworkTicket(
                                  request, new IamGrpcDevicesSpringTest.Capture<>()));
                      IamGrpcDevicesSpringTest.error(
                          "common.forbidden",
                          () ->
                              service.networkNodeActive(
                                  active, new IamGrpcDevicesSpringTest.Capture<>()));
                      IamGrpcDevicesSpringTest.error(
                          "common.forbidden",
                          () ->
                              service.batchNetworkNodesActive(
                                  batch, new IamGrpcDevicesSpringTest.Capture<>()));
                      return null;
                    });
              }
              identities.as(
                  "svc:ai-gateway",
                  null,
                  () -> {
                    IamGrpcDevicesSpringTest.error(
                        "common.forbidden",
                        () ->
                            service.networkNodeActive(
                                active, new IamGrpcDevicesSpringTest.Capture<>()));
                    return null;
                  });
              verifyNoInteractions(authority);
              var expiry = Instant.now().plusSeconds(3600);
              when(authority.redeem(
                      "secret",
                      "1",
                      "device",
                      "server-a",
                      new NetworkAuthority.IngressBinding("", "", "", "", "")))
                  .thenReturn(
                      new NetworkAuthority.Connection(
                          new NetworkAuthority.Node("2", "1", "account", "device", false),
                          "server-a",
                          expiry));
              when(authority.active("2", "1")).thenReturn(true);
              when(authority.activeBatch(anyList()))
                  .thenAnswer(
                      call -> {
                        assertEquals(
                            batch.getNodes(0).getTenantId(), TenantContext.require().toString());
                        return Map.of(new NetworkAuthority.NodeReference("2", "1"), true);
                      });
              identities.as(
                  "svc:ai-gateway",
                  identities.tenant,
                  () -> {
                    var redeemed =
                        new IamGrpcDevicesSpringTest.Capture<RedeemNetworkTicketResponse>();
                    service.redeemNetworkTicket(request, redeemed);
                    assertTrue(redeemed.completed);
                    assertEquals("2", redeemed.values.getFirst().getRealm());
                    assertEquals(expiry.toString(), redeemed.values.getFirst().getExpiresAt());
                    var live = new IamGrpcDevicesSpringTest.Capture<NetworkNodeActiveResponse>();
                    service.networkNodeActive(active, live);
                    assertTrue(live.completed);
                    assertTrue(live.values.getFirst().getActive());
                    var many =
                        new IamGrpcDevicesSpringTest.Capture<BatchNetworkNodesActiveResponse>();
                    service.batchNetworkNodesActive(batch, many);
                    assertTrue(many.completed);
                    assertTrue(many.values.getFirst().getNodes(0).getActive());
                    IamGrpcDevicesSpringTest.error(
                        "common.validation_failed",
                        () ->
                            service.batchNetworkNodesActive(
                                BatchNetworkNodesActiveRequest.newBuilder()
                                    .addNodes(
                                        NetworkNodeIdentity.newBuilder()
                                            .setTenantId(TenantContext.PLATFORM_TENANT.toString())
                                            .setRealm("2")
                                            .setNode("1"))
                                    .build(),
                                new IamGrpcDevicesSpringTest.Capture<>()));
                    return null;
                  });
            });
  }
}
