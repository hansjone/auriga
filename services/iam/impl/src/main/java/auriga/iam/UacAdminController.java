package auriga.iam;

import auriga.common.security.*;
import jakarta.servlet.http.*;
import org.springframework.web.bind.annotation.*;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

@RestController
@org.springframework.boot.autoconfigure.condition.ConditionalOnExpression(
    "${auriga.services.iam.enabled:true}")
@org.springframework.boot.autoconfigure.condition.ConditionalOnProperty(
    prefix = "auriga.iam",
    name = "issuer")
@RequestMapping("/api/iam/v1/admin/providers/{providerId}/uac")
@AdminOnly
public class UacAdminController {
  private final UacProviderService providers;
  private final IamStore store;
  private final RequestIdentity requests;
  private final SessionManager sessions;

  UacAdminController(
      UacProviderService providers,
      IamStore store,
      RequestIdentity requests,
      SessionManager sessions) {
    this.providers = providers;
    this.store = store;
    this.requests = requests;
    this.sessions = sessions;
  }

  @GetMapping
  @RequiresPermission("iam.providers.read")
  public ObjectNode get(
      @PathVariable String providerId, HttpServletRequest request, HttpServletResponse response) {
    return requests.call(
        request,
        () -> {
          requests.browserSession(request, sessions, false);
          return store.tx(
              () -> {
                ObjectNode result = providers.get(providerId);
                response.setHeader(
                    "ETag", UacProviderService.etag(providers.row(providerId, false)));
                response.setHeader("Cache-Control", "no-store");
                return result;
              });
        });
  }

  @PutMapping
  @RequiresPermission("iam.providers.write")
  public ObjectNode put(
      @PathVariable String providerId,
      @RequestBody JsonNode body,
      @RequestHeader(value = "If-Match", required = false) String match,
      HttpServletRequest request,
      HttpServletResponse response) {
    return requests.call(
        request,
        () -> {
          requests.browserSession(request, sessions, true);
          return store.tx(
              () -> {
                var result = providers.put(providerId, body, match);
                response.setHeader(
                    "ETag", UacProviderService.etag(providers.row(providerId, false)));
                response.setHeader("Cache-Control", "no-store");
                return result;
              });
        });
  }
}
