package auriga.iam;

import auriga.common.audit.*;
import auriga.common.context.*;
import java.time.*;
import java.util.*;
import java.util.concurrent.ArrayBlockingQueue;
import java.util.function.Supplier;
import org.slf4j.LoggerFactory;
import org.springframework.transaction.support.*;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** Closed projections are written before any logger, outbox or diagnostic storage sink. */
final class UacTelemetry {
  static final Set<String> STAGES =
      Set.of(
          "sso_cookie_check",
          "challenge_start",
          "scan_status",
          "token_acquire",
          "department_lookup",
          "token_verify",
          "session_revalidate",
          "session_revoke");
  private static final Set<String> OUTCOMES =
      Set.of(
          "start",
          "success",
          "failure",
          "waiting",
          "scanned",
          "verified",
          "denied",
          "unavailable",
          "failed",
          "cancelled",
          "expired",
          "queued",
          "completed",
          "stopped",
          "skipped",
          "scan_required",
          "authenticated");
  private static final Set<String> REASONS =
      Set.of(
          "business_rejected",
          "identity_mismatch",
          "upstream_fault",
          "retry_exhausted",
          "transport_error",
          "http_error",
          "invalid_json",
          "missing_code",
          "invalid_code",
          "invalid_success_payload",
          "missing_department",
          "missing_token",
          "missing_emp_no",
          "upstream_rejected",
          "invalid_response",
          "provider_disabled",
          "logout",
          "security",
          "revalidation_failed",
          "revalidation_rejected",
          "revalidation_unreachable",
          "missing",
          "suppressed",
          "disabled",
          "ineligible",
          "interrupted",
          "late_result",
          "not_accepted");
  private static final ScopedValue<Span> CURRENT = ScopedValue.newInstance();

  private record Pending(UUID tenant, ObjectNode event, boolean audit) {}

  private final ArrayBlockingQueue<Pending> retry = new ArrayBlockingQueue<>(1024);
  private final IamStore store;
  private final UacDiagnosticSettings settings;
  private final AuditLogger audit;
  private final Clock clock;
  UacSignals signals;

  UacTelemetry(IamStore store, UacDiagnosticSettings settings, AuditLogger audit, Clock clock) {
    this.store = store;
    this.settings = settings;
    this.audit = audit;
    this.clock = clock;
  }

  final class Span {
    final ObjectNode metadata;
    final String stage, purpose;
    final UUID tenant;
    final TraceContext trace;
    final long started = System.nanoTime(), detailRevision;
    final List<String> sensitive;
    final ObjectNode exchange = Json.object();
    ObjectNode detail;
    boolean ended;

    Span(ObjectNode metadata, String stage, String purpose, JsonNode credential) {
      if (!STAGES.contains(stage)
          || purpose != null && !Set.of("login_info", "login_sso", "revalidate").contains(purpose))
        throw new IllegalArgumentException("Invalid UAC diagnostic stage");
      this.metadata = metadata.deepCopy();
      this.stage = stage;
      this.purpose = purpose;
      this.tenant = TenantContext.require();
      trace = RequestContext.current().map(RequestContext::trace).orElseGet(TraceContext::start);
      sensitive = new ArrayList<>();
      for (String field : List.of("token", "empNo", "systemCode"))
        if (credential.path(field).isString()) sensitive.add(credential.path(field).asString());
      this.metadata.put(
          "tokenHashPrefix",
          UacDiagnosticSanitizer.tokenHashPrefix(credential.path("token").asString(null)));
      if (credential.path("empNo").isString()) this.metadata.set("empNo", credential.path("empNo"));
      if (credential.has("token"))
        this.metadata.put("tokenMissing", credential.path("token").asString().isEmpty());
      if (stage.equals("token_acquire"))
        this.metadata.put(
            "tokenMissing",
            !credential.path("token").isString() || credential.path("token").asString().isEmpty());
      if (credential.has("cookieFamily"))
        this.metadata.set("cookieFamily", credential.path("cookieFamily"));
      detailRevision = settings.capture(metadata.path("providerId").asString());
      emit(event("start", "start", 0, Json.object()), false, false);
    }

    <T> T call(Supplier<T> operation) {
      return ScopedValue.where(CURRENT, this).call(operation::get);
    }

