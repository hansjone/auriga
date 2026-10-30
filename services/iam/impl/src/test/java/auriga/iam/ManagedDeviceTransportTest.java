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
