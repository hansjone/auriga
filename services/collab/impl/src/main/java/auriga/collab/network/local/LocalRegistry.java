package auriga.collab.network.local;

import static auriga.collab.network.local.LocalErrors.*;

import auriga.collab.api.NetworkSourceAuthority;
import auriga.collab.api.NetworkSourceAuthority.Target;
import auriga.collab.app.NetworkErrors;
import auriga.collab.app.NetworkJson;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.error.ErrorCode;
import auriga.common.persistence.TenantTransactionManager;
import auriga.common.persistence.UuidV7;
import auriga.common.security.AuthorizationRules;
import auriga.iam.api.NetworkAuthority;
import auriga.iam.api.NetworkAuthority.*;
import java.math.BigDecimal;
import java.math.BigInteger;
import java.time.Clock;
import java.util.Map;
import java.util.Objects;
import java.util.UUID;
import java.util.function.Supplier;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.transaction.TransactionDefinition;
import org.springframework.transaction.support.AbstractPlatformTransactionManager;
import org.springframework.transaction.support.TransactionSynchronizationManager;
import org.springframework.transaction.support.TransactionTemplate;
import tools.jackson.databind.JsonNode;

/**
 * Server-only local witness and single-use contact creation admission. This is an internal
 * persistence API, not a wire endpoint: the ingress must sign/map the results and enforce the
 * registration recovery result together with receipt recovery before enabling new business. No host
 * mappings, local policy defaults or session trees are stored here.
 */
public final class LocalRegistry {
  private static final BigInteger MAX = new BigInteger("18446744073709551615");
  private final JdbcTemplate jdbc;
  private final TransactionTemplate tx;
  private final TransactionTemplate outside;
  private final NetworkSourceAuthority authority;
  private final NetworkAuthority iam;
  private final Clock clock;

  public LocalRegistry(
      JdbcTemplate jdbc,
      TenantTransactionManager manager,
      NetworkSourceAuthority authority,
      NetworkAuthority iam,
      Clock clock) {
    this.jdbc = Objects.requireNonNull(jdbc);
    this.tx = new TransactionTemplate(manager);
    var suspension = new TenantTransactionManager(manager.getDataSource());
    suspension.setTransactionSynchronization(
        AbstractPlatformTransactionManager.SYNCHRONIZATION_NEVER);
    this.outside = new TransactionTemplate(suspension);
    outside.setPropagationBehavior(TransactionDefinition.PROPAGATION_NOT_SUPPORTED);
    this.authority = authority;
    this.iam = iam;
    this.clock = Objects.requireNonNull(clock);
  }

  /** Original-operation lookup precedes revision/high-water checks. Never allocates implicitly. */
  public Registration register(UUID tenant, String json) {
    return run(
        tenant,
        () -> {
          var frame = LocalFrames.read("LocalRegister", json);
          Target target = target(tenant);
          checkIam(target.realm(), target.node(), target.node(), "0", Operation.CONNECT);
          var node = lockNode(tenant, target);
          fence(tenant, target, node, frame);
          String local = text(frame, "candidate_local");
          String op = text(frame, "allocation_op_id");
          String fingerprint = text(frame, "allocation_fingerprint");
          var original = registration(tenant, target.realm(), target.node(), local, op);
          if (original != null) {
            if (LocalRecovery.matches(original, local, op, fingerprint))
              return ok(view(original, "existing"));
            return conflict(tenant, target, "node_identity_conflict", ALLOCATION_CONFLICT);
          }
          ErrorCode health = health(node);
          if (health != null) return failed(health);
          if (number(local).compareTo(number(node, "local_high_water")) <= 0)
            return conflict(tenant, target, "node_identity_conflict", ALLOCATION_CONFLICT);
          BigInteger revision = number(node, "registration_revision");
          require(
              number(text(frame, "expected_registration_revision")).equals(revision),
              NetworkErrors.CONFLICT);
          require(revision.compareTo(MAX) < 0, NetworkErrors.QUOTA_EXCEEDED);
          UUID id = UuidV7.next();
          BigInteger next = revision.add(BigInteger.ONE);
          jdbc.update(
              """
          INSERT INTO collab.network_local_registration(
            id,tenant_id,realm,node_id,local_id,allocation_op_id,allocation_fingerprint,
            registration_revision,previous_high_water,granted_epoch,granted_boot_id)
          VALUES (?,?,?,?,?,?,?,?,?,?,?)
          """,
              id,
              tenant,
              target.realm(),
              target.node(),
              decimal(local),
              op,
              fingerprint,
              new BigDecimal(next),
              node.get("local_high_water"),
              target.epoch(),
              text(frame, "boot_id"));
          int changed =
              jdbc.update(
                  """
          UPDATE collab.network_node SET local_high_water=?,registration_revision=?,updated_at=now()
           WHERE tenant_id=? AND realm=? AND node_id=? AND registration_revision=?
          """,
                  decimal(local),
                  new BigDecimal(next),
                  tenant,
                  target.realm(),
                  target.node(),
                  new BigDecimal(revision));
          require(changed == 1, NetworkErrors.CONFLICT);
          return ok(
              new Registration(
                  id,
                  local,
                  op,
                  fingerprint,
                  next.toString(),
                  local,
                  "registered",
                  target.epoch(),
                  text(frame, "boot_id")));
        });
  }

