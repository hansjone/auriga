package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.autoconfigure.AurigaCoreAutoConfiguration;
import auriga.common.context.*;
import auriga.common.grpc.*;
import auriga.common.security.*;
import auriga.contracts.iam.v1.*;
import auriga.iam.api.NetworkAuthority.Decision;
import auriga.iam.api.NetworkAuthority.Limit;
import com.nimbusds.jose.jwk.*;
import com.nimbusds.jose.jwk.gen.ECKeyGenerator;
import java.time.*;
import java.util.*;
import org.junit.jupiter.api.Test;
import org.springframework.aop.support.AopUtils;
import org.springframework.boot.autoconfigure.AutoConfigurations;
import org.springframework.boot.autoconfigure.aop.AopAutoConfiguration;
import org.springframework.boot.test.context.runner.ApplicationContextRunner;
import org.springframework.context.annotation.*;

class IamGrpcNetworkAuthoritySpringTest {
  @Configuration(proxyBeanMethods = false)
  static class Beans {
    @Bean
    IamNetworkAuthority authority() {
      return mock(IamNetworkAuthority.class);
    }

    @Bean
    Clock clock() {
      return Clock.systemUTC();
    }

    @Bean
    IamGrpcNetworkAuthority service(IamNetworkAuthority authority, Clock clock) {
      var sources = mock(IamNetworkSourceAuthority.class);
      when(sources.authorizeDelivery(any()))
          .thenThrow(
              new auriga.common.error.AurigaException(
                  auriga.common.error.CommonErrors.UNAVAILABLE));
      return new IamRuntimeConfiguration()
          .iamGrpcNetworkAuthority(authority, mock(IamGrpcSessions.class), clock, sources);
    }

    @Bean
    IamGrpcNetwork gateway(IamNetworkAuthority authority) {
      return new IamRuntimeConfiguration().iamGrpcNetwork(authority, mock(IamGrpcSessions.class));
    }
  }

