package auriga.resource.web;

import auriga.common.error.*;
import auriga.common.security.*;
import auriga.resource.app.ResourceQueries;
import auriga.resource.web.model.VersionPage;
import org.springframework.boot.autoconfigure.condition.ConditionalOnExpression;
import org.springframework.http.*;
import org.springframework.web.bind.annotation.*;

/** Metadata-only governance. No signing, publishing or installation is implied by these reads. */
@RestController
@ConditionalOnExpression(
    "${auriga.services.resource.enabled:true} && ('${auriga.service.schema:}' == 'resource' || ${auriga.platform.combined:false})")
@RequestMapping("/api/resource/v1/admin")
@RequiresRecentStepUp
public class ResourceAdministrationController {
  private final ResourceQueries queries;

  public ResourceAdministrationController(ResourceQueries queries) {
    this.queries = queries;
  }

  @GetMapping("/review")
  @RequiresPermission("resource.versions.review")
  public ResponseEntity<VersionPage> review(
      @RequestParam(defaultValue = "50") int limit, @RequestParam(required = false) String cursor) {
    return read("review", limit, cursor);
  }

  @GetMapping("/published")
  @RequiresPermission("resource.versions.publish")
  public ResponseEntity<VersionPage> published(
      @RequestParam(defaultValue = "50") int limit, @RequestParam(required = false) String cursor) {
    return read("published", limit, cursor);
  }

  private ResponseEntity<VersionPage> read(String phase, int limit, String cursor) {
    var actor =
        AuthorizationRules.currentPrincipal()
            .orElseThrow(() -> new AurigaException(CommonErrors.UNAUTHENTICATED));
    return ResponseEntity.ok()
        .cacheControl(CacheControl.noStore())
        .body(queries.governance(actor, phase, limit, cursor));
  }
}
