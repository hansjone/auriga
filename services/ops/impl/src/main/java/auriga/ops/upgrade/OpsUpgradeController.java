package auriga.ops.upgrade;

import auriga.common.error.*;
import auriga.common.security.*;
import org.springframework.web.bind.annotation.*;
import tools.jackson.databind.node.ObjectNode;

@RestController
@org.springframework.boot.autoconfigure.condition.ConditionalOnExpression(
    "${auriga.services.ops.enabled:true} && ${auriga.ops.runtime.enabled:false}")
@AdminOnly
@RequestMapping("/api/workspace/v1/admin/ops/upgrades")
public class OpsUpgradeController {
  private final OpsUpgradeService service;

  public OpsUpgradeController(OpsUpgradeService service) {
    this.service = service;
  }

  public static AurigaPrincipal actor() {
    return AuthorizationRules.currentPrincipal()
        .orElseThrow(() -> new AurigaException(CommonErrors.UNAUTHENTICATED));
  }

  @GetMapping
  @ResponseBody
  @RequiresPermission("ops.upgrades.read")
  public ObjectNode status() {
    return service.status(actor());
  }

  @PostMapping
  @ResponseBody
  @RequiresPermission("ops.upgrades.write")
  @RequiresRecentStepUp
  public ObjectNode register(
      @jakarta.validation.Valid @RequestBody auriga.workspace.web.model.OpsUpgradeInput body,
      @RequestHeader("Idempotency-Key") String key) {
    return service.register(actor(), body.getTargetId(), key);
  }
}
