package auriga.iam;

import auriga.common.context.TenantContext;
import auriga.common.error.*;
import java.time.*;
import java.util.*;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** Browser navigation authorizes one SSO attempt, never device approval or cloud provisioning. */
final class UacNavigation {
  record Prepared(ObjectNode navigation, ObjectNode existing, boolean attempt) {}

  record Completion(LoginManager.Result result, String state, String correlation) {}

  static final String SUPPRESSION_COOKIE = "auriga_portal_sso_suppression";
  private final IamStore store;
  private final UacFlow flow;
  private final UacSessions uac;
  private final UacProviderService providers;
  private final LoginManager login;
  private final Clock clock;
  private final boolean enabled;

  UacNavigation(
      IamStore store,
      UacFlow flow,
      UacSessions uac,
      UacProviderService providers,
      LoginManager login,
      Clock clock,
      String enabled) {
    if (!Set.of("true", "false").contains(enabled))
      throw new IllegalArgumentException(
          "Invalid configuration: auriga.iam.uac.sso-cookie.enabled");
    this.enabled = Boolean.parseBoolean(enabled);
    this.store = store;
    this.flow = flow;
    this.uac = uac;
    this.providers = providers;
    this.login = login;
    this.clock = clock;
  }

  ObjectNode issue(JsonNode input, boolean userCode) {
    IamError.validate(input.isObject());
    return store.tx(
        () -> {
          ObjectNode normalized = ((ObjectNode) input).deepCopy();
          String csrfInput = normalized.path("csrfCookie").asString("");
          normalized.remove("csrfCookie");
          if (!csrfInput.isEmpty()) IamError.validate(SessionManager.canonicalCsrf(csrfInput));
          if (userCode) {
            IamError.validate(
                normalized.path("userCode").isString()
                    && normalized.path("continuation").asString().equals("device")
                    && !normalized.has("deviceGrantId"));
            ObjectNode grant = deviceByCode(normalized.path("userCode").asString());
            normalized.remove("userCode");
            normalized.put("deviceGrantId", grant.path("id").asString());
          }
          new ContractSchemas()
              .validate(
                  "auth/uac-control.schema.json#/$defs/UacNavigationIssueRequest", normalized);
          String request = normalized.path("requestId").asString(),
              provider = normalized.path("providerId").asString(),
              origin = normalized.path("origin").asString();
          store.lock("uac-navigation-request:" + request);
          String fingerprint = Secrets.hash(input.toString());
          ObjectNode prior = store.get("uac_navigation", "request:" + request, true);
          if (prior != null) {
            if (!fingerprint.equals(prior.path("fingerprint").asString())
                || !clock.instant().isBefore(Instant.parse(prior.path("expiresAt").asString())))
              throw new AurigaException(CommonErrors.CONFLICT);
            ObjectNode cached = uac.open(prior.path("response"), provider);
            ObjectNode live =
                store.get(
                    "uac_navigation",
                    Secrets.hash(cached.path("context").path("navigationProof").asString()),
                    false);
            if (live == null
                || live.path("cancelled").asBoolean()
                || live.path("consumed").asBoolean())
              throw new AurigaException(CommonErrors.CONFLICT);
            return cached;
          }
          ObjectNode configured = providers.row(provider, true);
          IamError.require(
              configured.path("enabled").asBoolean()
                  && configured.path("uacConfigured").asBoolean(),
              "provider_unavailable",
              404);
          String continuation = normalized.path("continuation").asString();
          ObjectNode device = null;
          if (continuation.equals("device")) {
            device = store.byId("oauth_grant", normalized.path("deviceGrantId").asString(), true);
            devicePending(device);
            if (device.has("uacNavigationProviderId")
                && !provider.equals(device.path("uacNavigationProviderId").asString()))
              throw new AurigaException(CommonErrors.NOT_FOUND);
            device.put("uacNavigationProviderId", provider);
            store.save("oauth_grant", device);
          }
          String old = normalized.path("transactionCookie").asString("");
          String csrf = null;
          if (!old.isEmpty()) {
            store.lock("uac-binding:portal:" + Secrets.hash(old));
            for (ObjectNode nav : store.all("uac_navigation"))
              if (origin.equals(nav.path("origin").asString())
                  && Secrets.same(nav.path("bindingHash").asString(), Secrets.hash(old))) {
                nav = store.byId("uac_navigation", nav.path("id").asString(), true);
                IamError.require(!nav.path("replaced").asBoolean(), "login_tx_finished", 409);
                nav.put("replaced", true);
                if (SessionManager.canonicalCsrf(csrfInput)
                    && Secrets.same(nav.path("csrfHash").asString(), Secrets.hash(csrfInput))
                    && clock.instant().isBefore(Instant.parse(nav.path("expiresAt").asString())))
                  csrf = csrfInput;
                nav.put("cancelled", true);
                nav.put("consumed", true);
                store.save("uac_navigation", nav);
                ObjectNode previousTx = cancelTransaction(nav.path("transactionId").asString());
                String previousSid =
                    previousTx == null
                        ? nav.path("issuedSessionId").asString()
                        : previousTx
                            .path("uacIssuedSessionId")
                            .asString(nav.path("issuedSessionId").asString());
                if (!previousSid.isBlank()) {
                  ObjectNode issued = store.byId("session", previousSid, true);
                  if (issued != null && !issued.path("revoked").asBoolean()) {
                    if (SessionManager.canonicalCsrf(csrfInput)
                        && Secrets.same(issued.path("csrfHash").asString(), Secrets.hash(csrfInput))
                        && uac.sessions.valid(issued)) csrf = csrfInput;
                    else uac.sessions.revokeOne(issued, "security");
                  }
                }
              }
            for (ObjectNode challenge : store.all("uac_challenge"))
              if (origin.equals(challenge.path("origin").asString())
                  && "portal".equals(challenge.path("audience").asString())
                  && Secrets.same(challenge.path("bindingHash").asString(), Secrets.hash(old))) {
                challenge = store.byId("uac_challenge", challenge.path("id").asString(), true);
                IamError.require(!challenge.path("replaced").asBoolean(), "login_tx_finished", 409);
                challenge.put("replaced", true);
                challenge.put("state", "cancelled");
                challenge.remove("credential");
                store.save("uac_challenge", challenge);
                ObjectNode previousTx =
                    cancelTransaction(challenge.path("transactionId").asString());
                if (previousTx != null && previousTx.has("uacIssuedSessionId")) {
                  ObjectNode issued =
                      store.byId("session", previousTx.path("uacIssuedSessionId").asString(), true);
                  if (issued != null && !issued.path("revoked").asBoolean())
                    uac.sessions.revokeOne(issued, "security");
                }
              }
          }
          if (csrf == null) csrf = Secrets.random();
          String binding = Secrets.random(), proof = Secrets.random();
          Instant expires = clock.instant().plusSeconds(120);
          ObjectNode tx =
              flow.transaction(provider, "login", "portal", origin, "", "", null, binding);
          JsonNode metadata = normalized.path("fetchMetadata");
          boolean auto =
              continuation.equals("portal")
                  && "document".equals(metadata.path("dest").asString())
                  && "navigate".equals(metadata.path("mode").asString())
                  && Set.of("none", "same-origin").contains(metadata.path("site").asString())
                  && "?1".equals(metadata.path("user").asString());
          boolean suppressed =
              suppressed(normalized.path("suppressionCookie").asString(null), origin);
          ObjectNode nav =
              Json.object(
                  "transactionId",
                  tx.path("id"),
                  "providerId",
                  provider,
                  "origin",
                  origin,
                  "audience",
                  "portal",
                  "purpose",
                  "login",
                  "continuation",
                  continuation,
                  "bindingHash",
                  Secrets.hash(binding),
                  "proofHash",
                  Secrets.hash(proof),
                  "csrfHash",
                  Secrets.hash(csrf),
                  "autoEligible",
                  auto,
                  "consumed",
                  false,
                  "cancelled",
                  false,
                  "expiresAt",
                  expires.toString(),
                  "correlationId",
                  tx.path("correlationId"),
                  "providerRevision",
                  UacProviderService.etag(configured));
          nav.set("configRevision", configured.path("revision"));
          if (device != null) nav.put("deviceGrantId", device.path("id").asString());
          store.insert("uac_navigation", Secrets.hash(proof), nav, expires);
          store.uacTenant();
          ObjectNode response =
              Json.object(
                  "context",
                  Json.object(
                      "navigationProof",
                      proof,
                      "expiresAt",
                      expires.toString(),
                      "transactionId",
                      tx.path("id"),
                      "purpose",
                      continuation,
                      "autoEligible",
                      auto,
                      "autoSuppressed",
                      suppressed),
                  "transactionCookie",
                  binding,
                  "csrfCookie",
                  csrf,
                  "ttlSeconds",
                  120);
          store.insert(
              "uac_navigation",
              "request:" + request,
              Json.object(
                  "providerId",
                  provider,
                  "fingerprint",
                  fingerprint,
                  "response",
                  uac.seal(response, provider),
                  "expiresAt",
                  expires.toString(),
                  "correlationId",
                  tx.path("correlationId")),
              expires);
          return response;
        });
  }

