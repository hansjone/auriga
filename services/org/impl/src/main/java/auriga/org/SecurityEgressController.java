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
public class SecurityEgressController {
  private final OrgEgressDistribution distribution;
  private final OrgService org;

  public SecurityEgressController(OrgEgressDistribution distribution, OrgService org) {
    this.distribution = distribution;
    this.org = org;
  }

  @GetMapping("/api/org/v1/admin/security/egress")
  @RequiresPermission("org.policies.read")
  public ObjectNode status() {
    var out = distribution.status();
    var entries = org.securityEntries();
    for (var field : new String[] {"relayPublicUrl", "relayBackendOrigin", "relayDefaultExit"})
      if (entries.has(field)) out.set(field, entries.get(field));
    return out;
  }
}