  @Test
  void springProxiesAndRealServiceJwtTransportKeepTheTwoAllowlistsSeparate() throws Exception {
    var clock = Clock.systemUTC();
    var key = new ECKeyGenerator(Curve.P_256).keyID(UUID.randomUUID().toString()).generate();
    var signing = mock(SigningKeys.class);
    when(signing.current()).thenReturn(key);
    String collabSecret = Secrets.random(), gatewaySecret = Secrets.random();
    var tokens =
        new TokenService(
            signing,
            new ContractSchemas(),
            (t, s, c) -> Set.of(),
            clock,
            "https://portal.example.com",
            Map.of(
                "collab", Secrets.hash(collabSecret), "ai-gateway", Secrets.hash(gatewaySecret)));
    var settings =
        new AccessTokenSettings(
            tokens.issuer(),
            java.net.URI.create(tokens.issuer() + "/oauth2/jwks"),
            null,
            null,
            null,
            null,
            clock,
            false);
    var jwks =
        new com.nimbusds.jose.jwk.source.ImmutableJWKSet<com.nimbusds.jose.proc.SecurityContext>(
            new JWKSet(key.toPublicJWK()));
    var verifier = new AccessTokenVerifier(AccessTokenDecoders.createInternal(settings, jwks));
    String collabToken = tokens.issue("collab", collabSecret),
        gatewayToken = tokens.issue("ai-gateway", gatewaySecret);
    UUID tenant = UUID.randomUUID();
    new ApplicationContextRunner()
        .withConfiguration(
            AutoConfigurations.of(AopAutoConfiguration.class, AurigaCoreAutoConfiguration.class))
        .withUserConfiguration(Beans.class)
        .run(
            context -> {
              assertNull(context.getStartupFailure());
              var service = context.getBean(IamGrpcNetworkAuthority.class);
              var gatewayService = context.getBean(IamGrpcNetwork.class);
              assertTrue(AopUtils.isCglibProxy(service));
              var authority = context.getBean(IamNetworkAuthority.class);
              when(authority.active("2", "1"))
                  .thenAnswer(
                      c -> {
                        assertEquals(tenant, TenantContext.require());
                        return true;
                      });
              var ceilings = new EnumMap<Limit, Long>(Limit.class);
              for (var limit : Limit.values()) ceilings.put(limit, limit.defaultValue());
              when(authority.effectiveLimits()).thenReturn(ceilings);
              when(authority.check(any())).thenReturn(new Decision(true, "private", ceilings));
              when(authority.resolveTarget(any())).thenReturn(clock.instant().plusSeconds(60));
              var server =
                  new GrpcServer(
                      "127.0.0.1",
                      0,
                      List.of(service, gatewayService),
                      new AurigaServerInterceptor(
                          verifier, Set.of(), Set.of("svc:collab", "svc:ai-gateway")),
                      null,
                      false,
                      Set.of());
              var channels = new ArrayList<io.grpc.ManagedChannel>();
              try {
                server.start();
                java.util.function.Function<String, io.grpc.ManagedChannel> open =
                    token -> {
                      var channel =
                          GrpcChannels.open(
                              "127.0.0.1:" + server.port(),
                              new AurigaClientInterceptor(() -> token, Duration.ofSeconds(5)));
                      channels.add(channel);
                      return channel;
                    };
                var collabChannel = open.apply(collabToken);
                var gatewayChannel = open.apply(gatewayToken);
                var stub = NetworkAuthorityServiceGrpc.newBlockingStub(collabChannel);
                var active =
                    NetworkAuthorityActiveRequest.newBuilder().setRealm("2").setNode("1").build();
                TenantContext.run(
                    tenant,
                    () -> {
                      assertTrue(stub.networkAuthorityActive(active).getActive());
                      assertEquals(
                          ceilings.size(),
                          stub.networkLimits(NetworkLimitsRequest.getDefaultInstance())
                              .getLimitsCount());
                      var fixture = new IamGrpcNetworkAuthorityTest();
                      assertTrue(stub.checkNetwork(fixture.check()).getAllowed());
                      var binding =
                          fixture.connection().toBuilder().setTenantId(tenant.toString()).build();
                      assertEquals(
                          binding,
                          stub.resolveNetworkTarget(
                                  ResolveNetworkTargetRequest.newBuilder()
                                      .setConnection(binding)
                                      .setLocal("0")
                                      .build())
                              .getConnection());
                      grpc(
                          io.grpc.Status.Code.UNAVAILABLE,
                          () ->
                              stub.authorizeNetworkDelivery(
                                  AuthorizeNetworkDeliveryRequest.newBuilder()
                                      .setConnection(binding)
                                      .build()));
                      grpc(
                          io.grpc.Status.Code.PERMISSION_DENIED,
                          () ->
                              stub.resolveNetworkTarget(
                                  ResolveNetworkTargetRequest.newBuilder()
                                      .setConnection(
                                          binding.toBuilder()
                                              .setTenantId(UUID.randomUUID().toString()))
                                      .setLocal("0")
                                      .build()));
                      var old = NetworkGatewayServiceGrpc.newBlockingStub(collabChannel);
                      grpc(
                          io.grpc.Status.Code.PERMISSION_DENIED,
                          () ->
                              old.redeemNetworkTicket(
                                  RedeemNetworkTicketRequest.getDefaultInstance()));
                      grpc(
                          io.grpc.Status.Code.PERMISSION_DENIED,
                          () ->
                              old.networkNodeActive(NetworkNodeActiveRequest.getDefaultInstance()));
                      grpc(
                          io.grpc.Status.Code.PERMISSION_DENIED,
                          () ->
                              old.batchNetworkNodesActive(
                                  BatchNetworkNodesActiveRequest.getDefaultInstance()));
                      var wrong = NetworkAuthorityServiceGrpc.newBlockingStub(gatewayChannel);
                      grpc(
                          io.grpc.Status.Code.PERMISSION_DENIED,
                          () -> wrong.networkAuthorityActive(active));
                      grpc(
                          io.grpc.Status.Code.PERMISSION_DENIED,
                          () ->
                              wrong.authorizeNetworkDelivery(
                                  AuthorizeNetworkDeliveryRequest.getDefaultInstance()));
                      assertTrue(
                          NetworkGatewayServiceGrpc.newBlockingStub(gatewayChannel)
                              .networkNodeActive(
                                  NetworkNodeActiveRequest.newBuilder()
                                      .setRealm("2")
                                      .setNode("1")
                                      .build())
                              .getActive());
                    });
                grpc(
                    io.grpc.Status.Code.PERMISSION_DENIED,
                    () -> stub.networkAuthorityActive(active));
                grpc(
                    io.grpc.Status.Code.PERMISSION_DENIED,
                    () ->
                        stub.authorizeNetworkDelivery(
                            AuthorizeNetworkDeliveryRequest.getDefaultInstance()));
                TenantContext.run(
                    TenantContext.PLATFORM_TENANT,
                    () ->
                        grpc(
                            io.grpc.Status.Code.INVALID_ARGUMENT,
                            () ->
                                stub.authorizeNetworkDelivery(
                                    AuthorizeNetworkDeliveryRequest.getDefaultInstance())));
                String user =
                    tokens.access(
                        tenant,
                        UUID.randomUUID().toString(),
                        "auriga",
                        "user",
                        "portal",
                        UUID.randomUUID(),
                        null,
                        clock.instant(),
                        null,
                        "openid");
                grpc(
                    io.grpc.Status.Code.UNAUTHENTICATED,
                    () ->
                        TenantContext.run(
                            tenant,
                            () ->
                                NetworkAuthorityServiceGrpc.newBlockingStub(open.apply(user))
                                    .networkAuthorityActive(active)));
                TenantContext.run(
                    tenant,
                    () ->
                        grpc(
                            io.grpc.Status.Code.UNAUTHENTICATED,
                            () ->
                                NetworkAuthorityServiceGrpc.newBlockingStub(open.apply(user))
                                    .authorizeNetworkDelivery(
                                        AuthorizeNetworkDeliveryRequest.getDefaultInstance())));
              } finally {
                for (var channel : channels) channel.shutdownNow();
                server.stop();
              }
            });
  }

