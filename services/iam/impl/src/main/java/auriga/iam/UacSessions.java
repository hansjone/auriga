package auriga.iam;

import auriga.common.context.TenantContext;
import auriga.common.error.*;
import auriga.common.security.*;
import auriga.iam.api.UacEnterpriseAdapter;
import java.time.*;
import java.util.*;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** Credentials, session generation and work registration commit in the session transaction. */
final class UacSessions {
  static boolean boundTo(ObjectNode holder, ObjectNode session) {
    return session.has("uacCredentialId")
        ? session.has("uacSessionBinding")
            && holder.has("uacSessionBinding")
            && holder.path("uacSessionBinding").equals(session.path("uacSessionBinding"))
        : !holder.has("uacSessionBinding");
  }

  static boolean roundContains(ObjectNode credential, ObjectNode session) {
    for (JsonNode item : credential.path("roundSessions"))
      if (item.path("id").asString().equals(session.path("id").asString())
          && item.path("generation").asLong() == session.path("authGeneration").asLong())
        return true;
    return false;
  }

  final IamStore store;
  final UacCredentialCipher cipher;
  final UacEnterpriseAdapter adapter;
  final ProviderManager providers;
  final SessionManager sessions;
  final Clock clock;
  final UacRevalidationConfiguration policy;

  UacSessions(
      IamStore store,
      UacCredentialCipher cipher,
      UacEnterpriseAdapter adapter,
      ProviderManager providers,
      SessionManager sessions,
      Clock clock,
      UacRevalidationConfiguration policy) {
    this.store = store;
    this.cipher = cipher;
    this.adapter = adapter;
    this.providers = providers;
    this.sessions = sessions;
    this.clock = clock;
    this.policy = policy;
    sessions.uacValidator(this::valid);
  }

  JsonNode seal(JsonNode credential, String provider) {
    return cipher.encrypt(
        credential.toString(), TenantContext.require(), UUID.fromString(provider));
  }

  ObjectNode open(JsonNode envelope, String provider) {
    return Json.read(cipher.decrypt(envelope, TenantContext.require(), UUID.fromString(provider)));
  }

  void established(ObjectNode tx, SessionManager.Issued issued) {
    if (!tx.has("uacCredential")) return;
    ObjectNode session = issued.session();
    // Keep the final MFA result on the transaction too. Replacement must fence a
    // session even when the challenge/navigation completed before MFA did.
    tx.put("uacIssuedSessionId", session.path("sessionId").asString());
    store.save("login_tx", tx);
    String provider = tx.path("providerId").asString(),
        account = session.path("accountId").asString();
    ObjectNode secret = open(tx.path("uacCredential"), provider);
    store.lock("uac-account:" + provider + ":" + account);
    String generationKey = "generation:" + provider + ":" + account;
    ObjectNode sequence = store.get("uac_credential", generationKey, true);
    if (sequence == null)
      sequence =
          store.insert(
              "uac_credential",
              generationKey,
              Json.object("providerId", provider, "kind", "generation", "generation", 0),
              null);
    long generation = Math.addExact(sequence.path("generation").asLong(), 1);
    sequence.put("generation", generation);
    store.save("uac_credential", sequence);
    String hash = Secrets.hash(secret.path("token").asString());
    ObjectNode credential =
        store.all("uac_credential").stream()
            .filter(
                c ->
                    "credential".equals(c.path("kind").asString())
                        && provider.equals(c.path("providerId").asString())
                        && account.equals(c.path("accountId").asString())
                        && hash.equals(c.path("tokenHash").asString())
                        && !c.path("invalid").asBoolean())
            .findFirst()
            .orElse(null);
    if (credential == null)
      credential =
          store.insert(
              "uac_credential",
              UUID.randomUUID().toString(),
              Json.object(
                  "kind",
                  "credential",
                  "providerId",
                  provider,
                  "accountId",
                  account,
                  "envelope",
                  seal(secret, provider),
                  "tokenHash",
                  hash,
                  "version",
                  1,
                  "updatedAt",
                  clock.instant().toString(),
                  "invalid",
                  false),
              null);
    session.put("uacCredentialId", credential.path("id").asString());
    session.put("authGeneration", generation);
    session.put("authenticatedAt", session.path("authTime").asString());
    ObjectNode snapshot = policy.policy();
    session.put("intervalSeconds", snapshot.path("intervalSeconds").asInt());
    session.put("configRevision", snapshot.path("configRevision").asLong());
    Instant next =
        Instant.parse(session.path("authTime").asString())
            .plusSeconds(snapshot.path("intervalSeconds").asInt());
    session.put("nextRevalidateAt", next.toString());
    session.set("uacLoginInfo", Json.object("purpose", "login_info", "status", "pending"));
    session.set(
        "uacSessionBinding",
        Json.object(
            "providerId",
            provider,
            "sourceSessionId",
            session.path("sessionId").asString(),
            "authGeneration",
            generation));
    store.save("session", session);
    String correlation = tx.path("correlationId").asString(tx.path("id").asString());
    enqueue(
        "info:" + session.path("sessionId").asString() + ":" + generation,
        Json.object(
            "kind",
            "info",
            "providerId",
            provider,
            "sessionId",
            session.path("sessionId").asString(),
            "authGeneration",
            generation,
            "credentialId",
            credential.path("id").asString(),
            "correlationId",
            correlation,
            "nextAt",
            clock.instant().toString()));
    enqueue(
        "revalidate:" + session.path("sessionId").asString(),
        Json.object(
            "kind",
            "revalidate",
            "providerId",
            provider,
            "sessionId",
            session.path("sessionId").asString(),
            "authGeneration",
            generation,
            "credentialId",
            credential.path("id").asString(),
            "parentCorrelationId",
            correlation,
            "correlationId",
            UUID.randomUUID().toString(),
            "nextAt",
            next.plusSeconds(jitter(session.path("sessionId").asString(), next)).toString()));
  }

