package auriga.workspace.app;

import static auriga.workspace.persistence.WorkspaceStore.*;

import auriga.contracts.node.v1.*;
import auriga.workspace.domain.NodeDeliveryPolicy;
import auriga.workspace.persistence.WorkspaceStore;
import java.time.*;
import java.util.*;

/** Durable command attempts and results. Called inside the session's fenced tenant transaction. */
public final class NodeDeliveries {
  public record Delivery(
      UUID id,
      UUID instance,
      String kind,
      long generation,
      int attempt,
      Instant deadline,
      SessionResponse message) {}

  private final WorkspaceStore store;
  private final Clock clock;

  public NodeDeliveries(WorkspaceStore store, Clock clock) {
    this.store = store;
    this.clock = clock;
  }

  public List<Delivery> claim(UUID node, UUID session, Set<UUID> inFlight) {
    List<Delivery> deliveries = new ArrayList<>();
    // A disconnect after the final send must still reach a terminal state without an ACK.
    store.jdbc.update(
        "UPDATE workspace_job SET done=true,failed=true,last_error=COALESCE(last_error,'common.unavailable') WHERE tenant_id=? AND NOT done AND kind IN ('restart','delete') AND attempts>=? AND due_at<=? AND instance_id IN (SELECT id FROM instance WHERE tenant_id=? AND node_id=?)",
        store.tenant(),
        NodeDeliveryPolicy.MAX_ATTEMPTS,
        time(clock.instant()),
        store.tenant(),
        node);
    var jobs =
        store.jdbc.queryForList(
            "SELECT j.* FROM workspace_job j JOIN instance i ON i.id=j.instance_id AND i.tenant_id=j.tenant_id WHERE j.tenant_id=? AND i.node_id=? AND (j.kind='delete' OR (j.kind='restart' AND NOT i.deleted)) AND NOT j.done AND j.due_at<=? AND j.attempts<? ORDER BY j.due_at,j.id FOR UPDATE OF j SKIP LOCKED LIMIT 32",
            store.tenant(),
            node,
            time(clock.instant()),
            NodeDeliveryPolicy.MAX_ATTEMPTS);
    for (var job : jobs) {
      UUID id = (UUID) job.get("id");
      if (inFlight.contains(id)) continue;
      int attempt = ((Number) job.get("attempts")).intValue() + 1;
      try {
        String kind = (String) job.get("kind");
        var message = SessionResponse.newBuilder();
        if (kind.equals("restart"))
          message.setCommand(Command.parseFrom((byte[]) job.get("command")));
        else message.setDelete(DeleteInstance.parseFrom((byte[]) job.get("command")));
        Instant deadline = clock.instant().plus(NodeDeliveryPolicy.ACK_TIMEOUT);
        store.jdbc.update(
            "UPDATE workspace_job SET attempts=?,dispatched_session=?,sent_at=?,due_at=? WHERE tenant_id=? AND id=?",
            attempt,
            session,
            time(clock.instant()),
            time(deadline.plus(NodeDeliveryPolicy.backoff(attempt))),
            store.tenant(),
            id);
        deliveries.add(
            new Delivery(
                id,
                (UUID) job.get("instance_id"),
                kind,
                ((Number) job.get("generation")).longValue(),
                attempt,
                deadline,
                message.build()));
      } catch (com.google.protobuf.InvalidProtocolBufferException e) {
        throw new IllegalStateException("Invalid persisted node job", e);
      }
    }
    return deliveries;
  }

  public void result(UUID node, UUID session, Delivery delivery, CommandResult result) {
    finish(
        node,
        session,
        delivery,
        result.getOk(),
        result.toByteArray(),
        result.getOk() ? null : result.getError().getCode());
  }

  public void timeout(UUID node, UUID session, Delivery delivery) {
    finish(node, session, delivery, false, null, "common.unavailable");
  }

  private void finish(
      UUID node, UUID session, Delivery delivery, boolean ok, byte[] result, String error) {
    boolean failed = !ok && NodeDeliveryPolicy.exhausted(delivery.attempt());
    store.jdbc.update(
        "UPDATE workspace_job SET done=?,failed=?,result=?,last_error=?,due_at=? WHERE tenant_id=? AND id=? AND instance_id=? AND dispatched_session=? AND attempts=? AND NOT done AND instance_id IN (SELECT id FROM instance WHERE tenant_id=? AND node_id=?)",
        ok || failed,
        failed,
        result,
        error,
        time(clock.instant().plus(NodeDeliveryPolicy.backoff(delivery.attempt()))),
        store.tenant(),
        delivery.id(),
        delivery.instance(),
        session,
        delivery.attempt(),
        store.tenant(),
        node);
  }

  public void deleted(UUID node, UUID session, Delivery delivery, InstanceStatus status) {
    if (delivery.kind().equals("delete")
        && delivery.generation() == status.getObservedGeneration()
        && status.getPhase() == InstancePhase.INSTANCE_PHASE_ABSENT)
      finish(node, session, delivery, true, status.toByteArray(), null);
  }
}
