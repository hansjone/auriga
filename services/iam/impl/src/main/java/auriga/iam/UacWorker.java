package auriga.iam;

import auriga.common.audit.*;
import auriga.common.context.TenantContext;
import java.time.*;
import java.util.*;
import java.util.concurrent.*;
import org.slf4j.LoggerFactory;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** Shared leases fence results; all upstream requests run outside database transactions. */
final class UacWorker
    implements org.springframework.beans.factory.DisposableBean,
        org.springframework.context.ApplicationListener<
            org.springframework.boot.context.event.ApplicationReadyEvent> {
  private final java.util.concurrent.atomic.AtomicBoolean started =
      new java.util.concurrent.atomic.AtomicBoolean();
  private final UacSessions uac;
  private final IamStore store;
  private final AuditLogger audit;
  private final ScheduledExecutorService executor =
      Executors.newSingleThreadScheduledExecutor(
          r -> {
            Thread t = new Thread(r, "iam-uac-worker");
            t.setDaemon(true);
            return t;
          });

  UacWorker(UacSessions uac, AuditLogger audit) {
    this.uac = uac;
    this.store = uac.store;
    this.audit = audit;
  }

  void start() {
    if (!started.compareAndSet(false, true)) return;
    executor.scheduleWithFixedDelay(
        () -> {
          try {
            if (uac.telemetry != null) uac.telemetry.flushQueue();
            UUID cursor = null;
            for (int batch = 0; batch < 20; batch++) {
              List<UUID> tenants = store.uacTenants(cursor);
              if (tenants.isEmpty()) break;
              for (UUID tenant : tenants) TenantContext.run(tenant, () -> runOnce());
              cursor = tenants.getLast();
            }
          } catch (RuntimeException e) {
            LoggerFactory.getLogger(UacWorker.class)
                .error("UAC worker iteration failed ({})", e.getClass().getSimpleName());
          }
        },
        0,
        2,
        TimeUnit.SECONDS);
  }

  int runOnce() {
    if (uac.telemetry != null) {
      uac.telemetry.flushQueue();
      uac.telemetry.flushAudit();
    }
    store.tx(
        () -> {
          retireUnused();
          return store.cleanUac(uac.clock.instant());
        });
    new UacKeyRotation(store, uac.cipher).rotateBatch(50);
    store.tx(
        () -> {
          int expired = 0;
          for (ObjectNode candidate : store.uacExpiredRetries(uac.clock.instant())) {
            if (expired >= 50) break;
            if (candidate.has("retryDeadline")
                && !candidate.path("invalid").asBoolean()
                && !uac.clock
                    .instant()
                    .isBefore(Instant.parse(candidate.path("retryDeadline").asString()))) {
              store.lock("uac-slots:" + candidate.path("providerId").asString());
              store.lock("uac-credential:" + candidate.path("id").asString());
              ObjectNode current =
                  store.byId("uac_credential", candidate.path("id").asString(), true);
              if (current != null
                  && current.has("retryDeadline")
                  && !current.path("invalid").asBoolean()
                  && !uac.clock
                      .instant()
                      .isBefore(Instant.parse(current.path("retryDeadline").asString()))) {
                invalidate(current, "revalidation_unreachable");
                expired++;
              }
            }
          }
          return null;
        });
    int count = 0;
    for (int i = 0; i < 50; i++) {
      ObjectNode job = store.tx(this::claim);
      if (job == null) break;
      try {
        execute(job);
      } catch (RuntimeException e) {
        LoggerFactory.getLogger(UacWorker.class)
            .warn("UAC work deferred ({})", e.getClass().getSimpleName());
      }
      count++;
    }
    if (uac.telemetry != null) {
      uac.telemetry.flushAudit();
      if (uac.telemetry.signals != null) uac.telemetry.signals.flush();
      uac.telemetry.clean();
    }
    return count;
  }

  private void retireUnused() {
    Instant now = uac.clock.instant();
    for (ObjectNode candidate : store.uacRetirementDue(now)) {
      String provider = candidate.path("providerId").asString();
      store.lock("uac-slots:" + provider);
      // Serialize with credential reuse during login, before locking the credential.
      store.lock("uac-account:" + provider + ":" + candidate.path("accountId").asString());
      store.lock("uac-credential:" + candidate.path("id").asString());
      ObjectNode credential = store.byId("uac_credential", candidate.path("id").asString(), true);
      if (credential == null || credential.path("invalid").asBoolean()) continue;
      var sources = store.matching("session", "uacCredentialId", credential.path("id").asString());
      if (sources.stream().anyMatch(uac.sessions::valid)) {
        store.deferUacRetirement(credential.path("id").asString(), now.plusSeconds(86400));
        continue;
      }
      for (ObjectNode source : sources)
        if (!source.path("revoked").asBoolean()) uac.sessions.revokeOne(source, "expired");
      invalidate(credential, "expired");
    }
  }

  private ObjectNode claim() {
    Instant now = uac.clock.instant();
    for (ObjectNode job : store.uacDue(now)) {
      String kind = job.path("kind").asString(), provider = job.path("providerId").asString();
      store.lock("uac-slots:" + provider);
      if (kind.equals("revalidate"))
        store.lock("uac-credential:" + job.path("credentialId").asString());
      job = store.byId("uac_job", job.path("id").asString(), true);
      if (job == null
          || job.path("done").asBoolean()
          || now.isBefore(Instant.parse(job.path("nextAt").asString()))
          || job.has("leaseUntil")
              && now.isBefore(Instant.parse(job.path("leaseUntil").asString()))) continue;
      Integer active =
          store
              .networkJdbc()
              .queryForObject(
                  "SELECT count(*) FROM iam.uac_job WHERE tenant_id=? AND data->>'providerId'=? AND lease_until>?",
                  Integer.class,
                  TenantContext.require(),
                  provider,
                  java.sql.Timestamp.from(now));
      if (active != null && active >= 4) continue;
      if (kind.equals("info")) {
        ObjectNode source = store.byId("session", job.path("sessionId").asString(), true);
        if (source == null
            || !uac.sessions.valid(source)
            || source.path("authGeneration").asLong() != job.path("authGeneration").asLong()) {
          done(job);
          continue;
        }
      }
      if (kind.equals("info") && job.path("started").asBoolean()) {
        job.put("reason", "interrupted");
        uac.observe(
            job,
            "department_lookup",
            "login_info",
            Json.object(),
            () -> Json.object("available", false, "faultReason", "interrupted"));
        info(job, Json.object("available", false));
        done(job);
        continue;
      }
      String token = UUID.randomUUID().toString();
      if (kind.equals("revalidate") && !prepareRevalidation(job, token, now)) continue;
      job.put("started", true);
      if (kind.equals("scan")) job.put("pollAttempt", job.path("pollAttempt").asInt() + 1);
      job.put("leaseToken", token);
      job.put("leaseUntil", now.plusSeconds(35).toString());
      store.save("uac_job", job);
      return job.deepCopy();
    }
    return null;
  }

  @Override
  public void onApplicationEvent(
      org.springframework.boot.context.event.ApplicationReadyEvent event) {
    start();
  }

  private boolean prepareRevalidation(ObjectNode job, String token, Instant now) {
    ObjectNode session = store.byId("session", job.path("sessionId").asString(), true);
    if (session == null
        || session.path("revoked").asBoolean()
        || session.path("authGeneration").asLong() != job.path("authGeneration").asLong()
        || !session.path("uacCredentialId").equals(job.path("credentialId"))) {
      done(job);
      return false;
    }
    ObjectNode credential = store.byId("uac_credential", job.path("credentialId").asString(), true);
    if (credential == null
        || credential.path("invalid").asBoolean()
        || !uac.sessions.valid(session)) {
      uac.sessions.revokeOne(
          session,
          credential != null && credential.has("retryDeadline")
              ? "revalidation_unreachable"
              : "expired");
      done(job);
      return false;
    }
    if (credential.has("leaseUntil")
        && now.isBefore(Instant.parse(credential.path("leaseUntil").asString()))) {
      release(job, now.plusSeconds(1));
      return false;
    }
    Instant due = Instant.parse(session.path("nextRevalidateAt").asString());
    Instant ready =
        credential.has("retryAt")
            ? Instant.parse(credential.path("retryAt").asString())
            : due.plusSeconds(UacSessions.jitter(session.path("sessionId").asString(), due));
    if (now.isBefore(due) || now.isBefore(ready)) {
      release(job, ready);
      return false;
    }
    if (credential.has("roundSessions") && !UacSessions.roundContains(credential, session)) {
      boolean live = false;
      for (JsonNode member : credential.path("roundSessions")) {
        ObjectNode source = store.byId("session", member.path("id").asString(), false);
        if (source != null
            && UacSessions.roundContains(credential, source)
            && uac.sessions.valid(source)) live = true;
      }
      if (live) {
        release(job, now.plusSeconds(1));
        return false;
      }
      clearRound(credential);
      store.save("uac_credential", credential);
    }
    if (!credential.has("roundPolicy")) {
      credential.set("roundPolicy", uac.policy.policy());
      credential.put("roundStartedAt", now.toString());
      credential.put("attempt", 0);
    }
    JsonNode policy = credential.path("roundPolicy");
    int attempt = credential.path("attempt").asInt();
    Instant deadline =
        credential.has("retryDeadline")
            ? Instant.parse(credential.path("retryDeadline").asString())
            : Instant.parse(credential.path("roundStartedAt").asString())
                .plusSeconds(
                    policy.path("retryIntervalSeconds").asLong()
                            * policy.path("maxRetries").asLong()
                        + 3);
    if (attempt > 0
        && (attempt >= 1 + policy.path("maxRetries").asInt() || !now.isBefore(deadline))) {
      invalidate(credential, "revalidation_unreachable");
      done(job);
      return false;
    }
    credential.put("attempt", attempt + 1);
    credential.put("leaseToken", token);
    credential.put("leaseUntil", now.plusSeconds(35).toString());
    store.save("uac_credential", credential);
    job.put("credentialVersion", credential.path("version").asLong());
    job.set("roundPolicy", policy);
    job.put("attempt", attempt + 1);
    job.set("strategy", policy.path("strategy"));
    job.set("maxRetries", policy.path("maxRetries"));
    job.set("intervalSeconds", policy.path("intervalSeconds"));
    job.set("configRevision", policy.path("configRevision"));
    job.set("nextRevalidateAt", session.path("nextRevalidateAt"));
    job.put("dueLagMs", Math.max(0, Duration.between(due, now).toMillis()));
    if (credential.has("retryAt")) job.set("retryAt", credential.path("retryAt"));
    if (credential.has("retryDeadline")) job.set("retryDeadline", credential.path("retryDeadline"));
    var dueSessions = Json.MAPPER.createArrayNode();
    for (ObjectNode candidate : store.uacDueSessions(credential.path("id").asString(), now))
      if (candidate.path("uacCredentialId").equals(credential.path("id"))
          && !candidate.path("revoked").asBoolean()
          && candidate.has("nextRevalidateAt")) {
        Instant next = Instant.parse(candidate.path("nextRevalidateAt").asString());
        if (!now.isBefore(next)
            && !now.isBefore(
                next.plusSeconds(UacSessions.jitter(candidate.path("sessionId").asString(), next))))
          dueSessions.add(
              Json.object(
                  "id",
                  candidate.path("id"),
                  "generation",
                  candidate.path("authGeneration"),
                  "due",
                  candidate.path("nextRevalidateAt")));
      }
    if (!credential.has("roundSessions")) {
      credential.set("roundSessions", dueSessions);
      store.save("uac_credential", credential);
    }
    job.set("dueSessions", credential.path("roundSessions"));
    return true;
  }

  private ObjectNode current(ObjectNode snapshot) {
    ObjectNode job = store.byId("uac_job", snapshot.path("id").asString(), true);
    return job != null
            && !job.path("done").asBoolean()
            && job.path("leaseToken").equals(snapshot.path("leaseToken"))
            && uac.clock.instant().isBefore(Instant.parse(job.path("leaseUntil").asString()))
        ? job
        : null;
  }

  private void execute(ObjectNode snapshot) {
    auriga.common.context.RequestContext.start(null, null).run(() -> executeTraced(snapshot));
  }

  private void executeTraced(ObjectNode snapshot) {
    String kind = snapshot.path("kind").asString();
    ObjectNode input =
        store.tx(
            () -> {
              ObjectNode job = current(snapshot);
              if (job == null) return null;
              ObjectNode provider =
                  store.get("idp_provider", job.path("providerId").asString(), false);
              if (provider == null || !provider.path("enabled").asBoolean()) {
                if (kind.equals("scan"))
                  uac.observe(
                      job,
                      "scan_status",
                      null,
                      Json.object(),
                      () -> Json.object("state", "cancelled", "faultReason", "provider_disabled"));
                done(job);
                return null;
              }
              if (kind.equals("scan")) {
                ObjectNode challenge =
                    store.get("uac_challenge", job.path("challengeId").asString(), false);
                if (challenge == null
                    || !Set.of("waiting", "scanned").contains(challenge.path("state").asString())
                    || !uac.clock
                        .instant()
                        .isBefore(Instant.parse(challenge.path("expiresAt").asString()))) {
                  uac.observe(
                      job,
                      "scan_status",
                      null,
                      Json.object(),
                      () ->
                          Json.object(
                              "state",
                              challenge == null
                                  ? "stopped"
                                  : !uac.clock
                                          .instant()
                                          .isBefore(
                                              Instant.parse(challenge.path("expiresAt").asString()))
                                      ? "expired"
                                      : "cancelled"));
                  done(job);
                  return null;
                }
                return Json.object(
                    "config",
                    uac.providers.decryptedConfig(provider),
                    "coordinates",
                    challenge.path("coordinates"));
              }
              ObjectNode credential =
                  store.byId("uac_credential", job.path("credentialId").asString(), false);
              if (credential == null || credential.path("invalid").asBoolean()) {
                done(job);
                return null;
              }
              return Json.object(
                  "config",
                  uac.providers.decryptedConfig(provider),
                  "credential",
                  uac.open(credential.path("envelope"), job.path("providerId").asString()));
            });
    if (input == null) return;
    ObjectNode traceCredential =
        input.path("credential").isObject()
            ? ((ObjectNode) input.path("credential")).deepCopy()
            : Json.object();
    traceCredential.set("systemCode", input.path("config").path("systemCode"));
    ObjectNode metadata = snapshot.deepCopy();
    if (kind.equals("scan")) {
      metadata.put("pollId", UUID.randomUUID().toString());
      metadata.put("attempt", snapshot.path("pollAttempt").asInt());
    }
    String purpose = kind.equals("scan") ? null : kind.equals("info") ? "login_info" : "revalidate";
    String stage =
        kind.equals("scan")
            ? "scan_status"
            : kind.equals("info")
                    || "department".equals(snapshot.path("roundPolicy").path("strategy").asString())
                ? "department_lookup"
                : "token_verify";
    UacTelemetry.Span round =
        uac.telemetry != null && kind.equals("revalidate")
            ? uac.telemetry.begin(metadata, "session_revalidate", "revalidate", traceCredential)
            : null;
    try {
      JsonNode result =
          uac.observe(
              metadata,
              stage,
              purpose,
              traceCredential,
              () ->
                  switch (kind) {
                    case "scan" ->
                        uac.adapter.scan(input.path("config"), input.path("coordinates"));
                    case "info" ->
                        uac.adapter.department(
                            input.path("config"), input.path("credential"), "login_info");
                    case "revalidate" ->
                        "department"
                                .equals(snapshot.path("roundPolicy").path("strategy").asString())
                            ? uac.adapter.department(
                                input.path("config"), input.path("credential"), "revalidate")
                            : uac.adapter.verify(input.path("config"), input.path("credential"));
                    default -> throw new IllegalStateException("Unsupported UAC work kind");
                  });
      if (kind.equals("scan")
          && !Set.of("waiting", "scanned", "unavailable").contains(result.path("state").asString())
          && uac.telemetry != null) {
        ObjectNode acquiredMetadata = metadata.deepCopy();
        if (result.path("_uacHttp").isObject())
          acquiredMetadata.setAll((ObjectNode) result.path("_uacHttp"));
        var acquired = uac.telemetry.begin(acquiredMetadata, "token_acquire", null, result);
        acquired.end(
            "verified".equals(result.path("state").asString()) ? "success" : "failure", result);
      }
      String applied =
          store.tx(
              () -> {
                if (kind.equals("revalidate")) {
                  store.lock("uac-slots:" + snapshot.path("providerId").asString());
                  store.lock("uac-credential:" + snapshot.path("credentialId").asString());
                }
                ObjectNode job = current(snapshot);
                if (job == null) return "skipped";
                switch (kind) {
                  case "scan" -> scan(job, result);
                  case "info" -> {
                    info(job, result);
                    done(job);
                  }
                  case "revalidate" -> {
                    return revalidate(job, result, input.path("credential"));
                  }
                  default -> throw new IllegalStateException();
                }
                return null;
              });
      if (round != null) {
        ObjectNode current =
            store.tx(
                () ->
                    store.byId("uac_credential", snapshot.path("credentialId").asString(), false));
        ObjectNode end = Json.object();
        if (current != null) UacTelemetry.copySafe(current, end, false);
        UacTelemetry.copySafe(result, end, false);
        boolean success =
            result.path("success").asBoolean() || result.path("available").asBoolean();
        end.put("retry_pending", current != null && current.has("retryAt"));
        end.put(
            "retry_exhausted",
            !success
                && !"skipped".equals(applied)
                && snapshot.path("attempt").asInt() >= 1 + snapshot.path("maxRetries").asInt()
                && "upstream_fault".equals(result.path("failureClass").asString()));
        if ("skipped".equals(applied)) end.put("faultReason", "late_result");
        else if (result.has("token")) end.set("token", result.path("token"));
        ObjectNode source =
            store.tx(() -> store.byId("session", snapshot.path("sessionId").asString(), false));
        if (source != null) UacTelemetry.copySafe(source, end, false);
        round.end(applied == null ? "failure" : applied, end);
      }
    } finally {
      if (round != null && !round.ended)
        round.end("failure", Json.object("faultReason", "transport_error"));
    }
  }

  private void scan(ObjectNode job, JsonNode result) {
    ObjectNode challenge = store.get("uac_challenge", job.path("challengeId").asString(), true);
    if (challenge == null
        || !Set.of("waiting", "scanned").contains(challenge.path("state").asString())) {
      done(job);
      return;
    }
    ObjectNode provider = store.get("idp_provider", job.path("providerId").asString(), false);
    if (provider == null
        || !provider.path("enabled").asBoolean()
        || !UacProviderService.etag(provider).equals(challenge.path("providerRevision").asString())
        || !uac.clock.instant().isBefore(Instant.parse(challenge.path("expiresAt").asString()))) {
      challenge.put("state", "expired");
      store.save("uac_challenge", challenge);
      done(job);
      return;
    }
    String state = result.path("state").asString("failed");
    IamError.validate(
        Set.of("waiting", "scanned", "verified", "denied", "unavailable", "failed")
            .contains(state));
    if (state.equals("unavailable")) {
      int failures = job.path("failures").asInt() + 1;
      job.put("failures", failures);
      release(job, uac.clock.instant().plusSeconds(Math.min(30, 2L << Math.min(failures - 1, 4))));
      return;
    }
    challenge.put("state", state);
    if (state.equals("verified")) {
      IamError.validate(
          result.path("token").isString()
              && !result.path("token").asString().isBlank()
              && result.path("token").asString().length() <= 16384
              && result.path("empNo").isString()
              && !result.path("empNo").asString().isBlank()
              && result.path("empNo").asString().length() <= 128);
      challenge.set(
          "credential",
          uac.seal(
              Json.object(
                  "token",
                  result.path("token"),
                  "empNo",
                  result.path("empNo"),
                  "clientIP",
                  result.path("clientIP").asString("")),
              job.path("providerId").asString()));
    } else if (result.has("failureCode"))
      challenge.put(
          "failureCode",
          Set.of(
                      "missing_token",
                      "missing_emp_no",
                      "upstream_rejected",
                      "invalid_response",
                      "transport_error")
                  .contains(result.path("failureCode").asString())
              ? result.path("failureCode").asString()
              : "invalid_response");
    store.save("uac_challenge", challenge);
    if (Set.of("waiting", "scanned").contains(state))
      release(job, uac.clock.instant().plusSeconds(2));
    else done(job);
  }

  private void info(ObjectNode job, JsonNode result) {
    ObjectNode session = store.byId("session", job.path("sessionId").asString(), true);
    if (session == null
        || !uac.sessions.valid(session)
        || !session.path("authGeneration").equals(job.path("authGeneration"))) return;
    boolean available = result.path("available").asBoolean();
    ObjectNode info =
        Json.object(
            "purpose",
            "login_info",
            "status",
            available ? "available" : "unavailable",
            "updatedAt",
            uac.clock.instant().toString());
    if (available) info.set("department", result.path("department"));
    new ContractSchemas().validate("auth/uac-login-info.schema.json#/$defs/UacLoginInfo", info);
    session.set("uacLoginInfo", info);
    store.save("session", session);
  }

  private String revalidate(ObjectNode job, JsonNode result, JsonNode oldSecret) {
    ObjectNode credential = store.byId("uac_credential", job.path("credentialId").asString(), true);
    if (credential == null
        || credential.path("invalid").asBoolean()
        || !credential.path("version").equals(job.path("credentialVersion"))
        || !credential.path("leaseToken").equals(job.path("leaseToken"))) {
      done(job);
      return "skipped";
    }
    boolean live = false;
    for (JsonNode due : job.path("dueSessions")) {
      ObjectNode source = store.byId("session", due.path("id").asString(), true);
      if (source != null
          && source.path("authGeneration").equals(due.path("generation"))
          && source.path("nextRevalidateAt").equals(due.path("due"))
          && uac.sessions.valid(source)) live = true;
    }
    if (!live) {
      clearRound(credential);
      store.save("uac_credential", credential);
      done(job);
      return "skipped";
    }
    Instant now = uac.clock.instant();
    boolean department = "department".equals(job.path("roundPolicy").path("strategy").asString());
    boolean success =
        department ? result.path("available").asBoolean() : result.path("success").asBoolean();
    if (credential.has("retryDeadline")
        && !now.isBefore(Instant.parse(credential.path("retryDeadline").asString()))) {
      invalidate(credential, "revalidation_unreachable");
      done(job);
      return "failure";
    }
    if (success) {
      if (!department) {
        IamError.validate(
            result.path("token").isString() && !result.path("token").asString().isBlank());
        ObjectNode updated = ((ObjectNode) oldSecret).deepCopy();
        updated.set("token", result.path("token"));
        credential.set("envelope", uac.seal(updated, job.path("providerId").asString()));
        credential.put("tokenHash", Secrets.hash(updated.path("token").asString()));
        credential.put("version", credential.path("version").asLong() + 1);
        credential.put("updatedAt", now.toString());
      }
      for (String field :
          List.of(
              "leaseToken",
              "leaseUntil",
              "retryAt",
              "retryDeadline",
              "roundPolicy",
              "roundStartedAt",
              "attempt",
              "roundSessions")) credential.remove(field);
      store.save("uac_credential", credential);
      ObjectNode policy = uac.policy.policy();
      for (JsonNode due : job.path("dueSessions")) {
        ObjectNode session = store.byId("session", due.path("id").asString(), true);
        if (session == null
            || !session.path("authGeneration").equals(due.path("generation"))
            || !session.path("nextRevalidateAt").equals(due.path("due"))
            || !uac.sessions.valid(session)) continue;
        Instant next = now.plusSeconds(policy.path("intervalSeconds").asLong());
        session.put("lastRevalidatedAt", now.toString());
        session.put("nextRevalidateAt", next.toString());
        session.put("intervalSeconds", policy.path("intervalSeconds").asInt());
        session.put("configRevision", policy.path("configRevision").asLong());
        store.save("session", session);
        ObjectNode other =
            store.get("uac_job", "revalidate:" + session.path("sessionId").asString(), true);
        if (other != null) {
          other.put("correlationId", UUID.randomUUID().toString());
          release(
              other,
              next.plusSeconds(UacSessions.jitter(session.path("sessionId").asString(), next)));
        }
      }
      return "success";
    }
    if (department
        || Set.of("business_rejected", "identity_mismatch")
            .contains(result.path("failureClass").asString())) {
      invalidate(credential, "revalidation_rejected");
      done(job);
      return "failure";
    }
    JsonNode policy = credential.path("roundPolicy");
    if (!credential.has("retryDeadline"))
      credential.put(
          "retryDeadline",
          now.plusSeconds(
                  policy.path("retryIntervalSeconds").asLong() * policy.path("maxRetries").asLong()
                      + 3)
              .toString());
    if (credential.path("attempt").asInt() >= 1 + policy.path("maxRetries").asInt()) {
      invalidate(credential, "revalidation_unreachable");
      done(job);
      return "failure";
    }
    Instant retry = now.plusSeconds(policy.path("retryIntervalSeconds").asLong());
    credential.put("retryAt", retry.toString());
    credential.remove("leaseToken");
    credential.remove("leaseUntil");
    store.save("uac_credential", credential);
    release(job, retry);
    return "failure";
  }

  private void invalidate(ObjectNode credential, String reason) {
    for (ObjectNode session :
        store.matching("session", "uacCredentialId", credential.path("id").asString()))
      if (session.path("uacCredentialId").equals(credential.path("id"))
          && UacSessions.roundContains(credential, session)
          && !session.path("revoked").asBoolean()) uac.sessions.revokeOne(session, reason);
    boolean remains =
        store.matching("session", "uacCredentialId", credential.path("id").asString()).stream()
            .anyMatch(
                s ->
                    s.path("uacCredentialId").equals(credential.path("id"))
                        && !s.path("revoked").asBoolean());
    credential.put("invalid", !remains);
    if (!remains) credential.put("expiresAt", uac.clock.instant().plusSeconds(86400).toString());
    clearRound(credential);
    store.save("uac_credential", credential);
  }

  private void clearRound(ObjectNode credential) {
    for (String field :
        List.of(
            "leaseToken",
            "leaseUntil",
            "retryAt",
            "retryDeadline",
            "roundPolicy",
            "roundStartedAt",
            "attempt",
            "roundSessions")) credential.remove(field);
  }

  private void release(ObjectNode job, Instant next) {
    job.remove("leaseToken");
    job.remove("leaseUntil");
    job.put("nextAt", next.toString());
    store.save("uac_job", job);
  }

  private void done(ObjectNode job) {
    job.put("done", true);
    job.put("expiresAt", uac.clock.instant().plusSeconds(300).toString());
    job.remove("leaseToken");
    job.remove("leaseUntil");
    store.save("uac_job", job);
  }

  @Override
  public void destroy() {
    executor.shutdownNow();
  }
}
