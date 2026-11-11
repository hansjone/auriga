package auriga.ops.upgrade;

import static auriga.common.error.CommonErrors.*;

import auriga.common.error.AurigaException;
import auriga.common.persistence.UuidV7;
import auriga.common.security.AurigaPrincipal;
import java.time.*;
import java.util.UUID;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.*;

public final class OpsUpgradeService {
  public final OpsRuntimeStore store;
  public final Clock clock;
  private final UUID tenant;

  public OpsUpgradeService(OpsRuntimeStore store, UUID tenant, Clock clock) {
    this.store = store;
    this.tenant = tenant;
    this.clock = clock;
    store.change(
        state -> {
          String installed = state.path("tenantId").asString();
          if (!installed.isEmpty() && !tenant.toString().equals(installed))
            throw new IllegalStateException("Operations installation tenant mismatch");
          state.put("tenantId", tenant.toString());
          return null;
        });
  }

  public void installation(AurigaPrincipal actor) {
    if (!actor.isAdminContext() || !tenant.equals(actor.tenantId()))
      throw new AurigaException(FORBIDDEN);
  }

  public ObjectNode status(AurigaPrincipal actor) {
    installation(actor);
    ObjectNode state = store.read();
    ObjectNode response = OpsRuntimeStore.JSON.createObjectNode();
    response.set("inventory", state.path("inventory"));
    response.set(
        "available",
        state.path("available").isArray()
            ? state.path("available")
            : OpsRuntimeStore.JSON.createArrayNode());
    response.set(
        "requests",
        state.path("upgrades").isArray()
            ? state.path("upgrades")
            : OpsRuntimeStore.JSON.createArrayNode());
    for (JsonNode item : response.path("requests")) {
      ((ObjectNode) item).remove("key");
      ((ObjectNode) item).remove("actor");
    }
    return response;
  }

  public ObjectNode register(AurigaPrincipal actor, String target, String key) {
    installation(actor);
    validKey(key);
    if (target == null || !target.matches("[A-Za-z0-9._-]{1,80}"))
      throw new AurigaException(VALIDATION_FAILED);
    return store.change(
        state -> {
          ArrayNode requests = state.withArray("upgrades");
          for (JsonNode previous : requests) {
            if (previous.path("key").asString().equals(key)
                && previous.path("actor").asString().equals(actor.subject())) {
              if (!previous.path("targetId").asString().equals(target))
                throw new AurigaException(CONFLICT);
              return (ObjectNode) previous.deepCopy();
            }
          }
          requireWorker(state);
          boolean known = false;
          for (JsonNode release : state.path("available"))
            if (release.path("id").asString().equals(target)) known = true;
          if (!known) throw new AurigaException(VALIDATION_FAILED);
          for (JsonNode previous : requests)
            if (java.util.Set.of("queued", "running").contains(previous.path("status").asString()))
              throw new AurigaException(CONFLICT);
          if (requests.size() >= 1000) throw new AurigaException(RATE_LIMITED);
          ObjectNode request = OpsRuntimeStore.JSON.createObjectNode();
          request.put("id", UuidV7.next().toString());
          request.put("targetId", target);
          request.put("key", key);
          request.put("actor", actor.subject());
          request.put("status", "queued");
          request.put("createdAt", clock.instant().toString());
          requests.add(request);
          return request.deepCopy();
        });
  }

  public void requireWorker(ObjectNode state) {
    try {
      Instant seen = Instant.parse(state.path("inventory").path("reportedAt").asString());
      if (seen.isBefore(clock.instant().minusSeconds(180))
          || seen.isAfter(clock.instant().plusSeconds(30))) throw new AurigaException(UNAVAILABLE);
    } catch (java.time.format.DateTimeParseException e) {
      throw new AurigaException(UNAVAILABLE);
    }
  }

  public static void validKey(String key) {
    if (key == null || !key.matches("[A-Za-z0-9_-]{16,100}"))
      throw new AurigaException(VALIDATION_FAILED);
  }
}
