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
 * are swallowed by {@link CompositeSessionKv}; iam's PostgreSQL store remains authoritative.
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
      kv().put(hash, value.toString().getBytes(StandardCharsets.UTF_8));
    } catch (Exception e) {
      throw new IllegalStateException("NATS session KV put failed", e);
    }
  }

  @Override
  public void delete(String hash) {
    try {
      kv().delete(hash);
    } catch (Exception e) {
      throw new IllegalStateException("NATS session KV delete failed", e);
    }
  }
}
