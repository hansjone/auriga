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
public class CatalogController {
  private final CatalogWorkflow workflow;

  public CatalogController(CatalogWorkflow workflow) {
    this.workflow = workflow;
  }

  private static AurigaPrincipal actor() {
    return ResourceWorkflowController.actor();
  }

  private static ResponseEntity<JsonNode> response(JsonNode n, int status) {
    return ResourceWorkflowController.response(n, status);
  }

  @GetMapping("/catalog/packages")
  @RequiresPermission(ResourceAccess.READ)
  public ResponseEntity<JsonNode> list(
      @RequestParam(required = false) String kind,
      @RequestParam(defaultValue = "50") int limit,
      @RequestParam(required = false) String cursor) {
    return response(workflow.list(actor(), null, kind, limit, cursor, null), 200);
  }

  @PostMapping("/catalog/packages")
  @RequiresPermission(ResourceAccess.UPLOAD)
  public ResponseEntity<JsonNode> create(HttpServletRequest r) {
    return response(
        workflow.create(
            actor(),
            read(
                r,
                Set.of("name", "kind", "visibility", "groupIds"),
                Set.of("name", "kind", "visibility")),
            header(r, "Idempotency-Key"),
            header(r, "If-Match")),
        201);
  }

  @GetMapping("/catalog/packages/{p}")
  @RequiresPermission(ResourceAccess.READ)
  public ResponseEntity<JsonNode> detail(@PathVariable UUID p) {
    return response(workflow.detail(actor(), p, null, false), 200);
  }

  @GetMapping("/catalog/packages/{p}/versions")
  @RequiresPermission(ResourceAccess.READ)
  public ResponseEntity<JsonNode> versions(
      @PathVariable UUID p,
      @RequestParam(defaultValue = "50") int limit,
      @RequestParam(required = false) String cursor) {
    return response(workflow.list(actor(), p, null, limit, cursor, null), 200);
  }

  @GetMapping("/catalog/packages/{p}/versions/{v}")
  @RequiresPermission(ResourceAccess.READ)
  public ResponseEntity<JsonNode> version(@PathVariable UUID p, @PathVariable UUID v) {
    return response(workflow.detail(actor(), p, v, false), 200);
  }

  @PostMapping(value = "/catalog/packages/{p}/versions", consumes = "multipart/form-data")
  @RequiresPermission(ResourceAccess.UPLOAD)
  @RequiresRecentStepUp
  public ResponseEntity<JsonNode> upload(@PathVariable UUID p, HttpServletRequest r) {
    var streams = new ArrayList<java.io.InputStream>();
    try {
      var parts = r.getParts();
      if (parts.size() > 34
          || parts.stream().filter(x -> x.getName().equals("metadata")).count() != 1
          || parts.stream().filter(x -> x.getName().equals("manifest")).count() != 1
          || parts.stream()
              .anyMatch(x -> !Set.of("metadata", "manifest", "artifacts").contains(x.getName())))
        throw invalid();
      var metadata = r.getPart("metadata");
      var body =
          parse(
              metadata.getInputStream().readNBytes(32769),
              32768,
              Set.of("version", "expectedDigest", "changeNotes"),
              Set.of("version", "expectedDigest"));
      if (body.has("changeNotes")) text(body, "changeNotes", 4096);
      byte[] manifest = r.getPart("manifest").getInputStream().readNBytes(262145);
      for (var part : parts)
        if (part.getName().equals("artifacts")) streams.add(part.getInputStream());
      return response(
          workflow.upload(
              actor(),
              p,
              body,
              manifest,
              streams,
              header(r, "Idempotency-Key"),
              header(r, "If-Match")),
          201);
    } catch (java.io.IOException | jakarta.servlet.ServletException e) {
      throw invalid();
    } finally {
      for (var in : streams)
        try {
          in.close();
        } catch (java.io.IOException e) {
          /* continue closing */
        }
    }
  }

  @PostMapping("/catalog/packages/{p}/versions/{v}/submissions")
  @RequiresPermission("resource.versions.submit")
  @RequiresRecentStepUp
  public ResponseEntity<JsonNode> submit(
      @PathVariable UUID p, @PathVariable UUID v, HttpServletRequest r) {
    return response(
        workflow.submit(
            actor(),
            p,
            v,
            read(r, Set.of("reason"), Set.of("reason")),
            header(r, "Idempotency-Key"),
            header(r, "If-Match")),
        201);
  }

