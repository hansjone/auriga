package io.github.hansjone.auriga.iam;

import io.github.hansjone.auriga.iam.api.IdentityConnector;
import java.util.List;
import java.util.Locale;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/**
 * Built-in team-account connector. Credential checks go through {@link TeamAccounts} / {@link
 * TeamPasskeys} / {@link QrChallenges} (same process); the connector never opens its own
 * persistence connection.
 */
public final class TeamAccountConnector implements IdentityConnector {
  private final TeamAccounts accounts;
  private final TeamPasskeys passkeys;
  private final QrChallenges qr;
  private final String portalBase;

  public TeamAccountConnector(
      TeamAccounts accounts, TeamPasskeys passkeys, QrChallenges qr, String portalBase) {
    this.accounts = accounts;
    this.passkeys = passkeys;
    this.qr = qr;
    this.portalBase = portalBase;
  }

  @Override
  public JsonNode descriptor() {
    return Json.object(
        "type",
        "team-account",
        "spiVersion",
        "1",
        "displayName",
        Json.object("en", "Team account", "zh-CN", "团队账号"),
        "description",
        Json.object(
            "en",
            "Invite registration, password + TOTP, Passkey, and phone QR confirmation.",
            "zh-CN",
            "邀请注册、口令 + TOTP、Passkey、手机扫码确认。"),
        "steps",
        List.of("form", "qr", "webauthn"),
        "configSchema",
        Json.object(
            "type",
            "object",
            "additionalProperties",
            false,
            "required",
            List.of("rpId", "origins"),
            "properties",
            Json.object(
                "rpId",
                Json.object(
                    "type",
                    "string",
                    "minLength",
                    1,
                    "description",
                    "WebAuthn RP ID (hostname or IP)."),
                "origins",
                Json.object(
                    "type",
                    "array",
                    "minItems",
                    1,
                    "items",
                    Json.object("type", "string"),
                    "description",
                    "Allowed WebAuthn origins, e.g. https://portal.example.com"),
                "primaryStep",
                Json.object(
                    "type",
                    "string",
                    "enum",
                    List.of("form", "qr", "webauthn"),
                    "default",
                    "form"))),
        "multipleInstances",
        false);
  }

  @Override
  public void validateConfig(JsonNode config) {
    IamError.require(
        !config.path("rpId").asString("").isBlank()
            && config.path("origins").isArray()
            && config.path("origins").size() > 0,
        "invalid_config",
        400);
  }

  @Override
  public JsonNode begin(JsonNode context, JsonNode config) {
    boolean canRegister = context.path("state").path("canRegister").asBoolean(false);
    if (canRegister) return result(registerForm(), Json.object("phase", "register"));
    String primary = config.path("primaryStep").asString("form");
    return switch (primary) {
      case "qr" -> beginQr(context, config);
      case "webauthn" -> beginWebAuthn(context, config, "");
      default -> result(passwordForm(null), Json.object("phase", "password"));
    };
  }

  @Override
  public JsonNode advance(JsonNode context, JsonNode config, JsonNode input) {
    ObjectNode state =
        context.path("state").isObject()
            ? ((ObjectNode) context.path("state")).deepCopy()
            : Json.object();
    String phase = state.path("phase").asString("password");
    String type = input.path("type").asString();
    return switch (type) {
      case "form" -> advanceForm(context, config, input, state, phase);
      case "poll" -> advancePoll(context, config, state);
      case "webauthn" -> advanceWebAuthn(context, config, input, state);
      default -> fail("iam.invalid_credentials");
    };
  }

  private JsonNode advanceForm(
      JsonNode context, JsonNode config, JsonNode input, ObjectNode state, String phase) {
    String formId = input.path("formId").asString();
    JsonNode values = input.path("values");
    String providerId = context.path("providerId").asString();
    if (phase.equals("register") && formId.equals("register")) {
      String username = values.path("username").asString("");
      String password = values.path("password").asString("");
      String displayName = values.path("displayName").asString(username);
      accounts.register(providerId, username, password, displayName);
      return done(
          accounts.profile(
              Json.object(
                  "username", username.toLowerCase(Locale.ROOT), "displayName", displayName),
              false));
    }
    if (phase.equals("password") && formId.equals("password")) {
      ObjectNode row =
          accounts.authenticatePassword(
              providerId,
              values.path("username").asString(""),
              values.path("password").asString(""));
      UUIDOrEmpty account = accountOf(providerId, row.path("username").asString());
      if (account.id != null && accounts.hasTotp(account.id)) {
        state.put("phase", "totp");
        state.put("username", row.path("username").asString());
        state.put("displayName", row.path("displayName").asString());
        return result(totpForm(), state);
      }
      return done(accounts.profile(row, false));
    }
    if (phase.equals("totp") && formId.equals("totp")) {
      String username = state.path("username").asString();
      UUIDOrEmpty account = accountOf(providerId, username);
      IamError.require(account.id != null, "invalid_credentials", 401);
      IamError.require(
          accounts.verifyTotp(account.id, values.path("code").asString("")),
          "invalid_credentials",
          401);
      return done(
          accounts.profile(
              Json.object(
                  "username", username, "displayName", state.path("displayName").asString()),
              true));
    }
    return fail("iam.invalid_credentials");
  }

