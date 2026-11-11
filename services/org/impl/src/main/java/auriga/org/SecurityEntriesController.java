package auriga.org;

import auriga.common.security.AdminOnly;
import auriga.common.security.RequiresPermission;
import org.springframework.boot.autoconfigure.condition.ConditionalOnExpression;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RestController;
import tools.jackson.databind.node.ObjectNode;

@RestController
@AdminOnly
@ConditionalOnExpression(
    "${auriga.services.org.enabled:true} && ('${auriga.service.schema:}' == 'org' || ${auriga.platform.combined:false})")
public class SecurityEntriesController {
  private final OrgService org;

  public SecurityEntriesController(OrgService org) {
    this.org = org;
  }

  @GetMapping("/api/org/v1/admin/security/entries")
  @RequiresPermission("org.policies.read")
  public ObjectNode entries() {
    return org.securityEntries();
  }
}
