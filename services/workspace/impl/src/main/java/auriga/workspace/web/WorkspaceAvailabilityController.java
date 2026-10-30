package auriga.workspace.web;

import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.security.AuthenticatedOnly;
import java.util.Map;
import org.springframework.boot.autoconfigure.condition.ConditionalOnExpression;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RestController;

/** Reports an explicitly disabled workspace without initializing its runtime or database. */
@ConditionalOnExpression(
    "!${auriga.services.workspace.enabled:true} && ('${auriga.service.schema:}' == 'workspace' || ${auriga.platform.combined:false})")
@RestController
public class WorkspaceAvailabilityController {
  @GetMapping("/api/workspace/v1/me/instances")
  @AuthenticatedOnly
  public void listMyInstances() {
    throw new AurigaException(
        CommonErrors.UNAVAILABLE, Map.of("service", "workspace", "reason", "disabled"));
  }
}
