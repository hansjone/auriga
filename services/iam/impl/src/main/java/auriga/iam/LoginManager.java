package auriga.iam;

import auriga.common.audit.*;
import auriga.common.context.*;
import auriga.common.error.AurigaException;
import auriga.common.events.EventPublisher;
import auriga.common.persistence.UuidV7;
import auriga.common.security.AuthorizationRules;
import auriga.iam.api.*;
import java.net.*;
import java.time.*;
import java.util.*;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

public final class LoginManager {
  public record Result(
      ObjectNode transaction, String bindingCookie, SessionManager.Issued session) {}

  private final IamStore store;
  private final Secrets secrets;
  private final ContractSchemas schemas;
  private final ConnectorRegistry connectors;
  private final EmergencyAccounts emergency;
  private final TeamAccounts teamAccounts;
  private final SessionManager sessions;
  private final IdentityPolicyHooks hooks;
  private final EventPublisher events;
  private final AuditLogger audit;
  private final Clock clock;
  private final String issuer;
  private OAuthProvider nativeDevices;

  public void nativeDevices(OAuthProvider devices) {
    this.nativeDevices = devices;
  }

  private static void validateNative(JsonNode request, String audience) {
    if (!request.has("nativeConnect")) return;
    IamError.validate(
        request.isObject()
            && "portal".equals(audience)
            && (!request.has("purpose")
                || request.path("purpose").isString()
                    && request.path("purpose").asString().equals("login"))
            && (!request.has("returnTo")
                || request.path("returnTo").isString()
                    && request.path("returnTo").asString().equals("/"))
            && !request.has("invitationCode"));
    request
        .properties()
        .forEach(
            e ->
                IamError.validate(
                    Set.of("providerId", "purpose", "returnTo", "nativeConnect")
                        .contains(e.getKey())));
    JsonNode nativeRequest = request.path("nativeConnect");
    IamError.validate(nativeRequest.isObject());
    nativeRequest
        .properties()
        .forEach(
            e ->
                IamError.validate(
                    Set.of("clientId", "codeChallenge", "codeChallengeMethod", "deviceName")
                        .contains(e.getKey())));
    IamError.validate(
        nativeRequest.path("clientId").isString()
            && !nativeRequest.path("clientId").asString().isBlank()
            && nativeRequest.path("codeChallenge").isString()
            && SessionManager.canonicalCsrf(nativeRequest.path("codeChallenge").asString())
            && nativeRequest.path("codeChallengeMethod").asString().equals("S256")
            && (!nativeRequest.has("deviceName")
                || nativeRequest.path("deviceName").isString()
                    && nativeRequest
                            .path("deviceName")
                            .asString()
                            .codePointCount(0, nativeRequest.path("deviceName").asString().length())
                        <= 64
                    && nativeRequest
                        .path("deviceName")
                        .asString()
                        .codePoints()
                        .noneMatch(Character::isISOControl)));
  }

  private final ScopedValue<Map<String, Integer>> preparedAttempts = ScopedValue.newInstance();

  private record Attempt(String key, int maximum) {}

  public LoginManager(
      IamStore store,
      Secrets secrets,
      ContractSchemas schemas,
      ConnectorRegistry connectors,
      EmergencyAccounts emergency,
      TeamAccounts teamAccounts,
      SessionManager sessions,
      IdentityPolicyHooks hooks,
      EventPublisher events,
      AuditLogger audit,
      Clock clock,
      String issuer) {
    this.store = store;
    this.secrets = secrets;
    this.schemas = schemas;
    this.connectors = connectors;
    this.emergency = emergency;
    this.teamAccounts = teamAccounts;
    this.sessions = sessions;
    this.hooks = hooks;
    this.events = events;
    this.audit = audit;
    this.clock = clock;
    this.issuer = issuer;
  }

  /**
   * Only HTTP login responses own this boundary; existing business transaction APIs stay intact.
   */
  private Result response(
      java.util.function.Supplier<List<Attempt>> preflight,
      java.util.function.Supplier<Result> work) {
    store.requireTransactionReleased();
    var attempts = store.tx(preflight);
    Map<String, Integer> charged = new HashMap<>();
    // Each counter commits independently before the login transaction borrows its connection.
    for (var attempt : attempts) {
      store.limit(attempt.key(), clock.instant(), attempt.maximum());
      charged.put(attempt.key(), attempt.maximum());
    }
    return sessions.withSigningKey(
        () ->
            ScopedValue.where(preparedAttempts, Map.copyOf(charged))
                .call(
                    () -> {
                      Result result = sessions.response(work);
                      if (result.session() != null) sessions.awaitVisible(result.session());
                      return result;
                    }));
  }

