package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.autoconfigure.AurigaCoreAutoConfiguration;
import auriga.contracts.iam.v1.*;
import auriga.iam.api.NetworkDirectory;
import java.util.*;
import org.junit.jupiter.api.Test;
import org.springframework.aop.support.AopUtils;
import org.springframework.boot.autoconfigure.AutoConfigurations;
import org.springframework.boot.autoconfigure.aop.AopAutoConfiguration;
import org.springframework.boot.test.context.runner.ApplicationContextRunner;
import org.springframework.context.annotation.*;

class IamGrpcNetworkDirectorySpringTest {
  @Configuration(proxyBeanMethods = false)
  static class Beans {
    @Bean
    NetworkDirectory directory() {
      return mock(NetworkDirectory.class);
    }

    @Bean
    IamGrpcNetworkDirectory service(NetworkDirectory directory) {
      return new IamRuntimeConfiguration()
          .iamGrpcNetworkDirectory(directory, mock(IamGrpcSessions.class));
    }
  }

  @Test
  void productionSecurityProxyRequiresExactCollabAndDelegatedBusinessTenant() {
    new ApplicationContextRunner()
        .withConfiguration(
            AutoConfigurations.of(AopAutoConfiguration.class, AurigaCoreAutoConfiguration.class))
        .withUserConfiguration(Beans.class)
        .run(
            context -> {
              assertNull(context.getStartupFailure());
              var service = context.getBean(IamGrpcNetworkDirectory.class);
              var directory = context.getBean(NetworkDirectory.class);
              assertTrue(AopUtils.isCglibProxy(service));
              auriga.common.grpc.GrpcAuthorizationCheck.verify(List.of(service), Set.of());
              var request =
                  ResolveNetworkNodeRequest.newBuilder().setRealm("2").setNode("1").build();
              var identities = new IamGrpcDevicesSpringTest();
              IamGrpcDevicesSpringTest.error(
                  "common.unauthenticated",
                  () ->
                      service.resolveNetworkNode(
                          request, new IamGrpcDevicesSpringTest.Capture<>()));
              for (String subject : List.of("svc:edge", "svc:ai-gateway", "svc:workspace")) {
                identities.as(
                    subject,
                    identities.tenant,
                    () -> {
                      IamGrpcDevicesSpringTest.error(
                          "common.forbidden",
                          () ->
                              service.resolveNetworkNode(
                                  request, new IamGrpcDevicesSpringTest.Capture<>()));
                      return null;
                    });
              }
              identities.as(
                  "svc:collab",
                  null,
                  () -> {
                    IamGrpcDevicesSpringTest.error(
                        "common.forbidden",
                        () ->
                            service.resolveNetworkNode(
                                request, new IamGrpcDevicesSpringTest.Capture<>()));
                    return null;
                  });
              verifyNoInteractions(directory);
              var found =
                  ResolveNetworkNodeResponse.newBuilder()
                      .setExists(true)
                      .setType(NetworkDirectoryNodeType.NETWORK_DIRECTORY_NODE_TYPE_DSH)
                      .build();
              when(directory.resolve("2", "1")).thenReturn(found);
              identities.as(
                  "svc:collab",
                  identities.tenant,
                  () -> {
                    var response =
                        new IamGrpcDevicesSpringTest.Capture<ResolveNetworkNodeResponse>();
                    service.resolveNetworkNode(request, response);
                    assertTrue(response.completed);
                    assertEquals(List.of(found), response.values);
                    return null;
                  });
              verify(directory).resolve("2", "1");
            });
  }
}
