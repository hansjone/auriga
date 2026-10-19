package io.github.hansjone.auriga.org;

import io.github.hansjone.auriga.common.security.AuthenticatedOnly;
import io.github.hansjone.auriga.common.security.PublicEndpoint;
import org.springframework.boot.autoconfigure.condition.ConditionalOnExpression;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RestController;
import tools.jackson.databind.node.ObjectNode;

@RestController
@ConditionalOnExpression("${auriga.services.org.enabled:true}")
@RequestMapping("/api/org/v1")
public class OrgPortalController {
  private final OrgService org;

  public OrgPortalController(OrgService org) {
    this.org = org;
  }

  @GetMapping("/me")
  @AuthenticatedOnly
  public ObjectNode me() {
    return org.me();
  }

  @GetMapping("/me/quota")
  @AuthenticatedOnly
  public ObjectNode myQuota() {
    return org.myQuota();
  }

  @GetMapping("/tenant")
  @AuthenticatedOnly
  public ObjectNode tenant() {
    return org.tenantJson(org.getTenant());
  }

  @GetMapping("/invitations/preview/{code}")
  @PublicEndpoint
  public ObjectNode preview(@PathVariable String code) {
    return org.previewInvitation(code);
  }
}