  public Result beginResponse(String ip, java.util.function.Supplier<Result> work) {
    return response(() -> List.of(new Attempt("login-ip:" + ip, 30)), work);
  }

  public Result advanceResponse(
      String id,
      String binding,
      String audience,
      String origin,
      JsonNode input,
      java.util.function.Supplier<Result> work) {
    return response(
        () -> {
          var tx = checkEntry(id, binding, audience, origin);
          IamError.require(
              tx.path("status").asString().equals("pending"), "login_tx_finished", 409);
          return attempts(tx, provider(tx.path("providerId").asString()), input);
        },
        work);
  }

  public Result callbackResponse(
      String providerId,
      String binding,
      Map<String, String> params,
      String audience,
      String origin) {
    JsonNode input = Json.object("type", "callback", "params", params);
    return response(
        () -> {
          var tx = callbackTransaction(providerId, params);
          checkEntry(tx.path("id").asString(), binding, audience, origin);
          IamError.require(
              tx.path("status").asString().equals("pending"), "login_tx_finished", 409);
          return attempts(tx, provider(tx.path("providerId").asString()), input);
        },
        () -> callback(providerId, binding, params, audience, origin));
  }

  private void charge(String key, int maximum) {
    if (!preparedAttempts.isBound()) store.limit(key, clock.instant(), maximum);
    else if (!Objects.equals(preparedAttempts.get().get(key), maximum))
      // A concurrently changed flow must not cause a new nested connection acquisition.
      throw new AurigaException(auriga.common.error.CommonErrors.UNAVAILABLE);
  }

  public Result begin(
      JsonNode request,
      String audience,
      String ip,
      String userAgent,
      String origin,
      String previousBinding) {
    validateNative(request, audience);
    return store.tx(
        () -> {
          if (previousBinding != null) {
            store.lock("login-binding:" + audience + ":" + Secrets.hash(previousBinding));
            for (ObjectNode old : store.all("login_tx"))
              if (old.path("audience").asString().equals(audience)
                  && old.path("origin").asString().equals(origin)
                  && Secrets.same(
                      old.path("bindingHash").asString(), Secrets.hash(previousBinding))) {
                ObjectNode current = transaction(old.path("id").asString(), previousBinding);
                IamError.require(!current.path("replaced").asBoolean(), "login_tx_finished", 409);
                cancel(current.path("id").asString(), previousBinding);
                current = transaction(current.path("id").asString(), previousBinding);
                // A binding may produce exactly one successor, even after completion/cancel.
                // This marker and its successor commit or roll back together.
                current.put("replaced", true);
                if (nativeDevices != null) nativeDevices.cancelNative(current);
                store.save("login_tx", current);
              }
          }
          charge("login-ip:" + ip, 30);
          String pid = request.path("providerId").asString();
          ObjectNode provider = provider(pid);
          if (request.has("nativeConnect"))
            IamError.require(
                nativeDevices != null
                    && provider.path("connectorType").asString().equals("team-account")
                    && config(provider).path("primaryStep").asString("form").equals("form"),
                "invalid_config",
                400);
          String purpose = request.path("purpose").asString("login");
          IamError.require(
              Set.of("login", "link", "stepup").contains(purpose), "invalid_purpose", 400);
          String binding = Secrets.random(),
              returnTo = safeReturn(request.path("returnTo").asString("/"));
          ObjectNode tx =
              Json.object(
                  "status",
                  "pending",
                  "purpose",
                  purpose,
                  "providerId",
                  pid,
                  "bindingHash",
                  Secrets.hash(binding),
                  "audience",
                  audience,
                  "origin",
                  origin,
                  "tenantId",
                  TenantContext.require().toString(),
                  "ip",
                  ip,
                  "userAgent",
                  userAgent == null ? "" : userAgent,
                  "returnTo",
                  returnTo,
                  "expiresAt",
                  clock.instant().plusSeconds(300).toString(),
                  "invitationCode",
                  request.path("invitationCode").asString(""));
          if (!purpose.equals("login")) {
            var principal =
                AuthorizationRules.currentPrincipal()
                    .orElseThrow(() -> IamError.error("unauthenticated", 401));
            IamError.require(
                principal.sessionId() != null
                    && principal.tenantId().equals(TenantContext.require()),
                "unauthenticated",
                401);
            ObjectNode active = sessions.activeById(principal.sessionId());
            IamError.require(
                active != null
                    && active.path("accountId").asString().equals(principal.subject())
                    && active.path("audience").asString().equals(audience),
                "unauthenticated",
                401);
            if (purpose.equals("link"))
              IamError.require(
                  principal.steppedUpWithin(Duration.ofMinutes(10), clock.instant()),
                  "step_up_required",
                  403);
            tx.put("accountId", principal.subject());
            tx.put("sessionId", principal.sessionId());
          }
          store.insert("login_tx", UuidV7.next().toString(), tx, clock.instant().plusSeconds(300));
          // Retrieval uses the generated transaction id, with no caller-controlled key.
          tx.put("txId", tx.path("id").asString());
          ObjectNode nativeDevice =
              request.has("nativeConnect")
                  ? nativeDevices.beginNative(tx, request.path("nativeConnect"))
                  : null;
          if (provider.path("connectorType").asString().equals("team-account")
              && !tx.path("invitationCode").asString("").isBlank()) {
            boolean valid =
                hooks.validateInvitation(
                    tx.path("invitationCode").asString(), UUID.fromString(pid));
            IamError.require(valid, "admission_denied", 403);
            tx.put("state", secrets.encrypt(Json.object("canRegister", valid).toString(), aad(tx)));
            store.save("login_tx", tx);
          }
          Result applied;
          try {
            applied =
                store.atomic(
                    () ->
                        apply(
                            tx,
                            provider,
                            connectors.result(
                                connectors
                                    .get(provider.path("connectorType").asString())
                                    .begin(context(tx, provider), config(provider))),
                            false));
          } catch (AurigaException error) {
            fail(tx, provider, error.errorCode().code(), "failed");
            applied = new Result(view(tx, provider), null, null);
          } catch (RuntimeException error) {
            fail(tx, provider, "iam.connector_unavailable", "failed");
            applied = new Result(view(tx, provider), null, null);
          }
          if (nativeDevice != null
              && applied.transaction().path("status").asString().equals("pending"))
            applied.transaction().set("nativeDevice", nativeDevice);
          return new Result(applied.transaction(), binding, applied.session());
        });
  }

