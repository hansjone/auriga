package auriga.collab.app;

import auriga.collab.api.NetworkSourceAuthority;
import auriga.collab.app.NetworkMailboxService.FetchItem;
import auriga.common.error.AurigaException;
import auriga.iam.api.NetworkAuthority.*;
import java.nio.charset.StandardCharsets;
import java.time.Clock;
import java.time.Instant;
import java.util.*;
import org.springframework.dao.DataAccessException;
import org.springframework.jdbc.core.JdbcTemplate;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** One bounded page of online decisions, completed before collab acquires any mailbox row locks. */
final class NetworkDispatchAuthorization {
  private final JdbcTemplate jdbc;
  private final NetworkIamChecks iam;
  private final NetworkSourceAuthority authority;
  private final Clock clock;

  NetworkDispatchAuthorization(
      JdbcTemplate jdbc, NetworkIamChecks iam, NetworkSourceAuthority authority, Clock clock) {
    this.jdbc = jdbc;
    this.iam = iam;
    this.authority = authority;
    this.clock = clock;
  }

  record Outcome(Instant deadline, String fingerprint, RuntimeException failure) {}

  private record Prepared(Check check, ObjectNode message, ObjectNode source, JsonNode dispatch) {}

  record Batch(
      Map<UUID, Outcome> failures, Map<UUID, Prepared> prepared, Set<NodeReference> sources) {}

  Batch prepare(UUID tenant, List<FetchItem> items, String realm, String target) {
    Map<UUID, Outcome> result = new HashMap<>();
    Map<UUID, Prepared> prepared = new LinkedHashMap<>();
    Set<NodeReference> sources = new LinkedHashSet<>();
    for (var item : items) {
      if (item.recipients().stream()
              .allMatch(r -> NetworkMailboxService.TERMINAL.contains(r.transportStatus()))
          || item.recipients().stream().anyMatch(r -> "quarantined".equals(r.transportStatus())))
        continue;
      try {
        var row =
            jdbc.queryForMap(
                "SELECT message_json,source_context_json,dispatch_context_json FROM collab.network_message WHERE tenant_id=? AND id=?",
                tenant,
                item.messageId());
        var message = (ObjectNode) NetworkJson.parse((String) row.get("message_json"));
        var source = (ObjectNode) NetworkJson.parse((String) row.get("source_context_json"));
        if (!realm.equals(message.path("from").path("realm").asString()))
          throw new AurigaException(NetworkErrors.UNAUTHORIZED);
        if (!Instant.parse(message.path("expires_at").asString()).isAfter(clock.instant()))
          continue;
        var dispatch =
            row.get("dispatch_context_json") == null
                ? null
                : NetworkJson.parse(row.get("dispatch_context_json").toString());
        boolean broadcast = "broadcast".equals(message.path("message_class").asString());
        long nodes = 1, locals = 1;
        if (broadcast) {
          Set<String> unique = new HashSet<>();
          for (var member : dispatch.path("members")) unique.add(member.path("node").asString());
          nodes = unique.size();
          locals = dispatch.path("members").size();
        }
        String sender = message.path("sender_kind").asString();
        if (!Set.of("agent", "human").contains(sender))
          throw new AurigaException(NetworkErrors.SENDER_MISMATCH);
        var check =
            new Check(
                realm,
                message.path("from").path("node").asString(),
                target,
                message.path("from").path("local").asString(),
                "human".equals(sender) ? Sender.HUMAN : Sender.AGENT,
                "first_contact".equals(item.kind())
                    ? Operation.FIRST_CONTACT
                    : broadcast ? Operation.BROADCAST : Operation.SEND,
                NetworkJson.canonical(message.path("body")).getBytes(StandardCharsets.UTF_8).length,
                nodes,
                locals);
        prepared.put(item.deliveryId(), new Prepared(check, message, source, dispatch));
        sources.add(new NodeReference(realm, check.sourceNode()));
      } catch (DataAccessException e) {
        throw e;
      } catch (RuntimeException e) {
        result.put(item.deliveryId(), new Outcome(null, null, e));
      }
    }
    return new Batch(Map.copyOf(result), Map.copyOf(prepared), Set.copyOf(sources));
  }

  Map<UUID, Outcome> check(UUID tenant, Batch batch, String realm, String target) {
    Map<UUID, Outcome> result = new HashMap<>(batch.failures());
    var prepared = batch.prepared();
    var sources = batch.sources();
    if (prepared.isEmpty()) return result;
    Map<NodeReference, Boolean> active;
    try {
      active = iam.activeBatch(List.copyOf(sources));
    } catch (RuntimeException e) {
      for (var id : prepared.keySet()) result.put(id, new Outcome(null, null, e));
      return result;
    }
    Map<Check, RuntimeException> decisions = new HashMap<>();
    for (var entry : prepared.entrySet()) {
      var p = entry.getValue();
      try {
        if (!Boolean.TRUE.equals(active.get(new NodeReference(realm, p.check().sourceNode()))))
          throw new AurigaException(NetworkErrors.UNAUTHORIZED);
        if (!decisions.containsKey(p.check())) {
          try {
            iam.check(p.check());
            decisions.put(p.check(), null);
          } catch (RuntimeException e) {
            decisions.put(p.check(), e);
          }
        }
        if (decisions.get(p.check()) != null) throw decisions.get(p.check());
        Instant deadline =
            authority.authorizeDelivery(
                tenant,
                NetworkJson.canonical(p.message()),
                NetworkJson.canonical(p.source()),
                target);
        if (deadline == null || !deadline.isAfter(clock.instant()))
          throw new AurigaException(NetworkErrors.UNAUTHORIZED);
        result.put(
            entry.getKey(),
            new Outcome(deadline, fingerprint(p.message(), p.source(), p.dispatch()), null));
      } catch (DataAccessException e) {
        throw e;
      } catch (RuntimeException e) {
        result.put(entry.getKey(), new Outcome(null, null, e));
      }
    }
    return result;
  }

  static String fingerprint(ObjectNode message, ObjectNode source, JsonNode dispatch) {
    return NetworkJson.digest(message)
        + "/"
        + NetworkJson.digest(source)
        + "/"
        + (dispatch == null ? "" : NetworkJson.digest(dispatch));
  }
}
