package auriga.resource.web;

import static auriga.resource.web.ResourceBody.*;

import auriga.common.security.RequiresPermission;
import auriga.resource.app.CatalogPlacementMarket;
import auriga.resource.app.ResourceAccess;
import jakarta.servlet.http.HttpServletRequest;
import java.util.Set;
import java.util.UUID;
import org.springframework.boot.autoconfigure.condition.ConditionalOnExpression;
import org.springframework.http.*;
import org.springframework.web.bind.annotation.*;
import tools.jackson.databind.JsonNode;

@RestController
@RequestMapping("/api/resource/v1/catalog/placements/{placementId}/installations")
@ConditionalOnExpression(
    "${auriga.services.resource.enabled:true} && ('${auriga.service.schema:}' == 'resource' || ${auriga.platform.combined:false})")
public class CatalogPlacementController {
  private final CatalogPlacementMarket market;

  public CatalogPlacementController(CatalogPlacementMarket market) {
    this.market = market;
  }

  @GetMapping
  @RequiresPermission(ResourceAccess.READ)
  public ResponseEntity<JsonNode> list(
      @PathVariable UUID placementId,
      @RequestParam(required = false) String kind,
      @RequestParam(defaultValue = "50") int limit,
      @RequestParam(required = false) String cursor) {
    return ResponseEntity.ok()
        .cacheControl(CacheControl.noStore())
        .body(market.list(ResourceWorkflowController.actor(), placementId, kind, limit, cursor));
  }

  @PostMapping
  @RequiresPermission(ResourceAccess.READ)
  public ResponseEntity<JsonNode> select(
      @PathVariable UUID placementId, HttpServletRequest request) {
    var result =
        market.select(
            ResourceWorkflowController.actor(),
            placementId,
            read(request, Set.of("selection"), Set.of("selection")),
            header(request, "Idempotency-Key"),
            header(request, "If-Match"));
    return ResponseEntity.status(202)
        .cacheControl(CacheControl.noStore())
        .eTag("\"" + result.path("selectionRevision").asLong() + "\"")
        .body(result);
  }
}
