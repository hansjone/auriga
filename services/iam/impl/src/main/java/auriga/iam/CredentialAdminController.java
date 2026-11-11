package auriga.iam;

import auriga.common.security.*;
import auriga.iam.web.model.CredentialRevoke;
import java.time.Clock;
import org.springframework.web.bind.annotation.*;
import tools.jackson.databind.node.ObjectNode;

@RestController
@org.springframework.boot.autoconfigure.condition.ConditionalOnExpression(
    "${auriga.services.iam.enabled:true}")
@org.springframework.boot.autoconfigure.condition.ConditionalOnProperty(
    prefix = "auriga.iam",
    name = "issuer")
@RequestMapping("/api/iam/v1/admin/credentials")
@AdminOnly
@RequiresPermission("iam.keys.write")
public class CredentialAdminController {
  private final CredentialInventory inventory;

  public CredentialAdminController(
      IamStore store,
      IamManagedDevices managed,
      Clock clock,
      auriga.common.audit.AuditLogger audit,
      org.springframework.beans.factory.ObjectProvider<auriga.iam.api.CredentialMetadataSource>
          sources,
      org.springframework.beans.factory.ObjectProvider<
              auriga.workspace.api.ManagedCredentialRotation>
          rotations) {
    inventory =
        new CredentialInventory(
            store,
            managed,
            clock,
            audit,
            () -> sources.orderedStream().toList(),
            rotations::getIfAvailable);
  }

  @GetMapping
  public ObjectNode list(
      @RequestParam(required = false) String type,
      @RequestParam(required = false) String status,
      @RequestParam(defaultValue = "false") boolean expiringSoon) {
    return inventory.list(type, status, expiringSoon);
  }

  @PostMapping("/{instanceId}/revoke")
  @RequiresRecentStepUp
  public ObjectNode revoke(
      @PathVariable java.util.UUID instanceId, @RequestBody CredentialRevoke body) {
    IamError.validate(
        Boolean.TRUE.equals(body.getConfirm())
            && body.getVersion() != null
            && body.getVersion() >= 1
            && body.getVersion() <= 9007199254740991L);
    return inventory.revoke(instanceId.toString(), body.getVersion());
  }
}