  /**
   * Only the first committed assignment returns a create permit. A retry may use a new attempt ID,
   * but receives the ORIGINAL attempt, for lookup only; it never transfers the permit to a clone.
   */
  public Assignment assign(UUID tenant, String json) {
    return run(
        tenant,
        () -> {
          var frame = LocalFrames.read("ContactAssign", json);
          Target target = target(tenant);
          var address = frame.path("assigned_address");
          require(
              target.realm().equals(text(address, "realm"))
                  && target.node().equals(text(address, "node")),
              NetworkErrors.UNAUTHORIZED);
          // Recursive subagent admission requires the separate N2 host/lineage gate.
          require("session".equals(text(address, "type")), NetworkErrors.PROTOCOL_ERROR);
          checkIam(target.realm(), target.node(), target.node(), "0", Operation.RECEIVE);
          UUID contactId = uuid(frame, "contact_id");
          // Match CONTACT_DECIDE/expiry's contact -> node order. READY only locks the node.
          var contact =
              one(
                  """
          SELECT * FROM collab.network_contact WHERE tenant_id=? AND id=? FOR UPDATE
          """,
                  tenant,
                  contactId);
          require(
              contact != null
                  && target.realm().equals(contact.get("target_realm"))
                  && target.node().equals(contact.get("target_node")),
              NetworkErrors.UNAUTHORIZED);
          var node = lockNode(tenant, target);
          fence(tenant, target, node, frame);
          ErrorCode health = health(node);
          if (health != null) return failed(health);
          require("conversation".equals(contact.get("scope")), NetworkErrors.PROTOCOL_ERROR);
          require(target.realm().equals(contact.get("source_realm")), NetworkErrors.UNAUTHORIZED);
          require(
              ((java.sql.Timestamp) contact.get("expires_at")).toInstant().isAfter(clock.instant()),
              NetworkErrors.CONTACT_EXPIRED);
          require("allowed".equals(contact.get("status")), NetworkErrors.CONTACT_DENIED);
          require(
              number(text(frame, "decision_version")).equals(number(contact, "decision_version")),
              NetworkErrors.CONFLICT);
          checkIam(
              contact.get("source_realm").toString(),
              contact.get("source_node").toString(),
              target.node(),
              contact.get("source_local").toString(),
              Operation.FIRST_CONTACT,
              authorizeContact(tenant, target, contact));
          require(
              ((java.sql.Timestamp) contact.get("expires_at")).toInstant().isAfter(clock.instant()),
              NetworkErrors.CONTACT_EXPIRED);
          require(target.validUntil().isAfter(clock.instant()), NetworkErrors.UNAUTHORIZED);
          String local = text(address, "local");
          UUID registrationId = uuid(frame, "registration_id");
          var reg = registered(tenant, target.realm(), target.node(), local, registrationId, null);
          var existing =
              one(
                  """
          SELECT * FROM collab.network_contact_assignment WHERE tenant_id=? AND contact_id=?
          """,
                  tenant,
                  contactId);
          if (existing != null) {
            require(
                registrationId.equals(existing.get("registration_id"))
                    && number(local).equals(number(existing, "local_id")),
                NetworkErrors.CONFLICT);
            return ok(assignment(existing, false));
          }
          require(
              one(
                      """
          SELECT id FROM collab.network_contact_assignment WHERE tenant_id=? AND registration_id=?
          """,
                      tenant,
                      reg.get("id"))
                  == null,
              NetworkErrors.CONFLICT);
          UUID id = UuidV7.next();
          String attempt = text(frame, "creation_attempt_id");
          jdbc.update(
              """
          INSERT INTO collab.network_contact_assignment(
            id,tenant_id,contact_id,realm,node_id,local_id,registration_id,creation_attempt_id,
            decision_version,granted_epoch,granted_boot_id)
          VALUES (?,?,?,?,?,?,?,?,?,?,?)
          """,
              id,
              tenant,
              contactId,
              target.realm(),
              target.node(),
              decimal(local),
              registrationId,
              attempt,
              contact.get("decision_version"),
              target.epoch(),
              text(frame, "boot_id"));
          return ok(
              new Assignment(
                  contactId,
                  target.realm(),
                  target.node(),
                  local,
                  registrationId,
                  attempt,
                  "assigned",
                  true,
                  target.epoch(),
                  text(frame, "boot_id")));
        });
  }

