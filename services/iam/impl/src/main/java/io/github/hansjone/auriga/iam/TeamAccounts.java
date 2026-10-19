package io.github.hansjone.auriga.iam;

import io.github.hansjone.auriga.common.context.TenantContext;
import io.github.hansjone.auriga.common.events.EventPublisher;
import io.github.hansjone.auriga.common.persistence.UuidV7;
import java.time.Clock;
import java.time.Instant;
import java.util.*;
import java.util.regex.Pattern;
import org.springframework.security.crypto.argon2.Argon2PasswordEncoder;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** Credential store for the built-in team-account connector (Argon2id + TOTP + passkeys). */
public final class TeamAccounts {
  private static final Pattern USERNAME = Pattern.compile("^[A-Za-z0-9._-]{3,64}$");
  private final IamStore store;
  private final Secrets secrets;
  private final Clock clock;
  private final EventPublisher events;
  private final Argon2PasswordEncoder passwords = new Argon2PasswordEncoder(16, 32, 1, 65536, 3);

  public TeamAccounts(IamStore store, Secrets secrets, Clock clock, EventPublisher events) {
    this.store = store;
    this.secrets = secrets;
    this.clock = clock;
    this.events = events;
  }

  static String accountKey(String providerId, String username) {
    return providerId + ":" + username.toLowerCase(Locale.ROOT);
  }

  public void register(String providerId, String username, String password, String displayName) {
    IamError.require(USERNAME.matcher(username).matches(), "invalid_username", 400);
    IamError.require(
        password != null && password.length() >= 10 && password.length() <= 256,
        "invalid_password",
        400);
    String key = accountKey(providerId, username);
    store.lock("team-account:" + key);
    IamError.require(store.get("team_account", key, true) == null, "username_taken", 409);
    store.insert(
        "team_account",
        key,
        Json.object(
            "providerId",
            providerId,
            "username",
            username.toLowerCase(Locale.ROOT),
            "displayName",
            displayName == null || displayName.isBlank() ? username : displayName,
            "passwordHash",
            passwords.encode(password),
            "createdAt",
            clock.instant().toString()),
        null);
  }

  /** Drop credentials if invite redemption failed after the connector already registered them. */
  public void abandonRegistration(String providerId, String username) {
    String key = accountKey(providerId, username);
    if (store.get("team_account", key, false) != null) store.delete("team_account", key);
  }

  public ObjectNode authenticatePassword(String providerId, String username, String password) {
    IamError.require(password != null && password.length() <= 256, "invalid_credentials", 401);
    ObjectNode row = store.get("team_account", accountKey(providerId, username), true);
    IamError.require(
        row != null && passwords.matches(password, row.path("passwordHash").asString()),
        "invalid_credentials",
        401);
    return row;
  }

  public void changePassword(UUID accountId, String providerId, String current, String next) {
    ObjectNode identity =
        store.all("identity").stream()
            .filter(
                i ->
                    i.path("accountId").asString().equals(accountId.toString())
                        && i.path("providerId").asString().equals(providerId))
            .findFirst()
            .orElseThrow(() -> IamError.error("forbidden", 403));
    ObjectNode row = authenticatePassword(providerId, identity.path("subject").asString(), current);
    IamError.require(
        next != null && next.length() >= 10 && next.length() <= 256, "invalid_password", 400);
    row.put("passwordHash", passwords.encode(next));
    store.save("team_account", row);
  }

  public boolean hasTotp(UUID accountId) {
    return store.get("team_totp", accountId.toString(), false) != null;
  }

  public int passkeyCount(UUID accountId) {
    return (int)
        store.all("team_passkey").stream()
            .filter(p -> p.path("accountId").asString().equals(accountId.toString()))
            .count();
  }

  public ObjectNode mfaStatus(UUID accountId, boolean required) {
    return Json.object(
        "totp", hasTotp(accountId), "passkeys", passkeyCount(accountId), "required", required);
  }

  public boolean verifyTotp(UUID accountId, String code) {
    ObjectNode row = store.get("team_totp", accountId.toString(), true);
    if (row == null) return false;
    String secret =
        secrets.decrypt(
            row.path("totp").asString(), "totp:" + TenantContext.require() + ":" + accountId);
    long counter = Totp.verify(secret, code, clock.instant(), row.path("lastCounter").asLong());
    if (counter < 0) return false;
    row.put("lastCounter", counter);
    store.save("team_totp", row);
    return true;
  }

