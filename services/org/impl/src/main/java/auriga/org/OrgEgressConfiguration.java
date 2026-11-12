package auriga.org;

import auriga.contracts.deploymentconfig.DeploymentAccess;
import java.nio.file.Path;
import java.time.Clock;
import java.util.UUID;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.boot.autoconfigure.condition.ConditionalOnExpression;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.core.env.Environment;
import org.springframework.scheduling.annotation.EnableScheduling;
import org.springframework.scheduling.annotation.Scheduled;

@Configuration(proxyBeanMethods = false)
@EnableScheduling
@ConditionalOnExpression(
    "${auriga.services.org.enabled:true} && ('${auriga.service.schema:}' == 'org' || ${auriga.platform.combined:false})")
public class OrgEgressConfiguration {
  @Bean
  OrgEgressDistribution orgEgressDistribution(
      OrgService org, Clock clock, ObjectProvider<DeploymentAccess> access, Environment env) {
    var sources = access.stream().limit(2).toList();
    return new OrgEgressDistribution(
        org,
        clock,
        sources.size() == 1 ? UUID.fromString(sources.getFirst().tenantId()) : null,
        env.getProperty("AURIGA_EGRESS_NODE_ID", "egress-primary"),
        path(env, "AURIGA_EGRESS_POLICY_DIRECTORY"),
        path(env, "AURIGA_EGRESS_REPORT_DIRECTORY"));
  }

  private static Path path(Environment env, String name) {
    String value = env.getProperty(name);
    return value == null || value.isBlank() ? null : Path.of(value);
  }

  @Bean
  EgressWorker egressWorker(OrgEgressDistribution distribution) {
    return new EgressWorker(distribution);
  }

  static final class EgressWorker {
    private static final org.slf4j.Logger LOG =
        org.slf4j.LoggerFactory.getLogger(EgressWorker.class);
    private final OrgEgressDistribution distribution;

    EgressWorker(OrgEgressDistribution distribution) {
      this.distribution = distribution;
    }

    @Scheduled(fixedDelay = 2000, initialDelay = 2000)
    public void synchronize() {
      try {
        distribution.reconcile();
      } catch (RuntimeException error) {
        LOG.warn("Egress distribution or report import failed");
      }
    }
  }
}