  /**
   * Registration-only part of READY. Receipt checkpoints/HostAccepted evidence belong to the
   * mailbox recovery boundary, which must also pass. A matching pending head explains at most one
   * missing registration response; counters alone cannot repair an old installation/backup.
   */
  public Recovery reconcileRegistration(UUID tenant, String json) {
    return run(
        tenant,
        () -> {
          var frame = LocalFrames.read("Ready", json);
          Target target = target(tenant);
          require(
              target.channelNonce().equals(text(frame, "channel_nonce")),
              NetworkErrors.UNAUTHORIZED);
          checkIam(target.realm(), target.node(), target.node(), "0", Operation.CONNECT);
          var node = lockNode(tenant, target);
          fence(tenant, target, node, frame);
          if (health(node) != null) return ok(recovery(node, null));
          BigInteger high = number(node, "local_high_water");
          BigInteger revision = number(node, "registration_revision");
          var pending = frame.path("pending_registration");
          Map<String, Object> original = null;
          if (!pending.isNull()) {
            original =
                registration(
                    tenant,
                    target.realm(),
                    target.node(),
                    text(pending, "candidate_local"),
                    text(pending, "allocation_op_id"));
          }
          var resolution = LocalRecovery.inspect(high, revision, frame, original);
          if (resolution == LocalRecovery.Resolution.RECOVERY_CONFLICT
              || resolution == LocalRecovery.Resolution.IDENTITY_ROLLBACK) {
            String problem =
                resolution == LocalRecovery.Resolution.IDENTITY_ROLLBACK
                    ? "identity_rollback"
                    : "recovery_conflict";
            ErrorCode error =
                "identity_rollback".equals(problem) ? IDENTITY_ROLLBACK : RECOVERY_CONFLICT;
            isolate(tenant, target, problem, error);
            return ok(new Recovery(problem, high.toString(), revision.toString(), null));
          }
          Registration recovered =
              resolution == LocalRecovery.Resolution.RETURN_ORIGINAL
                  ? view(original, "existing")
                  : null;
          return ok(new Recovery("ready", high.toString(), revision.toString(), recovered));
        });
  }

  /**
   * For source-binding/send/group/admit wiring: require the immutable witness before exposing or
   * using a nonzero local. Call with BOTH original ID and fingerprint for a source binding. Must be
   * composed under the caller's transaction to keep the node health/epoch gate atomic with use.
   */
  public Registration requireRegistered(
      UUID tenant,
      String realm,
      String node,
      String local,
      UUID registrationId,
      String fingerprint) {
    authorize(tenant);
    // This guard may join an existing tenant transaction (unlike diagnostic-writing operations).
    return tx.execute(
        status -> {
          TenantTransactionManager.verifyTransactionTenant();
          var row = lockNode(tenant, realm, node);
          ErrorCode error = health(row);
          if (error != null) throw new AurigaException(error);
          require(
              local != null
                  && local.matches("[1-9][0-9]{0,19}")
                  && number(local).compareTo(MAX) <= 0,
              NetworkErrors.PROTOCOL_ERROR);
          return view(
              registered(tenant, realm, node, local, registrationId, fingerprint), "existing");
        });
  }

