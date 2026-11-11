package auriga.ops.upgrade;

import auriga.common.security.*;
import java.nio.file.Path;
import java.time.Clock;
import java.util.UUID;
import org.springframework.boot.autoconfigure.condition.*;
import org.springframework.context.annotation.*;
import org.springframework.core.annotation.Order;
import org.springframework.core.env.Environment;
import org.springframework.security.config.annotation.web.builders.HttpSecurity;
import org.springframework.security.oauth2.jwt.JwtDecoder;
import org.springframework.security.web.SecurityFilterChain;

/**
 * Optional installation control bridge, routed through the existing authenticated workspace
 * upstream.
 */
@Configuration(proxyBeanMethods = false)
@ConditionalOnExpression("${auriga.ops.runtime.enabled:false}")
public class OpsRuntimeConfiguration {
  @Bean
  OpsRuntimeStore opsRuntimeStore(Environment env) throws Exception {
    return new OpsRuntimeStore(Path.of(env.getRequiredProperty("auriga.ops.runtime.directory")));
  }

  @Bean
  OpsUpgradeService opsUpgradeService(OpsRuntimeStore store, Environment env) {
    return new OpsUpgradeService(
        store,
        UUID.fromString(env.getRequiredProperty("auriga.ops.runtime.tenant-id")),
        Clock.systemUTC());
  }

  @Bean
  auriga.ops.backup.OpsBackupService opsBackupService(OpsUpgradeService upgrades) {
    return new auriga.ops.backup.OpsBackupService(upgrades);
  }

  @Bean
  @Order(1)
  @ConditionalOnWebApplication(type = ConditionalOnWebApplication.Type.SERVLET)
  SecurityFilterChain opsRuntimeChain(
      HttpSecurity http,
      JwtDecoder decoder,
      ProblemSecurityHandlers handlers,
      auriga.common.autoconfigure.AurigaProperties properties)
      throws Exception {
    http.securityMatcher("/api/workspace/v1/admin/ops/**");
    return AurigaWebSecurity.build(
        http, decoder, handlers, properties.getSecurity().getPublicPaths(), request -> false);
  }
}
