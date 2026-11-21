package auriga.platform;

import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.security.PrincipalType;
import auriga.common.security.TokenContext;
import auriga.iam.IamStore;
import auriga.iam.RequestIdentity;
import auriga.iam.SessionManager;
import auriga.iam.api.PlacementAuthorization;
import auriga.workspace.api.SharedDshAuthority.Inputs;
import auriga.workspace.api.SharedDshAuthority.ResourceInputs;
import auriga.workspace.api.WorkspaceInputs;
import auriga.workspace.web.SharedDshController;
import org.springframework.boot.autoconfigure.condition.ConditionalOnExpression;
import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;

@Configuration(proxyBeanMethods = false)
@ConditionalOnExpression(
    "${auriga.platform.combined:false} && ${auriga.services.workspace.enabled:false}")
@ConditionalOnProperty(name = "auriga.workspace.shared-dsh.enabled", havingValue = "true")
public class PlatformSharedDshConfiguration {
  @Bean
  Inputs combinedSharedDshInputs(
      WorkspaceInputs org,
      IamStore iam,
      SessionManager sessions,
      PlacementAuthorization placement,
      ResourceInputs resources) {
    return new PlatformSharedDshInputs(org, iam, sessions, placement, resources);
  }

  @Bean
  SharedDshController.RequestGate sharedDshBrowserGate(
      IamStore iam, SessionManager sessions, RequestIdentity browser) {
    return (request, actor, mutation) -> {
      PlatformSharedDshInputs.requireTenant(actor.tenantId());
      if (actor.type() != PrincipalType.USER
          || (actor.context() != TokenContext.PORTAL && actor.context() != TokenContext.ADMIN))
        throw new AurigaException(CommonErrors.FORBIDDEN);
      // The same IAM verifier checks the trusted Edge hop, live cookie session, Origin and
      // the appropriate CSRF policy. The browser's access JWT alone cannot open a placement.
      iam.tx(() -> browser.browserSession(request, sessions, mutation));
    };
  }
}
