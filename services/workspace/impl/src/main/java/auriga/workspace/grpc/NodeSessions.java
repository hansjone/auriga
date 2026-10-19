package auriga.workspace.grpc;

import static auriga.workspace.persistence.WorkspaceStore.*;

import auriga.common.context.TenantContext;
import auriga.common.error.*;
import auriga.common.grpc.GrpcErrors;
import auriga.contracts.node.v1.*;
import auriga.workspace.app.*;
import auriga.workspace.domain.WorkspaceCrypto;
import com.google.protobuf.Duration;
import io.grpc.*;
import io.grpc.stub.StreamObserver;
import java.security.cert.X509Certificate;
import java.sql.Connection;
import java.time.Instant;
import java.util.*;
import java.util.concurrent.ConcurrentHashMap;

/** Node authentication is exclusively mTLS; intentionally separate from internal JWT gRPC. */
public final class NodeSessions implements AutoCloseable {
  public static final Context.Key<X509Certificate> PEER = Context.key("workspace-node-certificate");
  private final NodeEnrollment enrollment;
  private final WorkspaceService workspace;
  private final Map<UUID, Session> sessions = new ConcurrentHashMap<>();

  public NodeSessions(NodeEnrollment enrollment, WorkspaceService workspace) {
    this.enrollment = enrollment;
    this.workspace = workspace;
    workspace.onChanged(this::dispatch);
  }

  public StreamObserver<SessionRequest> session(StreamObserver<SessionResponse> output) {
    X509Certificate leaf = PEER.get();
    if (leaf == null) {
      output.onError(GrpcErrors.toStatus(new AurigaException(CommonErrors.UNAUTHENTICATED)));
      return ignored();
    }
    UUID node = enrollment.identity(leaf), tenant = enrollment.store.route(node, "node");
    Session session = new Session(node, tenant, leaf, output);
    synchronized (sessions) {
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
    for (var s : List.copyOf(sessions.values()))
      if (s.seen.isBefore(workspace.clock.instant().minusSeconds(60)))
        s.fail(new AurigaException(CommonErrors.UNAVAILABLE));
  }

  public void revoke(UUID node) {
    var s = sessions.get(node);
    if (s != null) s.fail(new AurigaException(CommonErrors.UNAUTHENTICATED));
  }

  @Override
  public void close() {
    for (var s : List.copyOf(sessions.values()))
      s.fail(new AurigaException(CommonErrors.UNAVAILABLE));
  }

  private final class Session implements StreamObserver<SessionRequest> {
    final UUID node, tenant;
    final X509Certificate leaf;
    final StreamObserver<SessionResponse> output;
    volatile Instant seen = workspace.clock.instant();
    boolean hello, inventory, closed;
    Connection lease;
    final Map<String, Long> sent = new HashMap<>();

    Session(UUID node, UUID tenant, X509Certificate leaf, StreamObserver<SessionResponse> output) {
      this.node = node;
      this.tenant = tenant;
      this.leaf = leaf;
      this.output = output;
    }

    void acquire() {
      try {
        lease = enrollment.store.jdbc.getDataSource().getConnection();
        try (var statement = lease.prepareStatement("SELECT pg_try_advisory_lock(?)")) {
          statement.setLong(1, node.getMostSignificantBits() ^ node.getLeastSignificantBits());
          try (var result = statement.executeQuery()) {
            result.next();
            if (!result.getBoolean(1)) throw new AurigaException(CommonErrors.CONFLICT);
          }
        }
      } catch (Exception e) {
        release();
        if (e instanceof AurigaException a) throw a;
        throw new AurigaException(CommonErrors.UNAVAILABLE);
      }
    }

    void release() {
      if (lease != null)
        try {
          try (var statement = lease.prepareStatement("SELECT pg_advisory_unlock(?)")) {
            statement.setLong(1, node.getMostSignificantBits() ^ node.getLeastSignificantBits());
            statement.execute();
          }
          lease.close();
        } catch (Exception e) {
          org.slf4j.LoggerFactory.getLogger(getClass())
              .warn("Cannot release node session lease", e);
          try {
            lease.abort(Runnable::run);
          } catch (Exception ignored) {
            /* close below remains mandatory */
          }
        } finally {
          if (lease != null)
            try {
              lease.close();
            } catch (Exception e) {
              org.slf4j.LoggerFactory.getLogger(getClass())
                  .warn("Cannot close node lease connection", e);
            }
          lease = null;
        }
    }

    @Override
    public synchronized void onNext(SessionRequest request) {
      if (closed) return;
      try {
        enrollment.identity(leaf);
        TenantContext.run(
            tenant,
            () -> {
              if (!hello) {
                if (!request.hasHello())
                  throw new AurigaException(CommonErrors.PRECONDITION_FAILED);
                Hello h = request.getHello();
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
                            "UPDATE node SET hello=?,last_seen_at=?,agent_version=? WHERE tenant_id=? AND id=?",
                            h.toByteArray(),
                            time(workspace.clock.instant()),
                            h.getAgentVersion(),
                            tenant,
                            node));
                hello = true;
                output.onNext(
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
                  dispatch();
                } else if (request.hasStatus()) workspace.status(node, request.getStatus());
                else if (request.hasWake()) workspace.wake(node, request.getWake());
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
                else if (request.hasResult())
                  enrollment.store.tx(
                      () -> {
                        var result = request.getResult();
                        UUID command, id;
                        try {
                          command = UUID.fromString(result.getCommandId());
                          id = UUID.fromString(result.getInstanceId());
                        } catch (IllegalArgumentException e) {
                          return null;
                        }
                        if (result.getOk())
                          enrollment.store.jdbc.update(
                              "UPDATE workspace_job SET done=true WHERE tenant_id=? AND id=? AND instance_id=? AND instance_id IN (SELECT id FROM instance WHERE tenant_id=? AND node_id=?)",
                              tenant,
                              command,
                              id,
                              tenant,
                              node);
                        return null;
                      });
                else if (!request.hasLog())
                  throw new AurigaException(CommonErrors.VALIDATION_FAILED);
              }
              seen = workspace.clock.instant();
            });
      } catch (RuntimeException e) {
        fail(e);
      }
    }