  /** Transactional health gate for system-local operations as well as registered sessions. */
  public void requireHealthy(UUID tenant, String realm, String node) {
    authorize(tenant);
    tx.executeWithoutResult(
        status -> {
          TenantTransactionManager.verifyTransactionTenant();
          ErrorCode error = health(lockNode(tenant, realm, node));
          if (error != null) throw new AurigaException(error);
        });
  }

  private Map<String, Object> registered(
      UUID tenant, String realm, String node, String local, UUID id, String fingerprint) {
    var row =
        one(
            """
        SELECT * FROM collab.network_local_registration WHERE tenant_id=? AND realm=? AND node_id=? AND local_id=?
        """,
            tenant,
            realm,
            node,
            decimal(local));
    require(
        row != null
            && "registered".equals(row.get("status"))
            && (id == null || id.equals(row.get("id")))
            && (fingerprint == null || fingerprint.equals(row.get("allocation_fingerprint"))),
        REGISTRATION_REQUIRED);
    return row;
  }

  private Target target(UUID tenant) {
    return resolveTarget(tenant, authority, clock);
  }

  /** Shared control-plane boundary for registration, assignment and recovery. */
  static Target resolveTarget(UUID tenant, NetworkSourceAuthority authority, Clock clock) {
    require(authority != null, CommonErrors.UNAVAILABLE);
    Target target;
    try {
      target = authority.currentTarget(tenant);
    } catch (AurigaException e) {
      throw e;
    } catch (RuntimeException e) {
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    }
    require(
        target != null
            && target.validUntil() != null
            && target.validUntil().isAfter(clock.instant()),
        NetworkErrors.UNAUTHORIZED);
    require(
        target.realm() != null
            && target.realm().matches("[1-9][0-9]{0,19}")
            && number(target.realm()).compareTo(MAX) <= 0
            && target.node() != null
            && target.node().matches("[1-9][0-9]{0,19}")
            && number(target.node()).compareTo(MAX) <= 0,
        NetworkErrors.UNAUTHORIZED);
    return target;
  }

  private void checkIam(
      String realm, String source, String target, String local, Operation operation) {
    checkIam(realm, source, target, local, operation, Sender.HUMAN);
  }

  private Sender authorizeContact(UUID tenant, Target target, Map<String, Object> contact) {
    var message =
        one(
            """
        SELECT message_json,source_context_json FROM collab.network_message WHERE tenant_id=? AND id=?
        """,
            tenant,
            contact.get("message_id"));
    require(
        message != null
            && message.get("message_json") != null
            && message.get("source_context_json") != null,
        NetworkErrors.UNAUTHORIZED);
    String body = message.get("message_json").toString();
    String source = message.get("source_context_json").toString();
    JsonNode envelope = NetworkJson.parse(body);
    JsonNode stamp = NetworkJson.parse(source);
    String sender = text(stamp, "sender_kind");
    require(
        ("human".equals(sender) || "agent".equals(sender))
            && sender.equals(text(envelope, "sender_kind"))
            && NetworkJson.digest(envelope).equals(text(stamp, "message_digest")),
        NetworkErrors.UNAUTHORIZED);
    java.time.Instant until;
    try {
      // This collab adapter may read its own witnesses in the SAME transaction, like mailbox FETCH.
      // Only calls to the separate IAM service are suspended below.
      until = authority.authorizeDelivery(tenant, body, source, target.node());
    } catch (AurigaException e) {
      throw e;
    } catch (RuntimeException e) {
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    }
    require(until != null && until.isAfter(clock.instant()), NetworkErrors.UNAUTHORIZED);
    return "agent".equals(sender) ? Sender.AGENT : Sender.HUMAN;
  }

