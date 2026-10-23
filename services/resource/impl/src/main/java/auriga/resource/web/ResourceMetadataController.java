package auriga.resource.web;

import auriga.common.error.*;
import auriga.common.security.*;
import auriga.resource.app.*;
import auriga.resource.web.model.ModelPackage;
import jakarta.servlet.http.HttpServletRequest;
import java.util.UUID;
import org.springframework.boot.autoconfigure.condition.ConditionalOnExpression;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.*;

@RestController
@RequestMapping("/api/resource/v1/packages")
@ConditionalOnExpression(
    "${auriga.services.resource.enabled:true} && ('${auriga.service.schema:}' == 'resource' || ${auriga.platform.combined:false})")
@RequiresPermission(ResourceAccess.UPLOAD)
@RequiresRecentStepUp
public class ResourceMetadataController {
  private final ResourceCommands commands;

  public ResourceMetadataController(ResourceCommands commands) {
    this.commands = commands;
  }

  @PostMapping(consumes = "application/json")
  public ResponseEntity<ModelPackage> create(HttpServletRequest request) {
    var result =
        commands.create(
            actor(), singleHeader(request, "Idempotency-Key"), PackageBody.read(request, true));
    return response(result, 201);
  }

  @PatchMapping(value = "/{packageId}", consumes = "application/json")
  public ResponseEntity<ModelPackage> patch(
      @PathVariable UUID packageId, HttpServletRequest request) {
    var result =
        commands.patch(
            actor(),
            packageId,
            singleHeader(request, "If-Match"),
            PackageBody.read(request, false));
    return response(result, 200);
  }

  private String singleHeader(HttpServletRequest request, String name) {
    var values = request.getHeaders(name);
    if (!values.hasMoreElements()) return null;
    String value = values.nextElement();
    if (values.hasMoreElements())
      throw new AurigaException(
          name.equals("If-Match")
              ? CommonErrors.PRECONDITION_FAILED
              : CommonErrors.VALIDATION_FAILED);
    return value;
  }

  private AurigaPrincipal actor() {
    return AuthorizationRules.currentPrincipal()
        .orElseThrow(() -> new AurigaException(CommonErrors.UNAUTHENTICATED));
  }

  private ResponseEntity<ModelPackage> response(ModelPackage result, int status) {
    return ResponseEntity.status(status)
        .cacheControl(org.springframework.http.CacheControl.noStore())
        .eTag("\"" + result.getRevision() + "\"")
        .body(result);
  }
}
