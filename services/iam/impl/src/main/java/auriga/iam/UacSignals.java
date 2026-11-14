package auriga.iam;

import auriga.common.context.TenantContext;
import auriga.ops.api.OpsSignals;
import java.time.*;
import java.util.*;
import org.slf4j.LoggerFactory;
import tools.jackson.databind.node.ObjectNode;

/** Durable provider aggregation; Ops delivery runs outside the authentication transaction. */
final class UacSignals {
  private final IamStore store;
  private final OpsSignals ops;
  private final Clock clock;

  UacSignals(IamStore store, OpsSignals ops, Clock clock) {
    this.store = store;
    this.ops = ops;
    this.clock = clock;
  }

  void record(ObjectNode event) {
    if (!"end".equals(event.path("phase").asString())
        || "DEBUG".equals(event.path("level").asString())) return;
    String stage = event.path("stage").asString(), purpose = event.path("purpose").asString();
    boolean remote = Set.of("scan_status", "department_lookup", "token_verify").contains(stage);
    boolean revoked =
        stage.equals("session_revoke")
            && event.path("faultReason").asString().startsWith("revalidation_");
    if (!remote && !stage.equals("session_revoke")) return;
    String provider = event.path("providerId").asString(), key = "signal:" + provider;
    store.lock("uac-signal:" + provider);
    ObjectNode row = store.get("uac_diagnostics", key, true);
    if (row == null)
      row =
          store.insert(
              "uac_diagnostics",
              key,
              Json.object(
                  "kind", "signal", "providerId", provider, "nextAt", clock.instant().toString()),
              null);
    String timestamp = event.path("timestamp").asString(), id = event.path("eventId").asString();
    if (row.has("lastAt")) {
      int order = Instant.parse(row.path("lastAt").asString()).compareTo(Instant.parse(timestamp));
      if (order > 0 || order == 0 && row.path("lastEventId").asString().compareTo(id) >= 0) return;
    }
    row.put("lastAt", timestamp);
    row.put("lastEventId", id);
    row.put("correlationId", event.path("correlationId").asString());
    boolean success =
        Set.of("success", "waiting", "scanned", "verified")
            .contains(event.path("result").asString());
    boolean fault =
        event.path("failureClass").asString().equals("upstream_fault")
            || Set.of(
                    "transport_error",
                    "http_error",
                    "invalid_json",
                    "missing_code",
                    "invalid_code",
                    "invalid_success_payload")
                .contains(event.path("faultReason").asString());
    if (remote && success) {
      row.remove("firstFaultAt");
      row.put("revalidationFailed", false);
    } else if (remote && fault && !row.has("firstFaultAt")) row.put("firstFaultAt", timestamp);
    if (revoked
        || purpose.equals("revalidate")
            && !success
            && (stage.equals("department_lookup")
                || Set.of("business_rejected", "identity_mismatch", "retry_exhausted")
                    .contains(event.path("failureClass").asString())))
      row.put("revalidationFailed", true);
    row.put("nextAt", clock.instant().toString());
    store.save("uac_diagnostics", row);
  }

  void flush() {
    if (ops == null) return;
    for (int i = 0; i < 50; i++) {
      ObjectNode work =
          store.tx(
              () -> {
                var rows =
                    store
                        .networkJdbc()
                        .query(
                            "SELECT data::text FROM iam.uac_diagnostics WHERE tenant_id=? AND data->>'kind'='signal' AND (data->>'nextAt')::timestamptz<=? AND (data->>'leaseUntil' IS NULL OR (data->>'leaseUntil')::timestamptz<=?) ORDER BY data->>'nextAt',id LIMIT 1 FOR UPDATE SKIP LOCKED",
                            (rs, n) -> Json.read(rs.getString(1)),
                            TenantContext.require(),
                            java.sql.Timestamp.from(clock.instant()),
                            java.sql.Timestamp.from(clock.instant()));
                if (rows.isEmpty()) return null;
                var row = rows.getFirst();
                boolean pending =
                    Boolean.TRUE.equals(
                        store
                            .networkJdbc()
                            .queryForObject(
                                "SELECT EXISTS(SELECT 1 FROM iam.session s JOIN iam.session_projection p ON p.tenant_id=s.tenant_id AND p.cookie_hash=s.data->>'cookieHash' WHERE s.tenant_id=? AND s.provider_id=? AND s.data->>'uacCredentialId' IS NOT NULL AND s.data->>'revoked'='true' AND p.created_at<=?)",
                                Boolean.class,
                                TenantContext.require(),
                                UUID.fromString(row.path("providerId").asString()),
                                java.sql.Timestamp.from(clock.instant().minusSeconds(30))));
                boolean active =
                    pending
                        || row.path("revalidationFailed").asBoolean()
                        || row.has("firstFaultAt")
                            && !clock
                                .instant()
                                .isBefore(
                                    Instant.parse(row.path("firstFaultAt").asString())
                                        .plusSeconds(60));
                row.put("desiredActive", active);
                row.put("leaseToken", UUID.randomUUID().toString());
                row.put("leaseUntil", clock.instant().plusSeconds(35).toString());
                store.save("uac_diagnostics", row);
                return row;
              });
      if (work == null) break;
      try {
        store.requireTransactionReleased();
        ops.observe(
            TenantContext.require(),
            "iam_uac",
            work.path("providerId").asString(),
            "warning",
            work.path("desiredActive").asBoolean());
        store.tx(
            () -> {
              var live = store.byId("uac_diagnostics", work.path("id").asString(), true);
              if (live != null && live.path("leaseToken").equals(work.path("leaseToken"))) {
                live.put("lastSentActive", work.path("desiredActive").asBoolean());
                live.remove("leaseToken");
                live.remove("leaseUntil");
                live.put("nextAt", clock.instant().plusSeconds(2).toString());
                store.save("uac_diagnostics", live);
              }
              return null;
            });
      } catch (RuntimeException failure) {
        LoggerFactory.getLogger("auriga.iam.uac").warn("UAC provider alert delivery deferred");
      }
    }
  }
}
