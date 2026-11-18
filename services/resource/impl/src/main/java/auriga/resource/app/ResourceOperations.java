package auriga.resource.app;

import static auriga.resource.web.ResourceBody.JSON;
import static auriga.resource.web.ResourceBody.invalid;

import auriga.common.audit.AuditEntry;
import auriga.common.audit.OutboxAuditLogger;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.events.EventPublisher;
import auriga.common.security.AurigaPrincipal;
import auriga.resource.persistence.ResourceStore;
import auriga.resource.persistence.ResourceWorkflowStore;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.time.Clock;
import java.util.HexFormat;
import java.util.Objects;
import java.util.UUID;
import java.util.function.Consumer;
import java.util.function.Function;
import org.springframework.dao.DataIntegrityViolationException;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** Shared resource mutation transactions, idempotency receipts, and audit records. */
public final class ResourceOperations {
  static final long MAX = 9007199254740991L;
  private final ResourceStore resources;
  private final ResourceWorkflowStore db;
  private final ResourceAccess access;
  private final OutboxAuditLogger audit;
  private final Clock clock;

  public ResourceOperations(
      ResourceStore resources,
      ResourceWorkflowStore db,
      ResourceAccess access,
      EventPublisher events,
      Clock clock) {
    this.resources = resources;
    this.db = db;
    this.access = access;
    this.audit = new OutboxAuditLogger(events);
    this.clock = clock;
  }

  static void match(String etag, long revision) {
    if (etag == null || !etag.equals("\"" + revision + "\""))
      throw new AurigaException(CommonErrors.PRECONDITION_FAILED);
    if (revision >= MAX) throw new AurigaException(CommonErrors.CONFLICT);
  }

  private ResourceAccess.Reader authorize(AurigaPrincipal actor, String permission, boolean admin) {
    if (permission.equals(ResourceAccess.READ)) return access.member(actor);
    if (permission.equals("resource.levels.admin")) return access.owner(actor);
    return admin ? access.administrator(actor, permission) : access.memberWriter(actor, permission);
  }

  JsonNode operation(
      AurigaPrincipal actor,
      String permission,
      boolean admin,
      String op,
      String key,
      ObjectNode body,
      String etag,
      Consumer<ResourceAccess.Reader> guard,
      Function<ResourceAccess.Reader, JsonNode> action) {
    return operation(actor, permission, admin, op, key, body, etag, guard, action, true);
  }

  JsonNode operation(
      AurigaPrincipal actor,
      String permission,
      boolean admin,
      String op,
      String key,
      ObjectNode body,
      String etag,
      Consumer<ResourceAccess.Reader> guard,
      Function<ResourceAccess.Reader, JsonNode> action,
      boolean replayResponse) {
    authorize(actor, permission, admin);
    if (key == null || !key.matches("[\\x21-\\x7E]{1,128}")) throw invalid();
    String fingerprint;
    try {
      fingerprint =
          hash(op + "\n" + Objects.toString(etag, "") + "\n" + ResourceSigning.canonical(body));
    } catch (IllegalArgumentException e) {
      throw invalid();
    }
    try {
      return resources.write(
          () -> {
            db.lock(actor.subject() + ":" + op + ":" + key);
            var r = authorize(actor, permission, admin);
            guard.accept(r);
            var replay = db.replay(r.account(), op, key, fingerprint, clock.instant());
            if (replay.isPresent()) {
              if (!replayResponse) throw new AurigaException(CommonErrors.CONFLICT);
              return replay.get();
            }
            var result = action.apply(r);
            var receipt = replayResponse ? result : JSON.createObjectNode().put("completed", true);
            db.remember(r.account(), op, key, fingerprint, receipt, clock.instant());
            return result;
          });
    } catch (DataIntegrityViolationException e) {
      throw new AurigaException(CommonErrors.CONFLICT);
    }
  }

  public static String hash(String text) {
    try {
      return HexFormat.of()
          .formatHex(
              MessageDigest.getInstance("SHA-256").digest(text.getBytes(StandardCharsets.UTF_8)));
    } catch (NoSuchAlgorithmException e) {
      throw new IllegalStateException(e);
    }
  }

  void recorded(ResourceAccess.Reader r, String action, UUID id) {
    audit.record(auditEntry(r, action, id).build());
  }

  void recorded(ResourceAccess.Reader r, String action, UUID id, String reason) {
    audit.record(auditEntry(r, action, id).detail("reason", reason).build());
  }

  private static AuditEntry.Builder auditEntry(ResourceAccess.Reader r, String action, UUID id) {
    return AuditEntry.builder(action)
        .target("resource", id.toString())
        .actor(new AuditEntry.Actor("user", r.account().toString()));
  }
}
