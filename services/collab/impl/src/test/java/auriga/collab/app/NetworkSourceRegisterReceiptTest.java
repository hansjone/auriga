package auriga.collab.app;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.error.AurigaException;
import auriga.contracts.iam.v1.*;
import com.google.protobuf.UnknownFieldSet;
import java.lang.reflect.InvocationTargetException;
import java.util.stream.Stream;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.MethodSource;

class NetworkSourceRegisterReceiptTest {
  static final String DIGEST = "sha256:" + "a".repeat(64);
  static final RegisterAgentSourceRequest REQUEST =
      RegisterAgentSourceRequest.newBuilder()
          .setOperationId("original-op")
          .setConnection(
              NetworkAuthorityConnection.newBuilder()
                  .setTenantId("tenant")
                  .setRealm("2")
                  .setNode("20"))
          .setRegistration(
              NetworkAgentSourceRegistration.newBuilder().setRegistrationId("registration"))
          .setScope(NetworkAgentSourceScope.NETWORK_AGENT_SOURCE_SCOPE_DURABLE_UNICAST_SEND)
          .build();
  static final UnknownFieldSet UNKNOWN =
      UnknownFieldSet.newBuilder()
          .addField(100, UnknownFieldSet.Field.newBuilder().addVarint(1).build())
          .build();

  static RegisterAgentSourceResponse failure() {
    return RegisterAgentSourceResponse.newBuilder()
        .setDefinitivelyUncommitted(true)
        .setOperationId(REQUEST.getOperationId())
        .setRequestDigest(DIGEST)
        .setRejectionReason("original_connection_invalid_uncommitted")
        .build();
  }

  static RegisterAgentSourceResponse success() {
    return RegisterAgentSourceResponse.newBuilder()
        .setSource(
            NetworkAgentSource.newBuilder()
                .setSourceBinding("source-original")
                .setOriginalConnection(REQUEST.getConnection())
                .setRegistration(REQUEST.getRegistration())
                .setScope(REQUEST.getScope())
                .setRevision(1)
                .setState(NetworkAgentSourceState.NETWORK_AGENT_SOURCE_STATE_ACTIVE)
                .setOwnerUserId("owner")
                .setNotBefore("2026-10-27T00:00:00Z")
                .setExpiresAt("2026-10-27T00:03:00Z"))
        .build();
  }

  static void validate(RegisterAgentSourceResponse response) throws Throwable {
    var method =
        NetworkSourceControl.class.getDeclaredMethod(
            "validateRegister",
            RegisterAgentSourceRequest.class,
            RegisterAgentSourceResponse.class,
            String.class);
    method.setAccessible(true);
    try {
      method.invoke(null, REQUEST, response, DIGEST);
    } catch (InvocationTargetException e) {
      throw e.getCause();
    }
  }

  @Test
  void completeSuccessDoesNotRequireFailureOnlyOuterOperationOrDigest() {
    assertDoesNotThrow(() -> validate(success()));
    for (String reason :
        new String[] {
          "original_connection_invalid_uncommitted",
          "revision_mismatch",
          "predecessor_not_head",
          "lineage_stopped"
        })
      assertDoesNotThrow(() -> validate(failure().toBuilder().setRejectionReason(reason).build()));
  }

  static Stream<RegisterAgentSourceResponse> malformed() {
    return Stream.of(
        RegisterAgentSourceResponse.getDefaultInstance(),
        failure().toBuilder().clearDefinitivelyUncommitted().build(),
        failure().toBuilder().clearOperationId().build(),
        failure().toBuilder().setOperationId("other").build(),
        failure().toBuilder().clearRequestDigest().build(),
        failure().toBuilder().setRequestDigest("sha256:" + "b".repeat(64)).build(),
        failure().toBuilder().setRejectionReason("unavailable").build(),
        failure().toBuilder().setSource(success().getSource()).build(),
        failure().toBuilder().setUnknownFields(UNKNOWN).build(),
        success().toBuilder().setRejectionReason("lineage_stopped").build(),
        success().toBuilder().setOperationId("other").build(),
        success().toBuilder().setRequestDigest("sha256:" + "b".repeat(64)).build(),
        success().toBuilder().setUnknownFields(UNKNOWN).build(),
        success().toBuilder()
            .setSource(success().getSource().toBuilder().setUnknownFields(UNKNOWN))
            .build(),
        success().toBuilder()
            .setSource(success().getSource().toBuilder().setExpiresAt("2026-10-27T00:03:00.5Z"))
            .build(),
        success().toBuilder()
            .setSource(success().getSource().toBuilder().setOwnerUserId(""))
            .build(),
        success().toBuilder()
            .setSource(
                success().getSource().toBuilder()
                    .setOriginalConnection(REQUEST.getConnection().toBuilder().setNode("21")))
            .build(),
        success().toBuilder().setSource(success().getSource().toBuilder().setRevision(2)).build());
  }

  @ParameterizedTest
  @MethodSource("malformed")
  void incompleteConflictingOrUnknownReceiptsAreNeverDefinitive(
      RegisterAgentSourceResponse response) {
    assertEquals(
        "network.outcome_unknown",
        assertThrows(AurigaException.class, () -> validate(response)).errorCode().code());
  }
}
