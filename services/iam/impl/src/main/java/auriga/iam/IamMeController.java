package auriga.iam;

import auriga.common.context.TenantContext;
import auriga.common.security.*;
import java.time.Duration;
import java.time.Instant;
import java.util.*;
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
public class IamMeController {
  private final IamStore store;
  private final SessionManager sessions;
  private final TeamAccounts accounts;
  private final TeamPasskeys passkeys;
  private final ProviderManager providers;
  private final java.time.Clock clock;
  private final String issuer;

  public IamMeController(
      IamStore store,
      SessionManager sessions,
      TeamAccounts accounts,
      TeamPasskeys passkeys,
      ProviderManager providers,
      java.time.Clock clock,
      TokenService tokens) {
    this.store = store;
    this.sessions = sessions;
    this.accounts = accounts;
    this.passkeys = passkeys;
    this.providers = providers;
    this.clock = clock;
    this.issuer = tokens.issuer();
  }

  @GetMapping("/me")
  @AuthenticatedOnly
  public ObjectNode me() {
    var principal = AuthorizationRules.currentPrincipal().orElseThrow();
    return store.tx(
        () -> {
          ObjectNode account = store.get("account", principal.subject(), false);
          ObjectNode session = sessions.activeById(principal.sessionId());
          IamError.require(account != null && session != null, "unauthenticated", 401);
          return Json.object(
              "account",
              accountView(account),
              "session",
              sessionView(session, principal.sessionId()),
              "identities",
              identities(principal.subject()),
              "mfa",
              accounts.mfaStatus(UUID.fromString(principal.subject()), true));
        });
  }

  @GetMapping("/me/sessions")
  @AuthenticatedOnly
  public ObjectNode sessions() {
    var principal = AuthorizationRules.currentPrincipal().orElseThrow();
    return store.tx(
        () ->
            Json.object(
                "items",
                sessions.userSessions(principal.subject()).stream()
                    .filter(
                        s ->
                            s.path("accountId").asString().equals(principal.subject())
                                && !s.path("revoked").asBoolean())
                    .map(s -> sessionView(s, principal.sessionId()))
                    .toList()));
  }

  @PostMapping("/me/sessions/revoke-others")
  @AuthenticatedOnly
  public ObjectNode revokeOthers() {
    var principal = AuthorizationRules.currentPrincipal().orElseThrow();
    return sessions.response(
        () -> {
          int revoked = 0;
          for (ObjectNode s : sessions.userSessions(principal.subject()))
            if (s.path("accountId").asString().equals(principal.subject())
                && !s.path("sessionId").asString().equals(principal.sessionId())) {
              boolean wasRevoked = s.path("revoked").asBoolean();
              sessions.revokeOne(s, "user");
              if (!wasRevoked) revoked++;
            }
          return Json.object("revoked", revoked);
        });
  }

  @GetMapping("/me/identities")
  @AuthenticatedOnly
  public ObjectNode identitiesList() {
    var principal = AuthorizationRules.currentPrincipal().orElseThrow();
    return store.tx(() -> Json.object("items", identities(principal.subject())));
  }

  @DeleteMapping("/me/identities/{identityId}")
  @AuthenticatedOnly
  @ResponseStatus(org.springframework.http.HttpStatus.NO_CONTENT)
  public void unlink(@PathVariable String identityId) {
    var principal = AuthorizationRules.currentPrincipal().orElseThrow();
    store.tx(
        () -> {
          IamError.require(
              principal.steppedUpWithin(Duration.ofMinutes(10), clock.instant()),
              "step_up_required",
              403);
          List<ObjectNode> mine =
              store.all("identity").stream()
                  .filter(i -> i.path("accountId").asString().equals(principal.subject()))
                  .toList();
          IamError.require(mine.size() > 1, "last_identity", 409);
          ObjectNode target =
              mine.stream()
                  .filter(i -> i.path("id").asString().equals(identityId))
                  .findFirst()
                  .orElseThrow(() -> IamError.error("not_found", 404));
          store.delete(
              "identity",
              target.path("providerId").asString() + ":" + target.path("subject").asString());
          return null;
        });
  }

