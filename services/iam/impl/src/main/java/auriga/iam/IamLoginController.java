package auriga.iam;

import auriga.common.security.*;
import auriga.iam.web.model.CreateLoginTransaction;
import jakarta.servlet.http.*;
import java.util.*;
import org.springframework.web.bind.annotation.*;
import org.springframework.web.servlet.mvc.method.annotation.SseEmitter;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

@RestController
@org.springframework.boot.autoconfigure.condition.ConditionalOnExpression(
    "${auriga.services.iam.enabled:true}")
@org.springframework.boot.autoconfigure.condition.ConditionalOnProperty(
    prefix = "auriga.iam",
    name = "issuer")
@RequestMapping("/api/iam/v1")
public class IamLoginController implements org.springframework.beans.factory.DisposableBean {
  private final java.util.concurrent.ExecutorService streams =
      java.util.concurrent.Executors.newVirtualThreadPerTaskExecutor();
  private final LoginManager login;
  private final RequestIdentity requests;
  private final IamStore store;
  private final SessionManager sessions;
  private final QrChallenges qr;
  private final Secrets secrets;

  public IamLoginController(
      LoginManager login,
      RequestIdentity requests,
      IamStore store,
      SessionManager sessions,
      QrChallenges qr,
      Secrets secrets) {
    this.login = login;
    this.requests = requests;
    this.store = store;
    this.sessions = sessions;
    this.qr = qr;
    this.secrets = secrets;
  }

  @GetMapping("/login/providers")
  @PublicEndpoint
  public ObjectNode providers(HttpServletRequest request) {
    return requests.call(
        request,
        () ->
            store.tx(
                () ->
                    Json.object(
                        "items",
                        store.all("idp_provider").stream()
                            .filter(p -> p.path("enabled").asBoolean())
                            .map(
                                p ->
                                    Json.object(
                                        "id",
                                        p.path("providerId").asString(),
                                        "connectorType",
                                        p.path("connectorType").asString(),
                                        "displayName",
                                        p.path("display").path("name"),
                                        "order",
                                        p.path("display").path("order").asInt(100),
                                        "primaryStep",
                                        primaryStep(p)))
                            .toList())));
  }

  private String primaryStep(ObjectNode provider) {
    if (!provider.path("connectorType").asString().equals("team-account")) return "form";
    try {
      ObjectNode cfg =
          Json.read(
              secrets.decrypt(
                  provider.path("config").asString(),
                  "provider:"
                      + auriga.common.context.TenantContext.require()
                      + ":"
                      + provider.path("providerId").asString()));
      String step = cfg.path("primaryStep").asString("form");
      return Set.of("form", "qr", "webauthn").contains(step) ? step : "form";
    } catch (RuntimeException e) {
      return "form";
    }
  }

  @PostMapping(value = "/login/transactions", consumes = "application/json")
  @ResponseStatus(org.springframework.http.HttpStatus.CREATED)
  @PublicEndpoint
  public ObjectNode begin(
      @RequestBody CreateLoginTransaction body,
      HttpServletRequest request,
      HttpServletResponse response) {
    return requests.call(
        request,
        () ->
            store.tx(
                () -> {
                  requests.sameOrigin(request);
                  if (body.getPurpose() != null && !body.getPurpose().toString().equals("login"))
                    requests.browserSession(request, sessions, true);
                  LoginManager.Result result =
                      login.begin(
                          Json.MAPPER.valueToTree(body),
                          requests.host(request).audience(),
                          requests.clientIp(request),
                          request.getHeader("User-Agent"),
                          requests.origin(request),
                          requests.binding(request));
                  requests.issued(request, response, result);
                  return result.transaction();
                }));
  }

  @GetMapping("/login/transactions/{id}")
  @PublicEndpoint
  public ObjectNode get(@PathVariable String id, HttpServletRequest request) {
    return requests.call(
        request,
        () ->
            store.tx(
                () -> {
                  login.checkEntry(
                      id,
                      requests.binding(request),
                      requests.host(request).audience(),
                      requests.origin(request));
                  return login.get(id, requests.binding(request)).transaction();
                }));
  }

