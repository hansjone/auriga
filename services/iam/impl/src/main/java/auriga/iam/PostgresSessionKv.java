package auriga.iam;

import java.time.Instant;
import tools.jackson.databind.node.ObjectNode;

public final class PostgresSessionKv implements SessionKv {
  private final IamStore store;
  private final ContractSchemas schemas;
  private final java.time.Clock clock;

  public PostgresSessionKv(IamStore store, ContractSchemas schemas, java.time.Clock clock) {
    this.store = store;
    this.schemas = schemas;
    this.clock = clock;
  }

  @Override
  public ObjectNode get(String hash) {
    ObjectNode row = store.get("session_kv", hash, false);
    if (row == null) return null;
    ObjectNode value = (ObjectNode) row.get("value");
    if (!clock.instant().isBefore(Instant.parse(value.path("idleExpiresAt").asString()))
        || !clock.instant().isBefore(Instant.parse(value.path("expiresAt").asString())))
      return null;
    return value;
  }

  @Override
  public void put(String hash, ObjectNode value) {
    schemas.validate("kv/session.schema.json", value);
    store.delete("session_kv", hash);
    store.insert(
        "session_kv",
        hash,
        Json.object("value", value),
        Instant.parse(value.path("idleExpiresAt").asString()));
  }

  @Override
  public void delete(String hash) {
    store.delete("session_kv", hash);
  }
}
