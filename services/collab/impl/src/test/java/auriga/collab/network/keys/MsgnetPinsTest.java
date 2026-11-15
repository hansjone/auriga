package auriga.collab.network.keys;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import java.util.Base64;
import java.util.UUID;
import org.junit.jupiter.api.Test;
import tools.jackson.databind.node.ObjectNode;

class MsgnetPinsTest {
  @Test
  void pinsRequireVersionScopeUniqueIdentitiesAndCanonicalAlgorithms() throws Exception {
    var tenant = UUID.randomUUID();
    var signing = KeyTestMaterial.pair();
    var sealing = java.security.KeyPairGenerator.getInstance("X25519").generateKeyPair();
    var pins =
        MsgnetRenewal.object()
            .put("schema_version", 2)
            .put("tenant_id", tenant.toString())
            .put("realm", "1");
    var entries = pins.putArray("pins");
    var pin =
        entries
            .addObject()
            .put("target_id", "target")
            .put("identity_id", "device")
            .put("component", "network")
            .put("gateway_id", "gateway")
            .put("subject", "CN=projector")
            .put("signing_spki", encode(signing.getPublic().getEncoded()))
            .put("sealing_spki", encode(sealing.getPublic().getEncoded()));
    assertEquals(1, MsgnetControlLedger.pins(pins, tenant, "1").size());
    assertThrows(RuntimeException.class, () -> MsgnetControlLedger.pins(entries, tenant, "1"));
    assertThrows(
        RuntimeException.class, () -> MsgnetControlLedger.pins(pins, UUID.randomUUID(), "1"));
    var duplicate = pins.deepCopy();
    ((tools.jackson.databind.node.ArrayNode) duplicate.path("pins"))
        .add(pin.deepCopy().put("target_id", "other"));
    assertThrows(RuntimeException.class, () -> MsgnetControlLedger.pins(duplicate, tenant, "1"));
    var swapped = pins.deepCopy();
    ((ObjectNode) swapped.path("pins").get(0))
        .put("signing_spki", encode(sealing.getPublic().getEncoded()));
    assertThrows(RuntimeException.class, () -> MsgnetControlLedger.pins(swapped, tenant, "1"));
    pin.put("sealing_spki", pin.path("sealing_spki").asString() + "=");
    assertThrows(RuntimeException.class, () -> MsgnetControlLedger.pins(pins, tenant, "1"));
  }

  @Test
  void certificateSubjectIsBoundToTheInstallationPin() {
    var pin =
        new MsgnetControlLedger.Identity(
            "target", "device", "", "network", "gateway", "", "CN=projector");
    var ledger =
        new MsgnetControlLedger(
            null, null, UUID.randomUUID(), "1", java.util.Map.of("target", pin));
    var cert = mock(java.security.cert.X509Certificate.class);
    when(cert.getSubjectX500Principal())
        .thenReturn(new javax.security.auth.x500.X500Principal("CN=other"));
    var request = MsgnetRenewal.object().put("installation_id", "device");
    assertThrows(RuntimeException.class, () -> ledger.mtls(request, cert));
    when(cert.getSubjectX500Principal())
        .thenReturn(new javax.security.auth.x500.X500Principal("CN=projector"));
    assertDoesNotThrow(() -> ledger.mtls(request, cert));
  }

  private static String encode(byte[] bytes) {
    return Base64.getUrlEncoder().withoutPadding().encodeToString(bytes);
  }
}
