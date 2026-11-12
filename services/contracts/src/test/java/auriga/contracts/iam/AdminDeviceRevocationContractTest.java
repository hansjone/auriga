package auriga.contracts.iam;

import static org.junit.jupiter.api.Assertions.*;

import auriga.iam.web.model.RevokeAdminDevice202Response;
import com.networknt.schema.Schema;
import com.networknt.schema.SchemaLocation;
import com.networknt.schema.SchemaRegistry;
import com.networknt.schema.SchemaRegistryConfig;
import com.networknt.schema.SpecificationVersion;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.UUID;
import org.junit.jupiter.api.Test;
import tools.jackson.databind.json.JsonMapper;

class AdminDeviceRevocationContractTest {
  private static final JsonMapper JSON = JsonMapper.builder().build();
  private static final UUID DEVICE = UUID.fromString("01900000-0000-7000-8000-00000000abce");

  private Schema responseSchema() throws Exception {
    var schema =
        JSON.readTree(Files.readString(Path.of("target/bundled/iam.json")))
            .path("paths")
            .path("/admin/devices/{deviceId}")
            .path("delete")
            .path("responses")
            .path("202")
            .path("content")
            .path("application/json")
            .path("schema");
    return SchemaRegistry.withDefaultDialect(
            SpecificationVersion.DRAFT_2020_12,
            builder ->
                builder.schemaRegistryConfig(
                    SchemaRegistryConfig.builder().formatAssertionsEnabled(true).build()))
        .getSchema(SchemaLocation.of("urn:auriga:iam:device-revocation:202"), schema);
  }

  @Test
  void generatedResponseRoundTripsWithBooleanRevocation() throws Exception {
    var response = new RevokeAdminDevice202Response();
    response.setId(DEVICE);
    response.setCredentialRevoked(true);
    response.setDisconnectStatus(RevokeAdminDevice202Response.DisconnectStatusEnum.PENDING);
    var wire = JSON.valueToTree(response);
    assertTrue(wire.path("credentialRevoked").isBoolean());
    assertTrue(responseSchema().validate(wire).isEmpty());
    var decoded = JSON.treeToValue(wire, RevokeAdminDevice202Response.class);
    assertEquals(Boolean.TRUE, decoded.getCredentialRevoked());
    assertEquals(DEVICE, decoded.getId());
  }

  @Test
  void partialResultRequiresCommittedRevocationAndPendingDisconnect() throws Exception {
    var schema = responseSchema();
    var response =
        JSON.createObjectNode()
            .put("id", DEVICE.toString())
            .put("credentialRevoked", true)
            .put("disconnectStatus", "pending");
    assertTrue(schema.validate(response).isEmpty());
    response.put("credentialRevoked", false);
    assertFalse(schema.validate(response).isEmpty());
    response.put("credentialRevoked", "true");
    assertFalse(schema.validate(response).isEmpty());
    response.remove("credentialRevoked");
    assertFalse(schema.validate(response).isEmpty());
    response.put("credentialRevoked", true).put("disconnectStatus", "confirmed");
    assertFalse(schema.validate(response).isEmpty());
  }
}
