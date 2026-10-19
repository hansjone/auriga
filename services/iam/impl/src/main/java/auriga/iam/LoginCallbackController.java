package auriga.iam;

import auriga.common.context.TenantContext;
import auriga.common.security.PublicEndpoint;
import jakarta.servlet.http.*;
import java.net.URI;
import java.time.*;
import java.util.*;
import org.springframework.http.*;
import org.springframework.web.bind.annotation.*;
import tools.jackson.databind.node.ObjectNode;

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
  private final Secrets secrets;
  private final Clock clock;

  public LoginCallbackController(
      LoginManager login, RequestIdentity requests, IamStore store, Secrets secrets, Clock clock) {
    this.login = login;
    this.requests = requests;
    this.store = store;
    this.secrets = secrets;
    this.clock = clock;
  }

  @PostMapping(value = "/{providerId}", consumes = MediaType.APPLICATION_FORM_URLENCODED_VALUE)
  @PublicEndpoint
  public ResponseEntity<Void> post(
      @PathVariable UUID providerId,
      @RequestParam Map<String, String> params,
      HttpServletRequest request) {
    return requests.call(
        request,
        () ->
            store.tx(
                () -> {
                  IamError.require(
                      Json.MAPPER.writeValueAsString(params).length() <= 16384,
                      "invalid_callback",
                      400);
                  store.limit("callback-ip:" + requests.clientIp(request), clock.instant(), 30);
                  String resume = Secrets.random();
                  store.insert(
                      "oauth_grant",
                      Secrets.hash(resume),
                      Json.object(
                          "kind",
                          "callback",
                          "providerId",
                          providerId,
                          "params",
                          secrets.encrypt(
                              Json.MAPPER.writeValueAsString(params),
                              "callback:" + TenantContext.require() + ":" + providerId),
                          "used",
                          false,
                          "expiresAt",
                          clock.instant().plusSeconds(300).toString()),
                      clock.instant().plusSeconds(300));
                  return ResponseEntity.status(303)
                      .location(
                          URI.create(
                              "/api/iam/v1/login/callback/" + providerId + "?resume=" + resume))
                      .cacheControl(CacheControl.noStore())
                      .build();
                }));
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
        () ->
            store.tx(
                () -> {
                  Map<String, String> params = input;
                  if (input.containsKey("resume")) {
                    ObjectNode grant =
                        store.get("oauth_grant", Secrets.hash(input.get("resume")), true);
                    IamError.require(
                        grant != null
                            && grant.path("kind").asString().equals("callback")
                            && grant.path("providerId").asString().equals(providerId.toString())
                            && !grant.path("used").asBoolean()
                            && clock
                                .instant()
                                .isBefore(Instant.parse(grant.path("expiresAt").asString())),
                        "not_found",
                        404);
                    ObjectNode saved =
                        Json.read(
                            secrets.decrypt(
                                grant.path("params").asString(),
                                "callback:" + TenantContext.require() + ":" + providerId));
                    params = new HashMap<>();
                    for (var e : saved.properties())
                      params.put(e.getKey(), e.getValue().asString());
                    grant.put("used", true);
                    grant.remove("params");
                    store.save("oauth_grant", grant);
                  }
                  LoginManager.Result result =
                      login.callback(providerId.toString(), requests.binding(request), params);
                  requests.issued(request, response, result);
                  String target =
                      result.transaction().path("status").asString().equals("completed")
                          ? result.transaction().path("step").path("redirectTo").asString()
                          : "/login?tx=" + result.transaction().path("id").asString();
                  return ResponseEntity.status(302)
                      .location(URI.create(target))
                      .cacheControl(CacheControl.noStore())
                      .build();
                }));
  }
}
