package auriga.resource.web;

import auriga.common.error.*;
import auriga.common.security.PublicEndpoint;
import auriga.resource.app.*;
import jakarta.servlet.http.HttpServletRequest;
import java.security.cert.X509Certificate;
import java.util.UUID;
import org.springframework.boot.autoconfigure.condition.ConditionalOnExpression;
import org.springframework.core.io.InputStreamResource;
import org.springframework.http.*;
import org.springframework.web.bind.annotation.*;

@RestController
@RequestMapping("/internal/resource/v1/catalog/downloads")
@ConditionalOnExpression(
    "${auriga.services.resource.enabled:true} && ('${auriga.service.schema:}' == 'resource' || ${auriga.platform.combined:false})")
public class CatalogNodeController {
  private final CatalogDelivery delivery;
  private final ResourceDistributions identity;

  public CatalogNodeController(CatalogDelivery delivery, ResourceDistributions identity) {
    this.delivery = delivery;
    this.identity = identity;
  }

  @GetMapping("/{id}/manifest")
  @PublicEndpoint
  public ResponseEntity<InputStreamResource> manifest(@PathVariable UUID id, HttpServletRequest r) {
    return content(id, null, r);
  }

  @GetMapping("/{id}/artifacts/{artifact}")
  @PublicEndpoint
  public ResponseEntity<InputStreamResource> artifact(
      @PathVariable UUID id, @PathVariable String artifact, HttpServletRequest r) {
    return content(id, artifact, r);
  }

  private ResponseEntity<InputStreamResource> content(
      UUID id, String artifact, HttpServletRequest r) {
    if (!r.isSecure()
        || !(r.getAttribute("jakarta.servlet.request.X509Certificate")
            instanceof X509Certificate[] certificates))
      throw new AurigaException(CommonErrors.UNAUTHENTICATED);
    UUID node = identity.authenticate(certificates);
    return ResponseEntity.ok()
        .cacheControl(CacheControl.noStore())
        .contentType(
            artifact == null ? MediaType.APPLICATION_JSON : MediaType.APPLICATION_OCTET_STREAM)
        .header("X-Content-Type-Options", "nosniff")
        .body(
            new InputStreamResource(
                delivery.nodeDownload(
                    identity.nodeTenant(node), node, id, artifact, r.getHeader("Range") != null)));
  }
}
