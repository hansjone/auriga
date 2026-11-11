package auriga.platform;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.context.TenantContext;
import auriga.iam.api.CredentialMetadataSource;
import java.nio.file.*;
import java.util.*;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.mock.env.MockEnvironment;
import tools.jackson.databind.json.JsonMapper;

class PlatformCredentialMetadataTest {
  @TempDir Path directory;

  @Test
  void onlyMetadataLeavesPrivateKeyAndManifestSources() throws Exception {
    var identity = directory.resolve("identity.json");
    Files.writeString(
        identity,
        "{\"keys\":[{\"kid\":\"kid-1\",\"state\":\"CURRENT\",\"d\":\"must-not-leak\",\"x\":\"must-not-leak\"}]}");
    var panel = directory.resolve("manifest.json");
    Files.writeString(
        panel,
        "{\"signature\":{\"kid\":\"panel-1\",\"value\":\"must-not-leak\"},\"issuedAt\":\"2026-01-01T00:00:00Z\",\"expiresAt\":\"2026-02-01T00:00:00Z\"}");
    var env =
        new MockEnvironment()
            .withProperty("auriga.credentials.identity-keyset-file", identity.toString())
            .withProperty("auriga.credentials.panel-manifest-file", panel.toString());
    var source =
        new PlatformCredentialMetadataConfiguration.Source(
            env, mock(ObjectProvider.class), mock(ObjectProvider.class));
    var result = TenantContext.call(UUID.randomUUID(), source::snapshot);
    assertEquals(2, result.entries().size());
    assertFalse(result.toString().contains("must-not-leak"));
    assertTrue(result.gaps().stream().anyMatch(g -> g.source().equals("network-keyset")));
    assertTrue(result.gaps().stream().anyMatch(g -> g.reason().equals("validity_unavailable")));
  }

  @Test
  void networkKeysetVersionsAndValidityRemainActualAndPrivateFieldsAreExcluded() {
    var entries = new ArrayList<CredentialMetadataSource.Entry>();
    var json =
        JsonMapper.builder()
            .build()
            .readTree(
                "{\"keyset\":{\"keyset_version\":\"42\",\"issued_at\":\"2026-01-01T00:00:00Z\",\"valid_until\":\"2026-01-02T00:00:00Z\",\"keys\":[{\"kid\":\"k1\",\"state\":\"retired\",\"not_before\":\"2026-01-01T00:00:00Z\",\"not_after\":\"2026-01-03T00:00:00Z\",\"private\":\"must-not-leak\"}]}}");
    PlatformCredentialMetadataConfiguration.network(json, entries);
    assertEquals("42", entries.get(0).versionLabel());
    assertEquals("42:k1", entries.get(1).versionLabel());
    assertEquals("retiring", entries.get(1).status());
    assertFalse(entries.toString().contains("must-not-leak"));
  }
}
