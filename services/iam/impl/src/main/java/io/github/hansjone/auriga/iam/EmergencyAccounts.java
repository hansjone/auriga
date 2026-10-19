package io.github.hansjone.auriga.iam;

import io.github.hansjone.auriga.common.persistence.UuidV7;
import java.net.InetAddress;
import java.time.Clock;
import java.util.*;
import org.springframework.security.crypto.argon2.Argon2PasswordEncoder;
import tools.jackson.databind.node.ObjectNode;

public final class EmergencyAccounts {
  /** Installer must transfer these values to the operator via its protected one-time channel. */
  public record Enrollment(
      UUID accountId, UUID providerId, String username, String password, String totpSecret) {
    @Override
    public String toString() {
      return "Emergency enrollment [REDACTED]";
    }
  }

  private final IamStore store;
  private final Secrets secrets;
  private final Clock clock;
  private final Argon2PasswordEncoder passwords = new Argon2PasswordEncoder(16, 32, 1, 65536, 3);
  private final boolean remoteEnabled;

  public EmergencyAccounts(IamStore store, Secrets secrets, Clock clock, boolean remoteEnabled) {
    this.store = store;
    this.secrets = secrets;
    this.clock = clock;
    this.remoteEnabled = remoteEnabled;
  }

  public Enrollment install(UUID tenant) {
    return store.tenant(
        tenant,
        () -> {
          store.lock("emergency-install");
          IamError.require(
              store.get("emergency_account", "emergency", true) == null, "already_installed", 409);
          UUID aid = UuidV7.next(), pid = UuidV7.next();
          String username = "emergency-" + Secrets.random().substring(0, 10),
              password = Secrets.random(),
              totp = Totp.generate();
          store.insert(
              "account",
              aid.toString(),
              Json.object(
                  "accountId",
                  aid,
                  "status",
                  "active",
                  "displayName",
                  "Emergency administrator",
                  "createdAt",
                  clock.instant().toString()),
              null);
          store.insert(
              "idp_provider",
              pid.toString(),
              Json.object(
                  "providerId",
                  pid,
                  "connectorType",
                  "emergency",
                  "builtin",
                  true,
                  "createdAt",
                  clock.instant().toString(),
                  "updatedAt",
                  clock.instant().toString(),
                  "enabled",
                  true,
                  "display",
                  Json.object(
                      "name", Json.object("en", "Emergency account", "zh-CN", "应急账号"), "order", 0),
                  "config",
                  secrets.encrypt("{}", "provider:" + tenant + ":" + pid),
                  "admission",
                  Json.object(
                      "mode",
                      "linked_only",
                      "mfa",
                      "always",
                      "trustExternalMfa",
                      false,
                      "sessionTtlSeconds",
                      28800,
                      "idleTimeoutSeconds",
                      1800)),
              null);
          store.insert(
              "identity",
              pid + ":" + username,
              Json.object(
                  "accountId",
                  aid,
                  "providerId",
                  pid,
                  "subject",
                  username,
                  "linkedAt",
                  clock.instant().toString()),
              null);
          store.insert(
              "emergency_account",
              "emergency",
              Json.object(
                  "accountId",
                  aid,
                  "providerId",
                  pid,
                  "username",
                  username,
                  "passwordHash",
                  passwords.encode(password),
                  "totp",
                  secrets.encrypt(totp, "totp:" + tenant + ":" + aid),
                  "lastCounter",
                  -1),
              null);
          return new Enrollment(aid, pid, username, password, totp);
        });
  }

  static boolean loopback(String ip) {
    // Never resolve untrusted hostnames (DNS rebinding/SSRF).
    if (ip == null || !ip.matches("[0-9a-fA-F:.]+")) return false;
    try {
      return InetAddress.getByName(ip).isLoopbackAddress();
    } catch (java.net.UnknownHostException e) {
      return false;
    }
  }

  public ObjectNode authenticate(String ip, ObjectNode values) {
    IamError.require(remoteEnabled || loopback(ip), "emergency_local_only", 403);
    ObjectNode row = store.get("emergency_account", "emergency", true);
    String password = values.path("password").asString("");
    IamError.require(
        password.length() <= 256
            && row != null
            && Secrets.same(row.path("username").asString(), values.path("username").asString())
            && passwords.matches(password, row.path("passwordHash").asString()),
        "invalid_credentials",
        401);
    long counter =
        Totp.verify(
            secrets.decrypt(
                row.path("totp").asString(),
                "totp:"
                    + io.github.hansjone.auriga.common.context.TenantContext.require()
                    + ":"
                    + row.path("accountId").asString()),
            values.path("otp").asString(""),
            clock.instant(),
            row.path("lastCounter").asLong());
    IamError.require(counter >= 0, "invalid_credentials", 401);
    row.put("lastCounter", counter);
    store.save("emergency_account", row);
    return Json.object(
        "subject",
        row.path("username").asString(),
        "mfaPerformed",
        true,
        "authTime",
        clock.instant().toString());
  }
}