  private ObjectNode transaction(String id, String binding) {
    ObjectNode tx = store.get("login_tx", id, true);
    IamError.require(
        tx != null
            && binding != null
            && Secrets.same(tx.path("bindingHash").asString(), Secrets.hash(binding)),
        "not_found",
        404);
    return tx;
  }

  public ObjectNode checkEntry(String id, String binding, String audience, String origin) {
    ObjectNode tx = transaction(id, binding);
    if (!tx.path("audience").asString().equals(audience)
        || !tx.path("origin").asString().equals(origin)
        || !tx.path("tenantId").asString().equals(TenantContext.require().toString()))
      throw new auriga.common.error.AurigaException(auriga.common.error.CommonErrors.NOT_FOUND);
    return tx;
  }

  public Result get(String id, String binding) {
    return store.tx(
        () -> {
          ObjectNode tx = transaction(id, binding);
          if (tx.path("status").asString().equals("pending") && expired(tx))
            fail(tx, provider(tx.path("providerId").asString()), "iam.login_tx_expired", "expired");
          return new Result(view(tx, provider(tx.path("providerId").asString())), null, null);
        });
  }

  public void cancel(String id, String binding) {
    store.tx(
        () -> {
          ObjectNode tx = transaction(id, binding);
          if (nativeDevices != null) nativeDevices.cancelNative(tx);
          if (tx.path("status").asString().equals("pending")) {
            abandonFirstRegistration(tx, provider(tx.path("providerId").asString()));
            tx.put("status", "cancelled");
            tx.remove("firstTotpSecret");
            tx.set("step", Json.object("type", "fail", "code", "iam.login_tx_cancelled"));
            store.save("login_tx", tx);
          }
          return null;
        });
  }

