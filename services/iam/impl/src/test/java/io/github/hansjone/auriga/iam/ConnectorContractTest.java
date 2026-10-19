package io.github.hansjone.auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import io.github.hansjone.auriga.iam.api.IdentityConnector;
import java.util.*;
import org.junit.jupiter.api.Test;
import tools.jackson.databind.node.ObjectNode;

class ConnectorContractTest {
  private final ContractSchemas schemas = new ContractSchemas();

  @Test
  void teamAccountDescriptorValidatesAgainstContract() {
    var accounts = mock(TeamAccounts.class);
    var passkeys = mock(TeamPasskeys.class);
    var qr = mock(QrChallenges.class);
    var connector =
        new TeamAccountConnector(accounts, passkeys, qr, "https://portal.example.com");
    schemas.validate(
        "identity/connector.schema.json#/$defs/ConnectorDescriptor", connector.descriptor());
    assertEquals("team-account", connector.descriptor().path("type").asString());
  }

  @Test
  void rejectsDuplicateAndIncompatibleSpiDescriptors() {
    var registry = new ConnectorRegistry(schemas, List.of(new EmergencyConnector()));
    assertThrows(RuntimeException.class, () -> registry.register(new EmergencyConnector()));
    IdentityConnector incompatible = mock(IdentityConnector.class);
    var descriptor = (ObjectNode) new EmergencyConnector().descriptor();
    descriptor.put("type", "example-connector");
    descriptor.put("spiVersion", "2");
    when(incompatible.descriptor()).thenReturn(descriptor);
    assertThrows(RuntimeException.class, () -> registry.register(incompatible));
    assertEquals(1, registry.all().size());
  }

  @Test
  void configurationMustValidateBeforeConnectorSemanticValidation() {
    IdentityConnector connector = mock(IdentityConnector.class);
    var descriptor = (ObjectNode) new EmergencyConnector().descriptor();
    descriptor.put("type", "example-connector");
    descriptor.set(
        "configSchema",
        Json.object(
            "type",
            "object",
            "properties",
            Json.object("clientId", Json.object("type", "string", "minLength", 1)),
            "required",
            List.of("clientId"),
            "additionalProperties",
            false));
    when(connector.descriptor()).thenReturn(descriptor);
    var registry = new ConnectorRegistry(schemas, List.of(connector));
    assertThrows(
        RuntimeException.class, () -> registry.validate("example-connector", Json.object()));
    verify(connector, never()).validateConfig(any());
    var valid = Json.object("clientId", "example-client");
    registry.validate("example-connector", valid);
    verify(connector).validateConfig(valid);
  }

  @Test
  void privateStateIsBoundedAndConnectorDoneNeverPassesAsClientStep() {
    var registry = new ConnectorRegistry(schemas, List.of());
    assertThrows(
        RuntimeException.class,
        () -> registry.result(Json.object("step", Json.object("type", "unknown"))));
    assertThrows(
        RuntimeException.class,
        () ->
            registry.result(
                Json.object(
                    "step",
                    Json.object("type", "fail", "code", "iam.example"),
                    "state",
                    Json.object("value", "a".repeat(16385)))));
    assertThrows(
        RuntimeException.class,
        () ->
            schemas.validate(
                "identity/steps.schema.json#/$defs/ClientStep",
                Json.object("type", "done", "profile", Json.object("subject", "example-subject"))));
  }

  @Test
  void portalAndDevicePermissionCeilingsCannotAdmitAdministrativeGrants() {
    for (String p :
        List.of("org.members.write", "iam.providers.read", "workspace.admin", "workspace.grant"))
      assertFalse(TokenService.allowedPermission(p, "portal"));
    for (String p :
        List.of(
            "org.members.read",
            "auriga.connect.admin",
            "auriga.connect.write",
            "auriga.connect.grant")) assertFalse(TokenService.allowedPermission(p, "device"));
    assertTrue(TokenService.allowedPermission("workspace.read", "portal"));
    assertTrue(TokenService.allowedPermission("auriga.connect.read", "device"));
  }
}
