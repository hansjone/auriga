package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.error.AurigaException;
import auriga.contracts.iam.v1.*;
import org.junit.jupiter.api.Test;

class AdminDeviceBindingsAuthTest {
  @Test
  void onlyEdgeWithDelegatedTenantCanReadBindings() {
    var devices = mock(IamAdminDevices.class);
    var service = new IamGrpcAdminDevices(devices);
    var identity = new IamGrpcDevicesSpringTest();
    Runnable read =
        () ->
            service.readBindings(
                ReadBindingsRequest.getDefaultInstance(), new IamGrpcDevicesSpringTest.Capture<>());
    assertEquals(
        "common.unauthenticated",
        assertThrows(AurigaException.class, read::run).errorCode().code());
    for (String subject : java.util.List.of("alice", "svc:collab", "svc:ai-gateway")) {
      identity.as(
          subject,
          identity.tenant,
          () -> {
            assertEquals(
                "common.forbidden",
                assertThrows(AurigaException.class, read::run).errorCode().code());
            return null;
          });
    }
    identity.as(
        "svc:edge",
        null,
        () -> {
          assertEquals(
              "common.forbidden",
              assertThrows(AurigaException.class, read::run).errorCode().code());
          return null;
        });
    verifyNoInteractions(devices);
    when(devices.connectionBindings()).thenReturn(Json.object("items", java.util.List.of()));
    identity.as(
        "svc:edge",
        identity.tenant,
        () -> {
          var result = new IamGrpcDevicesSpringTest.Capture<ReadBindingsResponse>();
          service.readBindings(ReadBindingsRequest.getDefaultInstance(), result);
          assertTrue(result.completed);
          assertEquals(0, result.values.getFirst().getItemsCount());
          return null;
        });
    verify(devices).connectionBindings();
  }
}