    void end(String outcome, JsonNode extra) {
      if (ended) return;
      ended = true;
      ObjectNode result =
          event("end", outcome, Math.max(0, (System.nanoTime() - started) / 1_000_000), extra);
      emit(result, true, false);
      if (detail != null
          && settings.permits(metadata.path("providerId").asString(), detailRevision)) {
        ObjectNode detailed = result.deepCopy();
        detailed.put("eventId", UUID.randomUUID().toString());
        detailed.put("level", "DEBUG");
        detailed.put("diagnosticRevision", detailRevision);
        detailed.setAll(detail);
        emit(detailed, false, true);
      }
    }

    ObjectNode event(String phase, String outcome, long duration, JsonNode extra) {
      if (!OUTCOMES.contains(outcome)) outcome = "failure";
      ObjectNode event = Json.object();
      for (String key :
          List.of(
              "timestamp",
              "service",
              "level",
              "stage",
              "phase",
              "result",
              "durationMs",
              "correlationId",
              "eventId",
              "transactionId",
              "pollId",
              "attempt",
              "parentCorrelationId",
              "trace_id",
              "spanId",
              "requestId",
              "tenantId",
              "providerId",
              "configRevision",
              "empNo",
              "chain",
              "purpose",
              "tokenHashPrefix",
              "tokenMissing",
              "endpoint",
              "sourceAlias",
              "httpStatus",
              "udsCode",
              "udsSubCode",
              "udsMessage",
              "truncated",
              "departmentPresent",
              "matchingCount",
              "cookiePresent",
              "cookieFamily",
              "eligible",
              "suppressed",
              "fallbackReason",
              "failureClass",
              "faultReason",
              "retryAt",
              "retryDeadline",
              "tokenReplaced",
              "nextRevalidateAt",
              "responseBytes",
              "contentType")) event.putNull(key);
      event.put("timestamp", clock.instant().toString());
      event.put("service", "iam");
      event.put("stage", stage);
      event.put("phase", phase);
      event.put("result", outcome);
      event.put("durationMs", duration);
      event.put("eventId", auriga.common.persistence.UuidV7.next().toString());
      event.put("tenantId", tenant.toString());
      event.put("trace_id", trace.traceId());
      event.put("spanId", trace.spanId());
      event.put(
          "level",
          Set.of(
                      "start",
                      "success",
                      "waiting",
                      "scanned",
                      "verified",
                      "completed",
                      "authenticated",
                      "skipped")
                  .contains(outcome)
              ? "INFO"
              : "WARN");
      event.put(
          "chain",
          stage.equals("session_revoke")
              ? "revocation"
              : "revalidate".equals(purpose) || stage.equals("session_revalidate")
                  ? "revalidation"
                  : "login");
      if (purpose != null) event.put("purpose", purpose);
      event.put(
          "endpoint",
          switch (stage) {
            case "scan_status", "token_acquire" -> "scan";
            case "department_lookup" -> "department";
            case "token_verify" -> "token-verify";
            default -> "local";
          });
      event.put("sourceAlias", "uac");
      copySafe(metadata, event, true);
      copySafe(exchange, event, false);
      copySafe(extra, event, false);
      if (extra.path("token").isString()) {
        String hash = UacDiagnosticSanitizer.tokenHashPrefix(extra.path("token").asString());
        if (stage.equals("scan_status") || stage.equals("token_acquire"))
          event.put("tokenHashPrefix", hash);
        if (stage.equals("token_verify") && !extra.path("token").asString().isEmpty())
          event.put(
              "tokenReplaced",
              !Objects.equals(hash, metadata.path("tokenHashPrefix").asString(null)));
      }
      RequestContext.current()
          .map(RequestContext::requestId)
          .filter(UacTelemetry::uuid)
          .ifPresent(v -> event.put("requestId", v));
      return event;
    }
  }

  Span begin(ObjectNode metadata, String stage, String purpose, JsonNode credential) {
    return new Span(metadata, stage, purpose, credential);
  }

  JsonNode operation(
      ObjectNode metadata,
      String stage,
      String purpose,
      JsonNode credential,
      Supplier<JsonNode> operation) {
    Span span = begin(metadata, stage, purpose, credential);
    try {
      JsonNode result = span.call(operation);
      String outcome =
          stage.equals("scan_status")
              ? result.path("state").asString("failed")
              : result.path("available").asBoolean() || result.path("success").asBoolean()
                  ? "success"
                  : "failure";
      span.end(outcome, result);
      if (result.isObject()) {
        var projected = ((ObjectNode) result).deepCopy();
        projected.set("_uacHttp", span.exchange.deepCopy());
        return projected;
      }
      return result;
    } catch (RuntimeException e) {
      span.end("failure", Json.object("faultReason", "transport_error"));
      throw e;
    }
  }

