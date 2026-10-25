package auriga.collab.app;

import auriga.collab.api.NetworkSourceAuthority.Source;
import auriga.common.error.CommonErrors;
import auriga.common.error.ErrorCode;
import auriga.common.persistence.UuidV7;
import auriga.iam.api.NetworkAuthority.*;
import java.sql.Timestamp;
import java.time.Duration;
import java.time.Instant;
import java.util.*;
import org.springframework.jdbc.core.JdbcTemplate;

/**
 * Transaction-owned source reservations. No target lookup, relationship or outcome is read here.
 */
final class NetworkContactBudget {
  private final JdbcTemplate jdbc;

  NetworkContactBudget(JdbcTemplate jdbc) {
    this.jdbc = jdbc;
  }

  record Reservation(UUID id, boolean retry, ErrorCode error, String resultJson) {}

  private record Scope(
      String name,
      String subject,
      Limit rate,
      Limit burst,
      Limit pending,
      Limit hour,
      Limit day,
      int freezeAt) {}

  private record State(Scope scope, double tokens, Instant frozen, Instant cooldown) {}

  Reservation reserve(
      UUID tenant,
      Source source,
      String target,
      String epoch,
      String key,
      String fingerprint,
      Instant now,
      Instant expires,
      Decision decision) {
    var scopes =
        List.of(
            new Scope(
                "user",
                source.user(),
                Limit.FIRST_USER_PER_MINUTE,
                Limit.FIRST_USER_BURST,
                Limit.FIRST_USER_PENDING,
                Limit.FIRST_USER_TARGETS_HOUR,
                Limit.FIRST_USER_TARGETS_DAY,
                160),
            new Scope(
                "node",
                source.node(),
                Limit.FIRST_NODE_PER_MINUTE,
                Limit.FIRST_NODE_BURST,
                Limit.FIRST_NODE_PENDING,
                Limit.FIRST_NODE_TARGETS_HOUR,
                Limit.FIRST_NODE_TARGETS_DAY,
                80),
            new Scope(
                "local",
                source.node() + "." + source.local(),
                Limit.FIRST_LOCAL_PER_MINUTE,
                Limit.FIRST_LOCAL_BURST,
                Limit.FIRST_LOCAL_PENDING,
                Limit.FIRST_LOCAL_TARGETS_HOUR,
                Limit.FIRST_LOCAL_TARGETS_DAY,
                Integer.MAX_VALUE));
    var states = new ArrayList<State>();
    // All writers take user -> node -> local. Same-value UPDATE also fences stale RR snapshots.
    for (var scope : scopes) {
      var row =
          jdbc.queryForMap(
              """
          INSERT INTO collab.network_contact_budget(id,tenant_id,realm,scope,subject,tokens,refilled_at)
          VALUES (?,?,?,?,?,?,?)
          ON CONFLICT (tenant_id,realm,scope,subject) DO UPDATE SET subject=excluded.subject
          RETURNING tokens,refilled_at,frozen_until,cooldown_until
          """,
              UuidV7.next(),
              tenant,
              source.realm(),
              scope.name(),
              scope.subject(),
              NetworkIamChecks.limit(decision, scope.burst()),
              Timestamp.from(now));
      double elapsed =
          Math.max(0, Duration.between(instant(row.get("refilled_at")), now).toMillis() / 1000.0);
      double tokens =
          Math.min(
              NetworkIamChecks.limit(decision, scope.burst()),
              ((Number) row.get("tokens")).doubleValue()
                  + elapsed * NetworkIamChecks.limit(decision, scope.rate()) / 60.0);
      states.add(
          new State(
              scope, tokens, instant(row.get("frozen_until")), instant(row.get("cooldown_until"))));
    }
    var prior =
        jdbc.queryForList(
            """
        SELECT id,fingerprint,result_json,error_code FROM collab.network_contact_submission
        WHERE tenant_id=? AND realm=? AND source_node=? AND source_local=? AND producer_epoch=? AND idempotency_key=?
        """,
            tenant,
            source.realm(),
            source.node(),
            source.local(),
            epoch,
            key);
    if (!prior.isEmpty()) {
      var row = prior.getFirst();
      if (!fingerprint.equals(row.get("fingerprint")))
        return new Reservation((UUID) row.get("id"), true, NetworkErrors.CONFLICT, null);
      ErrorCode error =
          row.get("error_code") == null
              ? null
              : switch (row.get("error_code").toString()) {
                case "common.unavailable" -> CommonErrors.UNAVAILABLE;
                case "common.forbidden" -> CommonErrors.FORBIDDEN;
                default -> NetworkErrors.UNAUTHORIZED;
              };
      return new Reservation((UUID) row.get("id"), true, error, (String) row.get("result_json"));
    }
    for (var state : states) {
      var scope = state.scope();
      // Whitelisted predicates only. User scope spans every node; local scope includes sys.
      String predicate =
          switch (scope.name()) {
            case "user" -> "source_user=?";
            case "node" -> "source_node=?";
            default -> "source_node=? AND source_local=?";
          };
      var args =
          new ArrayList<Object>(
              List.of(
                  Timestamp.from(now),
                  Timestamp.from(now.minusSeconds(3600)),
                  Timestamp.from(now.minusSeconds(3600)),
                  target,
                  target,
                  tenant,
                  source.realm(),
                  Timestamp.from(now.minusSeconds(86400))));
      if (scope.name().equals("user")) args.add(source.user());
      else {
        args.add(source.node());
        if (scope.name().equals("local")) args.add(source.local());
      }
      var count =
          jdbc.queryForMap(
              """
          SELECT count(*) FILTER (WHERE visible_until>?) AS pending,
                 count(DISTINCT target_node) FILTER (WHERE created_at>?) AS hourly,
                 coalesce(bool_or(created_at>? AND target_node=?),false) AS in_hour,
                 coalesce(bool_or(target_node=?),false) AS in_day,
                 count(DISTINCT target_node) AS daily
          FROM collab.network_contact_submission
          WHERE tenant_id=? AND realm=? AND created_at>? AND """
                  + " "
                  + predicate,
              args.toArray());
      long daily = ((Number) count.get("daily")).longValue();
      boolean frozen = state.frozen() != null && state.frozen().isAfter(now);
      if (daily >= scope.freezeAt()) {
        if (state.frozen() == null) freeze(tenant, source.realm(), scope, now.plusSeconds(3600));
        frozen = true; // Expiry alone cannot thaw a source with unreduced volume.
      }
      if (frozen || state.cooldown() != null && state.cooldown().isAfter(now))
        return new Reservation(null, false, NetworkErrors.QUOTA_EXCEEDED, null);
      long nextDay = daily + (Boolean.TRUE.equals(count.get("in_day")) ? 0 : 1);
      long nextHour =
          ((Number) count.get("hourly")).longValue()
              + (Boolean.TRUE.equals(count.get("in_hour")) ? 0 : 1);
      if (state.tokens() < 1
          || ((Number) count.get("pending")).longValue()
              >= NetworkIamChecks.limit(decision, scope.pending())
          || nextHour > NetworkIamChecks.limit(decision, scope.hour())
          || nextDay > NetworkIamChecks.limit(decision, scope.day())) {
        jdbc.update(
            "UPDATE collab.network_contact_budget SET cooldown_until=? WHERE tenant_id=? AND realm=? AND scope=? AND subject=?",
            Timestamp.from(now.plusSeconds(600)),
            tenant,
            source.realm(),
            scope.name(),
            scope.subject());
        return new Reservation(null, false, NetworkErrors.QUOTA_EXCEEDED, null);
      }
    }
    UUID id = UuidV7.next();
    jdbc.update(
        """
        INSERT INTO collab.network_contact_submission(id,tenant_id,realm,source_node,source_local,source_user,
          target_node,producer_epoch,idempotency_key,fingerprint,created_at,visible_until)
        VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
        """,
        id,
        tenant,
        source.realm(),
        source.node(),
        source.local(),
        source.user(),
        target,
        epoch,
        key,
        fingerprint,
        Timestamp.from(now),
        Timestamp.from(expires));
    for (var state : states) {
      var scope = state.scope();
      jdbc.update(
          "UPDATE collab.network_contact_budget SET tokens=?,refilled_at=?,frozen_until=NULL,cooldown_until=NULL WHERE tenant_id=? AND realm=? AND scope=? AND subject=?",
          state.tokens() - 1,
          Timestamp.from(now),
          tenant,
          source.realm(),
          scope.name(),
          scope.subject());
      if (scope.freezeAt() != Integer.MAX_VALUE) {
        String column = scope.name().equals("user") ? "source_user" : "source_node";
        Long count =
            jdbc.queryForObject(
                "SELECT count(DISTINCT target_node) FROM collab.network_contact_submission WHERE tenant_id=? AND realm=? AND created_at>? AND "
                    + column
                    + "=?",
                Long.class,
                tenant,
                source.realm(),
                Timestamp.from(now.minusSeconds(86400)),
                scope.subject());
        if (count >= scope.freezeAt()) freeze(tenant, source.realm(), scope, now.plusSeconds(3600));
      }
    }
    return new Reservation(id, false, null, null);
  }