  @PutMapping("/me/password")
  @AuthenticatedOnly
  @ResponseStatus(org.springframework.http.HttpStatus.NO_CONTENT)
  public void changePassword(@RequestBody JsonNode body) {
    var principal = AuthorizationRules.currentPrincipal().orElseThrow();
    sessions.response(
        () -> {
          ObjectNode teamIdentity =
              store.all("identity").stream()
                  .filter(
                      i ->
                          i.path("accountId").asString().equals(principal.subject())
                              && providerType(i.path("providerId").asString())
                                  .equals("team-account"))
                  .findFirst()
                  .orElseThrow(() -> IamError.error("forbidden", 403));
          accounts.changePassword(
              UUID.fromString(principal.subject()),
              teamIdentity.path("providerId").asString(),
              body.path("currentPassword").asString(""),
              body.path("newPassword").asString(""));
          revokeOtherCredentialSessions();
          return null;
        });
  }

  @GetMapping("/me/mfa")
  @AuthenticatedOnly
  public ObjectNode mfa() {
    var principal = AuthorizationRules.currentPrincipal().orElseThrow();
    return store.tx(() -> accounts.mfaStatus(UUID.fromString(principal.subject()), true));
  }

  @PostMapping("/me/mfa/totp/enrollment")
  @RequiresRecentStepUp
  @AuthenticatedOnly
  @ResponseStatus(org.springframework.http.HttpStatus.CREATED)
  public ObjectNode beginTotp() {
    var principal = AuthorizationRules.currentPrincipal().orElseThrow();
    return store.tx(
        () -> accounts.beginTotpEnrollment(UUID.fromString(principal.subject()), issuerHost()));
  }

  @PostMapping("/me/mfa/totp/enrollment/{enrollmentId}/confirm")
  @RequiresRecentStepUp
  @AuthenticatedOnly
  @ResponseStatus(org.springframework.http.HttpStatus.NO_CONTENT)
  public void confirmTotp(@PathVariable String enrollmentId, @RequestBody JsonNode body) {
    var principal = AuthorizationRules.currentPrincipal().orElseThrow();
    sessions.response(
        () -> {
          accounts.confirmTotpEnrollment(
              UUID.fromString(principal.subject()), enrollmentId, body.path("code").asString(""));
          revokeOtherCredentialSessions();
          return null;
        });
  }

  @DeleteMapping("/me/mfa/totp")
  @AuthenticatedOnly
  @ResponseStatus(org.springframework.http.HttpStatus.NO_CONTENT)
  public void deleteTotp() {
    var principal = AuthorizationRules.currentPrincipal().orElseThrow();
    store.tx(
        () -> {
          IamError.require(
              principal.steppedUpWithin(Duration.ofMinutes(10), clock.instant()),
              "step_up_required",
              403);
          accounts.deleteTotp(UUID.fromString(principal.subject()), true);
          return null;
        });
  }

  @GetMapping("/me/passkeys")
  @AuthenticatedOnly
  public ObjectNode listPasskeys() {
    var principal = AuthorizationRules.currentPrincipal().orElseThrow();
    return store.tx(
        () -> Json.object("items", passkeys.list(UUID.fromString(principal.subject()))));
  }

  @PostMapping("/me/passkeys/registration")
  @RequiresRecentStepUp
  @AuthenticatedOnly
  @ResponseStatus(org.springframework.http.HttpStatus.CREATED)
  public ObjectNode beginPasskey() {
    var principal = AuthorizationRules.currentPrincipal().orElseThrow();
    return store.tx(
        () -> {
          ObjectNode account = store.get("account", principal.subject(), false);
          return passkeys.beginRegistration(
              UUID.fromString(principal.subject()),
              account.path("displayName").asString("user"),
              teamConfig());
        });
  }

