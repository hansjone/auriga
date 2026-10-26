package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.context.TenantContext;
import auriga.common.error.*;
import auriga.contracts.iam.v1.*;
import auriga.iam.api.NetworkAuthority.*;
import java.util.*;
import org.junit.jupiter.api.Test;

class IamGrpcNetworkAuthorityTest {
  final IamNetworkAuthority authority = mock(IamNetworkAuthority.class);
  final NetworkAuthorityTest.Time clock = new NetworkAuthorityTest.Time();
  final IamNetworkSourceAuthority sources = mock(IamNetworkSourceAuthority.class);
  final IamGrpcNetworkAuthority service = new IamGrpcNetworkAuthority(authority, clock, sources);
  final IamGrpcDevicesSpringTest identity = new IamGrpcDevicesSpringTest();

  CheckNetworkRequest check() {
    return CheckNetworkRequest.newBuilder()
        .setRealm("2")
        .setSourceNode("1")
        .setTargetNode("3")
        .setLocal("0")
        .setSender(NetworkAuthoritySender.NETWORK_AUTHORITY_SENDER_HUMAN)
        .setOperation(NetworkAuthorityOperation.NETWORK_AUTHORITY_OPERATION_RECEIVE)
        .setBodyBytes(10)
        .setTargetNodes(1)
        .setTargetLocals(1)
        .build();
  }

  NetworkAuthorityConnection connection() {
    return NetworkAuthorityConnection.newBuilder()
        .setTenantId(identity.tenant.toString())
        .setRealm("2")
        .setNode("1")
        .setIngress("a".repeat(26))
        .setServerId("N" + "A".repeat(55))
        .setClientId("42")
        .setClientAccount("CLIENTS")
        .build();
  }

  void collab(Runnable work) {
    identity.as(
        "svc:collab",
        identity.tenant,
        () -> {
          work.run();
          return null;
        });
  }

  static void error(String code, Runnable work) {
    var error = assertThrows(AurigaException.class, work::run);
    assertEquals(code, error.errorCode().code());
    assertTrue(error.params().isEmpty());
    assertTrue(error.violations().isEmpty());
    assertNull(error.getCause());
  }

  @Test
  void allMethodsRejectOtherCallersAndMissingBusinessDelegationBeforeWork() {
    List<Runnable> calls =
        List.of(
            () -> service.checkNetwork(check(), new IamGrpcDevicesSpringTest.Capture<>()),
            () ->
                service.networkAuthorityActive(
                    NetworkAuthorityActiveRequest.getDefaultInstance(),
                    new IamGrpcDevicesSpringTest.Capture<>()),
            () ->
                service.networkLimits(
                    NetworkLimitsRequest.getDefaultInstance(),
                    new IamGrpcDevicesSpringTest.Capture<>()),
            () ->
                service.resolveNetworkTarget(
                    ResolveNetworkTargetRequest.getDefaultInstance(),
                    new IamGrpcDevicesSpringTest.Capture<>()),
            () ->
                service.authorizeNetworkDelivery(
                    AuthorizeNetworkDeliveryRequest.getDefaultInstance(),
                    new IamGrpcDevicesSpringTest.Capture<>()));
    calls.forEach(call -> error("common.unauthenticated", call));
    for (String subject : List.of("svc:ai-gateway", "svc:edge", "alice"))
      identity.as(
          subject,
          identity.tenant,
          () -> {
            calls.forEach(call -> error("common.forbidden", call));
            return null;
          });
    for (UUID tenant : Arrays.asList(null, TenantContext.PLATFORM_TENANT))
      identity.as(
          "svc:collab",
          tenant,
          () -> {
            calls.forEach(call -> error("common.forbidden", call));
            return null;
          });
    verifyNoInteractions(authority);
  }

  @Test
  void rejectsUnknownEnumsUnsignedOverflowAndNoncanonicalFields() {
    collab(
        () -> {
          for (var bad :
              List.of(
                  check().toBuilder().setSenderValue(999),
                  check().toBuilder().clearSender(),
                  check().toBuilder().setOperationValue(999),
                  check().toBuilder().clearOperation(),
                  check().toBuilder().setRealm("02"),
                  check().toBuilder().setSourceNode("18446744073709551616"),
                  check().toBuilder().setTargetNode("0"),
                  check().toBuilder().setLocal("01"),
                  check().toBuilder().setLocal("1"),
                  check().toBuilder().setBodyBytes(-1),
                  check().toBuilder().setTargetNodes(-1),
                  check().toBuilder().setTargetLocals(-1)))
            error(
                "common.validation_failed",
                () -> service.checkNetwork(bad.build(), new IamGrpcDevicesSpringTest.Capture<>()));
          error(
              "common.validation_failed",
              () ->
                  service.networkAuthorityActive(
                      NetworkAuthorityActiveRequest.newBuilder()
                          .setRealm("2")
                          .setNode("01")
                          .build(),
                      new IamGrpcDevicesSpringTest.Capture<>()));
        });
    verifyNoInteractions(authority);
  }

