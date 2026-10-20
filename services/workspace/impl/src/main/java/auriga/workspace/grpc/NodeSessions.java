package auriga.workspace.grpc;

import static auriga.workspace.persistence.WorkspaceStore.*;

import auriga.common.error.*;
import auriga.common.grpc.GrpcErrors;
import auriga.contracts.node.v1.*;
import auriga.workspace.app.*;
import auriga.workspace.domain.WorkspaceCrypto;
import auriga.workspace.persistence.NodeSessionLease;
import com.google.protobuf.Duration;
import io.grpc.*;
import io.grpc.stub.StreamObserver;
import java.security.cert.X509Certificate;
import java.time.Instant;
import java.util.*;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.atomic.AtomicBoolean;

/** Node authentication is exclusively mTLS; intentionally separate from internal JWT gRPC. */
public final class NodeSessions implements AutoCloseable {
  public static final Context.Key<X509Certificate> PEER = Context.key("workspace-node-certificate");
  private final NodeEnrollment enrollment;
  private final WorkspaceService workspace;
  private final Map<UUID, Session> sessions = new ConcurrentHashMap<>();
  private final ExecutorService notifications =
      Executors.newSingleThreadExecutor(
          Thread.ofVirtual().name("workspace-node-dispatch").factory());
  private final AtomicBoolean dispatchPending = new AtomicBoolean();

  public NodeSessions(NodeEnrollment enrollment, WorkspaceService workspace) {
    this.enrollment = enrollment;
    this.workspace = workspace;
    // afterCommit still holds the caller's connection until cleanup. Never wait for another
    // connection there; one coalesced worker dispatches after the caller can return its connection.
    workspace.onChanged(
        () -> {
          if (!dispatchPending.compareAndSet(false, true)) return;
          try {
            notifications.execute(
                () -> {
                  try {
                    dispatch();
                  } finally {
                    dispatchPending.set(false);
                  }
                });
          } catch (java.util.concurrent.RejectedExecutionException e) {
            dispatchPending.set(false);
          }
        });
    workspace.onNodeRevoked(this::revoke);
  }

  public StreamObserver<SessionRequest> session(StreamObserver<SessionResponse> output) {
    try {
      return openSession(output);
    } catch (RuntimeException e) {
      output.onError(NodeGrpcServer.status(e));
      return ignored();
    }
  }

  private StreamObserver<SessionRequest> openSession(StreamObserver<SessionResponse> output) {
    X509Certificate leaf = PEER.get();
    if (leaf == null) {
      output.onError(GrpcErrors.toStatus(new AurigaException(CommonErrors.UNAUTHENTICATED)));
      return ignored();
    }
    UUID node = enrollment.identity(leaf), tenant = enrollment.store.route(node, "node");
    Session session = new Session(node, tenant, leaf, output);
    synchronized (sessions) {
      if (session.closed) return ignored();
      Session previous = sessions.remove(node);
      if (previous != null) previous.fail(new AurigaException(CommonErrors.CONFLICT));
      try {
        session.acquire();
        sessions.put(node, session);
      } catch (RuntimeException e) {
        session.fail(e);
        return ignored();
      }
    }
    return session;
  }

  /**
   * Do not expose this BindableService as a Spring bean: the JWT listener must never collect it.
   */
  public BindableService service() {
    return new NodeControlServiceGrpc.NodeControlServiceImplBase() {
      @Override
      public StreamObserver<SessionRequest> session(StreamObserver<SessionResponse> output) {
        return NodeSessions.this.session(output);
      }
    };
  }

  private static StreamObserver<SessionRequest> ignored() {
    return new StreamObserver<>() {
      public void onNext(SessionRequest ignored) {}

      public void onError(Throwable ignored) {}

      public void onCompleted() {}
    };
  }

  public void dispatch() {
    for (var s : List.copyOf(sessions.values())) s.dispatch();
  }

  public void expire() {
    for (var s : List.copyOf(sessions.values())) {
      s.checkTimeouts();
      if (s.seen.isBefore(workspace.clock.instant().minusSeconds(60)))
        s.fail(new AurigaException(CommonErrors.UNAVAILABLE));
    }
  }

  public void revoke(UUID node) {
    var s = sessions.get(node);
    if (s != null) s.fail(new AurigaException(CommonErrors.UNAUTHENTICATED));
  }

  @Override
  public void close() {
    notifications.shutdownNow();
    for (var s : List.copyOf(sessions.values()))
      s.fail(new AurigaException(CommonErrors.UNAVAILABLE));
  }

