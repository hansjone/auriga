package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.iam.api.InternalTokenIssuer;
import java.nio.file.Path;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;
import org.springframework.mock.env.MockEnvironment;

class MsgnetProjectorTokenConfigurationTest {
  @TempDir Path dir;

  MockEnvironment environment() {
    var env = new MockEnvironment().withProperty("auriga.platform.combined", "true");
    for (String component : java.util.List.of("network", "edge", "broker")) {
      String prefix = "auriga.iam.msgnet-projector-token-export." + component + ".";
      env.setProperty(prefix + "enabled", "true");
      env.setProperty(
          prefix + "installation-secret-file",
          dir.resolve(component + "-secret/install").toString());
      env.setProperty(prefix + "token-file", dir.resolve(component + "-token/service").toString());
      env.setProperty(prefix + "reader-group", component.equals("edge") ? "65532" : "10001");
    }
    return env;
  }

  @Test
  void independentComponentPublishersAcceptSharedUidWithPrivateMounts() {
    var issuer = mock(InternalTokenIssuer.class);
    var config = new MsgnetProjectorTokenConfiguration();
    var env = environment();
    try (var network = config.msgnetNetworkProjectorToken(issuer, env);
        var edge = config.msgnetEdgeProjectorToken(issuer, env);
        var broker = config.msgnetBrokerProjectorToken(issuer, env)) {
      assertNotSame(network, edge);
      assertNotSame(network, broker);
      verifyNoInteractions(issuer);
    }
  }

  @Test
  void componentCannotShareCredentialsOrOutputWithAnotherComponent() {
    var issuer = mock(InternalTokenIssuer.class);
    var config = new MsgnetProjectorTokenConfiguration();
    var env = environment();
    env.setProperty(
        "auriga.iam.msgnet-projector-token-export.edge.installation-secret-file",
        dir.resolve("network-secret/install").toString());
    assertThrows(
        IllegalStateException.class, () -> config.msgnetNetworkProjectorToken(issuer, env));
    verifyNoInteractions(issuer);
  }

  @Test
  void arbitraryServiceCannotUsePrivateFilePublisher() {
    assertThrows(
        IllegalStateException.class,
        () ->
            new ServiceTokenFilePublisher(
                "arbitrary",
                mock(InternalTokenIssuer.class),
                dir.resolve("secret"),
                dir.resolve("token"),
                10001,
                -1));
  }
}
