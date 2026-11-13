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
@RequestMapping("/api/iam/v1")
public class UacLoginController {
  private final UacFlow flow;
  private final UacSessions uac;
  private final LoginManager login;
  private final RequestIdentity requests;
  private final SessionManager sessions;
  private final IamStore store;

  UacLoginController(
      UacFlow flow,
      UacSessions uac,
      LoginManager login,
      RequestIdentity requests,
      SessionManager sessions,
      IamStore store) {
    this.flow = flow;
    this.uac = uac;
    this.login = login;
    this.requests = requests;
    this.sessions = sessions;
    this.store = store;
  }

  private void noStore(HttpServletResponse response) {
    response.setHeader("Cache-Control", "no-store");
    response.setHeader("Pragma", "no-cache");
  }

  @PostMapping("/login/uac/challenges")
  @PublicEndpoint
  @ResponseStatus(org.springframework.http.HttpStatus.CREATED)
  public ObjectNode create(
      @RequestBody JsonNode body, HttpServletRequest request, HttpServletResponse response) {
    return requests.call(
        request,
        () -> {
          requests.sameOrigin(request);
          noStore(response);
          store.limit("uac-create:" + requests.clientIp(request), java.time.Instant.now(), 30);
          ObjectNode source =
              "link".equals(body.path("purpose").asString())
                  ? store.tx(() -> requests.browserSession(request, sessions, true))
                  : null;
          var created =
              flow.create(
                  body,
                  requests.host(request).audience(),
                  requests.origin(request),
                  requests.clientIp(request),
                  requests.binding(request),
                  source);
          requests.setCookie(
              request, response, requests.transactionCookie(request), created.binding(), true, 120);
          UacCorrelation.set(
              response,
              flow.correlation(
                  created.challenge().path("id").asString(),
                  created.binding(),
                  requests.host(request).audience(),
                  requests.origin(request)));
          return created.challenge();
        });
  }

  @GetMapping("/login/uac/challenges/{id}")
  @PublicEndpoint
  public ObjectNode get(
      @PathVariable String id, HttpServletRequest request, HttpServletResponse response) {
    return requests.call(
        request,
        () -> {
          noStore(response);
          UacCorrelation.set(
              response,
              flow.correlation(
                  id,
                  requests.binding(request),
                  requests.host(request).audience(),
                  requests.origin(request)));
          return flow.status(
              id,
              requests.binding(request),
              requests.host(request).audience(),
              requests.origin(request));
        });
  }

  @DeleteMapping("/login/uac/challenges/{id}")
  @PublicEndpoint
  @ResponseStatus(org.springframework.http.HttpStatus.NO_CONTENT)
  public void cancel(
      @PathVariable String id, HttpServletRequest request, HttpServletResponse response) {
    requests.call(
        request,
        () -> {
          requests.sameOrigin(request);
          noStore(response);
          UacCorrelation.set(
              response,
              flow.correlation(
                  id,
                  requests.binding(request),
                  requests.host(request).audience(),
                  requests.origin(request)));
          flow.cancel(
              id,
              requests.binding(request),
              requests.host(request).audience(),
              requests.origin(request));
          return null;
        });
  }

  @PostMapping("/login/uac/challenges/{id}/complete")
  @PublicEndpoint
  public ObjectNode complete(
      @PathVariable String id, HttpServletRequest request, HttpServletResponse response) {
    return requests.call(
        request,
        () -> {
          requests.sameOrigin(request);
          noStore(response);
          UacCorrelation.set(
              response,
              flow.correlation(
                  id,
                  requests.binding(request),
                  requests.host(request).audience(),
                  requests.origin(request)));
          store.tx(
              () -> {
                ObjectNode link =
                    flow.linkTransaction(
                        id,
                        requests.binding(request),
                        requests.host(request).audience(),
                        requests.origin(request));
                if (link != null) {
                  ObjectNode source = requests.browserSession(request, sessions, true);
                  IamError.require(
                      source != null
                          && source.path("sessionId").equals(link.path("sessionId"))
                          && source.path("accountId").equals(link.path("accountId")),
                      "unauthenticated",
                      401);
                }
                return null;
              });
          var result =
              login.beginResponse(
                  requests.clientIp(request),
                  () ->
                      flow.complete(
                          id,
                          requests.binding(request),
                          requests.host(request).audience(),
                          requests.origin(request)));
          requests.issued(request, response, result);
          String binding = requests.binding(request);
          if (binding != null)
            requests.setCookie(
                request,
                response,
                requests.transactionCookie(request),
                binding,
                true,
                "pending".equals(result.transaction().path("status").asString()) ? 300 : 120);
          String state =
              "pending".equals(result.transaction().path("status").asString())
                  ? "additional_step_required"
                  : "link".equals(result.transaction().path("purpose").asString())
                      ? "linked"
                      : "authenticated";
          return Json.object("transactionId", result.transaction().path("id"), "state", state);
        });
  }

  @PostMapping("/me/uac/credentials")
  @AuthenticatedOnly
  public ObjectNode credentials(@RequestBody JsonNode body, HttpServletResponse response) {
    IamError.validate(body.isObject() && body.isEmpty());
    noStore(response);
    return store.tx(() -> uac.credentials(AuthorizationRules.currentPrincipal().orElse(null)));
  }
}
