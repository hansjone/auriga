package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.autoconfigure.AurigaCoreAutoConfiguration;
import auriga.common.context.TenantContext;
import auriga.common.grpc.*;
import auriga.common.security.*;
import auriga.contracts.iam.v1.*;
import java.time.*;
import java.util.*;
import org.junit.jupiter.api.Test;
import org.springframework.boot.autoconfigure.AutoConfigurations;
import org.springframework.boot.autoconfigure.aop.AopAutoConfiguration;
import org.springframework.boot.test.context.runner.ApplicationContextRunner;
import org.springframework.context.annotation.*;
import org.springframework.mock.env.MockEnvironment;

class IamGrpcNetworkSourcesSpringTest {
  @Configuration(proxyBeanMethods = false)
  static class Beans {
    @Bean
    IamNetworkSourceAuthority sourceAuthority() {
      return mock(IamNetworkSourceAuthority.class);
    }

    @Bean
    IamGrpcNetworkSources sources(IamNetworkSourceAuthority authority) {
      return new IamRuntimeConfiguration()
          .iamGrpcNetworkSources(authority, mock(IamGrpcSessions.class));
    }
  }

  @Test
  void springProxyRealJwtServiceTenantMatrixAndTransportGuard() throws Exception {
    var key =
        new com.nimbusds.jose.jwk.gen.ECKeyGenerator(com.nimbusds.jose.jwk.Curve.P_256)
            .keyID(UUID.randomUUID().toString())
            .generate();
    var signing = mock(SigningKeys.class);
    when(signing.current()).thenReturn(key);
    var clock = Clock.systemUTC();
    String secret = Secrets.random(), otherSecret = Secrets.random();
    var tokens =
        new TokenService(
            signing,
            new ContractSchemas(),
            (t, s, c) -> Set.of(),
            clock,
            "https://portal.example.com",
            Map.of("collab", Secrets.hash(secret), "edge", Secrets.hash(otherSecret)));
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
            new com.nimbusds.jose.jwk.JWKSet(key.toPublicJWK()));
    var verifier = new AccessTokenVerifier(AccessTokenDecoders.createInternal(settings, jwks));
    UUID tenant = UUID.randomUUID();
    String collab = tokens.issue("collab", secret), edge = tokens.issue("edge", otherSecret);
    new ApplicationContextRunner()
        .withConfiguration(
            AutoConfigurations.of(AopAutoConfiguration.class, AurigaCoreAutoConfiguration.class))
        .withUserConfiguration(Beans.class)
        .run(
            context -> {
              assertNull(context.getStartupFailure());
              var service = context.getBean(IamGrpcNetworkSources.class);
              assertTrue(org.springframework.aop.support.AopUtils.isCglibProxy(service));
              var authority = context.getBean(IamNetworkSourceAuthority.class);
              var c =
                  new NetworkSourceBoundaryTest()
                      .request().getConnection().toBuilder().setTenantId(tenant.toString()).build();
              var register = RegisterAgentSourceRequest.newBuilder().setConnection(c).build();
              var get = GetAgentSourceRequest.newBuilder().setConnection(c).build();
              var revoke = RevokeAgentSourceRequest.newBuilder().setConnection(c).build();
              when(authority.register(register))
                  .thenReturn(RegisterAgentSourceResponse.getDefaultInstance());
              when(authority.get(get)).thenReturn(GetAgentSourceResponse.getDefaultInstance());
              when(authority.revoke(revoke))
                  .thenReturn(RevokeAgentSourceResponse.getDefaultInstance());
              var server =
                  new GrpcServer(
                      "127.0.0.1",
                      0,
                      List.of(service),
                      new AurigaServerInterceptor(
                          verifier, Set.of(), Set.of("svc:collab", "svc:edge")),
                      null,
                      false,
                      Set.of());
              var channels = new ArrayList<io.grpc.ManagedChannel>();
              try {
                server.start();
                java.util.function.Function<
                        String,
                        NetworkSourceAuthorityServiceGrpc.NetworkSourceAuthorityServiceBlockingStub>
                    open =
                        token -> {
                          var channel =
                              GrpcChannels.open(
                                  "127.0.0.1:" + server.port(),
                                  new AurigaClientInterceptor(() -> token, Duration.ofSeconds(5)));
                          channels.add(channel);
                          return NetworkSourceAuthorityServiceGrpc.newBlockingStub(channel);
                        };
                var stub = open.apply(collab);
                TenantContext.run(
                    tenant,
                    () -> {
                      assertEquals(
                          RegisterAgentSourceResponse.getDefaultInstance(),
                          stub.registerAgentSource(register));
                      assertEquals(
                          GetAgentSourceResponse.getDefaultInstance(), stub.getAgentSource(get));
                      assertEquals(
                          RevokeAgentSourceResponse.getDefaultInstance(),
                          stub.revokeAgentSource(revoke));
                      for (var error :
                          List.of(
                              auriga.common.error.CommonErrors.CONFLICT,
                              auriga.common.error.CommonErrors.VALIDATION_FAILED,
                              auriga.common.error.CommonErrors.FORBIDDEN,
                              auriga.common.error.CommonErrors.UNAVAILABLE)) {
                        doThrow(new auriga.common.error.AurigaException(error))
                            .when(authority)
                            .register(register);
                        io.grpc.Status.Code status =
                            switch (error.code()) {
                              case "common.conflict" -> io.grpc.Status.Code.ABORTED;
                              case "common.validation_failed" ->
                                  io.grpc.Status.Code.INVALID_ARGUMENT;
                              case "common.forbidden" -> io.grpc.Status.Code.PERMISSION_DENIED;
                              default -> io.grpc.Status.Code.UNAVAILABLE;
                            };
                        IamGrpcNetworkAuthoritySpringTest.grpc(
                            status, () -> stub.registerAgentSource(register));
                      }
                      var other = open.apply(edge);
                      IamGrpcNetworkAuthoritySpringTest.grpc(
                          io.grpc.Status.Code.PERMISSION_DENIED, () -> other.getAgentSource(get));
                      IamGrpcNetworkAuthoritySpringTest.grpc(
                          io.grpc.Status.Code.PERMISSION_DENIED,
                          () ->
                              stub.getAgentSource(
                                  get.toBuilder()
                                      .setConnection(
                                          c.toBuilder().setTenantId(UUID.randomUUID().toString()))
                                      .build()));
                    });
                IamGrpcNetworkAuthoritySpringTest.grpc(
                    io.grpc.Status.Code.PERMISSION_DENIED, () -> stub.getAgentSource(get));
                TenantContext.run(
                    TenantContext.PLATFORM_TENANT,
                    () ->
                        IamGrpcNetworkAuthoritySpringTest.grpc(
                            io.grpc.Status.Code.INVALID_ARGUMENT, () -> stub.getAgentSource(get)));
                for (String type : List.of("user", "device")) {
                  String token =
                      tokens.access(
                          tenant,
                          UUID.randomUUID().toString(),
                          "auriga",
                          type,
                          type.equals("user") ? "portal" : "device",
                          type.equals("user") ? UUID.randomUUID() : null,
                          type.equals("device") ? UUID.randomUUID() : null,
                          clock.instant(),
                          null,
                          "openid");
                  var external = open.apply(token);
                  TenantContext.run(
                      tenant,
                      () ->
                          IamGrpcNetworkAuthoritySpringTest.grpc(
                              io.grpc.Status.Code.UNAUTHENTICATED,
                              () -> external.getAgentSource(get)));
                }
              } finally {
                for (var channel : channels) channel.shutdownNow();
                server.stop();
              }
            });
    var env =
        new MockEnvironment()
            .withProperty("auriga.grpc.server.enabled", "true")
            .withProperty("auriga.grpc.server.host", "0.0.0.0");
    assertThrows(
        IllegalStateException.class,
        () -> new IamRuntimeConfiguration().iamGrpcSessions(mock(SessionManager.class), env));
  }

  @Test
  void explicitImmutableDeploymentPolicyRejectsInvalidAndOverflowValues() {
    var empty = IamNetworkSourceAuthority.Policy.from(new MockEnvironment());
    assertFalse(empty.enabled());
    assertTrue(
        IamNetworkSourceAuthority.Policy.from(
                new MockEnvironment()
                    .withProperty(
                        "auriga.iam.network.source-max-seconds", Long.toString(Long.MAX_VALUE))
                    .withProperty("auriga.iam.network.source-decision-seconds", "300"))
            .enabled());
    for (String invalid : List.of("-1", "1.5", "NaN", "9223372036854775808", "")) {
      var env =
          new MockEnvironment().withProperty("auriga.iam.network.source-max-seconds", invalid);
      assertThrows(IllegalStateException.class, () -> IamNetworkSourceAuthority.Policy.from(env));
    }
    for (String decision : List.of("301", "-1", "0.1", "9223372036854775808")) {
      var env =
          new MockEnvironment()
              .withProperty("auriga.iam.network.source-decision-seconds", decision);
      assertThrows(IllegalStateException.class, () -> IamNetworkSourceAuthority.Policy.from(env));
    }
    var env =
        new MockEnvironment()
            .withProperty("auriga.iam.network.source-max-seconds", "30")
            .withProperty("auriga.iam.network.source-decision-seconds", "5");
    var policy = IamNetworkSourceAuthority.Policy.from(env);
    assertTrue(policy.enabled());
    env.setProperty("auriga.iam.network.source-max-seconds", "0");
    assertTrue(policy.enabled());
    assertFalse(IamNetworkSourceAuthority.Policy.from(env).enabled());
    env.setProperty("auriga.iam.network.source-max-seconds", "4");
    assertThrows(IllegalStateException.class, () -> IamNetworkSourceAuthority.Policy.from(env));
  }

  @Test
  void callersAndAllFourErrorsArePreservedWithoutDetails() {
    var authority = mock(IamNetworkSourceAuthority.class);
    var service = new IamGrpcNetworkSources(authority);
    var caller = new IamGrpcDevicesSpringTest();
    UUID tenant = UUID.randomUUID();
    var request =
        RegisterAgentSourceRequest.newBuilder()
            .setConnection(NetworkAuthorityConnection.newBuilder().setTenantId(tenant.toString()))
            .build();
    for (String subject : List.of("svc:edge", "svc:org", "svc:ai-gateway"))
      IamGrpcDevicesSpringTest.error(
          "common.forbidden",
          () ->
              caller.as(
                  subject,
                  tenant,
                  () -> {
                    service.registerAgentSource(request, new IamGrpcDevicesSpringTest.Capture<>());
                    return null;
                  }));
    caller.as(
        "svc:collab",
        tenant,
        () -> {
          for (var code :
              List.of(
                  auriga.common.error.CommonErrors.VALIDATION_FAILED,
                  auriga.common.error.CommonErrors.CONFLICT,
                  auriga.common.error.CommonErrors.FORBIDDEN,
                  auriga.common.error.CommonErrors.UNAVAILABLE)) {
            doThrow(new auriga.common.error.AurigaException(code))
                .when(authority)
                .register(request);
            IamGrpcDevicesSpringTest.error(
                code.code(),
                () ->
                    service.registerAgentSource(request, new IamGrpcDevicesSpringTest.Capture<>()));
          }
          return null;
        });
    IamGrpcDevicesSpringTest.error(
        "common.forbidden",
        () ->
            caller.as(
                "svc:collab",
                null,
                () -> {
                  service.registerAgentSource(request, new IamGrpcDevicesSpringTest.Capture<>());
                  return null;
                }));
    verify(authority, times(4)).register(request);
  }
}