  static long jitter(String session, Instant due) {
    return Long.parseLong(Secrets.hash(session + ":" + due).substring(0, 8), 16) % 31;
  }

  void enqueue(String key, ObjectNode data) {
    store.lock("uac-job:" + key);
    if (store.get("uac_job", key, false) == null) store.insert("uac_job", key, data, null);
    store.uacTenant();
  }

  boolean valid(ObjectNode session) {
    if (!session.has("uacCredentialId")) return !session.has("uacSessionBinding");
    ObjectNode provider = store.get("idp_provider", session.path("providerId").asString(), false);
    ObjectNode credential =
        store.byId("uac_credential", session.path("uacCredentialId").asString(), false);
    return provider != null
        && provider.path("enabled").asBoolean()
        && credential != null
        && !credential.path("invalid").asBoolean()
        && credential.path("providerId").equals(session.path("providerId"))
        && credential.path("accountId").equals(session.path("accountId"))
        && session.path("authGeneration").asLong() > 0
        && (!roundContains(credential, session)
            || !credential.has("retryDeadline")
            || clock
                .instant()
                .isBefore(Instant.parse(credential.path("retryDeadline").asString())));
  }

  ObjectNode credentials(AurigaPrincipal principal) {
    if (principal == null || !principal.tenantId().equals(TenantContext.require()))
      throw new AurigaException(CommonErrors.UNAUTHENTICATED);
    ObjectNode session;
    if (principal.type() == PrincipalType.USER)
      session = sessions.activeById(principal.sessionId());
    else if (principal.type() == PrincipalType.DEVICE
        && principal.deviceId() != null
        && principal.context() == TokenContext.DEVICE) {
      ObjectNode snapshot = store.get("device", principal.deviceId(), false);
      if (snapshot == null) throw new AurigaException(CommonErrors.UNAUTHENTICATED);
      session = sessions.activeById(snapshot.path("sessionId").asString());
      ObjectNode device = store.get("device", principal.deviceId(), true);
      if (device == null
          || device.path("revoked").asBoolean()
          || !"auriga.connect".equals(device.path("scope").asString())
          || session == null
          || !"portal".equals(session.path("audience").asString())
          || !device.path("uacSessionBinding").equals(session.path("uacSessionBinding"))
          || !device.path("accountId").asString().equals(principal.subject()))
        throw new AurigaException(CommonErrors.UNAUTHENTICATED);
    } else throw new AurigaException(CommonErrors.FORBIDDEN);
    if (session == null
        || principal.type() == PrincipalType.USER
            && !principal.context().name().equalsIgnoreCase(session.path("audience").asString())
        || !session.path("accountId").asString().equals(principal.subject())
        || !session.has("uacCredentialId")) throw new AurigaException(CommonErrors.UNAUTHENTICATED);
    ObjectNode credential =
        store.byId("uac_credential", session.path("uacCredentialId").asString(), false);
    ObjectNode secret = open(credential.path("envelope"), session.path("providerId").asString());
    return Json.object(
        "empNo",
        secret.path("empNo"),
        "token",
        secret.path("token"),
        "updatedAt",
        credential.path("updatedAt"));
  }
}
