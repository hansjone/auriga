package auriga.workspace.app;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.error.AurigaException;
import auriga.contracts.deploymentconfig.DeploymentAccess;
import auriga.contracts.node.v1.SharedDshBinding;
import auriga.workspace.persistence.WorkspaceStore;
import io.nats.client.Connection;
import io.nats.client.KeyValue;
import io.nats.client.api.KeyValueStatus;
import io.nats.client.api.StorageType;
import java.net.URI;
import java.net.http.HttpClient;
import java.nio.file.Files;
import java.nio.file.Path;
import java.security.KeyPairGenerator;
import java.time.Clock;
import java.time.Duration;
import java.util.UUID;
import org.junit.jupiter.api.Test;

class SharedDshEntryTransportTest {
  DeploymentAccess ip() throws Exception {
    return DeploymentAccess.load(
        Files.readAllBytes(Path.of("../../../contracts/deployment/fixtures/ip-http.json")));
  }

  SharedDshBinding binding(DeploymentAccess access) {
    return SharedDshBinding.newBuilder()
        .setTenantId(access.tenantId())
        .setPhysicalInstanceId(UUID.randomUUID().toString())
        .build();
  }

  SharedDshEntryTransport transport(DeploymentAccess access, Connection nats) throws Exception {
    var key = KeyPairGenerator.getInstance("Ed25519").generateKeyPair().getPrivate();
    return new SharedDshEntryTransport(
        access,
        nats,
        mock(WorkspaceStore.class),
        new SharedDshTickets.SigningKey("fixture", key),
        Clock.systemUTC(),
        mock(HttpClient.class));
  }

  @Test
  void acknowledgedIpExchangeIsBoundToTheExactInstallationOriginAndPoolPath() throws Exception {
    var access = ip();
    assertTrue(access.insecureHttpAcknowledged());
    assertEquals("ip", access.mode());
    var entry = transport(access, mock(Connection.class));
    var binding = binding(access);
    var expected =
        URI.create(
            access.portalUrl()
                + "/i/"
                + binding.getPhysicalInstanceId()
                + "/_auriga/placement-ticket");
    assertEquals(expected, entry.exchangeUrl(binding));
    assertTrue(entry.allowInsecureExchange(binding, expected));
    for (var changed :
        new URI[] {
          URI.create("http://other.invalid" + expected.getPath()),
          new URI(
              expected.getScheme(),
              null,
              expected.getHost(),
              expected.getPort() == 65535 ? 65534 : expected.getPort() + 1,
              expected.getPath(),
              null,
              null),
          URI.create(expected + "?ticket=synthetic"),
          URI.create(expected + "#fragment"),
          URI.create(access.portalUrl() + "/i/" + UUID.randomUUID() + "/_auriga/placement-ticket"),
          URI.create(access.portalUrl() + "/_auriga/placement-ticket")
        }) assertFalse(entry.allowInsecureExchange(binding, changed));
    assertThrows(
        AurigaException.class,
        () ->
            entry.exchangeUrl(
                binding.toBuilder().setTenantId(UUID.randomUUID().toString()).build()));
  }

  @Test
  void replayReadbackNeverTurnsAnUnavailableOrVolatileLedgerIntoAnUnusedTicket() throws Exception {
    var connection = mock(Connection.class);
    var ledger = mock(KeyValue.class);
    var status = mock(KeyValueStatus.class);
    when(connection.getStatus()).thenReturn(Connection.Status.CONNECTED);
    when(connection.keyValue("auriga-shared-entry")).thenReturn(ledger);
    when(ledger.getStatus()).thenReturn(status);
    when(status.getStorageType()).thenReturn(StorageType.File);
    when(status.getTtl()).thenReturn(Duration.ofHours(12));
    var entry = transport(ip(), connection);
    var id = UUID.randomUUID();
    assertFalse(entry.consumed(id));
    when(ledger.get("ticket." + id)).thenThrow(new java.io.IOException("synthetic ledger outage"));
    assertThrows(AurigaException.class, () -> entry.consumed(id));
    when(status.getStorageType()).thenReturn(StorageType.Memory);
    assertThrows(AurigaException.class, () -> entry.consumed(UUID.randomUUID()));
    when(status.getStorageType()).thenReturn(StorageType.File);
    when(status.getTtl()).thenReturn(Duration.ofSeconds(1));
    assertThrows(AurigaException.class, () -> entry.consumed(UUID.randomUUID()));
  }
}
