package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.context.*;
import auriga.common.error.AurigaException;
import auriga.common.security.*;
import auriga.contracts.iam.v1.*;
import java.util.*;
import org.junit.jupiter.api.Test;

class ManagedDeviceTransportTest {
  @Test
  void managedTokenUsesAuthenticatedHopClientAddress() {
    var oauth = mock(OAuthProvider.class);
    var requests = mock(RequestIdentity.class);
    var controller =
        new OAuthController(
            oauth,
            mock(SigningKeys.class),
            requests,
            mock(IamStore.class),
            mock(SessionManager.class));
    var request = new org.springframework.mock.web.MockHttpServletRequest();
    request.addParameter("grant_type", IamManagedDevices.GRANT);
    request.addHeader("X-Auriga-Client-IP", "198.51.100.1");
    when(requests.clientIp(request)).thenReturn("192.0.2.25");
    when(requests.call(eq(request), any()))
        .thenAnswer(i -> ((java.util.function.Supplier<?>) i.getArgument(1)).get());
    when(oauth.managedToken(anyMap(), anyString()))
        .thenReturn(Json.object("access_token", "fixture"));
    controller.token(
        Map.of("grant_type", IamManagedDevices.GRANT, "client_id", "auriga-managed-network"),
        request);
    verify(oauth).managedToken(anyMap(), eq("192.0.2.25"));
  }

  @Test
  void onlyWorkspaceInternalPrincipalWithDelegatedTenantMayIssueOrRevoke() {
    var devices = mock(IamManagedDevices.class);
    var grpc = new IamGrpcManagedDevices(devices);
    when(devices.issue(any())).thenReturn(IssueManagedDeviceResponse.getDefaultInstance());
    UUID tenant = UUID.randomUUID();
    for (String subject : List.of("svc:edge", "svc:ai-gateway", "member")) {
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
              "transport-test");
      RequestContext.start("managed-device-test", null)
          .withPrincipal(principal)
          .run(
              () ->
                  TenantContext.run(
                      tenant,
                      () -> {
                        assertEquals(
                            "common.forbidden",
                            assertThrows(
                                    AurigaException.class,
                                    () ->
                                        grpc.issueManagedDevice(
                                            IssueManagedDeviceRequest.getDefaultInstance(),
                                            mock(io.grpc.stub.StreamObserver.class)))
                                .errorCode()
                                .code());
                        assertEquals(
                            "common.forbidden",
                            assertThrows(
                                    AurigaException.class,
                                    () ->
                                        grpc.revokeManagedDevice(
                                            RevokeManagedDeviceRequest.getDefaultInstance(),
                                            mock(io.grpc.stub.StreamObserver.class)))
                                .errorCode()
                                .code());
                      }));
    }
    var workspace =
        new AurigaPrincipal(
            "svc:workspace",
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
            "transport-test");
    RequestContext.start("managed-device-test", null)
        .withPrincipal(workspace)
        .run(
            () -> {
              assertEquals(
                  "common.forbidden",
                  assertThrows(
                          AurigaException.class,
                          () ->
                              grpc.issueManagedDevice(
                                  IssueManagedDeviceRequest.getDefaultInstance(),
                                  mock(io.grpc.stub.StreamObserver.class)))
                      .errorCode()
                      .code());
              TenantContext.run(
                  tenant,
                  () ->
                      grpc.issueManagedDevice(
                          IssueManagedDeviceRequest.getDefaultInstance(),
                          mock(io.grpc.stub.StreamObserver.class)));
            });
    verify(devices).issue(any());
    verify(devices, never()).revoke(any());
  }
}
