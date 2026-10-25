package auriga.collab;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.mock;

import auriga.collab.app.NetworkSigningKeys;
import auriga.collab.config.CollabNetworkConfiguration;
import auriga.collab.config.CollabRuntimeConfiguration;
import auriga.collab.grpc.NetworkAuthorityClient;
import auriga.collab.grpc.NetworkSourceGrpcAuthority;
import auriga.common.persistence.TenantTransactionManager;
import java.nio.file.*;
import java.util.*;
import javax.sql.DataSource;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;
import org.springframework.context.annotation.AnnotationConfigApplicationContext;
import org.springframework.core.env.MapPropertySource;

class NetworkAuthorityConfigurationTest {
  @TempDir(factory = WorkspaceTempDirectory.class)
  Path temporary;

  public static class WorkspaceTempDirectory implements org.junit.jupiter.api.io.TempDirFactory {
    @Override
    public Path createTempDirectory(
        org.junit.jupiter.api.extension.AnnotatedElementContext element,
        org.junit.jupiter.api.extension.ExtensionContext context)
        throws Exception {
      return Files.createTempDirectory(Path.of("target"), "network-authority-").toAbsolutePath();
    }
  }

  @Test
  void runtimeRequiresSigningMaterialAndLoadsMountedKeys() throws Exception {
    var properties = new HashMap<String, Object>();
    properties.put("auriga.service.schema", "collab");
    properties.put(
        "auriga.collab.network.key-state-file", temporary.resolve("publication.json").toString());
    try (var context = runtime(properties)) {
      assertThrows(RuntimeException.class, context::refresh);
    }
    var pair = java.security.KeyPairGenerator.getInstance("Ed25519").generateKeyPair();
    Path publicFile = temporary.resolve("signing.pub"),
        privateFile = temporary.resolve("signing.key");
    Files.write(publicFile, pair.getPublic().getEncoded());
    Files.write(privateFile, pair.getPrivate().getEncoded());
    String p = "auriga.collab.network.signing-keys[0].";
    properties.put(p + "realm", "2");
    properties.put(p + "purpose", "protocol_response");
    properties.put(p + "kid", "wire-key");
    properties.put(p + "public-file", publicFile.toString());
    properties.put(p + "private-file", privateFile.toString());
    properties.put(p + "not-before", java.time.Instant.now().minusSeconds(60).toString());
    properties.put(p + "not-after", java.time.Instant.now().plusSeconds(3600).toString());
    properties.put(p + "state", "ACTIVE");
    try (var context = runtime(properties)) {
      context.refresh();
      assertEquals(
          pair.getPublic(), context.getBean(NetworkSigningKeys.class).publicKey("wire-key"));
    }
  }

  private AnnotationConfigApplicationContext runtime(Map<String, Object> properties) {
    var context = new AnnotationConfigApplicationContext();
    context
        .getEnvironment()
        .getPropertySources()
        .addFirst(new MapPropertySource("test", properties));
    var ds = mock(DataSource.class);
    context.registerBean(DataSource.class, () -> ds);
    context.registerBean(TenantTransactionManager.class, () -> new TenantTransactionManager(ds));
    context.registerBean(NetworkAuthorityClient.class, () -> mock(NetworkAuthorityClient.class));
    context.registerBean(
        NetworkSourceGrpcAuthority.class, () -> mock(NetworkSourceGrpcAuthority.class));
    context.register(CollabRuntimeConfiguration.class);
    return context;
  }

  @Test
  void missingTargetMissingOrEmptyTokenAndNonLoopbackPlaintextFailStartup() throws Exception {
    Path token = temporary.resolve("collab.jwt");
    Files.writeString(token, "test-token");
    Map<String, Object> properties = new HashMap<>();
    properties.put("auriga.service.schema", "collab");
    properties.put("auriga.collab.network.service-token-file", token.toString());
    fails(properties);
    properties.put("auriga.collab.network.iam-target", "iam.example.com:9090");
    fails(properties);
    properties.put("auriga.collab.network.iam-target", "127.0.0.1:9090");
    Files.writeString(token, "");
    fails(properties);
    Files.delete(token);
    fails(properties);
    Files.writeString(token, "test-token");
    try (var context = context(properties)) {
      context.refresh();
      assertNotNull(context.getBean(NetworkAuthorityClient.class));
      assertNotNull(context.getBean(NetworkSourceGrpcAuthority.class));
    }
    properties.put("auriga.collab.network.iam-timeout-ms", "0");
    fails(properties);
  }

  private void fails(Map<String, Object> properties) {
    try (var context = context(properties)) {
      assertThrows(RuntimeException.class, context::refresh);
    }
  }

  private AnnotationConfigApplicationContext context(Map<String, Object> properties) {
    var context = new AnnotationConfigApplicationContext();
    context
        .getEnvironment()
        .getPropertySources()
        .addFirst(new MapPropertySource("test", new HashMap<>(properties)));
    context.registerBean(
        TenantTransactionManager.class, () -> new TenantTransactionManager(mock(DataSource.class)));
    context.register(CollabNetworkConfiguration.class);
    return context;
  }
}