  static void grpc(io.grpc.Status.Code code, Runnable work) {
    var e = assertThrows(io.grpc.StatusRuntimeException.class, work::run);
    assertEquals(code, e.getStatus().getCode());
    assertFalse(e.getMessage().contains("private"));
  }

  @Test
  void nonServiceContextOrNonPlatformPrincipalCannotUseAnInternalLookingSubject() {
    var fixture = new IamGrpcNetworkAuthorityTest();
    for (var type :
        List.of(PrincipalType.USER, PrincipalType.DEVICE, PrincipalType.INTERNAL_SERVICE)) {
      var principal =
          new AurigaPrincipal(
              "svc:collab",
              fixture.identity.tenant,
              type,
              TokenContext.SERVICE,
              null,
              null,
              null,
              List.of(),
              Set.of(),
              null,
              null,
              null);
      RequestContext.start("rpc-test", null)
          .withPrincipal(principal)
          .run(
              () ->
                  TenantContext.run(
                      fixture.identity.tenant,
                      () ->
                          IamGrpcNetworkAuthorityTest.error(
                              "common.forbidden",
                              () ->
                                  fixture.service.networkLimits(
                                      NetworkLimitsRequest.getDefaultInstance(),
                                      new IamGrpcDevicesSpringTest.Capture<>()))));
    }
    var wrongContext =
        new AurigaPrincipal(
            "svc:collab",
            TenantContext.PLATFORM_TENANT,
            PrincipalType.INTERNAL_SERVICE,
            TokenContext.PORTAL,
            null,
            null,
            null,
            List.of(),
            Set.of(),
            null,
            null,
            null);
    RequestContext.start("rpc-test", null)
        .withPrincipal(wrongContext)
        .run(
            () ->
                TenantContext.run(
                    fixture.identity.tenant,
                    () ->
                        IamGrpcNetworkAuthorityTest.error(
                            "common.forbidden",
                            () ->
                                fixture.service.networkLimits(
                                    NetworkLimitsRequest.getDefaultInstance(),
                                    new IamGrpcDevicesSpringTest.Capture<>()))));
    verifyNoInteractions(fixture.authority);
  }
}