  @Test
  void checksUseExistingPolicyAndNeverExposeAuthorityReasonsOrFailures() {
    when(authority.check(any())).thenReturn(new Decision(false, "private owner", Map.of()));
    collab(
        () -> {
          var capture = new IamGrpcDevicesSpringTest.Capture<CheckNetworkResponse>();
          service.checkNetwork(check(), capture);
          assertFalse(capture.values.getFirst().getAllowed());
          assertEquals("network_denied", capture.values.getFirst().getReason());
          verify(authority)
              .check(new Check("2", "1", "3", "0", Sender.HUMAN, Operation.RECEIVE, 10, 1, 1));
          when(authority.check(any()))
              .thenThrow(AurigaException.of(CommonErrors.UNAVAILABLE, "secret", "private"));
          error(
              "common.unavailable",
              () -> service.checkNetwork(check(), new IamGrpcDevicesSpringTest.Capture<>()));
          doThrow(new IllegalStateException("private")).when(authority).check(any());
          error(
              "common.unavailable",
              () -> service.checkNetwork(check(), new IamGrpcDevicesSpringTest.Capture<>()));
        });
  }

  @Test
  void limitsMustBeCompleteNonnegativeAndBounded() {
    var limits = new EnumMap<Limit, Long>(Limit.class);
    for (Limit limit : Limit.values()) limits.put(limit, limit.defaultValue());
    when(authority.effectiveLimits()).thenAnswer(c -> Map.copyOf(limits));
    collab(
        () -> {
          var result = new IamGrpcDevicesSpringTest.Capture<NetworkLimitsResponse>();
          service.networkLimits(NetworkLimitsRequest.getDefaultInstance(), result);
          assertEquals(Limit.values().length, result.values.getFirst().getLimitsCount());
          limits.put(Limit.BODY_BYTES, -1L);
          error(
              "common.unavailable",
              () ->
                  service.networkLimits(
                      NetworkLimitsRequest.getDefaultInstance(),
                      new IamGrpcDevicesSpringTest.Capture<>()));
          limits.put(Limit.BODY_BYTES, Limit.BODY_BYTES.defaultValue() + 1);
          error(
              "common.unavailable",
              () ->
                  service.networkLimits(
                      NetworkLimitsRequest.getDefaultInstance(),
                      new IamGrpcDevicesSpringTest.Capture<>()));
          limits.remove(Limit.BODY_BYTES);
          error(
              "common.unavailable",
              () ->
                  service.networkLimits(
                      NetworkLimitsRequest.getDefaultInstance(),
                      new IamGrpcDevicesSpringTest.Capture<>()));
        });
  }

  @Test
  void resolveRejectsWrongTenantUnknownLocalAndExpiryBeforeResponse() {
    var request =
        ResolveNetworkTargetRequest.newBuilder().setConnection(connection()).setLocal("0").build();
    collab(
        () -> {
          error(
              "common.forbidden",
              () ->
                  service.resolveNetworkTarget(
                      request.toBuilder()
                          .setConnection(
                              connection().toBuilder().setTenantId(UUID.randomUUID().toString()))
                          .build(),
                      new IamGrpcDevicesSpringTest.Capture<>()));
          for (String local : List.of("", "00", "1", "18446744073709551615"))
            error(
                "common.validation_failed",
                () ->
                    service.resolveNetworkTarget(
                        request.toBuilder().setLocal(local).build(),
                        new IamGrpcDevicesSpringTest.Capture<>()));
          verifyNoInteractions(authority);
          when(authority.resolveTarget(any())).thenReturn(clock.instant().plusSeconds(1));
          var ok = new IamGrpcDevicesSpringTest.Capture<ResolveNetworkTargetResponse>();
          service.resolveNetworkTarget(request, ok);
          assertTrue(ok.values.getFirst().getAllowed());
          assertEquals(connection(), ok.values.getFirst().getConnection());
          assertEquals("system", ok.values.getFirst().getTargetType());
          when(authority.resolveTarget(any()))
              .thenAnswer(
                  c -> {
                    var expiry = clock.instant().plusSeconds(1);
                    clock.advance(1);
                    return expiry;
                  });
          var expired = new IamGrpcDevicesSpringTest.Capture<ResolveNetworkTargetResponse>();
          service.resolveNetworkTarget(request, expired);
          assertEquals(
              ResolveNetworkTargetResponse.getDefaultInstance(), expired.values.getFirst());
        });
  }

  @Test
  void deliveryErrorsAreSanitizedAndTenantIsCheckedBeforeWork() {
    when(sources.authorizeDelivery(any()))
        .thenThrow(AurigaException.of(CommonErrors.UNAVAILABLE, "private", "evidence"));
    collab(
        () -> {
          var error =
              assertThrows(
                  AurigaException.class,
                  () ->
                      service.authorizeNetworkDelivery(
                          AuthorizeNetworkDeliveryRequest.newBuilder()
                              .setConnection(connection())
                              .setMessageJson(
                                  com.google.protobuf.ByteString.copyFromUtf8(
                                      "private-invalid-json"))
                              .build(),
                          new IamGrpcDevicesSpringTest.Capture<>()));
          assertEquals("common.unavailable", error.errorCode().code());
          assertTrue(error.params().isEmpty());
          assertNull(error.getCause());
          error(
              "common.forbidden",
              () ->
                  service.authorizeNetworkDelivery(
                      AuthorizeNetworkDeliveryRequest.newBuilder()
                          .setConnection(
                              connection().toBuilder().setTenantId(UUID.randomUUID().toString()))
                          .build(),
                      new IamGrpcDevicesSpringTest.Capture<>()));
        });
    verifyNoInteractions(authority);
  }
}