  @PostMapping("/me/passkeys/registration/{registrationId}/complete")
  @RequiresRecentStepUp
  @AuthenticatedOnly
  @ResponseStatus(org.springframework.http.HttpStatus.CREATED)
  public ObjectNode completePasskey(
      @PathVariable String registrationId, @RequestBody JsonNode body) {
    var principal = AuthorizationRules.currentPrincipal().orElseThrow();
    return sessions.response(
        () -> {
          ObjectNode result =
              passkeys.completeRegistration(
                  UUID.fromString(principal.subject()),
                  registrationId,
                  body.path("name").asString(""),
                  body.path("credential"),
                  teamConfig());
          revokeOtherCredentialSessions();
          return result;
        });
  }

  @PatchMapping("/me/passkeys/{passkeyId}")
  @AuthenticatedOnly
  public ObjectNode renamePasskey(@PathVariable String passkeyId, @RequestBody JsonNode body) {
    var principal = AuthorizationRules.currentPrincipal().orElseThrow();
    return store.tx(
        () ->
            passkeys.rename(
                UUID.fromString(principal.subject()), passkeyId, body.path("name").asString("")));
  }

  @DeleteMapping("/me/passkeys/{passkeyId}")
  @RequiresRecentStepUp
  @AuthenticatedOnly
  @ResponseStatus(org.springframework.http.HttpStatus.NO_CONTENT)
  public void deletePasskey(@PathVariable String passkeyId) {
    var principal = AuthorizationRules.currentPrincipal().orElseThrow();
    store.tx(
        () -> {
          UUID account = UUID.fromString(principal.subject());
          passkeys.delete(account, passkeyId, true, accounts.hasTotp(account));
          return null;
        });
  }

  @GetMapping("/me/devices")
  @AuthenticatedOnly
  public ObjectNode devices() {
    var principal = AuthorizationRules.currentPrincipal().orElseThrow();
    return store.tx(
        () ->
            Json.object(
                "items",
                store.all("device").stream()
                    .filter(
                        d ->
                            d.path("accountId").asString().equals(principal.subject())
                                && !d.path("revoked").asBoolean())
                    .map(this::deviceView)
                    .toList()));
  }

  @PatchMapping("/me/devices/{deviceId}")
  @AuthenticatedOnly
  public ObjectNode renameDevice(@PathVariable String deviceId, @RequestBody JsonNode body) {
    var principal = AuthorizationRules.currentPrincipal().orElseThrow();
    return store.tx(
        () -> {
          ObjectNode d = requireDevice(principal.subject(), deviceId);
          String name = body.path("name").asString("");
          IamError.validate(!name.isBlank() && name.length() <= 64);
          d.put("name", name);
          store.save("device", d);
          return deviceView(d);
        });
  }

  @DeleteMapping("/me/devices/{deviceId}")
  @AuthenticatedOnly
  @ResponseStatus(org.springframework.http.HttpStatus.NO_CONTENT)
  public void revokeDevice(@PathVariable String deviceId) {
    var principal = AuthorizationRules.currentPrincipal().orElseThrow();
    store.tx(
        () -> {
          ObjectNode d = requireDevice(principal.subject(), deviceId);
          d.put("revoked", true);
          store.save("device", d);
          return null;
        });
  }

  private ObjectNode requireDevice(String accountId, String deviceId) {
    ObjectNode d = store.get("device", deviceId, true);
    IamError.require(
        d != null
            && d.path("accountId").asString().equals(accountId)
            && !d.path("revoked").asBoolean(),
        "not_found",
        404);
    return d;
  }

  private ObjectNode teamConfig() {
    return store.all("idp_provider").stream()
        .filter(
            p ->
                p.path("connectorType").asString().equals("team-account")
                    && p.path("enabled").asBoolean())
        .findFirst()
        .map(providers::decryptedConfig)
        .orElseThrow(() -> IamError.error("provider_unavailable", 404));
  }

  private String providerType(String providerId) {
    ObjectNode p = store.get("idp_provider", providerId, false);
    return p == null ? "" : p.path("connectorType").asString();
  }