  @DeleteMapping("/login/transactions/{id}")
  @ResponseStatus(org.springframework.http.HttpStatus.NO_CONTENT)
  @PublicEndpoint
  public void cancel(
      @PathVariable String id, HttpServletRequest request, HttpServletResponse response) {
    requests.call(
        request,
        () ->
            store.tx(
                () -> {
                  flow(id, request, true);
                  login.cancel(id, requests.binding(request));
                  requests.setCookie(
                      request, response, requests.transactionCookie(request), "", true, 0);
                  return null;
                }));
  }

  @PostMapping(value = "/login/transactions/{id}/advance", consumes = "application/json")
  @PublicEndpoint
  public ObjectNode advance(
      @PathVariable String id,
      @RequestBody JsonNode input,
      HttpServletRequest request,
      HttpServletResponse response) {
    return requests.call(
        request,
        () ->
            store.tx(
                () -> {
                  flow(id, request, true);
                  LoginManager.Result result =
                      login.advance(id, requests.binding(request), input, false);
                  requests.issued(request, response, result);
                  return result.transaction();
                }));
  }

  private void flow(String id, HttpServletRequest request, boolean write) {
    var tx =
        login.checkEntry(
            id,
            requests.binding(request),
            requests.host(request).audience(),
            requests.origin(request));
    if (write) {
      var state = login.get(id, requests.binding(request)).transaction().path("status").asString();
      if (!state.equals("pending")) throw IamError.error("login_tx_finished", 409);
    }
    if (write) requests.sameOrigin(request);
    if (!tx.path("purpose").asString().equals("login")) {
      var row = requests.browserSession(request, sessions, write);
      IamError.require(
          row.path("sessionId").asString().equals(tx.path("sessionId").asString())
              && row.path("accountId").asString().equals(tx.path("accountId").asString()),
          "not_found",
          404);
    }
  }

