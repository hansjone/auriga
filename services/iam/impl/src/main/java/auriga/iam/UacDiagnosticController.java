package auriga.iam;

import auriga.common.error.*;
import auriga.common.security.*;
import auriga.iam.api.PermissionResolver;
import jakarta.servlet.http.*;
import java.util.*;
import org.springframework.web.bind.annotation.*;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

@RestController
@org.springframework.boot.autoconfigure.condition.ConditionalOnExpression(
    "${auriga.services.iam.enabled:true}")
@org.springframework.boot.autoconfigure.condition.ConditionalOnProperty(
    prefix = "auriga.iam",
    name = "issuer")
@RequestMapping("/api/iam/v1/admin")
@AdminOnly
public class UacDiagnosticController {
  private final IamStore store;
  private final RequestIdentity requests;
  private final SessionManager sessions;
  private final UacDiagnosticSettings settings;
  private final PermissionResolver permissions;
  private final UacDiagnosticBundles bundles;

  UacDiagnosticController(
      IamStore store,
      RequestIdentity requests,
      SessionManager sessions,
      UacDiagnosticSettings settings,
      PermissionResolver permissions,
      UacDiagnosticBundles bundles) {
    this.store = store;
    this.requests = requests;
    this.sessions = sessions;
    this.settings = settings;
    this.permissions = permissions;
    this.bundles = bundles;
  }

  void authorize(HttpServletRequest request, boolean csrf, Set<String> required) {
    var p =
        AuthorizationRules.currentPrincipal()
            .orElseThrow(() -> new AurigaException(CommonErrors.UNAUTHENTICATED));
    if (p.type() != PrincipalType.USER
        || !p.isAdminContext()
        || !requests.host(request).audience().equals("admin"))
      throw new AurigaException(CommonErrors.FORBIDDEN);
    store.tx(() -> requests.browserSession(request, sessions, csrf));
    if (!permissions.resolve(p.tenantId(), p.subject(), "admin").containsAll(required))
      throw new AurigaException(CommonErrors.FORBIDDEN);
  }

  private ObjectNode output(ObjectNode result, HttpServletResponse response) {
    response.setHeader("Cache-Control", "no-store");
    response.setHeader("ETag", UacDiagnosticSettings.etag(result));
    return result;
  }

  @GetMapping("/providers/{providerId}/uac/diagnostics")
  @RequiresPermission("iam.providers.read")
  public ObjectNode get(
      @PathVariable String providerId, HttpServletRequest request, HttpServletResponse response) {
    return requests.call(
        request,
        () -> {
          authorize(request, false, Set.of("iam.providers.read"));
          return output(settings.get(providerId), response);
        });
  }

  @PostMapping(value = "/uac/diagnostic-bundles", produces = "application/zip")
  @RequiresPermission({"iam.providers.read", "audit.events.read", "ops.alerts.read"})
  public org.springframework.http.ResponseEntity<byte[]> download(
      @RequestBody JsonNode input, HttpServletRequest request) {
    return requests.call(
        request,
        () -> {
          var result =
              bundles.build(
                  input,
                  () ->
                      authorize(
                          request,
                          true,
                          Set.of("iam.providers.read", "audit.events.read", "ops.alerts.read")));
          return org.springframework.http.ResponseEntity.ok()
              .header("Cache-Control", "no-store")
              .header("Pragma", "no-cache")
              .header("X-Auriga-Diagnostic-Partial", Boolean.toString(result.partial()))
              .header("Content-Disposition", "attachment; filename=\"" + result.filename() + "\"")
              .contentType(org.springframework.http.MediaType.parseMediaType("application/zip"))
              .contentLength(result.bytes().length)
              .body(result.bytes());
        });
  }

  @PutMapping("/providers/{providerId}/uac/diagnostics")
  @RequiresPermission("iam.providers.write")
  public ObjectNode put(
      @PathVariable String providerId,
      @RequestBody JsonNode input,
      @RequestHeader(value = "If-Match", required = false) String match,
      HttpServletRequest request,
      HttpServletResponse response) {
    return requests.call(
        request,
        () -> {
          authorize(request, true, Set.of("iam.providers.write"));
          return output(
              settings.put(
                  providerId, input, match, () -> requests.browserSession(request, sessions, true)),
              response);
        });
  }
}
