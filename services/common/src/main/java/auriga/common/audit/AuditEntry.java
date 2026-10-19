package auriga.common.audit;

import java.util.LinkedHashMap;
import java.util.Map;
import java.util.Objects;

/**
 * One audit record (events/audit.schema.json {@code AuditRecorded}); actor, client and request id
 * are filled from the current request unless {@link Builder#actor} overrides them.
 */
public record AuditEntry(
    String action,
    Target target,
    Result result,
    String errorCode,
    AuditChanges changes,
    Map<String, Object> details,
    Actor actor) {
  public enum Result {
    SUCCESS,
    FAILURE,
    DENIED
  }

  public record Target(String type, String id, String name) {
    public Target {
      Objects.requireNonNull(type, "type");
      Objects.requireNonNull(id, "id");
    }
  }

  /** {@code type}: user, service_account, device, instance, node, system. */
  public record Actor(String type, String id) {
    public static final Actor SYSTEM = new Actor("system", null);
  }

  public static Builder builder(String action) {
    return new Builder(action);
  }

  public static final class Builder {
    private final String action;
    private Target target;
    private Result result = Result.SUCCESS;
    private String errorCode;
    private AuditChanges changes;
    private final Map<String, Object> details = new LinkedHashMap<>();
    private Actor actor;

    private Builder(String action) {
      this.action = Objects.requireNonNull(action, "action");
    }

    public Builder target(String type, String id) {
      return target(type, id, null);
    }

    public Builder target(String type, String id, String name) {
      this.target = new Target(type, id, name);
      return this;
    }

    public Builder result(Result result) {
      this.result = Objects.requireNonNull(result);
      return this;
    }

    public Builder failure(String errorCode) {
      this.result = Result.FAILURE;
      this.errorCode = errorCode;
      return this;
    }

    public Builder denied(String errorCode) {
      this.result = Result.DENIED;
      this.errorCode = errorCode;
      return this;
    }

    public Builder changes(AuditChanges changes) {
      this.changes = changes;
      return this;
    }

    public Builder detail(String key, Object value) {
      details.put(key, value);
      return this;
    }

    public Builder actor(Actor actor) {
      this.actor = actor;
      return this;
    }

    public AuditEntry build() {
      return new AuditEntry(
          action,
          target,
          result,
          errorCode,
          changes,
          java.util.Collections.unmodifiableMap(new LinkedHashMap<>(details)),
          actor);
    }
  }
}
