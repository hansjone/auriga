package auriga.resource.web;

import static auriga.resource.web.ResourceBody.*;
import static auriga.resource.web.ResourceWorkflowController.*;

import auriga.common.security.RequiresPermission;
import auriga.resource.app.ResourceAccess;
import auriga.resource.app.ResourceMembers;
import jakarta.servlet.http.HttpServletRequest;
import java.util.Set;
import java.util.UUID;
import org.springframework.core.io.InputStreamResource;
import org.springframework.http.*;
import org.springframework.web.bind.annotation.*;
import tools.jackson.databind.JsonNode;

@org.springframework.boot.autoconfigure.condition.ConditionalOnExpression(
    "${auriga.services.resource.enabled:true} && ('${auriga.service.schema:}' == 'resource' || ${auriga.platform.combined:false})")
@RestController
@RequestMapping("/api/resource/v1/member")
@RequiresPermission(ResourceAccess.READ)
public class ResourceMemberController {
  private final ResourceMembers members;

  public ResourceMemberController(ResourceMembers members) {
    this.members = members;
  }

  @PostMapping(value = "/download-tickets", consumes = "application/json")
  public ResponseEntity<JsonNode> ticket(HttpServletRequest request) {
    return response(
        members.ticket(
            actor(),
            read(
                request,
                Set.of("clientId", "packageId", "versionId", "digest"),
                Set.of("clientId", "packageId", "versionId", "digest")),
            header(request, "Idempotency-Key")),
        201);
  }

  @PostMapping(value = "/downloads", consumes = "application/json")
  public ResponseEntity<InputStreamResource> download(HttpServletRequest request) {
    // A consumed ticket is never replayed. A disconnect requires a new ticket.
    var key = header(request, "Idempotency-Key");
    if (key == null || !key.matches("[\\x21-\\x7E]{1,128}")) throw invalid();
    var content =
        members.download(
            actor(),
            read(request, Set.of("clientId", "ticket"), Set.of("clientId", "ticket")),
            request.getHeader("Range") != null);
    return ResponseEntity.ok()
        .cacheControl(CacheControl.noStore())
        .contentType(MediaType.parseMediaType("application/gzip"))
        .header("X-Content-Type-Options", "nosniff")
        .body(new InputStreamResource(content));
  }

  @PostMapping(value = "/installations", consumes = "application/json")
  public ResponseEntity<JsonNode> select(HttpServletRequest request) {
    return response(
        members.select(
            actor(),
            read(
                request,
                Set.of("clientId", "packageId", "versionId", "digest", "action"),
                Set.of("clientId", "packageId", "versionId", "digest", "action")),
            header(request, "Idempotency-Key"),
            header(request, "If-Match")),
        202);
  }

  @PostMapping(value = "/installation-reports", consumes = "application/json")
  public ResponseEntity<JsonNode> report(HttpServletRequest request) {
    return response(
        members.report(
            actor(),
            read(
                request,
                Set.of(
                    "clientId",
                    "packageId",
                    "versionId",
                    "digest",
                    "installationRevision",
                    "state"),
                Set.of(
                    "clientId",
                    "packageId",
                    "versionId",
                    "digest",
                    "installationRevision",
                    "state")),
            header(request, "Idempotency-Key")),
        200);
  }

  @GetMapping("/installations")
  public ResponseEntity<JsonNode> list(
      @RequestParam UUID clientId,
      @RequestParam(required = false) UUID afterPackageId,
      @RequestParam(defaultValue = "50") int limit) {
    return response(members.list(actor(), clientId, afterPackageId, limit), 200);
  }
}