  @GetMapping("/login/transactions/{id}/events")
  @PublicEndpoint
  public SseEmitter events(@PathVariable String id, HttpServletRequest request) {
    ObjectNode initial = get(id, request);
    var host = requests.host(request);
    String origin = requests.origin(request);
    String binding = requests.binding(request);
    SseEmitter emitter = new SseEmitter(310000L);
    var stopped = new java.util.concurrent.atomic.AtomicBoolean(false);
    emitter.onCompletion(() -> stopped.set(true));
    emitter.onTimeout(() -> stopped.set(true));
    emitter.onError(e -> stopped.set(true));
    streams.submit(
        () -> {
          try {
            ObjectNode state = initial;
            String previous = "";
            while (!stopped.get()) {
              String serialized = state.toString();
              if (!serialized.equals(previous)) {
                emitter.send(SseEmitter.event().name("step").data(state));
                previous = serialized;
              }
              if (!state.path("status").asString().equals("pending")) {
                emitter.send(SseEmitter.event().name("end").data("{}"));
                emitter.complete();
                break;
              }
              Thread.sleep(1000);
              state =
                  auriga.common.context.TenantContext.call(
                      host.tenant(),
                      () ->
                          store.tx(
                              () -> {
                                login.checkEntry(id, binding, host.audience(), origin);
                                return login.get(id, binding).transaction();
                              }));
            }
          } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
            emitter.complete();
          } catch (Exception e) {
            emitter.completeWithError(e);
          }
        });
    return emitter;
  }

  @Override
  public void destroy() {
    streams.shutdownNow();
  }

  @PostMapping("/logout")
  @AuthenticatedOnly
  @ResponseStatus(org.springframework.http.HttpStatus.NO_CONTENT)
  public void logout(HttpServletRequest request, HttpServletResponse response) {
    requests.call(
        request,
        () ->
            store.tx(
                () -> {
                  ObjectNode session = requests.browserSession(request, sessions, true);
                  sessions.revokeOne(session, "logout");
                  String binding = requests.binding(request);
                  if (binding != null)
                    for (ObjectNode tx : store.all("login_tx"))
                      if (tx.path("audience").asString().equals(requests.host(request).audience())
                          && Secrets.same(
                              tx.path("bindingHash").asString(), Secrets.hash(binding))) {
                        login.checkEntry(
                            tx.path("id").asString(),
                            binding,
                            requests.host(request).audience(),
                            requests.origin(request));
                        login.cancel(tx.path("id").asString(), binding);
                      }
                  return null;
                }));
    requests.setCookie(
        request,
        response,
        requests.host(request).audience().equals("admin")
            ? "auriga_admin_session"
            : "auriga_session",
        "",
        true,
        0);
    requests.setCookie(request, response, requests.transactionCookie(request), "", true, 0);
    if (!requests.ipAdmin(request))
      requests.setCookie(request, response, "auriga_csrf", "", false, 0);
  }

  @DeleteMapping("/me/sessions/{id}")
  @AuthenticatedOnly
  @ResponseStatus(org.springframework.http.HttpStatus.NO_CONTENT)
  public void revoke(@PathVariable String id) {
    var principal = AuthorizationRules.currentPrincipal().orElseThrow();
    store.tx(
        () -> {
          ObjectNode row = sessions.activeById(id);
          IamError.require(
              row != null && row.path("accountId").asString().equals(principal.subject()),
              "not_found",
              404);
          sessions.revokeOne(row, "user");
          return null;
        });
  }

  @GetMapping("/login/qr/{challengeId}")
  @AuthenticatedOnly
  public ObjectNode qrChallenge(@PathVariable String challengeId) {
    var principal = AuthorizationRules.currentPrincipal().orElseThrow();
    return store.tx(
        () -> {
          ObjectNode view = qr.get(challengeId);
          ObjectNode row = qr.row(challengeId, true);
          String purpose = row.path("purpose").asString("login");
          if (!purpose.equals("login"))
            IamError.require(
                principal.subject().equals(row.path("accountId").asString("")), "forbidden", 403);
          return view;
        });
  }

  @PostMapping("/login/qr/{challengeId}/confirm")
  @AuthenticatedOnly
  @ResponseStatus(org.springframework.http.HttpStatus.NO_CONTENT)
  public void confirmQr(@PathVariable String challengeId, @RequestBody JsonNode body) {
    var principal = AuthorizationRules.currentPrincipal().orElseThrow();
    String failure =
        store.tx(
            () -> {
              ObjectNode session = sessions.activeById(principal.sessionId());
              IamError.require(
                  session != null
                      && session.path("mfa").asBoolean()
                      && session.path("accountId").asString().equals(principal.subject()),
                  "step_up_required",
                  403);
              ObjectNode row = qr.row(challengeId, true);
              IamError.require(row != null, "not_found", 404);
              String purposeAccount =
                  row.path("purpose").asString("login").equals("login")
                      ? null
                      : row.path("accountId").asString(null);
              try {
                qr.confirm(
                    challengeId,
                    body.path("matchCode").asString(""),
                    principal.subject(),
                    purposeAccount);
              } catch (auriga.common.error.AurigaException error) {
                if (error.errorCode().code().equals("iam.qr_match_mismatch"))
                  return "qr_match_mismatch";
                throw error;
              }
              return null;
            });
    // Throw after commit: a wrong match code permanently consumes the challenge.
    if (failure != null) throw IamError.error(failure, 409);
  }

  @PostMapping("/login/qr/{challengeId}/reject")
  @AuthenticatedOnly
  @ResponseStatus(org.springframework.http.HttpStatus.NO_CONTENT)
  public void rejectQr(@PathVariable String challengeId) {
    var principal = AuthorizationRules.currentPrincipal().orElseThrow();
    store.tx(
        () -> {
          qr.reject(challengeId, principal.subject());
          return null;
        });
  }
}
