package auriga.audit;

import java.util.*;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.json.JsonMapper;
import tools.jackson.databind.node.ObjectNode;

/** Audit independently validates the producer's minimal UAC projection. */
final class UacAuditProjection {
  private static final Set<String> ACTIONS =
      Set.of(
          "sso_cookie_check",
          "challenge_start",
          "scan_status",
          "token_acquire",
          "department_lookup",
          "token_verify",
          "session_revalidate",
          "session_revoke");
  private static final Set<String> ENUMS =
      Set.of(
          "login",
          "login_info",
          "login_sso",
          "revalidate",
          "revalidation",
          "revocation",
          "success",
          "failure",
          "denied",
          "waiting",
          "scanned",
          "verified",
          "unavailable",
          "cancelled",
          "expired",
          "queued",
          "completed",
          "stopped",
          "skipped",
          "scan_required",
          "authenticated",
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
          "provider_disabled",
          "logout",
          "security",
          "revalidation_failed",
          "revalidation_rejected",
          "revalidation_unreachable",
          "interrupted",
          "late_result",
          "not_accepted",
          "portal",
          "uds",
          "none",
          "missing",
          "suppressed",
          "disabled",
          "ineligible");

  static ObjectNode project(JsonNode data) {
    String action = data.path("action").asString();
    if (action.equals("iam.uac.diagnostics.export")) return export(data.path("details"));
    if (!action.startsWith("iam.uac.") || !ACTIONS.contains(action.substring(8))) return null;
    ObjectNode out = JsonMapper.builder().build().createObjectNode();
    JsonNode input = data.path("details");
    for (String field :
        List.of(
            "correlationId",
            "parentCorrelationId",
            "transactionId",
            "pollId",
            "providerId",
            "eventId")) {
      var value = input.path(field);
      if (value.isString()
          && value
              .asString()
              .matches(
                  "[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}"))
        out.set(field, value);
    }
    for (String field :
        List.of(
            "purpose",
            "chain",
            "outcome",
            "failureClass",
            "faultReason",
            "cookieFamily",
            "fallbackReason")) {
      var value = input.path(field);
      if (value.isString() && ENUMS.contains(value.asString())) out.set(field, value);
    }
    for (String field :
        List.of(
            "departmentPresent",
            "tokenMissing",
            "tokenReplaced",
            "cookiePresent",
            "eligible",
            "suppressed",
            "identityMatched",
            "retry_pending",
            "retry_exhausted"))
      if (input.path(field).isBoolean()) out.set(field, input.path(field));
    for (String field :
        List.of(
            "durationMs",
            "attempt",
            "configRevision",
            "matchingCount",
            "credentialVersion",
            "authGeneration",
            "intervalSeconds",
            "maxRetries",
            "dueLagMs"))
      if (input.path(field).isIntegralNumber()
          && input.path(field).asLong(-1) >= 0
          && input.path(field).asLong() > -1
          && input.path(field).asLong() <= 9007199254740991L) out.set(field, input.path(field));
    if (input.path("httpStatus").asInt() >= 100 && input.path("httpStatus").asInt() <= 599)
      out.set("httpStatus", input.path("httpStatus"));
    for (String field : List.of("udsCode", "udsSubCode"))
      if (input.path(field).isString() && input.path(field).asString().matches("[0-9]{1,8}"))
        out.set(field, input.path(field));
    for (String field : List.of("tokenHashPrefix", "oldTokenHashPrefix", "newTokenHashPrefix"))
      if (input.path(field).asString().matches("[a-f0-9]{12}")) out.set(field, input.path(field));
    for (String field : List.of("retryAt", "retryDeadline", "nextRevalidateAt"))
      if (input.path(field).isString())
        try {
          out.put(field, java.time.Instant.parse(input.path(field).asString()).toString());
        } catch (RuntimeException ignored) {
        }
    if (Set.of("token-verify", "department").contains(input.path("strategy").asString()))
      out.set("strategy", input.path("strategy"));
    if ("strip-ascii-fold-v1".equals(input.path("identityComparisonVersion").asString()))
      out.set("identityComparisonVersion", input.path("identityComparisonVersion"));
    if (Set.of("primary", "fallback").contains(input.path("fieldBranch").asString()))
      out.set("fieldBranch", input.path("fieldBranch"));
    if (Set.of("requested", "committed", "rolled_back")
        .contains(input.path("revocationPhase").asString()))
      out.set("revocationPhase", input.path("revocationPhase"));
    // Only identifier syntax; names, URLs, messages and arbitrary free text never pass.
    if (input.path("empNo").asString().matches("[A-Za-z0-9_-]{1,128}"))
      out.set("empNo", input.path("empNo"));
    return out;
  }

  private static ObjectNode export(JsonNode input) {
    ObjectNode out = JsonMapper.builder().build().createObjectNode();
    for (String field : List.of("from", "to"))
      try {
        out.put(field, java.time.Instant.parse(input.path(field).asString()).toString());
      } catch (RuntimeException ignored) {
      }
    if (input.path("correlationId").asString().matches("[a-fA-F0-9-]{36}"))
      try {
        out.put(
            "correlationId", UUID.fromString(input.path("correlationId").asString()).toString());
      } catch (RuntimeException ignored) {
      }
    if (input.path("partial").isBoolean()) out.set("partial", input.path("partial"));
    var providers = out.putArray("providerIds");
    if (input.path("providerIds").isArray() && input.path("providerIds").size() <= 100)
      for (JsonNode id : input.path("providerIds"))
        try {
          if (id.asString().matches("[a-fA-F0-9-]{36}"))
            providers.add(UUID.fromString(id.asString()).toString());
        } catch (RuntimeException ignored) {
        }
    return out;
  }

  private UacAuditProjection() {}
}
