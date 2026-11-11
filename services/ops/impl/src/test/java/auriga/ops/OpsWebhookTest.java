package auriga.ops;

import static org.junit.jupiter.api.Assertions.*;

import java.net.InetAddress;
import org.junit.jupiter.api.Test;

class OpsWebhookTest {
  @Test
  void blocksPrivateMetadataAndAmbiguousTargets() throws Exception {
    for (String host :
        new String[] {
          "127.0.0.1",
          "10.0.0.1",
          "169.254.169.254",
          "192.168.1.1",
          "100.64.0.1",
          "198.18.0.1",
          "::1",
          "::ffff:127.0.0.1",
          "2001:db8::1"
        }) { // gitleaks:allow
      assertFalse(OpsWebhook.publicAddress(InetAddress.getByName(host)), host);
    }
    for (String url :
        new String[] {
          "http://example.com",
          "https://user:pass@example.com",
          "https://example.com:8443",
          "https://example.com/#fragment",
          "https://127.1"
        }) {
      assertThrows(RuntimeException.class, () -> OpsWebhook.target(url));
    }
  }

  @Test
  void allowsPublicIpv4AndRejectsMixedDns() throws Exception {
    assertTrue(OpsWebhook.publicAddress(InetAddress.getByName("93.184.216.34")));
    assertThrows(
        RuntimeException.class,
        () ->
            OpsWebhook.checkAddresses(
                new InetAddress[] {
                  InetAddress.getByName("93.184.216.34"), InetAddress.getLoopbackAddress()
                }));
  }

  @Test
  void encryptsSecretsWithTenantBinding() {
    var vault = new OpsSecrets(java.util.Base64.getEncoder().encodeToString(new byte[32]));
    String encrypted = vault.seal("tenant-a", "sensitive");
    assertFalse(encrypted.contains("sensitive"));
    assertEquals("sensitive", vault.open("tenant-a", encrypted));
    assertThrows(RuntimeException.class, () -> vault.open("tenant-b", encrypted));
    assertThrows(RuntimeException.class, () -> new OpsSecrets("").seal("t", "value"));
  }
}