  static void capture(int status, byte[] bytes) {
    if (!CURRENT.isBound()) return;
    Span span = CURRENT.get();
    span.exchange.put("httpStatus", status);
    span.exchange.put("responseBytes", bytes.length);
    ObjectNode safe = UacDiagnosticSanitizer.sanitize(bytes, span.sensitive);
    JsonNode body = safe.path("udsRawSanitized");
    for (var pair :
        List.of(new String[] {"udsCode", "/code/code"}, new String[] {"udsSubCode", "/bo/code"})) {
      JsonNode code = body.at(pair[1]);
      if (code.isString() && code.asString().matches("[0-9]{1,8}"))
        span.exchange.set(pair[0], code);
    }
    if (safe.path("truncated").asBoolean()) span.exchange.put("truncated", true);
    JsonNode message = body.at("/code/message");
    if (message.isString()
        && Set.of("success", "waiting", "rejected", "unavailable", "[REDACTED]")
            .contains(message.asString())) span.exchange.set("udsMessage", message);
    if (span.detailRevision >= 0) span.detail = safe;
  }

  private static boolean uuid(String value) {
    return value != null
        && value.matches(
            "[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}");
  }

  static void copySafe(JsonNode input, ObjectNode event, boolean identifiers) {
    if (identifiers)
      for (String key :
          List.of("correlationId", "parentCorrelationId", "transactionId", "pollId", "providerId"))
        if (uuid(input.path(key).asString(null))) event.set(key, input.path(key));
    for (String key : List.of("attempt", "configRevision", "matchingCount", "responseBytes"))
      if (input.path(key).isIntegralNumber()
          && input.path(key).asLong(-1) >= 0
          && input.path(key).asLong() <= 9007199254740991L) event.set(key, input.path(key));
    for (String key :
        List.of(
            "departmentPresent",
            "tokenMissing",
            "tokenReplaced",
            "cookiePresent",
            "eligible",
            "suppressed",
            "truncated")) if (input.path(key).isBoolean()) event.set(key, input.path(key));
    if (input.path("available").isBoolean())
      event.set("departmentPresent", input.path("available"));
    for (String key : List.of("fallbackReason", "failureClass", "faultReason"))
      if (REASONS.contains(input.path(key).asString())) event.set(key, input.path(key));
    if (REASONS.contains(input.path("failureCode").asString()))
      event.set("faultReason", input.path("failureCode"));
    if (input.path("empNo").asString().matches("[A-Za-z0-9_-]{1,128}"))
      event.set("empNo", input.path("empNo"));
    if (input.path("tokenHashPrefix").asString().matches("[a-f0-9]{12}"))
      event.set("tokenHashPrefix", input.path("tokenHashPrefix"));
    if (Set.of("portal", "uds", "none").contains(input.path("cookieFamily").asString()))
      event.set("cookieFamily", input.path("cookieFamily"));
    if (input.path("httpStatus").asInt() >= 100 && input.path("httpStatus").asInt() <= 599)
      event.set("httpStatus", input.path("httpStatus"));
    for (String key : List.of("udsCode", "udsSubCode"))
      if (input.path(key).asString().matches("[0-9]{1,8}")) event.set(key, input.path(key));
    if (Set.of("success", "waiting", "rejected", "unavailable", "[REDACTED]")
        .contains(input.path("udsMessage").asString()))
      event.set("udsMessage", input.path("udsMessage"));
    for (String key : List.of("retryAt", "retryDeadline", "nextRevalidateAt"))
      if (input.path(key).isString())
        try {
          event.put(key, Instant.parse(input.path(key).asString()).toString());
        } catch (RuntimeException ignored) {
        }
  }

