package auriga.iam;

import auriga.common.context.TenantContext;
import auriga.common.error.*;
import auriga.common.persistence.UuidV7;
import auriga.iam.api.UacEnterpriseAdapter;
import java.security.SecureRandom;
import java.time.*;
import java.util.*;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** Challenge reads are local. Only the background worker may perform upstream polling. */
final class UacFlow {
  record Created(ObjectNode challenge, String binding) {}

  private final IamStore store;
  private final UacProviderService providers;
  private final ProviderManager generic;
  private final UacEnterpriseAdapter adapter;
  private final UacSessions uac;
  private final LoginManager login;
  private final SessionManager sessions;
  private final Clock clock;

  UacFlow(
      IamStore store,
      UacProviderService providers,
      ProviderManager generic,
      UacEnterpriseAdapter adapter,
      UacSessions uac,
      LoginManager login,
      SessionManager sessions,
      Clock clock) {
    this.store = store;
    this.providers = providers;
    this.generic = generic;
    this.adapter = adapter;
    this.uac = uac;
    this.login = login;
    this.sessions = sessions;
    this.clock = clock;
  }

  ObjectNode transaction(
      String provider,
      String purpose,
      String audience,
      String origin,
      String ip,
      String invitation,
      ObjectNode source,
      String binding) {
    Instant now = clock.instant();
    String id = UuidV7.next().toString();
    var tx =
        Json.object(
            "txId",
            id,
            "providerId",
            provider,
            "purpose",
            purpose,
            "status",
            "pending",
            "uacPending",
            true,
            "uacPendingAccountId",
            UuidV7.next().toString(),
            "audience",
            audience,
            "origin",
            origin,
            "linkOrigin",
            origin,
            "tenantId",
            TenantContext.require().toString(),
            "bindingHash",
            Secrets.hash(binding),
            "expiresAt",
            now.plusSeconds(300).toString(),
            "ip",
            ip,
            "userAgent",
            "",
            "returnTo",
            "/",
            "invitationCode",
            invitation,
            "correlationId",
            UuidV7.next().toString());
    if (source != null) {
      tx.put("accountId", source.path("accountId").asString());
      tx.put("sessionId", source.path("sessionId").asString());
      tx.set("uacLinkAuthTime", source.path("authTime"));
      tx.put("uacLinkGeneration", source.path("authGeneration").asLong());
    }
    store.insert("login_tx", id, tx, now.plusSeconds(300));
    return tx;
  }

