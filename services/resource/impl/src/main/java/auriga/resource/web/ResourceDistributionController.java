package auriga.resource.web;

import static auriga.resource.web.ResourceBody.*;
import static auriga.resource.web.ResourceWorkflowController.*;

import auriga.common.security.*;
import auriga.resource.app.ResourceDistributions;
import jakarta.servlet.http.HttpServletRequest;
import java.util.*;
import org.springframework.boot.autoconfigure.condition.ConditionalOnExpression;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.*;
import tools.jackson.databind.JsonNode;

@RestController
@RequestMapping("/api/resource/v1")
@ConditionalOnExpression(
    "${auriga.services.resource.enabled:true} && ('${auriga.service.schema:}' == 'resource' || ${auriga.platform.combined:false})")
@RequiresPermission("resource.distributions.manage")
public class ResourceDistributionController {
  private final ResourceDistributions distributions;

  public ResourceDistributionController(ResourceDistributions distributions) {
    this.distributions = distributions;
  }

  @PostMapping(value = "/distributions", consumes = "application/json")
  @RequiresRecentStepUp
  public ResponseEntity<JsonNode> create(HttpServletRequest request) {
    return response(
        distributions.create(
            actor(),
            read(request, Set.of("instanceId", "selections"), Set.of("instanceId", "selections")),
            header(request, "Idempotency-Key")),
        202);
  }

  @GetMapping("/distributions/{distributionId}")
  public ResponseEntity<JsonNode> get(@PathVariable UUID distributionId) {
    return response(distributions.get(actor(), distributionId), 200);
  }

  @GetMapping("/admin/distributions")
  @RequiresRecentStepUp
  public ResponseEntity<JsonNode> list(
      @RequestParam(defaultValue = "50") int limit, @RequestParam(required = false) String cursor) {
    return response(distributions.list(actor(), limit, cursor), 200);
  }
}
