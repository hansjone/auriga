package auriga.contracts.iam;

import static org.junit.jupiter.api.Assertions.*;

import auriga.contracts.iam.v1.*;
import com.google.protobuf.Descriptors;
import java.util.List;
import org.junit.jupiter.api.Test;

class NetworkSourceReceiptContractTest {
  @Test
  void exactInternalMethodsAndReceiptWrapperReuseOriginalTypes() {
    var service = NetworkSource.getDescriptor().findServiceByName("NetworkSourceAuthorityService");
    assertEquals(
        List.of(
            "RegisterAgentSource", "ReadAgentSourceReceipt", "GetAgentSource", "RevokeAgentSource"),
        service.getMethods().stream().map(Descriptors.MethodDescriptor::getName).toList());
    assertEquals(4, NetworkSourceAuthorityServiceGrpc.getServiceDescriptor().getMethods().size());
    var read = service.findMethodByName("ReadAgentSourceReceipt");
    assertEquals(ReadAgentSourceReceiptRequest.getDescriptor(), read.getInputType());
    assertEquals(RegisterAgentSourceResponse.getDescriptor(), read.getOutputType());
    assertFalse(read.isClientStreaming());
    assertFalse(read.isServerStreaming());
    var wrapper = read.getInputType();
    assertEquals(1, wrapper.getFields().size());
    var original = wrapper.findFieldByName("original_request");
    assertEquals(1, original.getNumber());
    assertTrue(original.hasPresence());
    assertEquals(RegisterAgentSourceRequest.getDescriptor(), original.getMessageType());
    assertEquals(
        List.of(
            "source",
            "definitively_uncommitted",
            "operation_id",
            "request_digest",
            "rejection_reason"),
        read.getOutputType().getFields().stream()
            .map(Descriptors.FieldDescriptor::getName)
            .toList());
    assertEquals(
        List.of(
            "NETWORK_AGENT_SOURCE_SCOPE_UNSPECIFIED",
            "NETWORK_AGENT_SOURCE_SCOPE_DURABLE_UNICAST_SEND"),
        NetworkAgentSourceScope.getDescriptor().getValues().stream()
            .map(Descriptors.EnumValueDescriptor::getName)
            .toList());
    assertEquals(
        List.of(
            "NETWORK_AGENT_SOURCE_STATE_UNSPECIFIED",
            "NETWORK_AGENT_SOURCE_STATE_ACTIVE",
            "NETWORK_AGENT_SOURCE_STATE_REVOKED",
            "NETWORK_AGENT_SOURCE_STATE_EXPIRED"),
        NetworkAgentSourceState.getDescriptor().getValues().stream()
            .map(Descriptors.EnumValueDescriptor::getName)
            .toList());
    assertNull(
        Network.getDescriptor()
            .findServiceByName("NetworkGatewayService")
            .findMethodByName("ReadAgentSourceReceipt"));
  }
}
