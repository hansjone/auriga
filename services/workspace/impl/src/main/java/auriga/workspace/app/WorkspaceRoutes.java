package auriga.workspace.app;

import auriga.common.events.NatsEvents;
import auriga.workspace.persistence.WorkspaceStore;
import io.nats.client.*;
import io.nats.client.api.KeyValueConfiguration;
import java.net.URI;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.time.Instant;
import java.util.*;
import java.util.concurrent.*;
import java.util.function.Supplier;

/** Reconciles committed database state to the route KV, including restart/outage recovery. */
public final class WorkspaceRoutes implements AutoCloseable {
  public static final String BUCKET = "auriga-routes";
  private final WorkspaceStore store;
  private final Supplier<Connection> connect;
  private final ScheduledExecutorService worker =
      Executors.newSingleThreadScheduledExecutor(
          Thread.ofVirtual().name("workspace-routes").factory());
  private Connection connection;

  public WorkspaceRoutes(WorkspaceStore store, Supplier<Connection> connect) {
    this.store = store;
    this.connect = connect;
  }

  public static WorkspaceRoutes configured(
      WorkspaceStore store, String url, String creds, String seed, boolean tls) {
    return new WorkspaceRoutes(
        store,
        () -> {
          try {
            return Nats.connect(
                new Options.Builder(NatsEvents.options(url, "workspace-routes", creds, seed, tls))
                    .connectionTimeout(Duration.ofSeconds(2))
                    .build());
          } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
            throw new IllegalStateException("Route connection interrupted", e);
          } catch (java.io.IOException e) {
            throw new IllegalStateException("Route connection unavailable", e);
          }
        });
  }

  public void start() {
    // No NATS or database work on application startup or business commit threads.
    worker.scheduleWithFixedDelay(this::refresh, 0, 1, TimeUnit.SECONDS);
  }

  public synchronized void refresh() {
    try {
      requireNoTransaction();
      if (connection == null || connection.getStatus() == Connection.Status.CLOSED)
        connection = connect.get();
      if (connection.getStatus() != Connection.Status.CONNECTED) return;
      if (!connection.keyValueManagement().getBucketNames().contains(BUCKET)) {
        try {
          connection
              .keyValueManagement()
              .create(KeyValueConfiguration.builder().name(BUCKET).build());
        } catch (io.nats.client.JetStreamApiException e) {
          if (!connection.keyValueManagement().getBucketNames().contains(BUCKET)) throw e;
        }
      }
      KeyValue kv = connection.keyValue(BUCKET);
      for (UUID tenant : store.tenants()) {
        var ids =
            store.in(
                tenant,
                () ->
                    store.jdbc.queryForList(
                        "SELECT id FROM instance WHERE tenant_id=? ORDER BY id",
                        UUID.class,
                        tenant));
        // Read KV revisions BEFORE the fresh DB snapshot. A stale publisher cannot
        // overwrite any newer put/delete: CAS includes tombstone revisions as well.
        Map<String, io.nats.client.api.KeyValueEntry> observed = new HashMap<>();
        for (UUID id : ids) observed.put(id.toString(), latest(kv, id.toString()));
        var rows =
            store.in(
                tenant,
                () ->
                    store.jdbc.queryForList(
                        "SELECT i.*,n.ingress_endpoint,n.revoked AS node_revoked,n.last_seen_at AS node_seen FROM instance i LEFT JOIN node n ON n.tenant_id=i.tenant_id AND n.id=i.node_id WHERE i.tenant_id=? ORDER BY i.id",
                        tenant));
        // store.in has committed and returned its connection before any NATS write.
        for (var row : rows) {
          String id = row.get("id").toString();
          // Instances inserted after the ID scan are picked up by the next pass.
          if (observed.containsKey(id)) write(kv, id, encode(row), observed.get(id));
        }
      }
    } catch (Exception e) {
      // All state remains in PostgreSQL. Next pass resends the latest state, never old queued URLs.
      org.slf4j.LoggerFactory.getLogger(getClass())
          .warn("Workspace route reconciliation unavailable");
    }
  }

  static io.nats.client.api.KeyValueEntry latest(KeyValue kv, String id) throws Exception {
    requireNoTransaction();
    // get() hides deletion markers; history preserves the last subject revision.
    var history = kv.history(id);
    return history.isEmpty() ? null : history.getLast();
  }

  static void write(KeyValue kv, String id, byte[] value) throws Exception {
    write(kv, id, value, latest(kv, id));
  }

  static boolean write(
      KeyValue kv, String id, byte[] value, io.nats.client.api.KeyValueEntry observed)
      throws Exception {
    requireNoTransaction();
    boolean live =
        observed != null && observed.getOperation() == io.nats.client.api.KeyValueOperation.PUT;
    if (value == null && !live
        || value != null && live && Arrays.equals(value, observed.getValue())) return true;
    long revision = observed == null ? 0 : observed.getRevision();
    try {
      if (value == null) kv.delete(id, revision);
      else kv.update(id, value, revision);
      return true;
    } catch (io.nats.client.JetStreamApiException e) {
      if (e.getApiErrorCode() == 10071)
        return false; // wrong last subject sequence: retry latest DB state next pass
      throw e;
    }
  }

  private static void requireNoTransaction() {
    if (org.springframework.transaction.support.TransactionSynchronizationManager
        .isActualTransactionActive())
      throw new IllegalStateException(
          "Route NATS operations must run outside database transactions");
  }

  static byte[] encode(Map<String, Object> row) throws Exception {
    String endpoint = (String) row.get("ingress_endpoint");
    if (Boolean.TRUE.equals(row.get("deleted"))
        || row.get("node_id") == null
        || Boolean.TRUE.equals(row.get("node_revoked"))
        || !validEndpoint(endpoint)) return null;
    var route = new LinkedHashMap<String, Object>();
    route.put("instanceId", row.get("id").toString());
    route.put("tenantId", row.get("tenant_id").toString());
    route.put("ownerId", row.get("owner_id").toString());
    route.put("nodeId", row.get("node_id").toString());
    route.put("host", null);
    route.put("ingress", endpoint);
    String phase = (String) row.get("phase");
    String state =
        switch (phase) {
          case "running", "pending_restart" ->
              Boolean.TRUE.equals(row.get("ready")) ? "running" : "starting";
          case "stopped", "absent" -> "stopped";
          case "failed" -> "failed";
          default -> "starting";
        };
    if (Boolean.TRUE.equals(row.get("migration_pending"))
        || row.get("node_seen") == null
        || WorkspaceStore.instant(row, "node_seen").isBefore(Instant.now().minusSeconds(60)))
      state = "failed";
    route.put("state", state);
    route.put("generation", ((Number) row.get("generation")).longValue());
    route.put("updatedAt", WorkspaceStore.instant(row, "updated_at").toString());
    return WorkspaceService.JSON.writeValueAsString(route).getBytes(StandardCharsets.UTF_8);
  }

  public static boolean validEndpoint(String raw) {
    if (raw == null || raw.isBlank() || raw.length() > 4096) return false;
    try {
      var u = URI.create(raw);
      if (u.isOpaque()
          || u.getPath() == null
          || u.getRawUserInfo() != null
          || u.getRawQuery() != null
          || u.getRawFragment() != null) return false;
      return switch (Objects.toString(u.getScheme(), "")) {
        case "https" -> u.getHost() != null && (u.getPath().isEmpty() || u.getPath().equals("/"));
        case "unix" -> u.getRawAuthority() == null && u.getPath().startsWith("/");
        case "npipe" ->
            u.getRawAuthority() == null
                && u.getPath().startsWith("//./pipe/")
                && u.getPath().length() > 10;
        default -> false;
      };
    } catch (IllegalArgumentException e) {
      return false;
    }
  }

  @Override
  public void close() {
    worker.shutdownNow();
    synchronized (this) {
      if (connection != null)
        try {
          connection.close();
        } catch (InterruptedException e) {
          Thread.currentThread().interrupt();
        }
    }
  }
}
