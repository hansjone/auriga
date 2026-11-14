package auriga.iam;

import auriga.common.error.*;
import auriga.common.security.*;
import jakarta.servlet.http.*;
import org.springframework.web.bind.annotation.*;
import tools.jackson.databind.node.ObjectNode;

@RestController
@org.springframework.boot.autoconfigure.condition.ConditionalOnExpression(
    "${auriga.services.iam.enabled:true}")
@org.springframework.boot.autoconfigure.condition.ConditionalOnProperty(
    prefix = "auriga.iam",
    name = "issuer")
public class StandardDeviceController {
  private final OAuthProvider oauth;
  private final RequestIdentity requests;

  StandardDeviceController(OAuthProvider oauth, RequestIdentity requests) {
    this.oauth = oauth;
    this.requests = requests;
  }

  static String bearer(HttpServletRequest request) {
    String header = RequestIdentity.single(request, "Authorization", CommonErrors.UNAUTHENTICATED);
    if (!header.startsWith("Bearer ")) throw new AurigaException(CommonErrors.UNAUTHENTICATED);
    return header.substring(7);
  }

  @GetMapping("/api/iam/v1/me/device-session")
  @AuthenticatedOnly
  public ObjectNode identity(HttpServletRequest request, HttpServletResponse response) {
    IamError.validate(request.getParameterMap().isEmpty());
    response.setHeader("Cache-Control", "no-store");
    response.setHeader("Pragma", "no-cache");
    return requests.call(request, () -> oauth.standardDevices().identity(bearer(request)));
  }

  @GetMapping("/api/iam/v1/me/device-profile")
  @AuthenticatedOnly
  public ObjectNode profile(HttpServletRequest request, HttpServletResponse response) {
    IamError.validate(request.getParameterMap().isEmpty());
    response.setHeader("Cache-Control", "no-store");
    response.setHeader("Pragma", "no-cache");
    return requests.call(request, () -> oauth.standardDevices().profile(bearer(request)));
  }
}