  public Result advance(String id, String binding, JsonNode input, boolean trustedCallback) {
    schemas.validate("identity/steps.schema.json#/$defs/StepInput", input);
    IamError.require(
        trustedCallback || !Set.of("callback", "event").contains(input.path("type").asString()),
        "invalid_step_input",
        400);
    return store.tx(
        () -> {
          ObjectNode tx = transaction(id, binding),
              provider = provider(tx.path("providerId").asString());
          IamError.require(
              tx.path("status").asString().equals("pending"), "login_tx_finished", 409);
          if (expired(tx)) {
            fail(tx, provider, "iam.login_tx_expired", "expired");
            return new Result(view(tx, provider), null, null);
          }
          String expected = tx.path("step").path("type").asString();
          for (var attempt : attempts(tx, provider, input))
            charge(attempt.key(), attempt.maximum());
          IamError.require(
              expected.equals(input.path("type").asString())
                  || expected.equals("redirect") && trustedCallback
                  || Set.of("qr", "device").contains(expected)
                      && input.path("type").asString().equals("poll"),
              "invalid_step_input",
              400);
          if (expected.equals("form"))
            IamError.require(
                tx.path("step").path("formId").asString().equals(input.path("formId").asString()),
                "invalid_step_input",
                400);
          try {
            return store.atomic(
                () -> {
                  if (input.path("formId").asString("").equals("first_totp")) {
                    IamError.require(
                        provider.path("connectorType").asString().equals("team-account")
                            && tx.has("firstTotpSecret")
                            && tx.has("pendingProfile"),
                        "invalid_step_input",
                        400);
                    UUID account = UUID.fromString(tx.path("pendingAccount").asString());
                    if (tx.path("newAccount").asBoolean()) {
                      JsonNode profile =
                          Json.read(secrets.decrypt(tx.path("pendingProfile").asString(), aad(tx)));
                      teamAccounts.lockPendingRegistration(
                          provider.path("providerId").asString(),
                          profile.path("subject").asString(),
                          tx.path("id").asString());
                    }
                    teamAccounts.enrollFirstTotp(
                        account,
                        secrets.decrypt(
                            tx.path("firstTotpSecret").asString(), aad(tx) + ":first-totp"),
                        input.path("values").path("otp").asString(""));
                    tx.remove("firstTotpSecret");
                    for (ObjectNode session : store.all("session"))
                      if (session.path("accountId").asString().equals(account.toString())
                          && !session
                              .path("sessionId")
                              .asString()
                              .equals(tx.path("sessionId").asString(""))
                          && !session.path("revoked").asBoolean())
                        sessions.revokeOne(session, "security");
                    return finish(
                        tx,
                        provider,
                        Json.read(secrets.decrypt(tx.path("pendingProfile").asString(), aad(tx))),
                        true);
                  }
                  if (provider.path("connectorType").asString().equals("team-account")
                      && input.path("formId").asString("").equals("register"))
                    IamError.require(
                        hooks.validateInvitation(
                            tx.path("invitationCode").asString(),
                            UUID.fromString(provider.path("providerId").asString())),
                        "admission_denied",
                        403);
                  if (input.path("formId").asString("").equals("platform_totp")) {
                    UUID pending = UUID.fromString(tx.path("pendingAccount").asString());
                    IamError.require(
                        teamAccounts.verifyMfaInput(pending, input)
                            || !tx.has("nativeGrantHash") && hooks.verifyMfa(pending, input),
                        "invalid_credentials",
                        401);
                    return finish(
                        tx,
                        provider,
                        Json.read(secrets.decrypt(tx.path("pendingProfile").asString(), aad(tx))),
                        true);
                  }
                  JsonNode result;
                  boolean emergencyLogin =
                      provider.path("connectorType").asString().equals("emergency");
                  if (emergencyLogin)
                    result =
                        Json.object(
                            "step",
                            Json.object(
                                "type",
                                "done",
                                "profile",
                                emergency.authenticate(
                                    tx.path("ip").asString(), (ObjectNode) input.path("values"))));
                  else
                    result =
                        connectors
                            .get(provider.path("connectorType").asString())
                            .advance(context(tx, provider), config(provider), input);
                  return apply(tx, provider, connectors.result(result), emergencyLogin);
                });
          } catch (AurigaException e) {
            String code = e.errorCode().code();
            if (provider.path("connectorType").asString().equals("team-account")
                && code.equals("common.validation_failed")) {
              code = "iam.invalid_credentials";
            }
            fail(tx, provider, code, "failed");
            return new Result(view(tx, provider), null, null);
          } catch (RuntimeException e) {
            fail(tx, provider, "iam.connector_unavailable", "failed");
            return new Result(view(tx, provider), null, null);
          }
        });
  }