  void complete(UUID tenant, UUID id, NetworkMailboxService.StoreResult result, ErrorCode error) {
    jdbc.update(
        "UPDATE collab.network_contact_submission SET result_json=?,error_code=? WHERE tenant_id=? AND id=?",
        result == null ? null : NetworkJson.JSON.writeValueAsString(result),
        error == null ? null : error.code(),
        tenant,
        id);
  }

  Instant visibleUntil(UUID tenant, UUID id) {
    return jdbc.queryForObject(
            "SELECT visible_until FROM collab.network_contact_submission WHERE tenant_id=? AND id=?",
            Timestamp.class,
            tenant,
            id)
        .toInstant();
  }

  void materialized(UUID tenant, UUID id, UUID messageId) {
    jdbc.update(
        "UPDATE collab.network_contact_submission SET message_id=? WHERE tenant_id=? AND id=?",
        messageId,
        tenant,
        id);
  }

  private void freeze(UUID tenant, String realm, Scope scope, Instant until) {
    jdbc.update(
        "UPDATE collab.network_contact_budget SET frozen_until=? WHERE tenant_id=? AND realm=? AND scope=? AND subject=?",
        Timestamp.from(until),
        tenant,
        realm,
        scope.name(),
        scope.subject());
  }

  private static Instant instant(Object value) {
    return value == null ? null : ((Timestamp) value).toInstant();
  }
}
