package auriga.resource.web;

import auriga.common.error.*;
import auriga.common.security.PublicEndpoint;
import auriga.resource.app.ResourceDistributions;
import jakarta.servlet.http.HttpServletRequest;
import java.security.cert.X509Certificate;
import java.util.UUID;
import org.springframework.boot.autoconfigure.condition.ConditionalOnExpression;
import org.springframework.core.io.InputStreamResource;
import org.springframework.http.*;
import org.springframework.web.bind.annotation.*;
import tools.jackson.databind.JsonNode;

/** Direct mTLS only. No forwarded certificate/header or bearer token is a node identity. */
@RestController
@RequestMapping("/internal/resource/v1/downloads")
@ConditionalOnExpression(
    "${auriga.services.resource.enabled:true} && ('${auriga.service.schema:}' == 'resource' || ${auriga.platform.combined:false})")
public class ResourceNodeDownloadController {
  private final ResourceDistributions distributions;

  public ResourceNodeDownloadController(ResourceDistributions distributions) {
    this.distributions = distributions;
  }

  private UUID node(HttpServletRequest request) {
    if (!request.isSecure()
        || !(request.getAttribute("jakarta.servlet.request.X509Certificate")
            instanceof X509Certificate[] certificates))
      throw new AurigaException(CommonErrors.UNAUTHENTICATED);
    return distributions.authenticate(certificates);
  }

  @GetMapping("/{downloadId}")
  @PublicEndpoint
  public ResponseEntity<InputStreamResource> download(
      @PathVariable UUID downloadId, HttpServletRequest request) {
    UUID node = node(request);
    return ResponseEntity.ok()
        .cacheControl(CacheControl.noStore())
        .contentType(MediaType.parseMediaType("application/gzip"))
        .header("X-Content-Type-Options", "nosniff")
        .body(
            new InputStreamResource(
                distributions.download(
                    distributions.nodeTenant(node),
                    node,
                    downloadId,
                    request.getHeader("Range") != null)));
  }

  @PostMapping("/{downloadId}/renewals")
  @PublicEndpoint
  public ResponseEntity<JsonNode> renew(@PathVariable UUID downloadId, HttpServletRequest request) {
    UUID node = node(request);
    return ResourceWorkflowController.response(
        distributions.renew(
            distributions.nodeTenant(node),
            node,
            downloadId,
            ResourceBody.header(request, "Idempotency-Key")),
        201);
  }
}