  private ObjectNode deviceByCode(String code) {
    if (!code.matches("[A-Z]{4}-?[A-Z]{4}")) throw new AurigaException(CommonErrors.NOT_FOUND);
    String normalized = code.replace("-", "");
    ObjectNode found =
        store.all("oauth_grant").stream()
            .filter(g -> normalized.equals(g.path("userCode").asString().replace("-", "")))
            .findFirst()
            .orElse(null);
    if (found == null) throw new AurigaException(CommonErrors.NOT_FOUND);
    found = store.byId("oauth_grant", found.path("id").asString(), true);
    devicePending(found);
    return found;
  }

  private void devicePending(ObjectNode grant) {
    if (grant == null
        || !"device".equals(grant.path("kind").asString())
        || !"pending".equals(grant.path("status").asString())
        || grant.path("used").asBoolean()
        || grant.path("revoked").asBoolean()
        || "native_tx".equals(grant.path("authorizationMode").asString())
        || !clock.instant().isBefore(Instant.parse(grant.path("expiresAt").asString())))
      throw new AurigaException(CommonErrors.NOT_FOUND);
  }

  private ObjectNode cancelTransaction(String id) {
    ObjectNode tx = store.get("login_tx", id, true);
    if (tx != null && "pending".equals(tx.path("status").asString())) {
      tx.put("status", "cancelled");
      tx.remove("uacCredential");
      tx.remove("pendingProfile");
      tx.set("step", Json.object("type", "fail", "code", "iam.login_tx_cancelled"));
      store.save("login_tx", tx);
    }
    return tx;
  }