    synchronized void dispatch() {
      if (closed || !inventory) return;
      try {
        enrollment.identity(leaf);
        enrollment.store.in(
            tenant,
            () -> {
              for (var row :
                  enrollment.store.jdbc.queryForList(
                      "SELECT * FROM instance WHERE tenant_id=? AND node_id=?", tenant, node)) {
                var spec = WorkspaceService.spec(row);
                String id = spec.getInstanceId();
                if ((boolean) row.get("deleted") && "absent".equals(row.get("phase"))) continue;
                if ((boolean) row.get("deleted")) {
                  output.onNext(
                      SessionResponse.newBuilder()
                          .setDelete(
                              DeleteInstance.newBuilder()
                                  .setInstanceId(id)
                                  .setGeneration(spec.getGeneration())
                                  .setPurgeData((boolean) row.get("purge_data")))
                          .build());
                } else if (!Objects.equals(sent.get(id), spec.getGeneration())) {
                  output.onNext(
                      SessionResponse.newBuilder()
                          .setApply(ApplySpec.newBuilder().setSpec(spec))
                          .build());
                  sent.put(id, spec.getGeneration());
                }
              }
              for (var job :
                  enrollment.store.jdbc.queryForList(
                      "SELECT j.command FROM workspace_job j JOIN instance i ON i.id=j.instance_id AND i.tenant_id=j.tenant_id WHERE j.tenant_id=? AND i.node_id=? AND j.kind='restart' AND NOT j.done AND j.due_at<=?",
                      tenant,
                      node,
                      time(workspace.clock.instant()))) {
                try {
                  output.onNext(
                      SessionResponse.newBuilder()
                          .setCommand(Command.parseFrom((byte[]) job.get("command")))
                          .build());
                } catch (com.google.protobuf.InvalidProtocolBufferException e) {
                  throw new IllegalStateException(e);
                }
              }
              return null;
            });
      } catch (RuntimeException e) {
        fail(e);
      }
    }

    synchronized void fail(Throwable error) {
      if (closed) return;
      closed = true;
      sessions.remove(node, this);
      release();
      output.onError(GrpcErrors.toStatus(error));
    }

    @Override
    public synchronized void onError(Throwable error) {
      if (closed) return;
      closed = true;
      sessions.remove(node, this);
      release();
    }

    @Override
    public synchronized void onCompleted() {
      if (closed) return;
      closed = true;
      sessions.remove(node, this);
      release();
      output.onCompleted();
    }
  }
}
