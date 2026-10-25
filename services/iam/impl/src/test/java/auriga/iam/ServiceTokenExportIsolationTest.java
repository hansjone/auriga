package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.iam.api.InternalTokenIssuer;
import java.nio.file.Path;
import java.time.Clock;
import java.util.*;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;
import org.springframework.context.annotation.AnnotationConfigApplicationContext;
import org.springframework.core.env.MapPropertySource;

class ServiceTokenExportIsolationTest {
  @TempDir Path dir;
  static final String EDGE = "auriga.iam.edge-token-export.";
  static final String GATEWAY = "auriga.iam.ai-gateway-token-export.";

  Map<String, Object> properties() {
    return new HashMap<>(
        Map.of(
            "auriga.platform.combined",
            "true",
            EDGE + "enabled",
            "true",
            GATEWAY + "enabled",
            "true",
            EDGE + "token-file",
            dir.resolve("edge-out/token").toString(),
            GATEWAY + "token-file",
            dir.resolve("gateway-out/token").toString(),
            EDGE + "installation-secret-file",
            dir.resolve("edge-private/secret").toString(),
            GATEWAY + "installation-secret-file",
            dir.resolve("gateway-private/secret").toString(),
            GATEWAY + "reader-group",
            "65531"));
  }

  void rejected(Map<String, Object> properties, boolean gatewayFirst) {
    var issuer = mock(InternalTokenIssuer.class);
    try (var context = new AnnotationConfigApplicationContext()) {
      context
          .getEnvironment()
          .getPropertySources()
          .addFirst(new MapPropertySource("test", properties));
      context.registerBean(InternalTokenIssuer.class, () -> issuer);
      context.register(
          gatewayFirst ? GatewayTokenFileConfiguration.class : EdgeTokenFileConfiguration.class,
          gatewayFirst ? EdgeTokenFileConfiguration.class : GatewayTokenFileConfiguration.class);
      Throwable failure =
          assertThrows(org.springframework.beans.BeansException.class, context::refresh);
      while (failure.getCause() != null) failure = failure.getCause();
      assertTrue(
          failure
              .getMessage()
              .contains(
                  properties.containsKey(GATEWAY + "reader-group")
                      ? "Service token exports require"
                      : "reader-group"),
          failure.getMessage());
      verifyNoInteractions(issuer);
    }
  }

  @Test
  void bothBeanOrdersRejectOverlappingFilesDirectoriesAndReaderGroupsBeforeLifecycle() {
    for (boolean gatewayFirst : List.of(false, true)) {
      for (String[] conflict :
          List.of(
              new String[] {GATEWAY + "token-file", "edge-out/token"},
              new String[] {GATEWAY + "token-file", "edge-out/other"},
              new String[] {GATEWAY + "token-file", "edge-out/child/token"},
              new String[] {EDGE + "token-file", "gateway-out/child/token"},
              new String[] {GATEWAY + "installation-secret-file", "edge-private/secret"},
              new String[] {GATEWAY + "installation-secret-file", "edge-private/other"},
              new String[] {GATEWAY + "installation-secret-file", "edge-private/child/secret"},
              new String[] {GATEWAY + "token-file", "edge-private/secret"},
              new String[] {GATEWAY + "installation-secret-file", "edge-out/token"})) {
        var config = properties();
        config.put(conflict[0], dir.resolve(conflict[1]).toString());
        rejected(config, gatewayFirst);
      }
      var config = properties();
      config.put(GATEWAY + "reader-group", "65532");
      rejected(config, gatewayFirst);
    }
  }

  @Test
  void gatewayReaderGroupIsRequiredEvenWithoutEdgeExport() {
    var config = properties();
    config.put(EDGE + "enabled", "false");
    config.remove(GATEWAY + "reader-group");
    rejected(config, true);
  }

  @Test
  void conflictingInstallationIsRejectedByTheActualBeanFactories() {
    var config = properties();
    config.put(GATEWAY + "reader-group", "65532");
    var env = new org.springframework.mock.env.MockEnvironment();
    config.forEach((key, value) -> env.setProperty(key, value.toString()));
    var issuer = mock(InternalTokenIssuer.class);
    assertThrows(
        IllegalStateException.class,
        () -> new EdgeTokenFileConfiguration().iamEdgeTokenFilePublisher(issuer, env));
    assertThrows(
        IllegalStateException.class,
        () -> new GatewayTokenFileConfiguration().iamGatewayTokenFilePublisher(issuer, env));
    verifyNoInteractions(issuer);
  }

  @Test
  void independentPathsAndExplicitDistinctGroupsStillCreateFixedPublishers() {
    var env = new org.springframework.mock.env.MockEnvironment();
    properties().forEach((key, value) -> env.setProperty(key, value.toString()));
    var issuer = mock(InternalTokenIssuer.class);
    assertInstanceOf(
        EdgeTokenFilePublisher.class,
        new EdgeTokenFileConfiguration().iamEdgeTokenFilePublisher(issuer, env));
    assertInstanceOf(
        GatewayTokenFilePublisher.class,
        new GatewayTokenFileConfiguration().iamGatewayTokenFilePublisher(issuer, env));
    verifyNoInteractions(issuer);
  }

  @Test
  void differentServicesCannotReuseAnInstallationSecretDigest() {
    String digest = Secrets.hash(Secrets.random());
    assertThrows(
        IllegalStateException.class,
        () ->
            new TokenService(
                mock(SigningKeys.class),
                new ContractSchemas(),
                (t, s, c) -> Set.of(),
                Clock.systemUTC(),
                "https://portal.example.com",
                Map.of("edge", digest, "ai-gateway", digest)));
  }
}