  boolean suppressed(String cookie, String origin) {
    if (cookie == null) return false;
    ObjectNode record = store.get("uac_suppression", Secrets.hash(cookie), false);
    // An expired or unknown old cookie cannot restore automatic login eligibility.
    return record == null
        || !origin.equals(record.path("origin").asString())
        || record.path("suppressed").asBoolean(true)
        || !clock.instant().isBefore(Instant.parse(record.path("expiresAt").asString()));
  }

  String correlation(JsonNode input, String binding, String origin) {
    if (!input.path("navigationProof").isString() || binding == null) return null;
    return store.tx(
        () -> {
          ObjectNode row =
              store.get(
                  "uac_navigation", Secrets.hash(input.path("navigationProof").asString()), false);
          return row != null
                  && origin.equals(row.path("origin").asString())
                  && Secrets.same(row.path("bindingHash").asString(), Secrets.hash(binding))
              ? row.path("correlationId").asString()
              : null;
        });
  }

  String suppress(String origin) {
    String cookie = Secrets.random();
    Instant expires = clock.instant().plusSeconds(86400);
    store.insert(
        "uac_suppression",
        Secrets.hash(cookie),
        Json.object(
            "browserSessionId",
            UUID.randomUUID().toString(),
            "tenantId",
            TenantContext.require().toString(),
            "origin",
            origin,
            "suppressed",
            true,
            "revision",
            1,
            "expiresAt",
            expires.toString()),
        expires);
    return cookie;
  }

