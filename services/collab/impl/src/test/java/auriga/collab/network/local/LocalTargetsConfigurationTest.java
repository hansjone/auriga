package auriga.collab.network.local;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.mock;

import auriga.collab.api.NetworkLocalTargets;
import auriga.collab.config.CollabNetworkConfiguration;
import auriga.collab.grpc.NetworkSourceGrpcAuthority;
import auriga.common.persistence.TenantTransactionManager;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.Map;
import javax.sql.DataSource;
import org.junit.jupiter.api.Test;
import org.springframework.context.annotation.AnnotationConfigApplicationContext;
import org.springframework.core.env.MapPropertySource;

class LocalTargetsConfigurationTest {
  @Test
  void standaloneCollabProvidesItsProductionLookupToTheExistingOptionalPort() throws Exception {
    Path token = Files.createTempFile(Path.of("target"), "local-targets-", ".jwt");
    try (var context = new AnnotationConfigApplicationContext()) {
      Files.writeString(token, "test-service-token");
      context
          .getEnvironment()
          .getPropertySources()
          .addFirst(
              new MapPropertySource(
                  "test",
                  Map.of(
                      "auriga.service.schema", "collab",
                      "auriga.collab.network.iam-target", "127.0.0.1:9090",
                      "auriga.collab.network.service-token-file",
                          token.toAbsolutePath().toString())));
      context.registerBean(
          TenantTransactionManager.class,
          () -> new TenantTransactionManager(mock(DataSource.class)));
      context.register(CollabNetworkConfiguration.class);
      context.refresh();
      assertInstanceOf(LocalTargets.class, context.getBean(NetworkLocalTargets.class));
      assertNotNull(context.getBean(NetworkSourceGrpcAuthority.class));
    } finally {
      Files.deleteIfExists(token);
    }
  }
}
