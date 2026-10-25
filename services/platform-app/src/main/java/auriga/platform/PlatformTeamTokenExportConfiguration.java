package auriga.platform;

import auriga.iam.EdgeTokenFilePublisher;
import auriga.iam.Secrets;
import auriga.iam.api.InternalTokenIssuer;
import java.nio.file.Path;
import java.util.Map;
import java.util.Set;
import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty;
import org.springframework.boot.context.properties.bind.Bindable;
import org.springframework.boot.context.properties.bind.Binder;
import org.springframework.context.SmartLifecycle;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.core.env.Environment;

/**
 * Installation-only fixed Team identities; reuses the existing POSIX secure publisher unchanged.
 */
@Configuration(proxyBeanMethods = false)
@ConditionalOnProperty(
    prefix = "auriga.platform.team-token-export",
    name = "enabled",
    havingValue = "true")
public class PlatformTeamTokenExportConfiguration {
  private static final String PREFIX = "auriga.platform.team-token-export.";

  @Bean(destroyMethod = "close")
  TeamPublishers platformTeamTokenFilePublisher(InternalTokenIssuer issuer, Environment env) {
    if (!env.getProperty("auriga.platform.combined", Boolean.class, false)
        || !env.getProperty("auriga.services.iam.enabled", Boolean.class, true)) throw invalid();
    Map<String, String> hashes =
        Binder.get(env)
            .bind("auriga.iam.internal-secret-hashes", Bindable.mapOf(String.class, String.class))
            .orElse(Map.of());
    String gatewayHash = hashes.get("ai-gateway");
    String collabHash = hashes.get("collab");
    if (gatewayHash == null
        || collabHash == null
        || !gatewayHash.matches("[a-f0-9]{64}")
        || !collabHash.matches("[a-f0-9]{64}")
        || Secrets.same(gatewayHash, collabHash)) throw invalid();
    Installation gateway = installation(env, "ai-gateway");
    Installation collab = installation(env, "collab");
    if (new java.util.HashSet<>(
                    java.util.List.of(
                        gateway.secret(), gateway.token(), collab.secret(), collab.token()))
                .size()
            != 4
        || gateway.token().getParent().equals(collab.token().getParent())
        || gateway.group() == collab.group()) throw invalid();
    return new TeamPublishers(
        publisher(issuer, "ai-gateway", gateway), publisher(issuer, "collab", collab));
  }

  private static Installation installation(Environment env, String service) {
    String prefix = PREFIX + service + ".";
    return new Installation(
        Path.of(env.getRequiredProperty(prefix + "installation-secret-file")),
        Path.of(env.getRequiredProperty(prefix + "token-file")),
        env.getRequiredProperty(prefix + "reader-group", Integer.class),
        env.getProperty(prefix + "installation-owner", Integer.class, -1));
  }

  private static EdgeTokenFilePublisher publisher(
      InternalTokenIssuer issuer, String service, Installation installation) {
    return new EdgeTokenFilePublisher(
        fixedIssuer(issuer, service),
        installation.secret(),
        installation.token(),
        installation.group(),
        installation.owner());
  }

  static InternalTokenIssuer fixedIssuer(InternalTokenIssuer authority, String fixedService) {
    if (!Set.of("ai-gateway", "collab").contains(fixedService)) throw invalid();
    return (publisherService, secret) -> {
      // The reused publisher has one fixed callback, never a caller-controlled service name.
      if (!"edge".equals(publisherService)) throw invalid();
      return authority.issue(fixedService, secret);
    };
  }

  private static IllegalStateException invalid() {
    return new IllegalStateException("Invalid private Team token installation configuration");
  }

  private record Installation(Path secret, Path token, int group, int owner) {}

  static final class TeamPublishers implements SmartLifecycle, AutoCloseable {
    private final EdgeTokenFilePublisher gateway;
    private final EdgeTokenFilePublisher collab;

    TeamPublishers(EdgeTokenFilePublisher gateway, EdgeTokenFilePublisher collab) {
      this.gateway = gateway;
      this.collab = collab;
    }

    @Override
    public synchronized void start() {
      try {
        gateway.start();
        collab.start();
      } catch (RuntimeException e) {
        gateway.stop();
        collab.stop();
        throw e;
      }
    }

    @Override
    public synchronized void stop() {
      gateway.stop();
      collab.stop();
    }

    @Override
    public synchronized boolean isRunning() {
      return gateway.isRunning() && collab.isRunning();
    }

    @Override
    public void close() {
      stop();
    }
  }
}
