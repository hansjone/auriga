package auriga.resource.web;

import static auriga.resource.web.ResourceBody.*;

import auriga.common.security.*;
import auriga.resource.app.*;
import jakarta.servlet.http.HttpServletRequest;
import java.util.*;
import org.springframework.boot.autoconfigure.condition.ConditionalOnExpression;
import org.springframework.core.io.InputStreamResource;
import org.springframework.http.*;
import org.springframework.web.bind.annotation.*;
import tools.jackson.databind.JsonNode;

@RestController
@RequestMapping("/api/resource/v1")
@ConditionalOnExpression(
    "${auriga.services.resource.enabled:true} && ('${auriga.service.schema:}' == 'resource' || ${auriga.platform.combined:false})")
public class CatalogDeliveryController {
  private final CatalogDelivery delivery;

  public CatalogDeliveryController(CatalogDelivery delivery) {
    this.delivery = delivery;
  }

  private static AurigaPrincipal actor() {
    return ResourceWorkflowController.actor();
  }

  private static ResponseEntity<JsonNode> response(JsonNode n, int status) {
    return ResourceWorkflowController.response(n, status);
  }

  @PostMapping("/catalog/member/installations")
  @RequiresPermission(ResourceAccess.READ)
  public ResponseEntity<JsonNode> select(HttpServletRequest r) {
    var n =
        delivery.select(
            actor(), read(r, null, Set.of()), header(r, "Idempotency-Key"), header(r, "If-Match"));
    return ResponseEntity.status(202)
        .cacheControl(CacheControl.noStore())
        .eTag("\"" + n.path("installationRevision").asLong() + "\"")
        .body(n);
  }

  @PostMapping("/catalog/member/download-tickets")
  @RequiresPermission(ResourceAccess.READ)
  public ResponseEntity<JsonNode> ticket(HttpServletRequest r) {
    var body = read(r, null, Set.of());
    var n = delivery.ticket(actor(), body, header(r, "Idempotency-Key"), header(r, "If-Match"));
    return ResponseEntity.status(201)
        .cacheControl(CacheControl.noStore())
        .eTag("\"" + body.path("installationRevision").asLong() + "\"")
        .body(n);
  }

  @PostMapping("/catalog/member/renewals")
  @RequiresPermission(ResourceAccess.READ)
  public ResponseEntity<JsonNode> renew(HttpServletRequest r) {
    var body = read(r, null, Set.of());
    var n = delivery.renew(actor(), body, header(r, "Idempotency-Key"), header(r, "If-Match"));
    return ResponseEntity.status(200)
        .cacheControl(CacheControl.noStore())
        .eTag("\"" + body.path("installationRevision").asLong() + "\"")
        .body(n);
  }

  @PostMapping("/catalog/member/installation-reports")
  @RequiresPermission(ResourceAccess.READ)
  public ResponseEntity<JsonNode> report(HttpServletRequest r) {
    var n =
        delivery.report(
            actor(), read(r, null, Set.of()), header(r, "Idempotency-Key"), header(r, "If-Match"));
    return ResponseEntity.status(200)
        .cacheControl(CacheControl.noStore())
        .eTag("\"" + n.path("installationRevision").asLong() + "\"")
        .body(n);
  }

  @PostMapping("/catalog/member/downloads")
  @RequiresPermission(ResourceAccess.READ)
  public ResponseEntity<InputStreamResource> download(HttpServletRequest r) {
    return ResponseEntity.ok()
        .cacheControl(CacheControl.noStore())
        .contentType(MediaType.APPLICATION_OCTET_STREAM)
        .header("X-Content-Type-Options", "nosniff")
        .body(
            new InputStreamResource(
                delivery.download(
                    actor(),
                    read(r, null, Set.of()),
                    header(r, "Idempotency-Key"),
                    header(r, "If-Match"),
                    r.getHeader("Range") != null)));
  }

  @GetMapping("/catalog/member/installations")
  @RequiresPermission(ResourceAccess.READ)
  public ResponseEntity<JsonNode> list(
      @RequestParam UUID clientId,
      @RequestParam(required = false) String kind,
      @RequestParam(defaultValue = "50") int limit,
      @RequestParam(required = false) String cursor) {
    return response(delivery.installations(actor(), clientId, kind, limit, cursor), 200);
  }

  @PostMapping("/admin/catalog/distributions")
  @RequiresPermission("resource.distributions.manage")
  @RequiresRecentStepUp
  public ResponseEntity<JsonNode> distribute(HttpServletRequest r) {
    var n =
        delivery.distribute(
            actor(), read(r, null, Set.of()), header(r, "Idempotency-Key"), header(r, "If-Match"));
    return ResponseEntity.status(202)
        .cacheControl(CacheControl.noStore())
        .eTag("\"" + n.path("selectionRevision").asLong() + "\"")
        .body(n);
  }

  @GetMapping("/admin/catalog/distributions")
  @RequiresPermission("resource.distributions.manage")
  @RequiresRecentStepUp
  public ResponseEntity<JsonNode> distributions(
      @RequestParam(required = false) UUID instanceId,
      @RequestParam(required = false) String kind,
      @RequestParam(defaultValue = "50") int limit,
      @RequestParam(required = false) String cursor) {
    return response(delivery.distributions(actor(), instanceId, kind, limit, cursor), 200);
  }

  @GetMapping("/admin/catalog/distributions/{id}")
  @RequiresPermission("resource.distributions.manage")
  @RequiresRecentStepUp
  public ResponseEntity<JsonNode> distribution(@PathVariable UUID id) {
    return response(delivery.distribution(actor(), id), 200);
  }
}
