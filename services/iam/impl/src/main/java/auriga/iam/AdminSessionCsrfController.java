package auriga.iam;

import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.security.*;
import jakarta.servlet.http.*;
import org.springframework.http.MediaType;
import org.springframework.web.bind.annotation.*;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

@RestController
@org.springframework.boot.autoconfigure.condition.ConditionalOnExpression(
    "${auriga.services.iam.enabled:true}")
@org.springframework.boot.autoconfigure.condition.ConditionalOnProperty(
    prefix = "auriga.iam",
    name = "issuer")
@RequestMapping("/api/iam/v1/admin/session")
public class AdminSessionCsrfController {
  private final RequestIdentity requests;
  private final SessionManager sessions;

  public AdminSessionCsrfController(RequestIdentity requests, SessionManager sessions) {
    this.requests = requests;
    this.sessions = sessions;
  }

  @PostMapping(
      value = "/csrf-tokens",
      consumes = MediaType.APPLICATION_JSON_VALUE,
      produces = MediaType.APPLICATION_JSON_VALUE)
  @AuthenticatedOnly
  @AdminOnly
  public ObjectNode register(
      @RequestBody JsonNode body, HttpServletRequest request, HttpServletResponse response) {
    response.setHeader("Cache-Control", "no-store");
    response.setHeader("X-Content-Type-Options", "nosniff");
    return requests.call(
        request,
        () -> {
          if (!requests.ipAdmin(request)) throw new AurigaException(CommonErrors.FORBIDDEN);
          requests.sameOrigin(request);
          if (!"admin-session-v1"
              .equals(
                  RequestIdentity.single(
                      request, "X-Auriga-CSRF-Bootstrap", CommonErrors.FORBIDDEN)))
            throw new AurigaException(CommonErrors.FORBIDDEN);
          if (!body.isObject() || body.size() != 1 || !body.path("token").isString())
            throw new AurigaException(CommonErrors.VALIDATION_FAILED);
          var principal =
              AuthorizationRules.currentPrincipal()
                  .orElseThrow(() -> new AurigaException(CommonErrors.UNAUTHENTICATED));
          String token = body.path("token").asString();
          try {
            var expiry =
                sessions.registerAdminCsrf(requests.sessionCookie(request), token, principal);
            return Json.object("token", token, "expiresAt", expiry.toString());
          } catch (AurigaException error) {
            if (error.errorCode().equals(CommonErrors.RATE_LIMITED))
              response.setHeader("Retry-After", String.valueOf(error.params().get("retryAfter")));
            throw error;
          } catch (RuntimeException error) {
            throw new AurigaException(
                CommonErrors.UNAVAILABLE, java.util.Map.of(), java.util.List.of(), error);
          }
        });
  }
}
