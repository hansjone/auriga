package auriga.resource.web;

import static auriga.resource.web.ResourceBody.*;

import auriga.common.error.*;
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
public class ResourceWorkflowController {
  private final ResourceWorkflow workflow;
  private final ResourceQueries queries;
  private final ResourceDistributions distributions;

  public ResourceWorkflowController(
      ResourceWorkflow workflow, ResourceQueries queries, ResourceDistributions distributions) {
    this.workflow = workflow;
    this.queries = queries;
    this.distributions = distributions;
  }

  static AurigaPrincipal actor() {
    return AuthorizationRules.currentPrincipal()
        .orElseThrow(() -> new AurigaException(CommonErrors.UNAUTHENTICATED));
  }

  static ResponseEntity<JsonNode> response(JsonNode body, int status) {
    var builder = ResponseEntity.status(status).cacheControl(CacheControl.noStore());
    if (body.has("revision")) builder.eTag("\"" + body.path("revision").asLong() + "\"");
    return builder.body(body);
  }

  @PostMapping(value = "/packages/{packageId}/versions", consumes = "multipart/form-data")
  @RequiresPermission(ResourceAccess.UPLOAD)
  @RequiresRecentStepUp
  public ResponseEntity<JsonNode> upload(@PathVariable UUID packageId, HttpServletRequest request) {
    try {
      var parts = request.getParts();
      if (parts.size() != 2
          || parts.stream().filter(p -> p.getName().equals("metadata")).count() != 1
          || parts.stream().filter(p -> p.getName().equals("archive")).count() != 1)
        throw invalid();
      var metadata = request.getPart("metadata");
      var archive = request.getPart("archive");
      if (!MediaType.APPLICATION_JSON.isCompatibleWith(
          MediaType.parseMediaType(
              Objects.toString(metadata.getContentType(), "application/octet-stream"))))
        throw invalid();
      var body =
          parse(
              metadata.getInputStream().readNBytes(32769),
              32768,
              Set.of("version", "expectedDigest", "manifest", "changeNotes"),
              Set.of("version", "expectedDigest", "manifest"));
      return response(
          workflow.upload(
              actor(),
              packageId,
              body,
              archive.getInputStream(),
              header(request, "Idempotency-Key"),
              header(request, "If-Match")),
          201);
    } catch (java.io.IOException | jakarta.servlet.ServletException | IllegalArgumentException e) {
      throw invalid();
    }
  }

  @PostMapping(
      value = "/packages/{packageId}/versions/{versionId}/submissions",
      consumes = "application/json")
  @RequiresPermission("resource.versions.submit")
  @RequiresRecentStepUp
  public ResponseEntity<JsonNode> submit(
      @PathVariable UUID packageId, @PathVariable UUID versionId, HttpServletRequest request) {
    return response(
        workflow.submit(
            actor(),
            packageId,
            versionId,
            read(request, Set.of("reason"), Set.of("reason")),
            header(request, "Idempotency-Key"),
            header(request, "If-Match")),
        201);
  }

  @PostMapping(
      value = "/packages/{packageId}/versions/{versionId}/drafts",
      consumes = "application/json")
  @RequiresPermission(ResourceAccess.UPLOAD)
  @RequiresRecentStepUp
  public ResponseEntity<JsonNode> draft(
      @PathVariable UUID packageId, @PathVariable UUID versionId, HttpServletRequest request) {
    return response(
        workflow.draft(
            actor(),
            packageId,
            versionId,
            read(request, Set.of("reason"), Set.of("reason")),
            header(request, "Idempotency-Key"),
            header(request, "If-Match")),
        200);
  }

  @PostMapping(value = "/admin/reviews/{reviewId}/decisions", consumes = "application/json")
  @RequiresPermission("resource.versions.review")
  @RequiresRecentStepUp
  public ResponseEntity<JsonNode> decide(@PathVariable UUID reviewId, HttpServletRequest request) {
    return response(
        workflow.decide(
            actor(),
            reviewId,
            read(
                request,
                Set.of("decision", "reason", "versionRevision", "digest"),
                Set.of("decision", "reason", "versionRevision", "digest")),
            header(request, "Idempotency-Key"),
            header(request, "If-Match")),
        200);
  }

