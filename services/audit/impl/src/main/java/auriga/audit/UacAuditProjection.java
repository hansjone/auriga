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
          "revalidation_unreachable",
          "portal",
          "uds",
          "none",
          "missing",
          "suppressed",
          "disabled",
          "ineligible");

  static ObjectNode project(JsonNode data) {
    String action = data.path("action").asString();
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
            "suppressed")) if (input.path(field).isBoolean()) out.set(field, input.path(field));
    for (String field : List.of("durationMs", "attempt", "configRevision", "matchingCount"))
      if (input.path(field).isIntegralNumber()
          && input.path(field).asLong(-1) >= 0
          && input.path(field).asLong() > -1
          && input.path(field).asLong() <= 9007199254740991L) out.set(field, input.path(field));
    if (input.path("httpStatus").asInt() >= 100 && input.path("httpStatus").asInt() <= 599)
      out.set("httpStatus", input.path("httpStatus"));
    for (String field : List.of("udsCode", "udsSubCode"))
      if (input.path(field).isString() && input.path(field).asString().matches("[0-9]{1,8}"))
        out.set(field, input.path(field));
    if (input.path("tokenHashPrefix").asString().matches("[a-f0-9]{12}"))
      out.set("tokenHashPrefix", input.path("tokenHashPrefix"));
    // Only identifier syntax; names, URLs, messages and arbitrary free text never pass.
    if (input.path("empNo").asString().matches("[A-Za-z0-9_-]{1,128}"))
      out.set("empNo", input.path("empNo"));
    return out;
  }

  private UacAuditProjection() {}
}
