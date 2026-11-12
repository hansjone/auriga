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
    "${auriga.ops.runtime.enabled:false}")
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
        body.getRevision(),
        body.getRetentionCount() == null ? 7 : body.getRetentionCount(),
        body.getRetentionDays() == null ? 30 : body.getRetentionDays(),
        body.getMinFreeMiB() == null ? 1024 : body.getMinFreeMiB(),
        body.getMinimumSuccesses() == null ? 1 : body.getMinimumSuccesses());
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
  public ResponseEntity<ObjectNode> ticket(
      @PathVariable UUID id,
      @RequestHeader(value = "X-Auriga-CSRF", required = false) String csrf) {
    ObjectNode response = service.ticket(OpsUpgradeController.actor(), id);
    response.put("csrf", csrf == null ? "" : csrf);
    return ResponseEntity.ok().cacheControl(CacheControl.noStore()).body(response);
  }

  @PostMapping(value = "/{id}/download", consumes = MediaType.APPLICATION_FORM_URLENCODED_VALUE)
  @RequiresPermission("ops.backups.download")
  @RequiresRecentStepUp
  public ResponseEntity<InputStreamResource> download(
      @PathVariable UUID id, @RequestParam("ticket") String ticket) {
    String filename = service.filename(OpsUpgradeController.actor(), id);
    return ResponseEntity.ok()
        .cacheControl(CacheControl.noStore())
        .header("Content-Disposition", "attachment; filename=\"" + filename + "\"")
        .header("X-Content-Type-Options", "nosniff")
        .contentType(MediaType.APPLICATION_OCTET_STREAM)
        .body(new InputStreamResource(service.download(OpsUpgradeController.actor(), id, ticket)));
  }

  @DeleteMapping("/{id}")
  @RequiresPermission("ops.backups.write")
  @RequiresRecentStepUp
  public ResponseEntity<Void> delete(@PathVariable UUID id) {
    service.delete(OpsUpgradeController.actor(), id);
    return ResponseEntity.accepted().build();
  }
}