  private List<Attempt> attempts(ObjectNode tx, ObjectNode provider, JsonNode input) {
    if (input.path("type").asString().equals("poll") || expired(tx)) return List.of();
    String username =
        input.path("values").path("username").asString("").strip().toLowerCase(Locale.ROOT);
    String account = tx.path("pendingAccount").asString("");
    if (account.isEmpty()
        && tx.has("state")
        && provider.path("connectorType").asString().equals("team-account")) {
      JsonNode state = Json.MAPPER.readTree(secrets.decrypt(tx.path("state").asString(), aad(tx)));
      String knownUser = state.path("username").asString("");
      if (!knownUser.isBlank()) {
        UUID knownAccount =
            teamAccounts.accountIdFor(provider.path("providerId").asString(), knownUser);
        if (knownAccount != null) account = knownAccount.toString();
      }
    }
    String bucket =
        !account.isBlank()
            ? "account:" + account
            : tx.path("step").path("type").asString().equals("form") && !username.isBlank()
                ? "user:" + username
                : "tx:" + tx.path("id").asString();
    return List.of(
        new Attempt("login-ip:" + tx.path("ip").asString(), 60),
        new Attempt(
            "login-provider:" + provider.path("providerId").asString() + ":" + bucket,
            account.isBlank() ? 10 : 5));
  }

  private Result apply(ObjectNode tx, ObjectNode provider, JsonNode result, boolean verifiedMfa) {
    JsonNode step = result.path("step");
    if (tx.has("nativeGrantHash"))
      IamError.require(
          Set.of("done", "fail").contains(step.path("type").asString())
              || step.path("type").asString().equals("form")
                  && Set.of("password", "totp").contains(step.path("formId").asString()),
          "invalid_step_input",
          400);
    if (step.path("type").asString().equals("done"))
      return finish(tx, provider, step.path("profile"), verifiedMfa);
    if (step.path("type").asString().equals("fail")) {
      fail(tx, provider, step.path("code").asString(), "failed");
      return new Result(view(tx, provider), null, null);
    }
    if (step.path("type").asString().equals("redirect")) {
      URI uri = URI.create(step.path("url").asString());
      String state =
          step.path("params")
              .path("state")
              .asString(step.path("params").path("RelayState").asString(""));
      if (uri.getRawQuery() != null)
        for (String pair : uri.getRawQuery().split("&"))
          if (pair.startsWith("state="))
            state = URLDecoder.decode(pair.substring(6), java.nio.charset.StandardCharsets.UTF_8);
      IamError.require(!state.isBlank(), "missing_oauth_state", 400);
      tx.put("callbackState", state);
    }
    tx.set("step", step);
    tx.put(
        "state",
        secrets.encrypt(
            result.path("state").isMissingNode() ? "null" : result.path("state").toString(),
            aad(tx)));
    store.save("login_tx", tx);
    return new Result(view(tx, provider), null, null);
  }

  public Result callback(
      String providerId,
      String binding,
      Map<String, String> params,
      String audience,
      String origin) {
    return store.tx(
        () -> {
          ObjectNode tx = callbackTransaction(providerId, params);
          checkEntry(tx.path("id").asString(), binding, audience, origin);
          return advance(
              tx.path("id").asString(),
              binding,
              Json.object("type", "callback", "params", params),
              true);
        });
  }

  private ObjectNode callbackTransaction(String providerId, Map<String, String> params) {
    String state = params.getOrDefault("state", params.getOrDefault("RelayState", ""));
    IamError.require(!state.isEmpty(), "not_found", 404);
    return store.all("login_tx").stream()
        .filter(
            t ->
                t.path("providerId").asString().equals(providerId)
                    && Secrets.same(t.path("callbackState").asString(""), state))
        .findFirst()
        .orElseThrow(() -> IamError.error("not_found", 404));
  }

