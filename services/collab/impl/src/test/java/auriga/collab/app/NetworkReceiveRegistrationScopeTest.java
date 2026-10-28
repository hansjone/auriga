package auriga.collab.app;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.error.AurigaException;
import auriga.contracts.iam.v1.NetworkAuthorityConnection;
import org.junit.jupiter.api.Test;
import tools.jackson.databind.node.ObjectNode;

class NetworkReceiveRegistrationScopeTest {
  private static final NetworkAuthorityConnection CONNECTION =
      NetworkAuthorityConnection.newBuilder().setRealm("2").setNode("5001").build();

  private ObjectNode request() {
    return (ObjectNode)
        NetworkJson.parse(
            """
        {"app_version":"1.0","type":"receive_set","request_id":"receive-5001",
        "epoch":"1","boot_id":"boot-5001","enabled":true,"expected_version":"1",
        "registration":{"registration_id":"019ce924-5840-7000-8000-000000000001",
        "allocation_op_id":"allocation-5001","allocation_fingerprint":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
        "registration_revision":"1","local":"1","address_type":"session"}}
        """);
  }

  @Test
  void legitimateSixFieldWitnessUsesAuthenticatedConnectionScope() {
    var frame = request();
    NetworkSchemas.validate("protocol", "ReceiveSet", frame);
    var registration = frame.path("registration");
    assertEquals(6, registration.size(), "realm/node come from the authenticated connection");
    assertDoesNotThrow(
        () -> NetworkMailboxWireService.requireReceiveRegistrationScope(CONNECTION, registration));
  }

  @Test
  void matchingExplicitScopeIsAcceptedWithoutChangingConnectionIdentity() {
    var frame = request();
    ((ObjectNode) frame.path("registration"))
        .put("realm", CONNECTION.getRealm())
        .put("node", CONNECTION.getNode());
    NetworkSchemas.validate("protocol", "ReceiveSet", frame);
    assertDoesNotThrow(
        () ->
            NetworkMailboxWireService.requireReceiveRegistrationScope(
                CONNECTION, frame.path("registration")));
  }

  @Test
  void explicitNullOrNumericScopeNeverFallsBackToTheAuthenticatedIdentity() {
    for (String field : java.util.List.of("realm", "node")) {
      for (var invalid :
          java.util.List.of(
              NetworkJson.parse("null"), NetworkJson.parse("realm".equals(field) ? "2" : "5001"))) {
        var registration = (ObjectNode) request().path("registration");
        registration.set(field, invalid);
        assertEquals(
            "network.conflict",
            assertThrows(
                    AurigaException.class,
                    () ->
                        NetworkMailboxWireService.requireReceiveRegistrationScope(
                            CONNECTION, registration))
                .errorCode()
                .code());
      }
    }
  }

  @Test
  void mismatchedRealmCannotOverrideAuthenticatedConnection() {
    reject("realm", "3");
  }

  @Test
  void mismatchedNodeCannotOverrideAuthenticatedConnection() {
    reject("node", "5002");
  }

  private void reject(String field, String value) {
    var frame = request();
    ((ObjectNode) frame.path("registration"))
        .put("realm", CONNECTION.getRealm())
        .put("node", CONNECTION.getNode())
        .put(field, value);
    NetworkSchemas.validate("protocol", "ReceiveSet", frame);
    assertEquals(
        "network.conflict",
        assertThrows(
                AurigaException.class,
                () ->
                    NetworkMailboxWireService.requireReceiveRegistrationScope(
                        CONNECTION, frame.path("registration")))
            .errorCode()
            .code());
  }
}