  @GetMapping("/admin/catalog/review")
  @RequiresPermission("resource.versions.review")
  @RequiresRecentStepUp
  public ResponseEntity<JsonNode> reviews(
      @RequestParam(required = false) String kind,
      @RequestParam(defaultValue = "50") int limit,
      @RequestParam(required = false) String cursor) {
    return response(workflow.list(actor(), null, kind, limit, cursor, "review"), 200);
  }

  @GetMapping("/admin/catalog/published")
  @RequiresPermission("resource.versions.publish")
  @RequiresRecentStepUp
  public ResponseEntity<JsonNode> published(
      @RequestParam(required = false) String kind,
      @RequestParam(defaultValue = "50") int limit,
      @RequestParam(required = false) String cursor) {
    return response(workflow.list(actor(), null, kind, limit, cursor, "published"), 200);
  }

  @GetMapping("/admin/catalog/reviews/{id}")
  @RequiresPermission("resource.versions.review")
  @RequiresRecentStepUp
  public ResponseEntity<JsonNode> review(@PathVariable UUID id) {
    return response(workflow.reviewDetail(actor(), id), 200);
  }

  @PostMapping("/admin/catalog/reviews/{id}/decisions")
  @RequiresPermission("resource.versions.review")
  @RequiresRecentStepUp
  public ResponseEntity<JsonNode> decide(@PathVariable UUID id, HttpServletRequest r) {
    return response(
        workflow.decide(
            actor(),
            id,
            read(
                r,
                Set.of("decision", "reason", "versionRevision", "digest", "role", "riskLevel"),
                Set.of("decision", "reason", "versionRevision", "digest", "role")),
            header(r, "Idempotency-Key"),
            header(r, "If-Match")),
        200);
  }

  @PostMapping("/admin/catalog/packages/{p}/versions/{v}/publications")
  @RequiresPermission("resource.versions.publish")
  @RequiresRecentStepUp
  public ResponseEntity<JsonNode> publish(
      @PathVariable UUID p, @PathVariable UUID v, HttpServletRequest r) {
    return response(
        workflow.publish(
            actor(),
            p,
            v,
            read(r, Set.of("reason"), Set.of("reason")),
            header(r, "Idempotency-Key"),
            header(r, "If-Match")),
        201);
  }

  @PostMapping("/admin/catalog/packages/{p}/versions/{v}/withdrawals")
  @RequiresPermission("resource.versions.publish")
  @RequiresRecentStepUp
  public ResponseEntity<JsonNode> withdraw(
      @PathVariable UUID p, @PathVariable UUID v, HttpServletRequest r) {
    return response(
        workflow.withdraw(
            actor(),
            p,
            v,
            read(r, Set.of("mode", "reason"), Set.of("mode", "reason")),
            header(r, "Idempotency-Key"),
            header(r, "If-Match")),
        200);
  }

  @GetMapping("/catalog/packages/{p}/versions/{v}/manifest")
  @RequiresPermission(ResourceAccess.READ)
  public ResponseEntity<InputStreamResource> manifest(
      @PathVariable UUID p, @PathVariable UUID v, HttpServletRequest r) {
    return content(p, v, null, r);
  }

  @GetMapping("/catalog/packages/{p}/versions/{v}/artifacts/{id}")
  @RequiresPermission(ResourceAccess.READ)
  public ResponseEntity<InputStreamResource> artifact(
      @PathVariable UUID p, @PathVariable UUID v, @PathVariable String id, HttpServletRequest r) {
    return content(p, v, id, r);
  }

  private ResponseEntity<InputStreamResource> content(
      UUID p, UUID v, String id, HttpServletRequest r) {
    return ResponseEntity.ok()
        .cacheControl(CacheControl.noStore())
        .contentType(id == null ? MediaType.APPLICATION_JSON : MediaType.APPLICATION_OCTET_STREAM)
        .header("X-Content-Type-Options", "nosniff")
        .body(
            new InputStreamResource(
                workflow.download(actor(), p, v, id, r.getHeader("Range") != null)));
  }
}
