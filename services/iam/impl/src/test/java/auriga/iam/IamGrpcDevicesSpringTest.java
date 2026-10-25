package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.autoconfigure.AurigaCoreAutoConfiguration;
import auriga.common.context.*;
import auriga.common.error.AurigaException;
import auriga.common.security.*;
import auriga.contracts.iam.v1.*;
import io.grpc.stub.StreamObserver;
import java.util.*;
import java.util.function.Supplier;
import org.junit.jupiter.api.Test;
import org.springframework.aop.framework.Advised;
import org.springframework.aop.support.AopUtils;
import org.springframework.boot.autoconfigure.AutoConfigurations;
import org.springframework.boot.autoconfigure.aop.AopAutoConfiguration;
import org.springframework.boot.test.context.runner.ApplicationContextRunner;
import org.springframework.context.annotation.*;

class IamGrpcDevicesSpringTest {
  final UUID tenant = UUID.randomUUID();

  @Configuration(proxyBeanMethods = false)
  static class DeviceBeans {
    @Bean
    IamGrpcDevices iamGrpcDevices() {
      var sessions = mock(SessionManager.class);
      // Use the production bean factory, with the real authorization auto-configurations.
      return new IamRuntimeConfiguration()
          .iamGrpcDevices(
              mock(IamStore.class),
              sessions,
              mock(TokenService.class),
              new IamGrpcSessions(sessions));
    }
  }

  @Test
  void productionBeanStartsAsSecurityProxyAndKeepsExactCallerChecks() {
    new ApplicationContextRunner()
        .withConfiguration(
            AutoConfigurations.of(AopAutoConfiguration.class, AurigaCoreAutoConfiguration.class))
        .withUserConfiguration(DeviceBeans.class)
        .run(
            context -> {
              assertNull(
                  context.getStartupFailure(),
                  "Production IAM device bean must survive Spring security proxy creation");
              var service = context.getBean("iamGrpcDevices", IamGrpcDevices.class);
              assertTrue(AopUtils.isCglibProxy(service));
              var advised = assertInstanceOf(Advised.class, service);
              assertTrue(
                  Arrays.stream(advised.getAdvisors())
                      .anyMatch(AuthorizationAdvisor.class::isInstance));
              auriga.common.grpc.GrpcAuthorizationCheck.verify(List.of(service), Set.of());
              var access = VerifyDeviceAccessRequest.getDefaultInstance();
              var credential =
                  VerifyDeviceCredentialRequest.newBuilder()
                      .setTenantId(tenant.toString())
                      .setAccountId("invalid")
                      .setDeviceId("invalid")
                      .build();
              error(
                  "common.unauthenticated",
                  () -> service.verifyDeviceAccess(access, new Capture<>()));
              error(
                  "common.unauthenticated",
                  () -> service.verifyDeviceCredential(credential, new Capture<>()));
              error(
                  "common.forbidden",
                  () ->
                      as(
                          "svc:edge",
                          tenant,
                          () -> {
                            service.verifyDeviceAccess(access, new Capture<>());
                            return null;
                          }));
              error(
                  "common.forbidden",
                  () ->
                      as(
                          "svc:ai-gateway",
                          null,
                          () -> {
                            service.verifyDeviceAccess(access, new Capture<>());
                            return null;
                          }));
              error(
                  "common.forbidden",
                  () ->
                      as(
                          "svc:ai-gateway",
                          tenant,
                          () -> {
                            service.verifyDeviceCredential(credential, new Capture<>());
                            return null;
                          }));
              error(
                  "common.forbidden",
                  () ->
                      as(
                          "svc:collab",
                          tenant,
                          () -> {
                            service.verifyDeviceAccess(access, new Capture<>());
                            return null;
                          }));
              var accessResult = new Capture<VerifyDeviceAccessResponse>();
              as(
                  "svc:ai-gateway",
                  tenant,
                  () -> {
                    service.verifyDeviceAccess(access, accessResult);
                    return null;
                  });
              assertEquals(
                  List.of(VerifyDeviceAccessResponse.getDefaultInstance()), accessResult.values);
              assertTrue(accessResult.completed);
              var credentialResult = new Capture<VerifyDeviceCredentialResponse>();
              as(
                  "svc:collab",
                  tenant,
                  () -> {
                    service.verifyDeviceCredential(credential, credentialResult);
                    return null;
                  });
              assertEquals(
                  List.of(VerifyDeviceCredentialResponse.getDefaultInstance()),
                  credentialResult.values);
              assertTrue(credentialResult.completed);
            });
  }

  <T> T as(String subject, UUID delegated, Supplier<T> work) {
    var principal =
        new AurigaPrincipal(
            subject,
            TenantContext.PLATFORM_TENANT,
            PrincipalType.INTERNAL_SERVICE,
            TokenContext.SERVICE,
            null,
            null,
            null,
            List.of(),
            Set.of(),
            null,
            null,
            "spring-test");
    return RequestContext.start("native-spring-test", null)
        .withPrincipal(principal)
        .call(() -> delegated == null ? work.get() : TenantContext.call(delegated, work::get));
  }

  static void error(String code, Runnable work) {
    assertEquals(code, assertThrows(AurigaException.class, work::run).errorCode().code());
  }

  static class Capture<T> implements StreamObserver<T> {
    final List<T> values = new ArrayList<>();
    boolean completed;

    public void onNext(T value) {
      values.add(value);
    }

    public void onError(Throwable error) {
      fail(error);
    }

    public void onCompleted() {
      completed = true;
    }
  }
}
