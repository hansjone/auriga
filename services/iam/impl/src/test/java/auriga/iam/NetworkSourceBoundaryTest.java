package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.contracts.iam.v1.*;
import com.google.protobuf.UnknownFieldSet;
import java.util.*;
import org.junit.jupiter.api.Test;

/** Pure input boundary checks only; no fake identity or authorization implementation. */
class NetworkSourceBoundaryTest {
  final UUID tenant = UUID.randomUUID();

  RegisterAgentSourceRequest request() {
    return RegisterAgentSourceRequest.newBuilder()
        .setConnection(
            NetworkAuthorityConnection.newBuilder()
                .setTenantId(tenant.toString())
                .setRealm("1")
                .setNode("2")
                .setIngress("a".repeat(26))
                .setServerId("N" + "A".repeat(55))
                .setClientId("3")
                .setClientAccount("TEST"))
        .setRegistration(
            NetworkAgentSourceRegistration.newBuilder()
                .setRegistrationId("registration")
                .setAllocationOpId("allocation")
                .setAllocationFingerprint("sha256:" + "0".repeat(64))
                .setRegistrationRevision(1)
                .setRealm("1")
                .setNode("2")
                .setLocal("3")
                .setAddressType("session"))
        .setOperationId("op")
        .setScopeValue(1)
        .build();
  }

  void reject(RegisterAgentSourceRequest r, String code) {
    TenantContext.run(
        tenant,
        () ->
            assertEquals(
                code,
                assertThrows(AurigaException.class, () -> IamNetworkSources.validate(r))
                    .errorCode()
                    .code()));
  }

  @Test
  void allEightRegistrationFieldsAreRequiredAndTyped() {
    var r = request();
    var registration = r.getRegistration();
    for (var field : registration.getDescriptorForType().getFields()) {
      reject(
          r.toBuilder().setRegistration(registration.toBuilder().clearField(field)).build(),
          "common.validation_failed");
    }
    for (String type : List.of("system", "subagent", "group", "Session"))
      reject(
          r.toBuilder().setRegistration(registration.toBuilder().setAddressType(type)).build(),
          "common.validation_failed");
    for (String bad : List.of("0", "01", "-1", "18446744073709551616", "1.0", " 1", "1e1"))
      reject(
          r.toBuilder().setRegistration(registration.toBuilder().setLocal(bad)).build(),
          "common.validation_failed");
    reject(
        r.toBuilder().setRegistration(registration.toBuilder().setRealm("2")).build(),
        "common.validation_failed");
    reject(
        r.toBuilder().setRegistration(registration.toBuilder().setNode("1")).build(),
        "common.validation_failed");
  }

  @Test
  void scopesIdsFingerprintsAndPredecessorRevisionMustBeExact() {
    var r = request();
    for (int scope : new int[] {0, -1, 2, 999})
      reject(r.toBuilder().setScopeValue(scope).build(), "common.validation_failed");
    for (String id : List.of("", " ", "/path", "中", "a".repeat(129))) {
      reject(r.toBuilder().setOperationId(id).build(), "common.validation_failed");
      reject(
          r.toBuilder()
              .setRegistration(r.getRegistration().toBuilder().setRegistrationId(id))
              .build(),
          "common.validation_failed");
    }
    reject(
        r.toBuilder()
            .setRegistration(
                r.getRegistration().toBuilder()
                    .setAllocationFingerprint("sha256:" + "A".repeat(64)))
            .build(),
        "common.validation_failed");
    reject(r.toBuilder().setExpectedRevision(1).build(), "common.validation_failed");
    reject(r.toBuilder().setPredecessorBinding("predecessor").build(), "common.validation_failed");
    TenantContext.run(
        tenant,
        () ->
            IamNetworkSources.validate(
                r.toBuilder()
                    .setPredecessorBinding("predecessor")
                    .setExpectedRevision(-1L)
                    .setRegistration(
                        r.getRegistration().toBuilder()
                            .setLocal("18446744073709551615")
                            .setRegistrationRevision(-1L))
                    .build()));
  }

