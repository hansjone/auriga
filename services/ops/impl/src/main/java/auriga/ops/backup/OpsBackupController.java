package auriga.ops.backup;

import auriga.common.security.*;
import auriga.ops.upgrade.OpsUpgradeController;
import java.util.UUID;
import org.springframework.core.io.InputStreamResource;
import org.springframework.http.*;
import org.springframework.web.bind.annotation.*;
import tools.jackson.databind.node.ObjectNode;

@RestController
@org.springframework.boot.autoconfigure.condition.ConditionalOnExpression(
    "${auriga.services.ops.enabled:true} && ${auriga.ops.runtime.enabled:false}")
@AdminOnly
@RequestMapping("/api/workspace/v1/admin/ops/backups")
public class OpsBackupController {
  private final OpsBackupService service;

  public OpsBackupController(OpsBackupService service) {
    this.service = service;
  }

  @GetMapping
  @RequiresPermission("ops.backups.read")
  public ObjectNode status() {
    return service.status(OpsUpgradeController.actor());
  }

  @PutMapping("/plan")
  @RequiresPermission("ops.backups.write")
  @RequiresRecentStepUp
  public ObjectNode plan(
      @jakarta.validation.Valid @RequestBody auriga.workspace.web.model.OpsBackupPlanInput body) {
    return service.configure(
        OpsUpgradeController.actor(),
        body.getEnabled(),
        body.getIntervalHours(),
        body.getRevision());
  }

  @PostMapping
  @RequiresPermission("ops.backups.write")
  @RequiresRecentStepUp
  public ObjectNode trigger(@RequestHeader("Idempotency-Key") String key) {
    return service.trigger(OpsUpgradeController.actor(), key);
  }

  @PostMapping("/{id}/tickets")
  @RequiresPermission("ops.backups.download")
  @RequiresRecentStepUp
  public ResponseEntity<ObjectNode> ticket(@PathVariable UUID id) {
    return ResponseEntity.ok()
        .cacheControl(CacheControl.noStore())
        .body(service.ticket(OpsUpgradeController.actor(), id));
  }

  @GetMapping("/{id}/download")
  @RequiresPermission("ops.backups.download")
  @RequiresRecentStepUp
  public ResponseEntity<InputStreamResource> download(
      @PathVariable UUID id, @RequestHeader("X-Auriga-Download") String ticket) {
    return ResponseEntity.ok()
        .cacheControl(CacheControl.noStore())
        .header("Content-Disposition", "attachment; filename=\"" + id + ".dump\"")
        .header("X-Content-Type-Options", "nosniff")
        .contentType(MediaType.APPLICATION_OCTET_STREAM)
        .body(new InputStreamResource(service.download(OpsUpgradeController.actor(), id, ticket)));
  }
}