  Created create(
      JsonNode body,
      String audience,
      String origin,
      String ip,
      String previous,
      ObjectNode source) {
    IamError.validate(body.isObject() && body.path("providerId").isString());
    body.propertyNames()
        .forEach(
            name ->
                IamError.validate(
                    Set.of("providerId", "purpose", "invitationCode").contains(name)));
    String purpose = body.path("purpose").asString("login"),
        invitation = body.path("invitationCode").asString("");
    IamError.validate(
        Set.of("login", "link").contains(purpose)
            && invitation.length() <= 256
            && (!body.has("purpose") || body.path("purpose").isString())
            && (!body.has("invitationCode") || body.path("invitationCode").isString())
            && (!purpose.equals("link") || !body.has("invitationCode")));
    if (purpose.equals("link")) fresh(source);
    String provider = body.path("providerId").asString();
    String binding = Secrets.random();
    return store.tx(
        () -> {
          ObjectNode row = providers.row(provider, true);
          IamError.require(
              row.path("enabled").asBoolean() && row.path("uacConfigured").asBoolean(),
              "provider_unavailable",
              404);
          if (previous != null) {
            store.lock("uac-binding:" + audience + ":" + Secrets.hash(previous));
            for (ObjectNode navigation : store.all("uac_navigation"))
              if (origin.equals(navigation.path("origin").asString())
                  && audience.equals(navigation.path("audience").asString())
                  && Secrets.same(
                      navigation.path("bindingHash").asString(), Secrets.hash(previous))) {
                navigation = store.byId("uac_navigation", navigation.path("id").asString(), true);
                IamError.require(
                    !navigation.path("replaced").asBoolean(), "login_tx_finished", 409);
                navigation.put("replaced", true);
                navigation.put("cancelled", true);
                navigation.put("consumed", true);
                store.save("uac_navigation", navigation);
                replaceTransaction(navigation.path("transactionId").asString());
                if (navigation.has("issuedSessionId")) {
                  ObjectNode issued =
                      store.byId("session", navigation.path("issuedSessionId").asString(), true);
                  if (issued != null && !issued.path("revoked").asBoolean())
                    sessions.revokeOne(issued, "security");
                }
              }
            for (ObjectNode old : store.all("uac_challenge"))
              if (Secrets.same(old.path("bindingHash").asString(), Secrets.hash(previous))
                  && origin.equals(old.path("origin").asString())
                  && audience.equals(old.path("audience").asString())) {
                old = store.byId("uac_challenge", old.path("id").asString(), true);
                IamError.require(!old.path("replaced").asBoolean(), "login_tx_finished", 409);
                old.put("replaced", true);
                if (!old.path("consumed").asBoolean()) {
                  old.put("state", "cancelled");
                  old.remove("credential");
                }
                replaceTransaction(old.path("transactionId").asString());
                if (old.has("issuedSessionId")) {
                  ObjectNode issued =
                      store.byId("session", old.path("issuedSessionId").asString(), true);
                  if (issued != null && !issued.path("revoked").asBoolean())
                    sessions.revokeOne(issued, "security");
                }
                store.save("uac_challenge", old);
              }
          }
          ObjectNode tx =
              transaction(
                  provider,
                  purpose,
                  audience,
                  origin,
                  ip,
                  invitation,
                  purpose.equals("link") ? source : null,
                  binding);
          String id = UuidV7.next().toString();
          var telemetry =
              uac.telemetry == null
                  ? null
                  : uac.telemetry.begin(
                      Json.object(
                          "providerId",
                          provider,
                          "transactionId",
                          tx.path("id"),
                          "correlationId",
                          tx.path("correlationId"),
                          "configRevision",
                          row.path("revision")),
                      "challenge_start",
                      null,
                      Json.object());
          boolean created = false;
          try {
            ObjectNode coordinates = Json.object("x", coordinate(), "y", coordinate());
            String content = adapter.qrContent(generic.decryptedConfig(row), coordinates);
            ObjectNode challenge =
                store.insert(
                    "uac_challenge",
                    id,
                    Json.object(
                        "providerId",
                        provider,
                        "transactionId",
                        tx.path("id"),
                        "state",
                        "waiting",
                        "coordinates",
                        coordinates,
                        "bindingHash",
                        Secrets.hash(binding),
                        "audience",
                        audience,
                        "origin",
                        origin,
                        "providerRevision",
                        UacProviderService.etag(row),
                        "expiresAt",
                        clock.instant().plusSeconds(120).toString(),
                        "correlationId",
                        tx.path("correlationId")),
                    clock.instant().plusSeconds(120));
            uac.enqueue(
                "scan:" + id,
                Json.object(
                    "kind",
                    "scan",
                    "providerId",
                    provider,
                    "challengeId",
                    id,
                    "correlationId",
                    tx.path("correlationId"),
                    "transactionId",
                    tx.path("id"),
                    "configRevision",
                    row.path("revision"),
                    "nextAt",
                    clock.instant().toString()));
            created = true;
            return new Created(
                Json.object(
                    "id",
                    id,
                    "transactionId",
                    tx.path("id"),
                    "qrContent",
                    content,
                    "expiresAt",
                    challenge.path("expiresAt"),
                    "pollIntervalSeconds",
                    2),
                binding);
          } finally {
            if (telemetry != null) telemetry.end(created ? "success" : "failure", Json.object());
          }
        });
  }

  private static String coordinate() {
    byte[] raw = new byte[16];
    new SecureRandom().nextBytes(raw);
    return HexFormat.of().formatHex(raw);
  }

  private void replaceTransaction(String id) {
    ObjectNode tx = store.get("login_tx", id, true);
    if (tx == null) return;
    if ("pending".equals(tx.path("status").asString())) {
      tx.put("status", "cancelled");
      tx.remove("uacCredential");
      tx.remove("pendingProfile");
      tx.set("step", Json.object("type", "fail", "code", "iam.login_tx_cancelled"));
      store.save("login_tx", tx);
    }
    if (tx.has("uacIssuedSessionId")) {
      ObjectNode issued = store.byId("session", tx.path("uacIssuedSessionId").asString(), true);
      if (issued != null && !issued.path("revoked").asBoolean())
        sessions.revokeOne(issued, "security");
    }
  }

  private void fresh(ObjectNode source) {
    if (source == null) throw new AurigaException(CommonErrors.UNAUTHENTICATED);
    Instant now = clock.instant();
    Instant auth = Instant.parse(source.path("authTime").asString());
    boolean authenticated =
        !auth.isBefore(now.minusSeconds(600)) && !auth.isAfter(now.plusSeconds(30));
    boolean stepped =
        source.has("stepUpAt")
            && !Instant.parse(source.path("stepUpAt").asString()).isBefore(now.minusSeconds(600))
            && !Instant.parse(source.path("stepUpAt").asString()).isAfter(now.plusSeconds(30));
    IamError.require(authenticated || stepped, "step_up_required", 403);
  }