  @Test
  void malformedConnectionsAndForeignOrPlatformTenantsAreRejected() {
    var r = request();
    var c = r.getConnection();
    for (var field : c.getDescriptorForType().getFields()) {
      reject(
          r.toBuilder().setConnection(c.toBuilder().clearField(field)).build(),
          field.getName().equals("tenant_id") ? "common.forbidden" : "common.validation_failed");
    }
    reject(
        r.toBuilder()
            .setConnection(c.toBuilder().setTenantId(UUID.randomUUID().toString()))
            .build(),
        "common.forbidden");
    reject(
        r.toBuilder()
            .setConnection(c.toBuilder().setTenantId(TenantContext.PLATFORM_TENANT.toString()))
            .build(),
        "common.forbidden");
    reject(
        r.toBuilder().setConnection(c.toBuilder().setClientId("00")).build(),
        "common.validation_failed");
  }

  @Test
  void unknownProtoFieldsCannotDisappearFromCanonicalRequestIdentity() {
    var r = request();
    var unknown =
        UnknownFieldSet.newBuilder()
            .addField(100, UnknownFieldSet.Field.newBuilder().addVarint(1).build())
            .build();
    reject(r.toBuilder().setUnknownFields(unknown).build(), "common.validation_failed");
    reject(
        r.toBuilder()
            .setConnection(r.getConnection().toBuilder().setUnknownFields(unknown))
            .build(),
        "common.validation_failed");
    reject(
        r.toBuilder()
            .setRegistration(r.getRegistration().toBuilder().setUnknownFields(unknown))
            .build(),
        "common.validation_failed");
  }

  @Test
  void registerDigestIncludesDefaultsAndUnsignedStringsUsingExplicitContractJcs() {
    // Independent Node crypto/JCS golden vector; no protobuf JSON default elision.
    var r =
        request().toBuilder()
            .setConnection(
                request().getConnection().toBuilder()
                    .setTenantId("00000000-0000-0000-0000-000000000001"))
            .build();
    assertEquals(
        "sha256:8cb6f8f9a370a310b50ba21e1b6f4cd37b2f96a46bccc31dff5e1e3b4c489eb7",
        IamNetworkSources.registerDigest(r));
    var wide =
        r.toBuilder()
            .setExpectedRevision(-1L)
            .setPredecessorBinding("predecessor")
            .setRegistration(r.getRegistration().toBuilder().setRegistrationRevision(-1L))
            .setConnection(r.getConnection().toBuilder().setClientAccount("a\"\\\n\uD83D\uDE00"))
            .build();
    assertEquals(
        "sha256:305b38f93385c62441ab12d439530ee8f72ee09af9b7341c45620066bf1fbc76",
        IamNetworkSources.registerDigest(wide));
  }

  @Test
  void revokeAcceptsEveryLegacyRevisionAndKernelHasNoPublicSurfaceOrAuthorityGetResponse() {
    var request =
        RevokeAgentSourceRequest.newBuilder()
            .setConnection(request().getConnection())
            .setOperationId("stop")
            .setSourceBinding(UUID.randomUUID().toString())
            .build();
    TenantContext.run(
        tenant,
        () -> {
          for (long revision : new long[] {0, 1, 2, -1L})
            assertDoesNotThrow(
                () ->
                    IamNetworkSources.validate(
                        request.toBuilder().setExpectedRevision(revision).build()));
        });
    assertFalse(java.lang.reflect.Modifier.isPublic(IamNetworkSources.class.getModifiers()));
    for (var method : IamNetworkSources.class.getDeclaredMethods()) {
      assertFalse(java.lang.reflect.Modifier.isPublic(method.getModifiers()));
      assertNotEquals(GetAgentSourceResponse.class, method.getReturnType());
    }
    assertTrue(
        Arrays.stream(IamNetworkSources.View.class.getRecordComponents())
            .noneMatch(
                c -> c.getName().equals("validUntil") || c.getName().equals("policyRevision")));
  }
}