  Prepared prepare(
      JsonNode input,
      String binding,
      String csrfCookie,
      String csrfHeader,
      String suppression,
      String sessionCookie,
      String origin) {
    IamError.validate(
        input.isObject()
            && input.path("providerId").isString()
            && input.path("navigationProof").isString()
            && SessionManager.canonicalCsrf(input.path("navigationProof").asString())
            && Set.of("auto", "continue").contains(input.path("mode").asString()));
    input
        .propertyNames()
        .forEach(
            name ->
                IamError.validate(
                    Set.of("providerId", "navigationProof", "mode", "invitationCode")
                        .contains(name)));
    IamError.validate(
        !input.has("invitationCode")
            || input.path("invitationCode").isString()
                && input.path("invitationCode").asString().length() <= 512);
    return store.tx(
        () -> {
          ObjectNode nav =
              store.get(
                  "uac_navigation", Secrets.hash(input.path("navigationProof").asString()), true);
          if (nav == null
              || binding == null
              || !Secrets.same(nav.path("bindingHash").asString(), Secrets.hash(binding))
              || !origin.equals(nav.path("origin").asString())
              || !input.path("providerId").equals(nav.path("providerId"))
              || !clock.instant().isBefore(Instant.parse(nav.path("expiresAt").asString()))
              || nav.path("cancelled").asBoolean()
              || !SessionManager.canonicalCsrf(csrfCookie)
              || !Secrets.same(csrfCookie, csrfHeader)
              || !Secrets.same(nav.path("csrfHash").asString(), Secrets.hash(csrfCookie)))
            throw new AurigaException(CommonErrors.FORBIDDEN);
          if (nav.path("consumed").asBoolean()) throw new AurigaException(CommonErrors.CONFLICT);
          nav.put("consumed", true);
          store.save("uac_navigation", nav);
          ObjectNode existing =
              sessionCookie == null ? null : uac.sessions.activeByCookie(sessionCookie, "portal");
          ObjectNode provider = providers.row(nav.path("providerId").asString(), false);
          boolean attempt =
              existing == null
                  && enabled
                  && provider.path("enabled").asBoolean()
                  && UacProviderService.etag(provider)
                      .equals(nav.path("providerRevision").asString())
                  && (!"auto".equals(input.path("mode").asString())
                      || nav.path("autoEligible").asBoolean()
                          && "portal".equals(nav.path("continuation").asString())
                          && !suppressed(suppression, origin));
          nav.put("invitationCode", input.path("invitationCode").asString(""));
          nav.put("eligible", attempt);
          nav.put("suppressed", suppressed(suppression, origin));
          nav.put(
              "fallbackReason",
              !enabled
                  ? "disabled"
                  : nav.path("suppressed").asBoolean()
                          && "auto".equals(input.path("mode").asString())
                      ? "suppressed"
                      : !attempt ? "ineligible" : "none");
          return new Prepared(nav, existing, attempt);
        });
  }

