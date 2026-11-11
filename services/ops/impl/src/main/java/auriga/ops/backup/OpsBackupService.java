package auriga.ops.backup;

import static auriga.common.error.CommonErrors.*;

import auriga.common.error.AurigaException;
import auriga.common.persistence.UuidV7;
import auriga.common.security.AurigaPrincipal;
import auriga.ops.upgrade.*;
import java.io.*;
import java.nio.file.*;
import java.security.*;
import java.time.Instant;
import java.util.*;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.*;

public final class OpsBackupService {
  private final OpsUpgradeService operations;

  public OpsBackupService(OpsUpgradeService operations) {
    this.operations = operations;
  }

  public ObjectNode status(AurigaPrincipal actor) {
    operations.installation(actor);
    ObjectNode state = operations.store.read();
    ObjectNode result = OpsRuntimeStore.JSON.createObjectNode();
    result.set("plan", plan(state));
    result.set(
        "items",
        state.path("backups").isArray()
            ? state.path("backups")
            : OpsRuntimeStore.JSON.createArrayNode());
    result.set("worker", state.path("inventory"));
    return result;
  }

  public ObjectNode configure(AurigaPrincipal actor, boolean enabled, int interval, long revision) {
    operations.installation(actor);
    if (interval < 1 || interval > 720 || revision < 0)
      throw new AurigaException(VALIDATION_FAILED);
    return operations.store.change(
        state -> {
          ObjectNode previous = plan(state);
          if (previous.path("revision").asLong() != revision)
            throw new AurigaException(PRECONDITION_FAILED);
          ObjectNode value = OpsRuntimeStore.JSON.createObjectNode();
          value.put("enabled", enabled);
          value.put("intervalHours", interval);
          value.put("revision", revision + 1);
          value.put("nextAt", operations.clock.instant().plusSeconds(interval * 3600L).toString());
          value.put("actor", actor.subject());
          state.set("plan", value);
          return value.deepCopy();
        });
  }

  private ObjectNode plan(ObjectNode state) {
    if (state.path("plan").isObject()) return (ObjectNode) state.path("plan").deepCopy();
    ObjectNode value = OpsRuntimeStore.JSON.createObjectNode();
    value.put("enabled", false);
    value.put("intervalHours", 24);
    value.put("revision", 0);
    return value;
  }

  public ObjectNode trigger(AurigaPrincipal actor, String key) {
    operations.installation(actor);
    OpsUpgradeService.validKey(key);
    return operations.store.change(
        state -> {
          ArrayNode backups = state.withArray("backups");
          for (JsonNode previous : backups)
            if (previous.path("key").asString().equals(key)
                && previous.path("actor").asString().equals(actor.subject()))
              return (ObjectNode) previous.deepCopy();
          operations.requireWorker(state);
          for (JsonNode previous : backups)
            if (Set.of("queued", "running").contains(previous.path("status").asString()))
              throw new AurigaException(CONFLICT);
          if (backups.size() >= 1000) throw new AurigaException(RATE_LIMITED);
          ObjectNode request = OpsRuntimeStore.JSON.createObjectNode();
          request.put("id", UuidV7.next().toString());
          request.put("actor", actor.subject());
          request.put("key", key);
          request.put("createdAt", operations.clock.instant().toString());
          request.put("status", "queued");
          backups.add(request);
          return request.deepCopy();
        });
  }

  public ObjectNode ticket(AurigaPrincipal actor, UUID id) {
    operations.installation(actor);
    if (actor.sessionId() == null) throw new AurigaException(FORBIDDEN);
    return operations.store.change(
        state -> {
          completed(state, id);
          ArrayNode tickets = state.withArray("tickets");
          for (int i = tickets.size() - 1; i >= 0; i--)
            if (!Instant.parse(tickets.get(i).path("expiresAt").asString())
                .isAfter(operations.clock.instant())) tickets.remove(i);
          if (tickets.size() >= 128) throw new AurigaException(RATE_LIMITED);
          byte[] bytes = new byte[32];
          new SecureRandom().nextBytes(bytes);
          String secret = Base64.getUrlEncoder().withoutPadding().encodeToString(bytes);
          ObjectNode record = OpsRuntimeStore.JSON.createObjectNode();
          String expires = operations.clock.instant().plusSeconds(60).toString();
          record.put("hash", hash(secret));
          record.put("actor", actor.subject());
          record.put("session", actor.sessionId());
          record.put("backupId", id.toString());
          record.put("expiresAt", expires);
          tickets.add(record);
          ObjectNode response = OpsRuntimeStore.JSON.createObjectNode();
          response.put("token", secret);
          response.put("expiresAt", expires);
          return response;
        });
  }

  public InputStream download(AurigaPrincipal actor, UUID id, String token) {
    operations.installation(actor);
    if (token == null || !token.matches("[A-Za-z0-9_-]{43}")) throw new AurigaException(FORBIDDEN);
    Path archive =
        operations.store.change(
            state -> {
              completed(state, id);
              ArrayNode tickets = state.withArray("tickets");
              for (int i = 0; i < tickets.size(); i++) {
                JsonNode ticket = tickets.get(i);
                if (ticket.path("hash").asString().equals(hash(token))
                    && ticket.path("backupId").asString().equals(id.toString())
                    && ticket.path("actor").asString().equals(actor.subject())
                    && ticket.path("session").asString().equals(actor.sessionId())
                    && Instant.parse(ticket.path("expiresAt").asString())
                        .isAfter(operations.clock.instant())) {
                  tickets.remove(i);
                  return operations.store.root().resolve("backups").resolve(id + ".dump");
                }
              }
              throw new AurigaException(FORBIDDEN);
            });
    // Persist consumption before opening the stream, including failed transfers.
    try {
      if (Files.isSymbolicLink(archive.getParent()))
        throw new IOException("Symlink backup directory");
      return Files.newInputStream(archive, LinkOption.NOFOLLOW_LINKS);
    } catch (IOException e) {
      throw new AurigaException(UNAVAILABLE);
    }
  }

  private void completed(ObjectNode state, UUID id) {
    for (JsonNode item : state.path("backups"))
      if (item.path("id").asString().equals(id.toString())
          && item.path("status").asString().equals("succeeded")) return;
    throw new AurigaException(NOT_FOUND);
  }

  private static String hash(String value) {
    try {
      return HexFormat.of()
          .formatHex(
              MessageDigest.getInstance("SHA-256")
                  .digest(value.getBytes(java.nio.charset.StandardCharsets.UTF_8)));
    } catch (NoSuchAlgorithmException e) {
      throw new IllegalStateException(e);
    }
  }
}