  private Result finish(ObjectNode tx, ObjectNode provider, JsonNode profile, boolean coreMfa) {
    String pid = provider.path("providerId").asString(),
        subject = profile.path("subject").asString();
    store.lock("identity:" + pid + ":" + subject);
    ObjectNode identity = store.get("identity", pid + ":" + subject, true),
        policy = (ObjectNode) provider.path("admission");
    String purpose = tx.path("purpose").asString();
    String accountId =
        identity == null
            ? tx.path("pendingAccount").asString("")
            : identity.path("accountId").asString();
    if (!purpose.equals("login")) {
      IamError.require(
          accountId.isEmpty() || accountId.equals(tx.path("accountId").asString()),
          "identity_already_linked",
          409);
      accountId = tx.path("accountId").asString();
    }
    if (accountId.isEmpty()) {
      boolean admit =
          policy.path("mode").asString().equals("auto_provision") && autoProvision(policy, profile)
              || policy.path("mode").asString().equals("invite_only")
                  && !tx.path("invitationCode").asString("").isBlank();
      IamError.require(admit, "admission_denied", 403);
      accountId = UuidV7.next().toString();
      tx.put("pendingAccount", accountId);
      tx.put("newAccount", true);
    }
    ObjectNode account = store.get("account", accountId, true);
    if (account == null && tx.path("newAccount").asBoolean()) {
      account =
          Json.object(
              "accountId",
              accountId,
              "status",
              "active",
              "displayName",
              profile.path("displayName").asString(""),
              "createdAt",
              clock.instant().toString(),
              "memberType",
              policy.path("defaultMemberType").asString("member"));
    }
    IamError.require(
        account != null && account.path("status").asString().equals("active"),
        "account_disabled",
        403);
    boolean team = provider.path("connectorType").asString().equals("team-account");
    // Built-in team-account MFA (TOTP / Passkey / QR) is performed by the connector itself.
    boolean mfa =
        coreMfa
            || profile.path("mfaPerformed").asBoolean()
                && (team || policy.path("trustExternalMfa").asBoolean(false));
    boolean teamNeedsPlatformTotp = team && teamAccounts.hasTotp(UUID.fromString(accountId));
    if (!mfa
        && (!policy.path("mfa").asString("new_device").equals("never")
            || purpose.equals("stepup")
            || tx.has("nativeGrantHash"))) {
      tx.put("pendingAccount", accountId);
      tx.put("pendingProfile", secrets.encrypt(profile.toString(), aad(tx)));
      if (team && !teamAccounts.hasMfa(UUID.fromString(accountId))) {
        if (tx.path("newAccount").asBoolean())
          teamAccounts.pendingRegistrationUntil(
              pid,
              subject,
              tx.path("id").asString(),
              Instant.parse(tx.path("expiresAt").asString()));
        tx.put("firstTotpSecret", secrets.encrypt(Totp.generate(), aad(tx) + ":first-totp"));
        tx.set("step", firstTotpStep(null));
        store.save("login_tx", tx);
        return new Result(view(tx, provider), null, null);
      }
      IamError.require(!team || teamNeedsPlatformTotp, "mfa_required", 403);
      tx.set(
          "step",
          Json.object(
              "type",
              "form",
              "formId",
              "platform_totp",
              "fields",
              List.of(Json.object("name", "otp", "kind", "otp", "required", true))));
      store.save("login_tx", tx);
      return new Result(view(tx, provider), null, null);
    }
    if (identity == null) {
      if (tx.path("newAccount").asBoolean() && store.get("account", accountId, false) == null) {
        if (policy.path("mode").asString().equals("invite_only")) {
          ObjectNode inviteProfile =
              profile.isObject() ? ((ObjectNode) profile).deepCopy() : Json.object();
          inviteProfile.put("accountId", accountId);
          boolean redeemed =
              hooks.consumeInvitation(
                  tx.path("invitationCode").asString(), UUID.fromString(pid), inviteProfile);
          if (!redeemed) {
            if (team) teamAccounts.abandonRegistration(pid, subject);
            throw IamError.error("admission_denied", 403);
          }
        }
        store.insert("account", accountId, account, null);
      }
      identity =
          store.insert(
              "identity",
              pid + ":" + subject,
              Json.object(
                  "accountId",
                  accountId,
                  "providerId",
                  pid,
                  "subject",
                  subject,
                  "linkedAt",
                  clock.instant().toString()),
              null);
      Map<String, Object> linked = new LinkedHashMap<>();
      linked.put("accountId", accountId);
      linked.put("identityId", identity.path("id").asString());
      linked.put("providerId", pid);
      linked.put("connectorType", provider.path("connectorType").asString());
      linked.put("actorId", purpose.equals("link") ? accountId : null);
      events.publish("identity.linked", accountId, linked);
      store.insert(
          "account_link_log",
          UuidV7.next().toString(),
          Json.object(
              "identityId",
              identity.path("id").asString(),
              "accountId",
              accountId,
              "action",
              "linked"),
          null);
    }
    identity.set("profile", profile);
    identity.put("lastLoginAt", clock.instant().toString());
    account.put("lastLoginAt", clock.instant().toString());
    store.save("identity", identity);
    account.put(
        "displayName",
        profile.path("displayName").asString(account.path("displayName").asString("")));
    if (profile.path("emailVerified").asBoolean())
      account.put("email", profile.path("email").asString());
    hooks.synchronize(UUID.fromString(accountId), profile, policy.path("attributeMapping"));
    store.save("account", account);
    SessionManager.Issued issued = null;
    if (purpose.equals("login"))
      issued =
          sessions.create(
              UUID.fromString(accountId),
              UUID.fromString(pid),
              tx.path("audience").asString(),
              mfa,
              Duration.ofSeconds(policy.path("sessionTtlSeconds").asLong(604800)),
              Duration.ofSeconds(policy.path("idleTimeoutSeconds").asLong(86400)));
    else {
      ObjectNode active = sessions.activeById(tx.path("sessionId").asString());
      IamError.require(active != null, "unauthenticated", 401);
      if (purpose.equals("stepup")) {
        IamError.require(mfa, "step_up_required", 403);
        sessions.stepUp(active);
      }
    }
    String sid =
        issued == null
            ? tx.path("sessionId").asString()
            : issued.session().path("sessionId").asString();
    if (tx.has("nativeGrantHash")) {
      IamError.require(
          mfa
              && issued != null
              && (coreMfa || tx.path("step").path("formId").asString().equals("totp")),
          "mfa_required",
          403);
      nativeDevices.approveNative(tx, issued.session());
    }
    events.publish(
        "identity.login",
        accountId,
        Json.object(
            "accountId",
            accountId,
            "identityId",
            identity.path("id").asString(),
            "providerId",
            pid,
            "connectorType",
            provider.path("connectorType").asString(),
            "sessionId",
            sid,
            "mfa",
            mfa,
            "client",
            client(tx)));
    audit.record(
        AuditEntry.builder("iam.identity.login")
            .actor(new AuditEntry.Actor("user", accountId))
            .target("account", accountId)
            .detail("connectorType", provider.path("connectorType").asString())
            .build());
    tx.put("status", "completed");
    tx.remove("pendingProfile");
    tx.remove("firstTotpSecret");
    tx.remove("state");
    tx.set("step", Json.object("type", "complete", "redirectTo", tx.path("returnTo").asString()));
    store.save("login_tx", tx);
    return new Result(view(tx, provider), null, issued);
  }

