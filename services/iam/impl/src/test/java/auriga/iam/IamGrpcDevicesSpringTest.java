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
              new IamGrpcSessions(sessions),
              java.time.Clock.systemUTC(),
              new org.springframework.beans.factory.support.StaticListableBeanFactory()
                  .getBeanProvider(auriga.iam.api.InstanceAiAuthorization.class));
    }
  }

  @Test
  void productionSpringFactoryConsumesRealAiAuthorityAndOnlinePermissions() throws Exception {
    var fixture = new DeviceAiAccessAuthorityTest();
    fixture.setup();
    var store = mock(IamStore.class);
    when(store.tx(any())).thenAnswer(i -> ((Supplier<?>) i.getArgument(0)).get());
    String sid = UUID.randomUUID().toString();
    var row =
        Json.object(
            "sessionId",
            sid,
            "accountId",
            fixture.account.toString(),
            "audience",
            "portal",
            "expiresAt",
            fixture.clock.instant().plusSeconds(600).toString(),
            "idleExpiresAt",
            fixture.clock.instant().plusSeconds(120).toString());
    var device =
        Json.object(
            "sessionId",
            sid,
            "accountId",
            fixture.account.toString(),
            "scope",
            "auriga.connect",
            "revoked",
            false);
    when(store.get(eq("device"), eq(fixture.device.toString()), anyBoolean())).thenReturn(device);
    var sessions = mock(SessionManager.class);
    when(sessions.activeById(sid)).thenReturn(row);
    var provider = new org.springframework.beans.factory.support.StaticListableBeanFactory();
    provider.addBean("authorization", fixture.authorization);
    String raw = fixture.sign(fixture.claims());
    new ApplicationContextRunner()
        .withConfiguration(
            AutoConfigurations.of(AopAutoConfiguration.class, AurigaCoreAutoConfiguration.class))
        .withBean(
            IamGrpcDevices.class,
            () ->
                new IamRuntimeConfiguration()
                    .iamGrpcDevices(
                        store,
                        sessions,
                        fixture.tokens,
                        new IamGrpcSessions(sessions),
                        fixture.clock,
                        provider.getBeanProvider(auriga.iam.api.InstanceAiAuthorization.class)))
        .run(
            context -> {
              assertNull(context.getStartupFailure());
              var service = context.getBean(IamGrpcDevices.class);
              assertTrue(AopUtils.isCglibProxy(service));
              var response = new Capture<VerifyDeviceAccessResponse>();
              as(
                  "svc:ai-gateway",
                  fixture.tenant,
                  () -> {
                    service.verifyDeviceAiAccess(
                        DeviceAiResourcePostgresTest.request(raw), response);
                    return null;
                  });
              var identity = response.values.getFirst();
              assertTrue(identity.getActive());
              assertEquals(fixture.account.toString(), identity.getAccountId());
              assertEquals(fixture.tenant.toString(), identity.getTenantId());
              assertEquals(fixture.device.toString(), identity.getDeviceId());
              assertEquals(
                  fixture.clock.instant().plusSeconds(120).getEpochSecond(),
                  identity.getExpiresAt().getSeconds());
              verify(fixture.authorization).getEffectivePermissions(any());
              doReturn(null).when(fixture.authorization).getEffectivePermissions(any());
              error(
                  "common.unavailable",
                  () ->
                      as(
                          "svc:ai-gateway",
                          fixture.tenant,
                          () -> {
                            service.verifyDeviceAiAccess(
                                DeviceAiResourcePostgresTest.request(raw), new Capture<>());
                            return null;
                          }));
            });
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
                  () -> service.verifyDeviceAiAccess(access, new Capture<>()));
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
              for (String denied : List.of("svc:edge", "svc:collab", "svc:workspace"))
                error(
                    "common.forbidden",
                    () ->
                        as(
                            denied,
                            tenant,
                            () -> {
                              service.verifyDeviceAiAccess(access, new Capture<>());
                              return null;
                            }));
              error(
                  "common.forbidden",
                  () ->
                      as(
                          "svc:ai-gateway",
                          null,
                          () -> {
                            service.verifyDeviceAiAccess(access, new Capture<>());
                            return null;
                          }));
              var aiResult = new Capture<VerifyDeviceAccessResponse>();
              as(
                  "svc:ai-gateway",
                  tenant,
                  () -> {
                    service.verifyDeviceAiAccess(access, aiResult);
                    return null;
                  });
              assertEquals(
                  List.of(VerifyDeviceAccessResponse.getDefaultInstance()), aiResult.values);
              assertTrue(aiResult.completed);
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
