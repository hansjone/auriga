package auriga.resource.web;

import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.security.*;
import auriga.resource.app.ResourceAccess;
import auriga.resource.app.ResourceQueries;
import auriga.resource.web.model.*;
import java.util.UUID;
import org.springframework.boot.autoconfigure.condition.ConditionalOnExpression;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.*;

/** Four existing metadata GETs; no generated default mutation/download handlers are registered. */
@RestController
@RequestMapping("/api/resource/v1/packages")
@ConditionalOnExpression(
    "${auriga.services.resource.enabled:true} && ('${auriga.service.schema:}' == 'resource' || ${auriga.platform.combined:false})")
@RequiresPermission(ResourceAccess.READ)
public class ResourceController {
  private final ResourceQueries queries;
  private final auriga.resource.app.ResourceWorkflow workflow;

  public ResourceController(
      ResourceQueries queries, auriga.resource.app.ResourceWorkflow workflow) {
    this.queries = queries;
    this.workflow = workflow;
  }

  private AurigaPrincipal actor() {
    return AuthorizationRules.currentPrincipal()
        .orElseThrow(() -> new AurigaException(CommonErrors.UNAUTHENTICATED));
  }

  @GetMapping
  public ResponseEntity<PackagePage> list(
      @RequestParam(defaultValue = "50") int limit, @RequestParam(required = false) String cursor) {
    return ResponseEntity.ok()
        .cacheControl(org.springframework.http.CacheControl.noStore())
        .body(queries.packages(actor(), limit, cursor));
  }

  @GetMapping("/{packageId}")
  public ResponseEntity<ModelPackage> detail(@PathVariable UUID packageId) {
    var result = queries.resourcePackage(actor(), packageId);
    return ResponseEntity.ok()
        .cacheControl(org.springframework.http.CacheControl.noStore())
        .eTag("\"" + result.getRevision() + "\"")
        .body(result);
  }

  @GetMapping("/{packageId}/versions")
  public ResponseEntity<VersionPage> versions(
      @PathVariable UUID packageId,
      @RequestParam(defaultValue = "50") int limit,
      @RequestParam(required = false) String cursor) {
    return ResponseEntity.ok()
        .cacheControl(org.springframework.http.CacheControl.noStore())
        .body(queries.versions(actor(), packageId, limit, cursor));
  }

  @GetMapping("/{packageId}/versions/{versionId}")
  public ResponseEntity<tools.jackson.databind.JsonNode> version(
      @PathVariable UUID packageId, @PathVariable UUID versionId) {
    var result = workflow.catalogVersion(actor(), packageId, versionId);
    return ResponseEntity.ok()
        .cacheControl(org.springframework.http.CacheControl.noStore())
        .eTag("\"" + result.path("revision").asLong() + "\"")
        .body(result);
  }
}