  private final class Session implements StreamObserver<SessionRequest> {
    final UUID node, tenant;
    final X509Certificate leaf;
    final StreamObserver<SessionResponse> output;
    volatile Instant seen = workspace.clock.instant();
    boolean hello, inventory, closed;
    final UUID holder = auriga.common.persistence.UuidV7.next();
    final NodeSessionLease lease;
    final NodeOutput outbound;
    final NodeDeliveries deliveries = new NodeDeliveries(enrollment.store, workspace.clock);
    final Map<UUID, NodeDeliveries.Delivery> inFlight = new HashMap<>();
    UUID specCursor;
    boolean acquired;
    final Map<String, Long> sent = new HashMap<>();

    Session(UUID node, UUID tenant, X509Certificate leaf, StreamObserver<SessionResponse> output) {
      this.node = node;
      this.tenant = tenant;
      this.leaf = leaf;
      this.output = output;
      this.lease = new NodeSessionLease(enrollment.store, tenant, node, holder);
      this.outbound =
          new NodeOutput(
              output,
              this::fail,
              () -> onError(io.grpc.Status.CANCELLED.asRuntimeException()),
              workspace.clock);
    }

    void acquire() {
      lease.acquire();
      acquired = true;
    }

    void release() {
      if (!acquired) return;
      acquired = false;
      try {
        lease.release();
      } catch (RuntimeException e) {
        org.slf4j.LoggerFactory.getLogger(getClass()).warn("Cannot release node session lease", e);
      }
    }

    @Override
    public synchronized void onNext(SessionRequest request) {
      if (closed) return;
      try {
        java.util.List<SessionResponse> responses = new java.util.ArrayList<>();
        enrollment.identity(leaf);
        lease.guard(
            () -> {
              if (!hello) {
                if (!request.hasHello())
                  throw new AurigaException(CommonErrors.PRECONDITION_FAILED);
                Hello h = request.getHello();
                if (!h.getIngressEndpoint().isEmpty()
                    && !WorkspaceRoutes.validEndpoint(h.getIngressEndpoint()))
                  throw new AurigaException(CommonErrors.VALIDATION_FAILED);
                if (!h.getNodeId().equals(node.toString()))
                  throw new AurigaException(CommonErrors.UNAUTHENTICATED);
                if (h.getProtocolVersion() != 1
                    || h.getCapacity().getCpuMillis() < 0
                    || h.getCapacity().getMemoryMb() < 0
                    || h.getCapacity().getDiskGb() < 0)
                  throw new AurigaException(CommonErrors.VALIDATION_FAILED);
                String keyId =
                    HexFormat.of()
                        .formatHex(
                            WorkspaceCrypto.hash(
                                enrollment.store.tx(
                                    () -> (byte[]) enrollment.store.node(node).get("sealing_key"))))
                        .substring(0, 16);
                if (!h.getSealingKeyId().equals(keyId))
                  throw new AurigaException(CommonErrors.PRECONDITION_FAILED);
                enrollment.store.tx(
                    () ->
                        enrollment.store.jdbc.update(
                            "UPDATE node SET hello=?,last_seen_at=?,agent_version=?,ingress_endpoint=? WHERE tenant_id=? AND id=?",
                            h.toByteArray(),
                            time(workspace.clock.instant()),
                            h.getAgentVersion(),
                            h.getIngressEndpoint(),
                            tenant,
                            node));
                hello = true;
                responses.add(
                    SessionResponse.newBuilder()
                        .setWelcome(
                            Welcome.newBuilder()
                                .setNodeId(node.toString())
                                .setProtocolVersion(1)
                                .setServerTime(NodeEnrollment.timestamp(workspace.clock.instant()))
                                .setHeartbeatInterval(Duration.newBuilder().setSeconds(15)))
                        .build());
              } else {
                if (request.hasHello() || !inventory && !request.hasInventory())
                  throw new AurigaException(CommonErrors.PRECONDITION_FAILED);
                if (request.hasInventory()) {
                  inventory = true;
                  for (var status : request.getInventory().getInstancesList())
                    workspace.status(node, status);
                } else if (request.hasStatus()) {
                  workspace.status(node, request.getStatus());
                  if (request.getStatus().getPhase() == InstancePhase.INSTANCE_PHASE_ABSENT) {
                    for (var delivery : List.copyOf(inFlight.values()))
                      if (delivery.instance().toString().equals(request.getStatus().getInstanceId())
                          && delivery.kind().equals("delete")
                          && delivery.generation() == request.getStatus().getObservedGeneration()) {
                        deliveries.deleted(node, holder, delivery, request.getStatus());
                        inFlight.remove(delivery.id());
                      }
                  }
                } else if (request.hasWake()) workspace.wake(node, request.getWake());
                else if (request.hasUsage())
                  enrollment.store.tx(
                      () -> {
                        enrollment.store.jdbc.update(
                            "UPDATE node SET usage=?,last_seen_at=? WHERE tenant_id=? AND id=?",
                            request.getUsage().toByteArray(),
                            time(workspace.clock.instant()),
                            tenant,
                            node);
                        for (var usage : request.getUsage().getInstancesList()) {
                          UUID id;
                          try {
                            id = UUID.fromString(usage.getInstanceId());
                          } catch (IllegalArgumentException e) {
                            continue;
                          }
                          enrollment.store.jdbc.update(
                              "UPDATE instance SET cpu_seen_at=?,cpu_active=?,last_activity_at=CASE WHEN ? THEN ? ELSE last_activity_at END WHERE tenant_id=? AND id=? AND node_id=? AND NOT deleted",
                              time(workspace.clock.instant()),
                              usage.getRecentCpuActive(),
                              usage.getRecentCpuActive(),
                              time(workspace.clock.instant()),
                              tenant,
                              id,
                              node);
                        }
                        return null;
                      });
                else if (request.hasResult()) {
                  var result = request.getResult();
                  UUID id;
                  try {
                    id = UUID.fromString(result.getCommandId());
                  } catch (IllegalArgumentException e) {
                    return null;
                  }
                  var delivery = inFlight.get(id);
                  if (delivery != null
                      && delivery.kind().equals("restart")
                      && delivery.instance().toString().equals(result.getInstanceId())) {
                    deliveries.result(node, holder, delivery, result);
                    inFlight.remove(id);
                  }
                } else if (!request.hasLog())
                  throw new AurigaException(CommonErrors.VALIDATION_FAILED);
              }
              seen = workspace.clock.instant();
              return null;
            });
        for (var response : responses) if (!outbound.send(response)) return;
        if (request.hasInventory()) dispatch();
      } catch (RuntimeException e) {
        fail(e);
      }
    }