  JsonNode verify(Prepared prepared, JsonNode cookies) {
    store.requireTransactionReleased();
    ObjectNode metadata = prepared.navigation().deepCopy();
    Boolean present = uac.adapter.ssoCookiePresent(cookies);
    if (present != null) metadata.put("cookiePresent", present);
    var check =
        uac.telemetry == null
            ? null
            : uac.telemetry.begin(metadata, "sso_cookie_check", null, Json.object());
    if (!prepared.attempt()) {
      if (check != null) check.end("skipped", metadata);
      return Json.object("success", false);
    }
    JsonNode pair = uac.adapter.ssoCookies(cookies);
    ObjectNode checked =
        Json.object(
            "cookieFamily",
            pair.path("cookieFamily").asString("none"),
            "empNo",
            pair.path("empNo"),
            "tokenHashPrefix",
            UacDiagnosticSanitizer.tokenHashPrefix(pair.path("token").asString(null)));
    if (!pair.has("token") || !pair.has("empNo")) {
      checked.put("fallbackReason", "missing");
      if (check != null) check.end("scan_required", checked);
      return Json.object("success", false);
    }
    ObjectNode config =
        store.tx(
            () ->
                uac.providers.decryptedConfig(
                    providers.row(prepared.navigation().path("providerId").asString(), false)));
    JsonNode result =
        uac.observe(
            metadata,
            "token_verify",
            "login_sso",
            ((ObjectNode) pair).deepCopy().set("systemCode", config.path("systemCode")),
            () -> uac.adapter.verify(config, pair));
    if (!result.path("success").asBoolean()
        || !result.path("token").isString()
        || result.path("token").asString().isBlank()
        || result.path("token").asString().length() > 16384) {
      if (check != null) check.end("scan_required", checked);
      return Json.object("success", false);
    }
    ObjectNode credential = ((ObjectNode) pair).deepCopy();
    credential.set("token", result.path("token"));
    if (check != null) check.end("success", checked);
    return Json.object("success", true, "credential", credential);
  }

  Completion finish(Prepared prepared, JsonNode verified, String binding) {
    return store.tx(
        () -> {
          String providerId = prepared.navigation().path("providerId").asString();
          ObjectNode provider = providers.row(providerId, true);
          ObjectNode nav =
              store.byId("uac_navigation", prepared.navigation().path("id").asString(), true);
          if (nav == null
              || nav.path("finished").asBoolean()
              || nav.path("cancelled").asBoolean()
              || !clock.instant().isBefore(Instant.parse(nav.path("expiresAt").asString())))
            throw new AurigaException(CommonErrors.CONFLICT);
          nav.put("finished", true);
          store.save("uac_navigation", nav);
          ObjectNode tx = store.get("login_tx", nav.path("transactionId").asString(), true);
          if (tx == null || !"pending".equals(tx.path("status").asString()))
            throw new AurigaException(CommonErrors.CONFLICT);
          if (prepared.existing() != null) {
            ObjectNode existing =
                uac.sessions.activeById(prepared.existing().path("sessionId").asString());
            if (existing == null) throw new AurigaException(CommonErrors.UNAUTHENTICATED);
            tx.remove("uacPending");
            tx.put("status", "completed");
            tx.set("step", Json.object("type", "complete", "redirectTo", "/"));
            store.save("login_tx", tx);
            return new Completion(
                login.get(tx.path("id").asString(), binding),
                "authenticated",
                nav.path("correlationId").asString());
          }
          if (!verified.path("success").asBoolean()
              || !provider.path("enabled").asBoolean()
              || !UacProviderService.etag(provider)
                  .equals(nav.path("providerRevision").asString())) {
            tx.remove("uacPending");
            tx.put("status", "failed");
            tx.set("step", Json.object("type", "fail", "code", "common.unavailable"));
            store.save("login_tx", tx);
            return new Completion(
                login.get(tx.path("id").asString(), binding),
                "scan_required",
                nav.path("correlationId").asString());
          }
          tx.set("uacCredential", uac.seal(verified.path("credential"), providerId));
          tx.put("uacVerified", true);
          tx.put("invitationCode", prepared.navigation().path("invitationCode").asString(""));
          store.save("login_tx", tx);
          var result =
              login.finishUac(
                  tx.path("id").asString(),
                  Json.object("subject", verified.path("credential").path("empNo")));
          if (result.session() != null) {
            nav.put("issuedSessionId", result.session().session().path("sessionId").asString());
            store.save("uac_navigation", nav);
          }
          return new Completion(
              result,
              "pending".equals(result.transaction().path("status").asString())
                  ? "additional_step_required"
                  : "authenticated",
              nav.path("correlationId").asString());
        });
  }
}