  private void checkIam(
      String realm,
      String source,
      String target,
      String local,
      Operation operation,
      Sender sender) {
    require(iam != null, CommonErrors.UNAVAILABLE);
    Boolean active;
    Decision decision;
    try {
      active = outside.execute(s -> iam.active(realm, source));
      decision =
          Boolean.TRUE.equals(active)
              ? outside.execute(
                  s ->
                      iam.check(
                          new Check(realm, source, target, local, sender, operation, 0, 1, 1)))
              : null;
    } catch (RuntimeException e) {
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    }
    require(Boolean.TRUE.equals(active), NetworkErrors.UNAUTHORIZED);
    require(decision != null, CommonErrors.UNAVAILABLE);
    require(decision.allowed(), NetworkErrors.UNAUTHORIZED);
  }

  private void fence(UUID tenant, Target target, Map<String, Object> node, JsonNode frame) {
    require(target.validUntil().isAfter(clock.instant()), NetworkErrors.UNAUTHORIZED);
    require(
        target.epoch() > 0 && target.epoch() == ((Number) node.get("connection_epoch")).longValue(),
        NetworkErrors.STALE_EPOCH);
    if (frame.has("epoch"))
      require(
          number(text(frame, "epoch")).equals(BigInteger.valueOf(target.epoch())),
          NetworkErrors.STALE_EPOCH);
    String boot = text(frame, "boot_id");
    Object boundEpoch = node.get("local_boot_epoch");
    if (boundEpoch != null && ((Number) boundEpoch).longValue() == target.epoch())
      require(boot.equals(node.get("local_boot_id")), NetworkErrors.STALE_EPOCH);
    else
      jdbc.update(
          """
        UPDATE collab.network_node SET local_boot_epoch=?,local_boot_id=? WHERE tenant_id=? AND realm=? AND node_id=?
        """,
          target.epoch(),
          boot,
          tenant,
          target.realm(),
          target.node());
  }

  private Map<String, Object> lockNode(UUID tenant, Target target) {
    return lockNode(tenant, target.realm(), target.node());
  }

  private Map<String, Object> lockNode(UUID tenant, String realm, String node) {
    var row =
        one(
            """
        SELECT * FROM collab.network_node WHERE tenant_id=? AND realm=? AND node_id=? FOR UPDATE
        """,
            tenant,
            realm,
            node);
    require(row != null, NetworkErrors.UNAUTHORIZED);
    return row;
  }

  private Map<String, Object> registration(
      UUID tenant, String realm, String node, String local, String op) {
    var rows =
        jdbc.queryForList(
            """
        SELECT * FROM collab.network_local_registration WHERE tenant_id=? AND realm=? AND node_id=?
          AND (local_id=? OR allocation_op_id=?)
        """,
            tenant,
            realm,
            node,
            decimal(local),
            op);
    if (rows.isEmpty()) return null;
    // Two different rows matching the two keys is also a conflict, never an arbitrary winner.
    if (rows.size() != 1) return Map.of();
    return rows.getFirst();
  }

  private static Registration view(Map<String, Object> row, String result) {
    String local = number(row, "local_id").toString();
    return new Registration(
        (UUID) row.get("id"),
        local,
        row.get("allocation_op_id").toString(),
        row.get("allocation_fingerprint").toString(),
        number(row, "registration_revision").toString(),
        local,
        result,
        ((Number) row.get("granted_epoch")).longValue(),
        row.get("granted_boot_id").toString());
  }

  private static Assignment assignment(Map<String, Object> row, boolean permit) {
    return new Assignment(
        (UUID) row.get("contact_id"),
        row.get("realm").toString(),
        row.get("node_id").toString(),
        number(row, "local_id").toString(),
        (UUID) row.get("registration_id"),
        row.get("creation_attempt_id").toString(),
        "existing",
        permit,
        ((Number) row.get("granted_epoch")).longValue(),
        row.get("granted_boot_id").toString());
  }

  private static ErrorCode health(Map<String, Object> node) {
    return switch (node.get("local_identity_status").toString()) {
      case "healthy" -> null;
      case "node_identity_conflict" -> ALLOCATION_CONFLICT;
      case "identity_rollback" -> IDENTITY_ROLLBACK;
      default -> RECOVERY_CONFLICT;
    };
  }