  static boolean autoProvision(JsonNode policy, JsonNode profile) {
    for (JsonNode rule : policy.path("autoProvisionRules")) {
      String attribute = rule.path("attribute").asString();
      if ((attribute.equals("email") || attribute.startsWith("email."))
          && !profile.path("emailVerified").asBoolean()) continue;
      JsonNode actual = profile.at("/" + attribute.replace(".", "/"));
      JsonNode expected = rule.path("value");
      String a = actual.asString(""), b = expected.asString("");
      boolean matched =
          switch (rule.path("operator").asString()) {
            case "equals" -> actual.isString() && a.equals(b);
            case "suffix" -> actual.isString() && !b.isEmpty() && a.endsWith(b);
            case "prefix" -> actual.isString() && !b.isEmpty() && a.startsWith(b);
            case "in" ->
                expected.isArray()
                    && java.util.stream.StreamSupport.stream(expected.spliterator(), false)
                        .anyMatch(v -> v.equals(actual));
            case "contains" ->
                actual.isArray()
                    && java.util.stream.StreamSupport.stream(actual.spliterator(), false)
                        .anyMatch(v -> v.equals(expected));
            default -> false;
          };
      if (matched) return true;
    }
    return false;
  }

  private void fail(ObjectNode tx, ObjectNode provider, String code, String status) {
    if (nativeDevices != null) nativeDevices.cancelNative(tx);
    abandonFirstRegistration(tx, provider);
    tx.put("status", status);
    tx.set("step", Json.object("type", "fail", "code", code));
    tx.remove("state");
    tx.remove("pendingProfile");
    tx.remove("firstTotpSecret");
    store.save("login_tx", tx);
    events.publish(
        "identity.login_failed",
        tx.path("id").asString(),
        Json.object(
            "providerId",
            provider.path("providerId").asString(),
            "connectorType",
            provider.path("connectorType").asString(),
            "accountId",
            null,
            "code",
            code,
            "client",
            client(tx)));
    audit.record(
        AuditEntry.builder("iam.identity.login")
            .target("login_tx", tx.path("id").asString())
            .failure(code)
            .build());
  }

  private void abandonFirstRegistration(ObjectNode tx, ObjectNode provider) {
    if (tx.path("newAccount").asBoolean()
        && tx.path("step").path("formId").asString("").equals("first_totp")
        && tx.has("pendingProfile")
        && provider.path("connectorType").asString().equals("team-account")
        && store.get("account", tx.path("pendingAccount").asString(), false) == null) {
      JsonNode profile = Json.read(secrets.decrypt(tx.path("pendingProfile").asString(), aad(tx)));
      teamAccounts.abandonPendingRegistration(
          provider.path("providerId").asString(),
          profile.path("subject").asString(),
          tx.path("id").asString());
    }
  }

