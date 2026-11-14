package auriga.iam;

import java.nio.charset.StandardCharsets;
import java.time.*;
import java.util.*;
import tools.jackson.core.StreamReadConstraints;
import tools.jackson.core.StreamReadFeature;
import tools.jackson.core.json.JsonFactory;
import tools.jackson.databind.*;
import tools.jackson.databind.json.JsonMapper;
import tools.jackson.databind.node.ObjectNode;

/** Collection and export repeat the closed projection; arbitrary log text never enters a ZIP. */
final class UacBundleRedactor {
  private static final JsonMapper JSON =
      JsonMapper.builder(
              JsonFactory.builder()
                  .streamReadConstraints(
                      StreamReadConstraints.builder()
                          .maxNestingDepth(32)
                          .maxStringLength(64 * 1024)
                          .build())
                  .build())
          .enable(StreamReadFeature.STRICT_DUPLICATE_DETECTION)
          .enable(DeserializationFeature.FAIL_ON_TRAILING_TOKENS)
          .build();

  static JsonNode parse(String text) {
    if (text == null || text.getBytes(StandardCharsets.UTF_8).length > 64 * 1024)
      throw new IllegalArgumentException("Invalid diagnostic record");
    return JSON.readTree(text);
  }

  static UUID id(JsonNode value) {
    String text = value.asString();
    if (!text.matches(
        "[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}"))
      throw new IllegalArgumentException("Invalid diagnostic identifier");
    return UUID.fromString(text);
  }

  static ObjectNode stage(JsonNode input, boolean detail) {
    if (input.path("message").isString()) input = parse(input.path("message").asString());
    if (!"iam".equals(input.path("service").asString())
        || !UacTelemetry.STAGES.contains(input.path("stage").asString())
        || !Set.of("start", "end").contains(input.path("phase").asString())
        || !Set.of("INFO", "WARN", "ERROR", "DEBUG").contains(input.path("level").asString())
        || !Set.of(
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
                "authenticated")
            .contains(input.path("result").asString()))
      throw new IllegalArgumentException("Invalid diagnostic event");
    var out =
        Json.object(
            "timestamp",
            Instant.parse(input.path("timestamp").asString()).toString(),
            "service",
            "iam",
            "eventId",
            id(input.path("eventId")).toString(),
            "tenantId",
            id(input.path("tenantId")).toString(),
            "providerId",
            id(input.path("providerId")).toString(),
            "correlationId",
            id(input.path("correlationId")).toString());
    UacTelemetry.copySafe(input, out, true);
    for (String field : List.of("stage", "phase", "result", "level"))
      out.set(field, input.path(field));
    if (input.path("durationMs").isIntegralNumber()
        && input.path("durationMs").asLong() >= 0
        && input.path("durationMs").asLong() <= 86400000)
      out.set("durationMs", input.path("durationMs"));
    else out.putNull("durationMs");
    for (String field : List.of("purpose", "chain", "endpoint", "sourceAlias")) {
      String value = input.path(field).asString();
      boolean allowed =
          switch (field) {
            case "purpose" -> Set.of("login_info", "login_sso", "revalidate").contains(value);
            case "chain" -> Set.of("login", "revalidation", "revocation").contains(value);
            case "endpoint" ->
                Set.of("scan", "department", "token-verify", "local").contains(value);
            default -> value.equals("uac");
          };
      if (allowed) out.put(field, value);
      else out.putNull(field);
    }
    for (String field : List.of("trace_id", "spanId"))
      if (input
          .path(field)
          .asString()
          .matches(field.equals("trace_id") ? "[a-f0-9]{32}" : "[a-f0-9]{16}"))
        out.set(field, input.path(field));
    if (input.path("requestId").isString())
      try {
        out.put("requestId", id(input.path("requestId")).toString());
      } catch (RuntimeException ignored) {
      }
    if (detail && input.has("udsRawSanitized")) {
      var again =
          UacDiagnosticSanitizer.sanitize(
              input.path("udsRawSanitized").toString().getBytes(StandardCharsets.UTF_8), List.of());
      if (again.has("udsRawSanitized")) out.set("udsRawSanitized", again.path("udsRawSanitized"));
      else out.put("truncated", true);
    }
    return out;
  }

  static ObjectNode audit(JsonNode input) {
    String action = input.path("action").asString();
    if (!action.startsWith("iam.uac.")
        || !UacTelemetry.STAGES.contains(action.substring(8))
        || !Set.of("success", "failure", "denied").contains(input.path("result").asString()))
      throw new IllegalArgumentException("Invalid diagnostic audit");
    var out =
        Json.object(
            "id",
            id(input.path("id")).toString(),
            "tenantId",
            id(input.path("tenantId")).toString(),
            "time",
            Instant.parse(input.path("time").asString()).toString(),
            "action",
            action,
            "result",
            input.path("result"));
    ObjectNode details = Json.object();
    UacTelemetry.copySafe(input.path("details"), details, true);
    for (String field : List.of("purpose", "chain", "outcome")) {
      String value = input.path("details").path(field).asString();
      if (Set.of(
              "login_info",
              "login_sso",
              "revalidate",
              "login",
              "revalidation",
              "revocation",
              "success",
              "failure",
              "denied",
              "waiting",
              "scanned",
              "verified",
              "unavailable",
              "completed",
              "skipped",
              "scan_required",
              "cancelled",
              "expired",
              "stopped")
          .contains(value)) details.put(field, value);
    }
    out.set("details", details);
    out.set(
        "target",
        Json.object("type", "provider", "id", id(input.path("target").path("id")).toString()));
    var actor = Json.object("type", "system");
    if (input.path("actor").path("type").asString().equals("user"))
      try {
        actor.put("id", id(input.path("actor").path("id")).toString());
        actor.put("type", "user");
      } catch (RuntimeException ignored) {
      }
    out.set("actor", actor);
    return out;
  }

  static ObjectNode ops(JsonNode input) {
    if (!"iam_uac".equals(input.path("source").asString())
        || !Set.of("warning", "critical").contains(input.path("severity").asString()))
      throw new IllegalArgumentException("Invalid diagnostic alert");
    var out =
        Json.object(
            "id",
            id(input.path("id")).toString(),
            "providerId",
            id(input.path("providerId")).toString(),
            "source",
            "iam_uac",
            "severity",
            input.path("severity"),
            "firstSeen",
            Instant.parse(input.path("firstSeen").asString()).toString(),
            "lastSeen",
            Instant.parse(input.path("lastSeen").asString()).toString());
    if (input.path("resolvedAt").isString())
      out.put("resolvedAt", Instant.parse(input.path("resolvedAt").asString()).toString());
    else out.putNull("resolvedAt");
    return out;
  }

  private UacBundleRedactor() {}
}
