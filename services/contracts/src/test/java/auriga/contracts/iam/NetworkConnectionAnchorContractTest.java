package auriga.contracts.iam;

import static org.junit.jupiter.api.Assertions.*;

import auriga.contracts.iam.v1.*;
import com.google.protobuf.Descriptors;
import java.util.List;
import org.junit.jupiter.api.Test;

class NetworkConnectionAnchorContractTest {
  @Test
  void additiveInternalReferencesExposeNoIdentityOrPermission() {
    var service = NetworkAuthority.getDescriptor().findServiceByName("NetworkAuthorityService");
    var method = service.findMethodByName("ReadNetworkConnectionAnchor");
    assertEquals(ReadNetworkConnectionAnchorRequest.getDescriptor(), method.getInputType());
    assertEquals(ReadNetworkConnectionAnchorResponse.getDescriptor(), method.getOutputType());
    assertFalse(method.isClientStreaming());
    assertFalse(method.isServerStreaming());
    for (var descriptor : List.of(method.getInputType(), method.getOutputType())) {
      assertEquals(
          List.of("original", "current"),
          descriptor.getFields().stream().map(Descriptors.FieldDescriptor::getName).toList());
      for (int i = 1; i <= 2; i++) {
        var field = descriptor.findFieldByNumber(i);
        assertTrue(field.hasPresence());
        assertEquals(NetworkConnectionAnchor.getDescriptor(), field.getMessageType());
      }
    }
    assertEquals(
        List.of("anchor_id", "connection"),
        NetworkConnectionAnchor.getDescriptor().getFields().stream()
            .map(Descriptors.FieldDescriptor::getName)
            .toList());
    assertEquals(
        NetworkAuthorityConnection.getDescriptor(),
        NetworkConnectionAnchor.getDescriptor().findFieldByNumber(2).getMessageType());
    var resolve = ResolveNetworkTargetResponse.getDescriptor();
    assertEquals(
        List.of(
            "allowed", "connection", "target_type", "valid_until", "local", "connection_anchor"),
        resolve.getFields().stream().map(Descriptors.FieldDescriptor::getName).toList());
    assertEquals(
        NetworkConnectionAnchor.getDescriptor(), resolve.findFieldByNumber(6).getMessageType());
    assertNull(
        Network.getDescriptor()
            .findServiceByName("NetworkGatewayService")
            .findMethodByName("ReadNetworkConnectionAnchor"));
  }
}
