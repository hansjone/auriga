package auriga.iam;

import auriga.common.security.*;
import jakarta.servlet.http.*;
import java.nio.charset.StandardCharsets;
import java.util.*;
import org.springframework.http.*;
import org.springframework.web.bind.annotation.*;
import org.springframework.web.util.HtmlUtils;
import tools.jackson.databind.node.ObjectNode;

@RestController
@org.springframework.boot.autoconfigure.condition.ConditionalOnExpression(
    "${auriga.services.iam.enabled:true}")
@org.springframework.boot.autoconfigure.condition.ConditionalOnProperty(
    prefix = "auriga.iam",
    name = "issuer")
public class OAuthController {
  private final OAuthProvider oauth;
  private final SigningKeys keys;
  private final RequestIdentity requests;
  private final IamStore store;
  private final SessionManager sessions;

  public OAuthController(
      OAuthProvider oauth,
      SigningKeys keys,
      RequestIdentity requests,
      IamStore store,
      SessionManager sessions) {
    this.oauth = oauth;
    this.keys = keys;
    this.requests = requests;
    this.store = store;
    this.sessions = sessions;
  }

  @GetMapping("/.well-known/openid-configuration")
  @PublicEndpoint
  public ObjectNode metadata() {
    return oauth.metadata();
  }

  @GetMapping("/oauth2/jwks")
  @PublicEndpoint
  public ResponseEntity<ObjectNode> jwks() {
    return ResponseEntity.ok()
        .cacheControl(CacheControl.maxAge(java.time.Duration.ofSeconds(30)))
        .body(keys.jwks());
  }

  @PostMapping(value = "/oauth2/token", consumes = MediaType.APPLICATION_FORM_URLENCODED_VALUE)
  @PublicEndpoint
  public ResponseEntity<ObjectNode> token(
      @RequestParam Map<String, String> input, HttpServletRequest request) {
    return standard(
        () ->
            requests.call(
                request, () -> sessions.response(() -> oauth.token(credentials(input, request)))));
  }

  @PostMapping(
      value = "/oauth2/device_authorization",
      consumes = MediaType.APPLICATION_FORM_URLENCODED_VALUE)
  @PublicEndpoint
  public ResponseEntity<ObjectNode> device(
      @RequestParam Map<String, String> input, HttpServletRequest request) {
    return standard(
        () -> requests.call(request, () -> oauth.deviceAuthorization(credentials(input, request))));
  }

  @PostMapping(value = "/oauth2/revoke", consumes = MediaType.APPLICATION_FORM_URLENCODED_VALUE)
  @PublicEndpoint
  public ResponseEntity<ObjectNode> revoke(
      @RequestParam Map<String, String> input, HttpServletRequest request) {
    return standard(() -> requests.call(request, () -> oauth.revoke(credentials(input, request))));
  }

