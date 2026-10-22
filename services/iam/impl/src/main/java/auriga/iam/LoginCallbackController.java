package auriga.iam;

import auriga.common.security.PublicEndpoint;
import jakarta.servlet.http.*;
import java.net.URI;
import java.time.*;
import java.util.*;
import org.springframework.http.*;
import org.springframework.web.bind.annotation.*;

@RestController
@org.springframework.boot.autoconfigure.condition.ConditionalOnExpression(
    "${auriga.services.iam.enabled:true}")
@org.springframework.boot.autoconfigure.condition.ConditionalOnProperty(
    prefix = "auriga.iam",
    name = "issuer")
@RequestMapping("/api/iam/v1/login/callback")
public class LoginCallbackController {
  private final LoginManager login;
  private final RequestIdentity requests;
  private final IamStore store;

  public LoginCallbackController(LoginManager login, RequestIdentity requests, IamStore store) {
    this.login = login;
    this.requests = requests;
    this.store = store;
  }

  @PostMapping(value = "/{providerId}", consumes = MediaType.APPLICATION_FORM_URLENCODED_VALUE)
  @PublicEndpoint
  public ResponseEntity<Void> post(
      @PathVariable UUID providerId,
      @RequestParam Map<String, String> params,
      HttpServletRequest request) {
    return requests.call(
        request,
        () -> {
          // No connector currently registers a form-POST callback protocol.
          throw new auriga.common.error.AurigaException(auriga.common.error.CommonErrors.FORBIDDEN);
        });
  }

  @GetMapping("/{providerId}")
  @PublicEndpoint
  public ResponseEntity<Void> get(
      @PathVariable UUID providerId,
      @RequestParam Map<String, String> input,
      HttpServletRequest request,
      HttpServletResponse response) {
    return requests.call(
        request,
        () -> {
          if (input.containsKey("resume"))
            throw new auriga.common.error.AurigaException(
                auriga.common.error.CommonErrors.NOT_FOUND);
          LoginManager.Result result =
              login.callbackResponse(
                  providerId.toString(),
                  requests.binding(request),
                  input,
                  requests.host(request).audience(),
                  requests.origin(request));
          requests.issued(request, response, result);
          String target =
              result.transaction().path("status").asString().equals("completed")
                  ? result.transaction().path("step").path("redirectTo").asString()
                  : "/login?tx=" + result.transaction().path("id").asString();
          return ResponseEntity.status(302)
              .location(URI.create(target))
              .cacheControl(CacheControl.noStore())
              .build();
        });
  }
}