  ObjectNode bound(String id, String binding, String audience, String origin, boolean lock) {
    ObjectNode row = store.get("uac_challenge", id, lock);
    if (row == null
        || binding == null
        || !Secrets.same(row.path("bindingHash").asString(), Secrets.hash(binding))
        || !audience.equals(row.path("audience").asString())
        || !origin.equals(row.path("origin").asString()))
      throw new AurigaException(CommonErrors.NOT_FOUND);
    return row;
  }

  ObjectNode status(String id, String binding, String audience, String origin) {
    return store.tx(
        () -> {
          ObjectNode row = bound(id, binding, audience, origin, false);
          String state =
              clock.instant().isBefore(Instant.parse(row.path("expiresAt").asString()))
                  ? row.path("state").asString()
                  : "expired";
          ObjectNode output =
              Json.object("id", id, "state", state, "expiresAt", row.path("expiresAt"));
          if (row.has("failureCode") && Set.of("failed", "denied", "unavailable").contains(state))
            output.set("failureCode", row.path("failureCode"));
          return output;
        });
  }

  ObjectNode linkTransaction(String id, String binding, String audience, String origin) {
    ObjectNode challenge = bound(id, binding, audience, origin, false);
    ObjectNode tx =
        login.checkEntry(challenge.path("transactionId").asString(), binding, audience, origin);
    return "link".equals(tx.path("purpose").asString()) ? tx : null;
  }

  String correlation(String id, String binding, String audience, String origin) {
    return store.tx(
        () -> bound(id, binding, audience, origin, false).path("correlationId").asString());
  }

  void cancel(String id, String binding, String audience, String origin) {
    store.tx(
        () -> {
          ObjectNode row = bound(id, binding, audience, origin, true);
          if (!row.path("consumed").asBoolean()) {
            row.put("state", "cancelled");
            row.remove("credential");
            store.save("uac_challenge", row);
            login.cancel(row.path("transactionId").asString(), binding);
          }
          return null;
        });
  }

  LoginManager.Result complete(String id, String binding, String audience, String origin) {
    return store.tx(
        () -> {
          // Provider before challenge/session is also the configuration writer's lock order.
          ObjectNode snapshot = bound(id, binding, audience, origin, false);
          ObjectNode provider = providers.row(snapshot.path("providerId").asString(), true);
          ObjectNode row = bound(id, binding, audience, origin, true);
          IamError.require(
              clock.instant().isBefore(Instant.parse(row.path("expiresAt").asString())),
              "login_tx_expired",
              409);
          IamError.require(
              provider.path("enabled").asBoolean()
                  && UacProviderService.etag(provider)
                      .equals(row.path("providerRevision").asString()),
              "provider_unavailable",
              404);
          String transaction = row.path("transactionId").asString();
          IamError.require(!row.path("replaced").asBoolean(), "login_tx_finished", 409);
          if (row.path("consumed").asBoolean()) {
            LoginManager.Result existing = login.get(transaction, binding);
            if (!row.has("issued")) return existing;
            ObjectNode secret = uac.open(row.path("issued"), row.path("providerId").asString());
            ObjectNode session = sessions.activeById(secret.path("sessionId").asString());
            IamError.require(session != null, "unauthenticated", 401);
            return new LoginManager.Result(
                existing.transaction(),
                null,
                new SessionManager.Issued(
                    session, secret.path("cookie").asString(), secret.path("csrf").asString()));
          }
          IamError.require(
              "verified".equals(row.path("state").asString()) && row.has("credential"),
              "invalid_step_input",
              400);
          ObjectNode tx = login.checkEntry(transaction, binding, audience, origin);
          if ("link".equals(tx.path("purpose").asString()))
            fresh(sessions.activeById(tx.path("sessionId").asString()));
          tx.set("uacCredential", row.path("credential"));
          tx.put("uacVerified", true);
          store.save("login_tx", tx);
          ObjectNode credential =
              uac.open(row.path("credential"), row.path("providerId").asString());
          LoginManager.Result result =
              login.finishUac(transaction, Json.object("subject", credential.path("empNo")));
          row.put("consumed", true);
          row.remove("credential");
          if (result.session() != null) {
            row.put("issuedSessionId", result.session().session().path("sessionId").asString());
            row.set(
                "issued",
                uac.seal(
                    Json.object(
                        "sessionId",
                        result.session().session().path("sessionId"),
                        "cookie",
                        result.session().cookie(),
                        "csrf",
                        result.session().csrf()),
                    row.path("providerId").asString()));
          }
          store.save("uac_challenge", row);
          return result;
        });
  }
}
