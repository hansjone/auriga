package auriga.iam;

import auriga.common.context.TenantContext;
import auriga.common.error.*;
import auriga.common.security.*;
import auriga.contracts.deploymentconfig.DeploymentAccess;
import jakarta.servlet.http.*;
import java.util.*;
import java.util.concurrent.atomic.AtomicReference;
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
public class UacNavigationController {
  private final UacNavigation navigation;
  private final DeploymentAccess access;
  private final Set<String> delegates;
  private final RequestIdentity requests;
  private final LoginManager login;
  private final IamStore store;

  UacNavigationController(
      UacNavigation navigation,
      DeploymentAccess access,
      auriga.common.autoconfigure.AurigaProperties properties,
      RequestIdentity requests,
      LoginManager login,
      IamStore store) {
    this.navigation = navigation;
    this.access = access;
    this.delegates = Set.copyOf(properties.getGrpc().getServer().getTenantDelegates());
    this.requests = requests;
    this.login = login;
    this.store = store;
  }

  private ObjectNode issue(
      JsonNode input, HttpServletRequest request, HttpServletResponse response, boolean userCode) {
    var principal =
        AuthorizationRules.currentPrincipal()
            .orElseThrow(() -> new AurigaException(CommonErrors.UNAUTHENTICATED));
    if (!principal.isInternalService()
        || !principal.subject().equals("svc:edge")
        || !delegates.contains(principal.subject()))
      throw new AurigaException(CommonErrors.FORBIDDEN);
    String header = RequestIdentity.single(request, "auriga-tenant-id", CommonErrors.FORBIDDEN);
    UUID tenant;
    try {
      tenant = UUID.fromString(header);
      if (!tenant.toString().equals(header.toLowerCase(Locale.ROOT)))
        throw new IllegalArgumentException();
    } catch (IllegalArgumentException error) {
      throw new AurigaException(CommonErrors.VALIDATION_FAILED);
    }
    if (!tenant.toString().equals(access.tenantId())
        || tenant.equals(TenantContext.PLATFORM_TENANT))
      throw new AurigaException(CommonErrors.FORBIDDEN);
    if (!access.portalUrl().equals(input.path("origin").asString()))
      throw new AurigaException(CommonErrors.FORBIDDEN);
    response.setHeader("Cache-Control", "no-store");
    response.setHeader("Pragma", "no-cache");
    return TenantContext.call(
        tenant,
        () -> {
          store.tx(
              () -> {
                ObjectNode prior =
                    store.get(
                        "uac_navigation", "request:" + input.path("requestId").asString(), false);
                if (prior != null && prior.has("correlationId"))
                  UacCorrelation.set(response, prior.path("correlationId").asString());
                return null;
              });
          ObjectNode result = navigation.issue(input, userCode);
          String id = result.path("context").path("transactionId").asString();
          store.tx(
              () -> {
                UacCorrelation.set(
                    response, store.get("login_tx", id, false).path("correlationId").asString());
                return null;
              });
          return result;
        });
  }

  @PostMapping("/internal/uac/navigation-proofs")
  @AuthenticatedOnly
  public ObjectNode issuePortal(
      @RequestBody JsonNode input, HttpServletRequest request, HttpServletResponse response) {
    return issue(input, request, response, false);
  }

  @PostMapping("/internal/uac/device-navigation-proofs")
  @AuthenticatedOnly
  public ObjectNode issueDevice(
      @RequestBody JsonNode input, HttpServletRequest request, HttpServletResponse response) {
    return issue(input, request, response, true);
  }

  @PostMapping("/login/uac/sso")
  @PublicEndpoint
  public ObjectNode sso(
      @RequestBody JsonNode input, HttpServletRequest request, HttpServletResponse response) {
    return requests.call(
        request,
        () -> {
          requests.sameOrigin(request);
          if (!requests.host(request).audience().equals("portal"))
            throw new AurigaException(CommonErrors.FORBIDDEN);
          String binding = requests.binding(request), origin = requests.origin(request);
          String correlation = navigation.correlation(input, binding, origin);
          if (correlation != null) UacCorrelation.set(response, correlation);
          String csrf =
              RequestIdentity.cookie(request, requests.cookieName(request, "auriga_csrf"));
          var prepared =
              navigation.prepare(
                  input,
                  binding,
                  csrf,
                  RequestIdentity.single(request, "X-Auriga-CSRF", CommonErrors.FORBIDDEN),
                  RequestIdentity.cookie(
                      request, requests.cookieName(request, UacNavigation.SUPPRESSION_COOKIE)),
                  requests.sessionCookie(request),
                  origin);
          ObjectNode cookies = Json.object();
          if (request.getCookies() != null)
            for (Cookie cookie : request.getCookies()) {
              var values =
                  cookies.has(cookie.getName())
                      ? (tools.jackson.databind.node.ArrayNode) cookies.path(cookie.getName())
                      : Json.MAPPER.createArrayNode();
              values.add(cookie.getValue());
              cookies.set(cookie.getName(), values);
            }
          JsonNode verified = navigation.verify(prepared, cookies);
          AtomicReference<UacNavigation.Completion> outcome = new AtomicReference<>();
          var result =
              login.beginResponse(
                  requests.clientIp(request),
                  () -> {
                    var completed = navigation.finish(prepared, verified, binding);
                    outcome.set(completed);
                    return completed.result();
                  });
          requests.issued(request, response, result);
          requests.setCookie(
              request,
              response,
              requests.transactionCookie(request),
              binding,
              true,
              "pending".equals(result.transaction().path("status").asString()) ? 300 : 120);
          UacCorrelation.set(response, outcome.get().correlation());
          return Json.object(
              "transactionId", result.transaction().path("id"), "state", outcome.get().state());
        });
  }
}
