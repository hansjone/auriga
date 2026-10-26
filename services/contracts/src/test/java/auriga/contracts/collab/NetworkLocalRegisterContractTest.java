package auriga.contracts.collab;

import static org.junit.jupiter.api.Assertions.*;

import auriga.contracts.collab.v1.*;
import com.google.protobuf.ByteString;
import com.google.protobuf.Descriptors;
import io.grpc.MethodDescriptor;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.List;
import org.junit.jupiter.api.Test;
import tools.jackson.databind.json.JsonMapper;

/** Generated contract and byte transport only; no production authentication or persistence. */
class NetworkLocalRegisterContractTest {
  @Test
  void exactlyOneAdditionalUnaryAndTwoMessages() {
    var file = NetworkMailboxServiceLocalRegisterRequest.getDescriptor().getFile();
    var service = file.findServiceByName("NetworkMailboxService");
    var names = List.of("Ready", "Head", "Fetch", "Receipt", "LocalRegister", "SourceRegister", "SourceGet", "SourceRevoke");
    assertEquals(1, file.getServices().size());
    assertEquals(17, file.getMessageTypes().size());
    assertEquals(
        names, service.getMethods().stream().map(Descriptors.MethodDescriptor::getName).toList());
    assertEquals(8, NetworkMailboxServiceGrpc.getServiceDescriptor().getMethods().size());
    for (var method : NetworkMailboxServiceGrpc.getServiceDescriptor().getMethods()) {
      assertEquals(MethodDescriptor.MethodType.UNARY, method.getType());
    }
    for (var method : service.getMethods()) {
      var input = method.getInputType();
      assertFalse(method.isClientStreaming());
      assertFalse(method.isServerStreaming());
      assertEquals("NetworkMailboxService" + method.getName() + "Request", input.getName());
      assertEquals(
          "NetworkMailboxService" + method.getName() + "Response",
          method.getOutputType().getName());
      assertEquals(method.getName().equals("Head") ? 1 : 2, input.getFields().size());
      var connection = input.findFieldByNumber(1);
      assertEquals("connection", connection.getName());
      assertEquals(NetworkMailboxConnection.getDescriptor(), connection.getMessageType());
      assertFalse(connection.isRepeated());
      assertNull(connection.getContainingOneof());
      if (!method.getName().equals("Head")) assertBytes(input.findFieldByNumber(2));
      assertEquals(1, method.getOutputType().getFields().size());
      assertBytes(method.getOutputType().findFieldByNumber(1));
    }
    var connection = NetworkMailboxConnection.getDescriptor();
    var fields =
        List.of(
            "tenant_id", "realm", "node", "ingress", "server_id", "client_id", "client_account");
    assertEquals(fields.size(), connection.getFields().size());
    for (int i = 0; i < fields.size(); i++) {
      assertEquals(fields.get(i), connection.findFieldByNumber(i + 1).getName());
      assertEquals(
          Descriptors.FieldDescriptor.Type.STRING, connection.findFieldByNumber(i + 1).getType());
    }
  }

  private static void assertBytes(Descriptors.FieldDescriptor field) {
    assertNotNull(field);
    assertEquals("frame_json", field.getName());
    assertEquals(Descriptors.FieldDescriptor.Type.BYTES, field.getType());
    assertFalse(field.isRepeated());
    assertNull(field.getContainingOneof());
  }

  @Test
  void completeFramesPreserveUint64OpaqueWitnessAndExistingSignatures() throws Exception {
    var json = JsonMapper.builder().build();
    var frames =
        json.readTree(Files.readString(Path.of("../../contracts/network/fixtures/frames.json")))
            .get("frames");
    var frame =
        json.writeValueAsString(frames.get("local_register").get("value"))
            .replace("\"42\"", "\"18446744073709551615\"")
            .replace("\"419\"", "\"18446744073709551615\"")
            .replace("\"418\"", "\"18446744073709551615\"");
    var request =
        NetworkMailboxServiceLocalRegisterRequest.newBuilder()
            .setConnection(
                NetworkMailboxConnection.newBuilder()
                    .setTenantId("tenant-a")
                    .setRealm("2")
                    .setNode("64")
                    .setIngress("ingress-a")
                    .setServerId("server-a")
                    .setClientId("client-a")
                    .setClientAccount("account-a"))
            .setFrameJson(ByteString.copyFromUtf8(frame))
            .build();
    var decoded = NetworkMailboxServiceLocalRegisterRequest.parseFrom(request.toByteArray());
    assertEquals(request, decoded);
    assertEquals(frame, decoded.getFrameJson().toStringUtf8());
    for (var field : List.of("epoch", "candidate_local", "expected_registration_revision")) {
      assertEquals("18446744073709551615", json.readTree(frame).get(field).asString());
    }
    for (var name : List.of("register_result", "register_result_subagent", "error")) {
      var bytes = ByteString.copyFrom(json.writeValueAsBytes(frames.get(name).get("value")));
      var response =
          NetworkMailboxServiceLocalRegisterResponse.newBuilder().setFrameJson(bytes).build();
      assertEquals(
          bytes,
          NetworkMailboxServiceLocalRegisterResponse.parseFrom(response.toByteArray())
              .getFrameJson());
    }
  }
}