  @PostMapping(
      value = "/admin/packages/{packageId}/versions/{versionId}/publications",
      consumes = "application/json")
  @RequiresPermission("resource.versions.publish")
  @RequiresRecentStepUp
  public ResponseEntity<JsonNode> publish(
      @PathVariable UUID packageId, @PathVariable UUID versionId, HttpServletRequest request) {
    return response(
        workflow.publish(
            actor(),
            packageId,
            versionId,
            read(request, Set.of("reason"), Set.of("reason")),
            header(request, "Idempotency-Key"),
            header(request, "If-Match"),
            false),
        201);
  }

  @PostMapping(
      value = "/packages/{packageId}/versions/{versionId}/personal-signatures",
      consumes = "application/json")
  @RequiresPermission(ResourceAccess.UPLOAD)
  @RequiresRecentStepUp
  public ResponseEntity<JsonNode> personal(
      @PathVariable UUID packageId, @PathVariable UUID versionId, HttpServletRequest request) {
    return response(
        workflow.publish(
            actor(),
            packageId,
            versionId,
            read(request, Set.of("reason"), Set.of("reason")),
            header(request, "Idempotency-Key"),
            header(request, "If-Match"),
            true),
        201);
  }

  @PostMapping(
      value = "/admin/packages/{packageId}/versions/{versionId}/withdrawals",
      consumes = "application/json")
  @RequiresPermission("resource.versions.publish")
  @RequiresRecentStepUp
  public ResponseEntity<JsonNode> withdraw(
      @PathVariable UUID packageId, @PathVariable UUID versionId, HttpServletRequest request) {
    var result =
        workflow.withdraw(
            actor(),
            packageId,
            versionId,
            read(request, Set.of("mode", "reason"), Set.of("mode", "reason")),
            header(request, "Idempotency-Key"),
            header(request, "If-Match"));
    distributions.propagateNow();
    return response(result, 200);
  }

  @PatchMapping(value = "/admin/packages/{packageId}", consumes = "application/json")
  @RequiresPermission("resource.levels.admin")
  @RequiresRecentStepUp
  public ResponseEntity<JsonNode> govern(@PathVariable UUID packageId, HttpServletRequest request) {
    return response(
        workflow.govern(
            actor(),
            packageId,
            read(
                request,
                Set.of("level", "visibility", "groupIds", "enabled", "defaultVersionId", "reason"),
                Set.of("reason")),
            header(request, "If-Match")),
        200);
  }

  @GetMapping("/admin/review/packages/{packageId}/versions/{versionId}")
  @RequiresPermission("resource.versions.review")
  @RequiresRecentStepUp
  public ResponseEntity<JsonNode> detail(
      @PathVariable UUID packageId, @PathVariable UUID versionId) {
    return response(
        workflow.detail(actor(), packageId, versionId, "resource.versions.review"), 200);
  }

  @GetMapping("/admin/published/packages/{packageId}/versions/{versionId}")
  @RequiresPermission("resource.versions.publish")
  @RequiresRecentStepUp
  public ResponseEntity<JsonNode> publishedDetail(
      @PathVariable UUID packageId, @PathVariable UUID versionId) {
    return response(
        workflow.detail(actor(), packageId, versionId, "resource.versions.publish"), 200);
  }

  @GetMapping("/admin/reviews")
  @RequiresPermission("resource.versions.review")
  @RequiresRecentStepUp
  public ResponseEntity<JsonNode> reviews(
      @RequestParam(defaultValue = "50") int limit, @RequestParam(required = false) String cursor) {
    return response(
        workflow.reviewPage(actor(), queries.governance(actor(), "review", limit, cursor)), 200);
  }

  @GetMapping("/admin/reviews/{reviewId}")
  @RequiresPermission("resource.versions.review")
  @RequiresRecentStepUp
  public ResponseEntity<JsonNode> review(@PathVariable UUID reviewId) {
    return response(workflow.review(actor(), reviewId), 200);
  }

  @GetMapping("/packages/{packageId}/versions/{versionId}/content")
  @RequiresPermission(ResourceAccess.READ)
  public ResponseEntity<InputStreamResource> content(
      @PathVariable UUID packageId, @PathVariable UUID versionId, HttpServletRequest request) {
    return ResponseEntity.ok()
        .cacheControl(CacheControl.noStore())
        .contentType(MediaType.parseMediaType("application/gzip"))
        .header("Content-Disposition", "attachment; filename=\"plugin.tgz\"")
        .header("X-Content-Type-Options", "nosniff")
        .body(
            new InputStreamResource(
                workflow.content(
                    actor(), packageId, versionId, request.getHeader("Range") != null)));
  }
}