  private ObjectNode client(ObjectNode tx) {
    return Json.object(
        "kind",
        tx.path("audience").asString().equals("admin") ? "admin" : "web",
        "ip",
        tx.path("ip").asString(),
        "userAgent",
        tx.path("userAgent").asString());
  }

  private boolean expired(ObjectNode tx) {
    return !clock.instant().isBefore(Instant.parse(tx.path("expiresAt").asString()));
  }

  ObjectNode provider(String id) {
    ObjectNode p = store.get("idp_provider", id, false);
    IamError.require(p != null && p.path("enabled").asBoolean(), "provider_unavailable", 404);
    return p;
  }

  private JsonNode config(ObjectNode p) {
    return Json.read(
        secrets.decrypt(
            p.path("config").asString(),
            "provider:" + TenantContext.require() + ":" + p.path("providerId").asString()));
  }

  private String aad(ObjectNode tx) {
    return "login:" + TenantContext.require() + ":" + tx.path("id").asString();
  }

  private ObjectNode context(ObjectNode tx, ObjectNode provider) {
    JsonNode state =
        tx.has("state")
            ? Json.MAPPER.readTree(secrets.decrypt(tx.path("state").asString(), aad(tx)))
            : Json.MAPPER.nullNode();
    ObjectNode value =
        Json.object(
            "txId",
            tx.path("id").asString(),
            "tenantId",
            TenantContext.require().toString(),
            "providerId",
            provider.path("providerId").asString(),
            "purpose",
            tx.path("purpose").asString(),
            "locale",
            "en",
            "clientIp",
            tx.path("ip").asString(),
            "userAgent",
            tx.path("userAgent").asString(),
            "callbackUrl",
            tx.path("origin").asString()
                + "/api/iam/v1/login/callback/"
                + provider.path("providerId").asString(),
            "state",
            state,
            "expiresAt",
            tx.path("expiresAt").asString());
    schemas.validate("identity/connector.schema.json#/$defs/LoginContext", value);
    return value;
  }

  private ObjectNode view(ObjectNode tx, ObjectNode p) {
    JsonNode step = tx.path("step");
    if (tx.path("status").asString().equals("pending") && tx.has("firstTotpSecret"))
      step =
          firstTotpStep(
              secrets.decrypt(tx.path("firstTotpSecret").asString(), aad(tx) + ":first-totp"));
    schemas.validate("identity/steps.schema.json#/$defs/ClientStep", step);
    return Json.object(
        "id",
        tx.path("id").asString(),
        "status",
        tx.path("status").asString(),
        "purpose",
        tx.path("purpose").asString(),
        "provider",
        Json.object(
            "id",
            p.path("providerId").asString(),
            "connectorType",
            p.path("connectorType").asString(),
            "displayName",
            p.path("display").path("name")),
        "step",
        step,
        "expiresAt",
        tx.path("expiresAt").asString());
  }

  private ObjectNode firstTotpStep(String secret) {
    var fields = new ArrayList<ObjectNode>();
    if (secret != null)
      fields.add(
          Json.object(
              "name",
              "enrollment",
              "kind",
              "info",
              "label",
              Json.object(
                  "en",
                  "Add this secret to your authenticator: " + secret,
                  "zh-CN",
                  "请将此密钥添加到验证器：" + secret)));
    fields.add(
        Json.object(
            "name",
            "otp",
            "kind",
            "otp",
            "required",
            true,
            "label",
            Json.object("en", "Authenticator code", "zh-CN", "验证器代码"),
            "pattern",
            "^[0-9]{6}$",
            "autocomplete",
            "one-time-code"));
    return Json.object(
        "type",
        "form",
        "formId",
        "first_totp",
        "title",
        Json.object("en", "Set up your first authenticator", "zh-CN", "登记首个验证器"),
        "fields",
        fields);
  }

  static String safeReturn(String path) {
    IamError.require(
        path.length() <= 2048
            && path.startsWith("/")
            && !path.startsWith("//")
            && !path.matches(".*[\\\\\\s\\p{Cntrl}].*")
            && !path.matches("(?i).*(%2f|%5c|%0[ad]|%25).*"),
        "invalid_return_to",
        400);
    IamError.require(
        URI.create(path).normalize().toString().equals(path), "invalid_return_to", 400);
    return path;
  }
}