  public ObjectNode beginTotpEnrollment(UUID accountId, String issuerLabel) {
    store.lock("team-totp-enroll:" + accountId);
    String enrollmentId = UuidV7.next().toString();
    String secret = Totp.generate();
    Instant expires = clock.instant().plusSeconds(600);
    store.insert(
        "team_ceremony",
        enrollmentId,
        Json.object(
            "kind",
            "totp_enrollment",
            "accountId",
            accountId.toString(),
            "secret",
            secrets.encrypt(secret, "totp-enroll:" + TenantContext.require() + ":" + enrollmentId),
            "expiresAt",
            expires.toString()),
        expires);
    String uri =
        "otpauth://totp/"
            + url(issuerLabel)
            + ":"
            + url(accountId.toString())
            + "?secret="
            + secret
            + "&issuer="
            + url(issuerLabel)
            + "&digits=6&period=30";
    return Json.object(
        "enrollmentId",
        enrollmentId,
        "secret",
        secret,
        "otpauthUri",
        uri,
        "expiresAt",
        expires.toString());
  }

  public void confirmTotpEnrollment(UUID accountId, String enrollmentId, String code) {
    ObjectNode ceremony = store.get("team_ceremony", enrollmentId, true);
    IamError.require(
        ceremony != null
            && ceremony.path("kind").asString().equals("totp_enrollment")
            && ceremony.path("accountId").asString().equals(accountId.toString())
            && clock.instant().isBefore(Instant.parse(ceremony.path("expiresAt").asString())),
        "not_found",
        404);
    String secret =
        secrets.decrypt(
            ceremony.path("secret").asString(),
            "totp-enroll:" + TenantContext.require() + ":" + enrollmentId);
    long counter = Totp.verify(secret, code, clock.instant(), -1);
    IamError.require(counter >= 0, "invalid_credentials", 400);
    ObjectNode existing = store.get("team_totp", accountId.toString(), true);
    ObjectNode row =
        Json.object(
            "accountId",
            accountId.toString(),
            "totp",
            secrets.encrypt(secret, "totp:" + TenantContext.require() + ":" + accountId),
            "lastCounter",
            counter);
    if (existing == null) store.insert("team_totp", accountId.toString(), row, null);
    else {
      row.put("id", existing.path("id").asString());
      store.save("team_totp", row);
    }
    store.delete("team_ceremony", enrollmentId);
    events.publish(
        "identity.mfa_changed",
        accountId.toString(),
        Json.object(
            "accountId",
            accountId.toString(),
            "method",
            "totp",
            "change",
            existing == null ? "added" : "reset",
            "actorId",
            accountId.toString()));
  }

  public void deleteTotp(UUID accountId, boolean requireRemainingMfa) {
    IamError.require(hasTotp(accountId), "not_found", 404);
    if (requireRemainingMfa) IamError.require(passkeyCount(accountId) > 0, "mfa_required", 409);
    store.delete("team_totp", accountId.toString());
    events.publish(
        "identity.mfa_changed",
        accountId.toString(),
        Json.object(
            "accountId",
            accountId.toString(),
            "method",
            "totp",
            "change",
            "removed",
            "actorId",
            accountId.toString()));
  }

  public UUID accountIdFor(String providerId, String username) {
    ObjectNode identity =
        store.get("identity", providerId + ":" + username.toLowerCase(Locale.ROOT), false);
    return identity == null ? null : UUID.fromString(identity.path("accountId").asString());
  }

  public ObjectNode identityForAccount(String providerId, String accountId) {
    return store.all("identity").stream()
        .filter(
            i ->
                i.path("accountId").asString().equals(accountId)
                    && i.path("providerId").asString().equals(providerId))
        .findFirst()
        .orElse(null);
  }

  public ObjectNode profile(ObjectNode accountRow, boolean mfa) {
    return Json.object(
        "subject",
        accountRow.path("username").asString(),
        "displayName",
        accountRow.path("displayName").asString(),
        "mfaPerformed",
        mfa,
        "authTime",
        clock.instant().toString());
  }

  private static String url(String value) {
    return java.net.URLEncoder.encode(value, java.nio.charset.StandardCharsets.UTF_8)
        .replace("+", "%20");
  }

  /** Platform MFA form uses field {@code otp}; connector totp form uses {@code code}. */
  public boolean verifyMfaInput(UUID account, JsonNode input) {
    String code =
        input.path("values").path("otp").asString(input.path("values").path("code").asString(""));
    return verifyTotp(account, code);
  }
}