  private List<ObjectNode> identities(String accountId) {
    return store.all("identity").stream()
        .filter(i -> i.path("accountId").asString().equals(accountId))
        .map(
            i -> {
              ObjectNode p = store.get("idp_provider", i.path("providerId").asString(), false);
              return Json.object(
                  "id",
                  i.path("id").asString(),
                  "provider",
                  Json.object(
                      "id",
                      i.path("providerId").asString(),
                      "connectorType",
                      p == null ? "" : p.path("connectorType").asString(),
                      "displayName",
                      p == null ? Json.object("en", "Unknown") : p.path("display").path("name")),
                  "subjectHint",
                  hint(i.path("subject").asString()),
                  "displayName",
                  i.path("profile").path("displayName").asString(""),
                  "linkedAt",
                  i.path("linkedAt").asString());
            })
        .toList();
  }

  private static String hint(String subject) {
    if (subject.length() <= 4) return "****";
    return subject.substring(0, 2) + "****" + subject.substring(subject.length() - 2);
  }

  private ObjectNode accountView(ObjectNode account) {
    ObjectNode v =
        Json.object(
            "id",
            account.path("accountId").asString(account.path("id").asString()),
            "tenantId",
            TenantContext.require().toString(),
            "displayName",
            account.path("displayName").asString(""),
            "status",
            account.path("status").asString("active"),
            "createdAt",
            account.path("createdAt").asString());
    if (account.has("email")) v.put("email", account.path("email").asString());
    if (account.has("lastLoginAt")) v.put("lastLoginAt", account.path("lastLoginAt").asString());
    return v;
  }

  private ObjectNode sessionView(ObjectNode session, String currentId) {
    ObjectNode v =
        Json.object(
            "id",
            session.path("sessionId").asString(),
            "audience",
            session.path("audience").asString(),
            "current",
            session.path("sessionId").asString().equals(currentId),
            "providerId",
            session.path("providerId").asString(),
            "mfa",
            session.path("mfa").asBoolean(),
            "createdAt",
            session.path("authTime").asString(),
            "expiresAt",
            session.path("expiresAt").asString());
    if (session.has("ip")) v.put("ip", session.path("ip").asString());
    if (session.has("userAgent")) v.put("userAgent", session.path("userAgent").asString());
    if (session.has("idleExpiresAt"))
      v.put(
          "lastSeenAt",
          Instant.parse(session.path("idleExpiresAt").asString())
              .minusSeconds(session.path("idleSeconds").asLong(0))
              .toString());
    return v;
  }

  private ObjectNode deviceView(ObjectNode d) {
    ObjectNode v =
        Json.object(
            "id",
            d.path("deviceId").asString(d.path("id").asString()),
            "name",
            d.path("name").asString("Device"),
            "kind",
            d.path("kind").asString("dsh_connect"),
            "createdAt",
            d.path("createdAt").asString());
    if (d.has("platform")) v.put("platform", d.path("platform").asString());
    if (d.has("clientVersion")) v.put("clientVersion", d.path("clientVersion").asString());
    if (d.has("lastSeenAt")) v.put("lastSeenAt", d.path("lastSeenAt").asString());
    if (d.has("lastIp")) v.put("lastIp", d.path("lastIp").asString());
    return v;
  }

  private String issuerHost() {
    try {
      return java.net.URI.create(issuer).getHost() == null
          ? "Auriga"
          : java.net.URI.create(issuer).getHost();
    } catch (Exception e) {
      return "Auriga";
    }
  }

  private void revokeOtherCredentialSessions() {
    var principal = AuthorizationRules.currentPrincipal().orElseThrow();
    for (ObjectNode session : sessions.userSessions(principal.subject()))
      if (session.path("accountId").asString().equals(principal.subject())
          && !session.path("sessionId").asString().equals(principal.sessionId()))
        // Earlier credential changes may have committed while their deletion confirmation failed.
        sessions.revokeOne(session, "security");
  }
}