  @GetMapping("/oauth2/authorize")
  @PublicEndpoint
  public ResponseEntity<String> consent(
      @RequestParam Map<String, String> input, HttpServletRequest request) {
    if (requests.ipAdmin(request))
      throw new auriga.common.error.AurigaException(auriga.common.error.CommonErrors.FORBIDDEN);
    return requests.call(
        request,
        () ->
            store.tx(
                () -> {
                  ObjectNode session =
                      sessions.activeByCookie(
                          requests.sessionCookie(request), requests.host(request).audience());
                  IamError.require(session != null, "login_required", 401);
                  String csrf =
                      RequestIdentity.cookie(request, requests.cookieName(request, "auriga_csrf"));
                  IamError.require(
                      csrf != null
                          && Secrets.same(session.path("csrfHash").asString(), Secrets.hash(csrf)),
                      "csrf_invalid",
                      403);
                  StringBuilder form =
                      new StringBuilder(
                              "<!doctype html><html lang=\"en\"><meta charset=\"utf-8\"><title>Authorize / 授权</title><h1>Authorize / 授权</h1><p>Client / 客户端: ")
                          .append(HtmlUtils.htmlEscape(input.getOrDefault("client_id", "")))
                          .append("</p><p>Scopes / 权限范围: ")
                          .append(HtmlUtils.htmlEscape(input.getOrDefault("scope", "")))
                          .append("</p><form method=\"post\" action=\"/oauth2/authorize\">");
                  for (String key :
                      List.of(
                          "client_id",
                          "response_type",
                          "redirect_uri",
                          "scope",
                          "state",
                          "nonce",
                          "code_challenge",
                          "code_challenge_method"))
                    if (input.containsKey(key)) hidden(form, key, input.get(key));
                  hidden(form, "csrf_token", csrf);
                  form.append("<button type=\"submit\">Allow / 允许</button></form></html>");
                  return ResponseEntity.ok()
                      .contentType(MediaType.TEXT_HTML)
                      .cacheControl(CacheControl.noStore())
                      .header(
                          "Content-Security-Policy",
                          "default-src 'none'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
                      .header("X-Frame-Options", "DENY")
                      .body(form.toString());
                }));
  }

  private static void hidden(StringBuilder form, String key, String value) {
    form.append("<input type=\"hidden\" name=\"")
        .append(key)
        .append("\" value=\"")
        .append(HtmlUtils.htmlEscape(value))
        .append("\">");
  }

  @PostMapping(value = "/oauth2/authorize", consumes = MediaType.APPLICATION_FORM_URLENCODED_VALUE)
  @PublicEndpoint
  public ResponseEntity<Void> authorize(
      @RequestParam Map<String, String> input, HttpServletRequest request) {
    if (requests.ipAdmin(request))
      throw new auriga.common.error.AurigaException(auriga.common.error.CommonErrors.FORBIDDEN);
    requests.sameOrigin(request);
    return requests.call(
        request,
        () -> {
          store.tx(
              () -> {
                ObjectNode session =
                    sessions.activeByCookie(
                        requests.sessionCookie(request), requests.host(request).audience());
                IamError.require(
                    session != null
                        && Secrets.same(
                            session.path("csrfHash").asString(),
                            Secrets.hash(input.getOrDefault("csrf_token", ""))),
                    "csrf_invalid",
                    403);
                return null;
              });
          return ResponseEntity.status(302)
              .location(
                  java.net.URI.create(
                      oauth.authorize(
                          input,
                          requests.sessionCookie(request),
                          requests.host(request).audience())))
              .cacheControl(CacheControl.noStore())
              .build();
        });
  }

  @GetMapping("/userinfo")
  @PublicEndpoint
  public ResponseEntity<ObjectNode> userinfo(@RequestHeader("Authorization") String authorization) {
    IamError.require(authorization.startsWith("Bearer "), "invalid_token", 401);
    return response(oauth.userinfo(authorization.substring(7)));
  }

  @PostMapping("/api/iam/v1/device/{userCode}/approve")
  @AuthenticatedOnly
  @RequiresRecentStepUp
  public ResponseEntity<Void> approve(@PathVariable String userCode) {
    oauth.decideDevice(
        userCode, AuthorizationRules.currentPrincipal().orElseThrow().sessionId(), true);
    return ResponseEntity.noContent().build();
  }

  @PostMapping("/api/iam/v1/device/{userCode}/deny")
  @AuthenticatedOnly
  public ResponseEntity<Void> deny(@PathVariable String userCode) {
    oauth.decideDevice(
        userCode, AuthorizationRules.currentPrincipal().orElseThrow().sessionId(), false);
    return ResponseEntity.noContent().build();
  }

  private static ResponseEntity<ObjectNode> response(ObjectNode body) {
    return ResponseEntity.status(
            body.has("error")
                ? body.path("error").asString().equals("invalid_client") ? 401 : 400
                : 200)
        .cacheControl(CacheControl.noStore())
        .header("Pragma", "no-cache")
        .body(body);
  }

  private static ResponseEntity<ObjectNode> standard(java.util.function.Supplier<ObjectNode> work) {
    try {
      return response(work.get());
    } catch (auriga.common.error.AurigaException error) {
      String code = error.errorCode().code().replace("iam.", "");
      if (!Set.of(
              "invalid_client", "invalid_scope", "unauthorized_client", "unsupported_grant_type")
          .contains(code)) code = "invalid_request";
      return response(OAuthProvider.error(code));
    }
  }

  private static Map<String, String> credentials(
      Map<String, String> input, HttpServletRequest request) {
    Map<String, String> params = new HashMap<>(input);
    String auth = request.getHeader("Authorization");
    if (auth != null) {
      IamError.require(
          auth.startsWith("Basic ") && !params.containsKey("client_secret"), "invalid_client", 401);
      try {
        String decoded =
            new String(Base64.getDecoder().decode(auth.substring(6)), StandardCharsets.UTF_8);
        int colon = decoded.indexOf(':');
        IamError.require(colon > 0, "invalid_client", 401);
        String id = java.net.URLDecoder.decode(decoded.substring(0, colon), StandardCharsets.UTF_8);
        IamError.require(
            !params.containsKey("client_id") || params.get("client_id").equals(id),
            "invalid_client",
            401);
        params.put("client_id", id);
        params.put(
            "client_secret",
            java.net.URLDecoder.decode(decoded.substring(colon + 1), StandardCharsets.UTF_8));
      } catch (IllegalArgumentException e) {
        throw IamError.error("invalid_client", 401);
      }
    }
    return params;
  }
}