  private JsonNode advancePoll(JsonNode context, JsonNode config, ObjectNode state) {
    String challengeId = state.path("challengeId").asString("");
    ObjectNode row = qr.row(challengeId, false);
    if (row == null) return fail("iam.login_tx_expired");
    String status = row.path("status").asString();
    if (status.equals("confirmed")) {
      String accountId = row.path("confirmedAccountId").asString();
      ObjectNode identity =
          accounts.identityForAccount(context.path("providerId").asString(), accountId);
      IamError.require(identity != null, "invalid_credentials", 401);
      return done(
          Json.object(
              "subject",
              identity.path("subject").asString(),
              "displayName",
              identity.path("profile").path("displayName").asString(""),
              "mfaPerformed",
              true,
              "authTime",
              java.time.Instant.now().toString()));
    }
    if (status.equals("rejected") || status.equals("failed"))
      return fail(status.equals("failed") ? "iam.qr_match_mismatch" : "iam.external_denied");
    ObjectNode step =
        Json.object(
            "type",
            "qr",
            "content",
            state.path("content").asString(),
            "matchCode",
            row.path("matchCode").asString(),
            "status",
            status.equals("scanned") ? "scanned" : "waiting",
            "expiresAt",
            row.path("expiresAt").asString(),
            "pollIntervalMs",
            2000);
    return result(step, state);
  }

  private JsonNode advanceWebAuthn(
      JsonNode context, JsonNode config, JsonNode input, ObjectNode state) {
    String challengeId = state.path("challengeId").asString();
    ObjectNode profile = passkeys.finishAssertion(challengeId, input.path("credential"), config);
    return done(profile);
  }

  private JsonNode beginQr(JsonNode context, JsonNode config) {
    ObjectNode created =
        qr.create(
            context.path("txId").asString(),
            context.path("providerId").asString(),
            context.path("purpose").asString("login"),
            context.path("clientIp").asString(""),
            context.path("userAgent").asString(""),
            portalBase);
    ObjectNode step =
        Json.object(
            "type",
            "qr",
            "content",
            created.path("content").asString(),
            "matchCode",
            created.path("matchCode").asString(),
            "status",
            "waiting",
            "expiresAt",
            created.path("expiresAt").asString(),
            "pollIntervalMs",
            2000);
    return result(
        step,
        Json.object(
            "phase",
            "qr",
            "challengeId",
            created.path("challengeId").asString(),
            "content",
            created.path("content").asString()));
  }

  private JsonNode beginWebAuthn(JsonNode context, JsonNode config, String username) {
    return passkeys.beginAssertion(context.path("providerId").asString(), username, config);
  }

  private UUIDOrEmpty accountOf(String providerId, String username) {
    java.util.UUID id = accounts.accountIdFor(providerId, username);
    return new UUIDOrEmpty(id);
  }

  private static ObjectNode passwordForm(ObjectNode error) {
    ObjectNode form =
        Json.object(
            "type",
            "form",
            "formId",
            "password",
            "title",
            Json.object("en", "Sign in", "zh-CN", "登录"),
            "fields",
            List.of(
                Json.object(
                    "name",
                    "username",
                    "kind",
                    "text",
                    "label",
                    Json.object("en", "Username", "zh-CN", "用户名"),
                    "required",
                    true,
                    "autocomplete",
                    "username"),
                Json.object(
                    "name",
                    "password",
                    "kind",
                    "password",
                    "label",
                    Json.object("en", "Password", "zh-CN", "口令"),
                    "required",
                    true,
                    "autocomplete",
                    "current-password")),
            "submitLabel",
            Json.object("en", "Continue", "zh-CN", "继续"));
    if (error != null) form.set("error", error);
    return form;
  }

  private static ObjectNode registerForm() {
    return Json.object(
        "type",
        "form",
        "formId",
        "register",
        "title",
        Json.object("en", "Create account", "zh-CN", "创建账号"),
        "fields",
        List.of(
            Json.object(
                "name",
                "username",
                "kind",
                "text",
                "label",
                Json.object("en", "Username", "zh-CN", "用户名"),
                "required",
                true,
                "minLength",
                3,
                "maxLength",
                64),
            Json.object(
                "name",
                "displayName",
                "kind",
                "text",
                "label",
                Json.object("en", "Display name", "zh-CN", "显示名"),
                "required",
                false,
                "maxLength",
                64),
            Json.object(
                "name",
                "password",
                "kind",
                "password",
                "label",
                Json.object("en", "Password", "zh-CN", "口令"),
                "required",
                true,
                "minLength",
                10,
                "maxLength",
                256,
                "autocomplete",
                "new-password")),
        "submitLabel",
        Json.object("en", "Register", "zh-CN", "注册"));
  }

  private static ObjectNode totpForm() {
    return Json.object(
        "type",
        "form",
        "formId",
        "totp",
        "title",
        Json.object("en", "Authenticator code", "zh-CN", "验证器代码"),
        "fields",
        List.of(
            Json.object(
                "name",
                "code",
                "kind",
                "otp",
                "label",
                Json.object("en", "Code", "zh-CN", "验证码"),
                "required",
                true,
                "minLength",
                6,
                "maxLength",
                6,
                "pattern",
                "^[0-9]{6}$",
                "autocomplete",
                "one-time-code")),
        "submitLabel",
        Json.object("en", "Verify", "zh-CN", "验证"));
  }

  private static JsonNode result(ObjectNode step, ObjectNode state) {
    return Json.object("step", step, "state", state);
  }

  private static JsonNode done(ObjectNode profile) {
    return Json.object("step", Json.object("type", "done", "profile", profile));
  }

  private static JsonNode fail(String code) {
    return Json.object("step", Json.object("type", "fail", "code", code));
  }

  private record UUIDOrEmpty(java.util.UUID id) {}
}