  private static Recovery recovery(Map<String, Object> node, Registration pending) {
    String state = node.get("local_identity_status").toString();
    return new Recovery(
        "node_identity_conflict".equals(state) ? "recovery_conflict" : state,
        number(node, "local_high_water").toString(),
        number(node, "registration_revision").toString(),
        pending);
  }

  private void isolate(UUID tenant, Target target, String state, ErrorCode error) {
    jdbc.update(
        """
        UPDATE collab.network_node
           SET local_identity_status=?,local_identity_reason=?,local_identity_observed_at=now()
         WHERE tenant_id=? AND realm=? AND node_id=? AND local_identity_status='healthy'
        """,
        state,
        error.code(),
        tenant,
        target.realm(),
        target.node());
  }

  private <T> Outcome<T> conflict(UUID tenant, Target target, String state, ErrorCode error) {
    isolate(tenant, target, state, error);
    return failed(error);
  }

  private Map<String, Object> one(String sql, Object... args) {
    var rows = jdbc.queryForList(sql, args);
    return rows.isEmpty() ? null : rows.getFirst();
  }

  private <T> T run(UUID tenant, Supplier<Outcome<T>> work) {
    authorize(tenant);
    // An outer rollback must not erase a committed identity-conflict diagnosis or reissue a permit.
    require(
        !TransactionSynchronizationManager.isActualTransactionActive()
            && !TransactionSynchronizationManager.isSynchronizationActive()
            && !TransactionSynchronizationManager.hasResource(jdbc.getDataSource()),
        CommonErrors.UNAVAILABLE);
    Outcome<T> outcome =
        tx.execute(
            status -> {
              TenantTransactionManager.verifyTransactionTenant();
              return work.get();
            });
    if (outcome.error() != null) throw new AurigaException(outcome.error());
    return outcome.value();
  }

  private static void authorize(UUID tenant) {
    var caller =
        AuthorizationRules.currentPrincipal()
            .orElseThrow(() -> new AurigaException(CommonErrors.UNAUTHENTICATED));
    require(
        caller.isInternalService()
            && "svc:ai-gateway".equals(caller.subject())
            && TenantContext.PLATFORM_TENANT.equals(caller.tenantId()),
        CommonErrors.FORBIDDEN);
    require(
        tenant != null
            && !TenantContext.PLATFORM_TENANT.equals(tenant)
            && TenantContext.current().filter(tenant::equals).isPresent(),
        CommonErrors.FORBIDDEN);
  }

  private record Outcome<T>(T value, ErrorCode error) {}

  private static <T> Outcome<T> ok(T value) {
    return new Outcome<>(value, null);
  }

  private static <T> Outcome<T> failed(ErrorCode error) {
    return new Outcome<>(null, error);
  }

  private static String text(JsonNode node, String field) {
    return node.path(field).asString();
  }

  private static BigInteger number(String value) {
    return new BigInteger(value);
  }

  private static BigInteger number(Map<String, Object> row, String key) {
    return new BigDecimal(row.get(key).toString()).toBigIntegerExact();
  }

  private static BigDecimal decimal(String value) {
    return new BigDecimal(value);
  }

  private static UUID uuid(JsonNode frame, String key) {
    try {
      return UUID.fromString(text(frame, key));
    } catch (IllegalArgumentException e) {
      throw new AurigaException(NetworkErrors.PROTOCOL_ERROR);
    }
  }

  private static void require(boolean condition, ErrorCode error) {
    if (!condition) throw new AurigaException(error);
  }

  /** Internal projections only; protocol response signing belongs to the ingress. */
  public record Registration(
      UUID registrationId,
      String local,
      String allocationOpId,
      String allocationFingerprint,
      String registrationRevision,
      String localHighWater,
      String result,
      long grantedEpoch,
      String grantedBootId) {}

  public record Assignment(
      UUID contactId,
      String realm,
      String node,
      String local,
      UUID registrationId,
      String creationAttemptId,
      String result,
      boolean creationPermit,
      long grantedEpoch,
      String grantedBootId) {}

  public record Recovery(
      String status,
      String localHighWater,
      String registrationRevision,
      Registration pendingRegistration) {}
}
