package auriga.resource.web;

import auriga.common.security.RequiresPermission;
import auriga.resource.app.ResourceDistributions;
import org.springframework.boot.autoconfigure.condition.ConditionalOnExpression;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.*;
import tools.jackson.databind.JsonNode;

@RestController
@ConditionalOnExpression(
    "${auriga.services.resource.enabled:true} && ('${auriga.service.schema:}' == 'resource' || ${auriga.platform.combined:false})")
public class ResourceRevocationController {
  private final ResourceDistributions distributions;

  public ResourceRevocationController(ResourceDistributions distributions) {
    this.distributions = distributions;
  }

  @GetMapping("/api/resource/v1/revocations")
  @RequiresPermission("resource.packages.read")
  public ResponseEntity<JsonNode> get() {
    return ResourceWorkflowController.response(
        distributions.revocationDocument(ResourceWorkflowController.actor()), 200);
  }
}
