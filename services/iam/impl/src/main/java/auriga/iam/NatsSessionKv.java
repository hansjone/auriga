package auriga.iam;

import io.nats.client.Connection;
import io.nats.client.KeyValue;
import io.nats.client.api.KeyValueConfiguration;
import io.nats.client.api.KeyValueEntry;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.util.concurrent.atomic.AtomicReference;
import tools.jackson.databind.node.ObjectNode;

/**
 * Write-through cache for edge: bucket {@code auriga-sessions}, key = cookie SHA-256 hex. Failures
 * are retained for retry by {@link CompositeSessionKv}; login responses also require publication
 * confirmation. IAM's PostgreSQL store remains authoritative.
 */
public final class NatsSessionKv implements SessionKv {
  static final String BUCKET = "auriga-sessions";

  private final Connection connection;
  private final ContractSchemas schemas;
  private final AtomicReference<KeyValue> kv = new AtomicReference<>();

  public NatsSessionKv(Connection connection, ContractSchemas schemas) {
    this.connection = connection;
    this.schemas = schemas;
  }

  private KeyValue kv() {
    KeyValue existing = kv.get();
    if (existing != null) return existing;
    synchronized (kv) {
      existing = kv.get();
      if (existing != null) return existing;
      try {
        existing = connection.keyValue(BUCKET);
      } catch (Exception missing) {
        try {
          connection
              .keyValueManagement()
              .create(KeyValueConfiguration.builder().name(BUCKET).ttl(Duration.ofDays(7)).build());
          existing = connection.keyValue(BUCKET);
        } catch (Exception e) {
          throw new IllegalStateException("NATS sessions KV unavailable", e);
        }
      }
      kv.set(existing);
      return existing;
    }
  }

  @Override
  public ObjectNode get(String hash) {
    try {
      KeyValueEntry entry = kv().get(hash);
      if (entry == null || entry.getValue() == null) return null;
      return (ObjectNode)
          Json.MAPPER.readTree(new String(entry.getValue(), StandardCharsets.UTF_8));
    } catch (Exception e) {
      return null;
    }
  }

  @Override
  public void put(String hash, ObjectNode value) {
    schemas.validate("kv/session.schema.json", value);
    try {
      KeyValue target = kv();
      // Also fence a timed-out write whose packet reaches NATS after a newer projection.
      // Do not use create(): jnats deliberately retries create across tombstones.
      long previous = revision(target, hash);
      target.update(hash, value.toString().getBytes(StandardCharsets.UTF_8), previous);
    } catch (Exception e) {
      throw new IllegalStateException("NATS session KV put failed", e);
    }
  }

  @Override
  public void delete(String hash) {
    try {
      KeyValue target = kv();
      // A missing key still needs a tombstone, to reject an in-flight initial write at revision 0.
      target.delete(hash, revision(target, hash));
    } catch (Exception e) {
      throw new IllegalStateException("NATS session KV delete failed", e);
    }
  }

  private long revision(KeyValue target, String hash) throws Exception {
    KeyValueEntry entry = target.get(hash);
    if (entry != null) return entry.getRevision();
    // get() hides DEL entries; history retains their revision for compare-and-set.
    return target.history(hash).stream().mapToLong(KeyValueEntry::getRevision).max().orElse(0);
  }
}