  private void emit(ObjectNode event, boolean audited, boolean detailed) {
    var logger = LoggerFactory.getLogger(detailed ? "auriga.iam.uac.detail" : "auriga.iam.uac");
    var writer =
        detailed
            ? logger.atDebug()
            : "WARN".equals(event.path("level").asString()) ? logger.atWarn() : logger.atInfo();
    try {
      writer.log("{}", event.toString());
    } catch (RuntimeException ignored) {
    }
    Pending pending = new Pending(TenantContext.require(), event.deepCopy(), audited);
    boolean accepted = persist(pending);
    if (TransactionSynchronizationManager.isSynchronizationActive()) {
      TransactionSynchronizationManager.registerSynchronization(
          new TransactionSynchronization() {
            @Override
            public void afterCompletion(int status) {
              if (!accepted || status != STATUS_COMMITTED) enqueue(pending);
            }
          });
    } else if (!accepted) enqueue(pending);
  }

  private void enqueue(Pending pending) {
    if (!retry.offer(pending))
      LoggerFactory.getLogger("auriga.iam.uac")
          .error(
              "UAC diagnostic queue full; eventId={}", pending.event().path("eventId").asString());
  }

  private boolean persist(Pending pending) {
    if ("DEBUG".equals(pending.event().path("level").asString())
        && !settings.permits(
            pending.event().path("providerId").asString(),
            pending.event().path("diagnosticRevision").asLong(-1))) return true;
    try {
      return store.tx(
          () -> {
            try {
              store.atomic(
                  () -> {
                    String key = "event:" + pending.event().path("eventId").asString();
                    if (store.get("uac_diagnostics", key, false) == null) {
                      store.insert(
                          "uac_diagnostics",
                          key,
                          Json.object(
                              "kind",
                              "event",
                              "providerId",
                              pending.event().path("providerId"),
                              "event",
                              pending.event(),
                              "auditPending",
                              pending.audit()),
                          clock.instant().plusSeconds(3 * 86400));
                      if (signals != null) signals.record(pending.event());
                    }
                    store.uacTenant();
                    return null;
                  });
              return true;
            } catch (RuntimeException failure) {
              return false;
            }
          });
    } catch (RuntimeException failure) {
      LoggerFactory.getLogger("auriga.iam.uac")
          .warn(
              "UAC diagnostic storage deferred; eventId={}",
              pending.event().path("eventId").asString());
      return false;
    }
  }

  void flushQueue() {
    for (int i = 0; i < 50; i++) {
      Pending pending = retry.poll();
      if (pending == null) break;
      TenantContext.run(
          pending.tenant(),
          () -> {
            if (!persist(pending)) enqueue(pending);
          });
    }
  }

  void flushAudit() {
    for (int i = 0; i < 50; i++) {
      try {
        boolean found =
            store.tx(
                () ->
                    store.atomic(
                        () -> {
                          var rows =
                              store
                                  .networkJdbc()
                                  .query(
                                      "SELECT data::text FROM iam.uac_diagnostics WHERE tenant_id=? AND data->>'auditPending'='true' ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED",
                                      (rs, n) -> Json.read(rs.getString(1)),
                                      TenantContext.require());
                          if (rows.isEmpty()) return false;
                          ObjectNode row = rows.getFirst(), event = (ObjectNode) row.path("event");
                          var builder =
                              AuditEntry.builder("iam.uac." + event.path("stage").asString())
                                  .actor(AuditEntry.Actor.SYSTEM)
                                  .target("provider", event.path("providerId").asString())
                                  .result(
                                      "WARN".equals(event.path("level").asString())
                                          ? AuditEntry.Result.FAILURE
                                          : AuditEntry.Result.SUCCESS);
                          builder.detail("outcome", event.path("result").asString());
                          for (var entry : event.properties())
                            if (!entry.getValue().isNull())
                              builder.detail(
                                  entry.getKey(),
                                  Json.MAPPER.convertValue(entry.getValue(), Object.class));
                          IamAudit.record(audit, builder.build());
                          row.put("auditPending", false);
                          store.save("uac_diagnostics", row);
                          return true;
                        }));
        if (!found) break;
      } catch (RuntimeException failure) {
        LoggerFactory.getLogger("auriga.iam.uac").warn("UAC audit delivery deferred");
        break;
      }
    }
  }

  void clean() {
    store.tx(
        () -> {
          store
              .networkJdbc()
              .update(
                  "DELETE FROM iam.uac_diagnostics WHERE tenant_id=? AND id IN (SELECT id FROM iam.uac_diagnostics WHERE tenant_id=? AND expires_at<? AND data->>'auditPending'='false' ORDER BY id LIMIT 200)",
                  TenantContext.require(),
                  TenantContext.require(),
                  java.sql.Timestamp.from(clock.instant()));
          return null;
        });
  }
}