    synchronized void checkTimeouts() {
      if (closed) return;
      outbound.checkStalled();
      if (closed) return;
      var overdue =
          inFlight.values().stream()
              .filter(d -> !d.deadline().isAfter(workspace.clock.instant()))
              .toList();
      if (overdue.isEmpty()) return;
      try {
        lease.guard(
            () -> {
              for (var delivery : overdue) deliveries.timeout(node, holder, delivery);
              return null;
            });
      } catch (RuntimeException e) {
        fail(e);
        return;
      }
      for (var delivery : overdue) inFlight.remove(delivery.id());
    }

    synchronized void dispatch() {
      if (closed || !inventory) return;
      checkTimeouts();
      if (closed) return;
      try {
        enrollment.identity(leaf);
        var messages =
            lease.guard(
                () -> {
                  List<SessionResponse> frames = new ArrayList<>();
                  var rows =
                      enrollment.store.jdbc.queryForList(
                          "SELECT * FROM instance WHERE tenant_id=? AND node_id=? AND NOT deleted AND (?::uuid IS NULL OR id>?) ORDER BY id LIMIT 64",
                          tenant,
                          node,
                          specCursor,
                          specCursor);
                  for (var row : rows) {
                    var spec = WorkspaceService.spec(row);
                    String id = spec.getInstanceId();
                    if (!Objects.equals(sent.get(id), spec.getGeneration())) {
                      frames.add(
                          SessionResponse.newBuilder()
                              .setApply(ApplySpec.newBuilder().setSpec(spec))
                              .build());
                      sent.put(id, spec.getGeneration());
                    }
                  }
                  specCursor = rows.size() < 64 ? null : (UUID) rows.getLast().get("id");
                  for (var delivery : deliveries.claim(node, holder, inFlight.keySet())) {
                    inFlight.put(delivery.id(), delivery);
                    frames.add(delivery.message());
                  }
                  return frames;
                });
        if (!messages.isEmpty())
          org.slf4j.LoggerFactory.getLogger(getClass())
              .info("node {}: dispatching {} control message(s)", node, messages.size());
        for (var message : messages) if (!outbound.send(message)) break;
      } catch (RuntimeException e) {
        org.slf4j.LoggerFactory.getLogger(getClass())
            .warn("node {}: dispatch failed, closing session", node, e);
        fail(e);
      }
    }

    synchronized void fail(Throwable error) {
      if (closed) return;
      org.slf4j.LoggerFactory.getLogger(getClass())
          .info("node {}: session closed ({})", node, error.toString());
      closed = true;
      sessions.remove(node, this);
      release();
      if (outbound != null) outbound.terminate(NodeGrpcServer.status(error));
    }

    @Override
    public synchronized void onError(Throwable error) {
      if (closed) return;
      closed = true;
      sessions.remove(node, this);
      release();
      outbound.terminate(null);
    }

    @Override
    public synchronized void onCompleted() {
      if (closed) return;
      closed = true;
      sessions.remove(node, this);
      release();
      outbound.terminate(null);
    }
  }
}
