package auriga.collab.app;

import auriga.collab.api.NetworkSourceAuthority.Target;
import auriga.collab.grpc.NetworkAuthorityClient;
import auriga.collab.grpc.NetworkSourceGrpcAuthority;
import auriga.collab.network.local.LocalRegistry;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.ErrorCode;
import auriga.common.persistence.TenantTransactionManager;
import auriga.contracts.iam.v1.NetworkAuthorityConnection;
import auriga.contracts.iam.v1.NetworkConnectionAnchor;
import io.grpc.Status;
import java.time.*;
import java.util.*;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.transaction.support.TransactionTemplate;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/**
 * Transactional wire adaptation. Unsupported recovery modes stay fenced, never silently ignored.
 */
public class NetworkMailboxWireService {
  private static final ErrorCode RECOVERY =
      ErrorCode.of("network.recovery_conflict", 412, Status.Code.FAILED_PRECONDITION);
  private final NetworkMailboxService mailbox;
  private final JdbcTemplate jdbc;
  private final TransactionTemplate tx;
  private final NetworkSourceGrpcAuthority source;
  private final NetworkEnvelopeSigner signer;
  private final Clock clock;
  private final boolean broadcast;
  private final LocalRegistry locals;
  private final NetworkSourceControl sources;
  private final NetworkDurableSubmit submissions;

  public NetworkMailboxWireService(
      NetworkMailboxService mailbox,
      LocalRegistry locals,
      JdbcTemplate jdbc,
      TenantTransactionManager manager,
      NetworkSourceGrpcAuthority source,
      NetworkSigningKeys keys,
      Clock clock) {
    this(mailbox, locals, jdbc, manager, source, keys, clock, false);
  }

  public NetworkMailboxWireService(
      NetworkMailboxService mailbox,
      LocalRegistry locals,
      JdbcTemplate jdbc,
      TenantTransactionManager manager,
      NetworkSourceGrpcAuthority source,
      NetworkSigningKeys keys,
      Clock clock,
      boolean broadcast) {
    this.mailbox = mailbox;
    this.locals = Objects.requireNonNull(locals);
    this.jdbc = jdbc;
    tx = new TransactionTemplate(manager);
    this.source = source;
    signer = new NetworkEnvelopeSigner(keys, clock);
    this.clock = clock;
    this.broadcast = broadcast;
    sources = new NetworkSourceControl(this, jdbc, manager, source, clock);
    submissions = new NetworkDurableSubmit(this, jdbc, manager, source, clock);
  }

  public String submit(NetworkAuthorityConnection c, byte[] bytes) {
    return submissions.submit(c, bytes);
  }

  public String receiveSet(NetworkAuthorityConnection c, byte[] bytes) {
    var request = frame(bytes, "ReceiveSet");
    return source.sourceRequest(
        () -> {
          var gate = sourceGate(c, request);
          return tx.execute(
              s -> {
                lockSourceGate(gate, true);
                var registration = request.path("registration");
                var actual =
                    locals.requireRegistered(
                        TenantContext.require(),
                        c.getRealm(),
                        c.getNode(),
                        registration.path("local").asString(),
                        uuid(registration.path("registration_id").asString()),
                        registration.path("allocation_fingerprint").asString());
                require(
                    "session".equals(actual.addressType())
                        && actual
                            .allocationOpId()
                            .equals(registration.path("allocation_op_id").asString())
                        && actual
                            .registrationRevision()
                            .equals(registration.path("registration_revision").asString())
                        && "session".equals(registration.path("address_type").asString()),
                    NetworkErrors.CONFLICT);
                var row =
                    jdbc.queryForMap(
                        "SELECT receive_enabled,receive_version FROM collab.network_local_registration WHERE tenant_id=? AND id=? FOR UPDATE",
                        TenantContext.require(),
                        actual.registrationId());
                long expected = decimal(request, "expected_version");
                boolean enabled = request.path("enabled").asBoolean();
                long version = number(row, "receive_version");
                if (version == expected) {
                  jdbc.update(
                      "UPDATE collab.network_local_registration SET receive_enabled=?,receive_version=receive_version+1 WHERE tenant_id=? AND id=?",
                      enabled,
                      TenantContext.require(),
                      actual.registrationId());
                  version++;
                } else
                  require(
                      version == expected + 1
                          && Boolean.valueOf(enabled).equals(row.get("receive_enabled")),
                      NetworkErrors.CONFLICT);
                return signed(
                    base("receive_set_result")
                        .put("request_id", gate.target().requestId())
                        .put("registration_id", actual.registrationId().toString())
                        .put("enabled", enabled)
                        .put("state_version", Long.toString(version)),
                    "ReceiveSetResult",
                    gate.target());
              });
        });
  }

  public String attemptBegin(NetworkAuthorityConnection c, byte[] bytes) {
    var request = NetworkJson.parse(bytes);
    NetworkSchemas.validate("protocol", "AttemptBegin", request);
    require(
        "attempt_begin".equals(request.path("type").asString())
            && "process".equals(request.path("purpose").asString()),
        NetworkErrors.PROTOCOL_ERROR);
    return associated(
        c,
        request.path("request_id").asString(),
        (row, target) -> {
          var permission = prepareExecution(c, request.path("recipient_id").asString());
          return () -> {
            registrationAssociation(c, request, row, locked(c), target);
            var recipient = executionRecipient(c, request.path("recipient_id").asString());
            require(
                permission.until().isAfter(clock.instant())
                    && permission.fingerprint().equals(executionFingerprint(recipient)),
                NetworkErrors.CONFLICT);
            require(
                request
                    .path("delivery_id")
                    .asString()
                    .equals(recipient.get("delivery_id").toString()),
                NetworkErrors.UNAUTHORIZED);
            var business = ((ObjectNode) request).deepCopy();
            business.remove("request_id");
            String canonical = NetworkJson.canonical(business);
            var previous =
                jdbc.queryForList(
                    "SELECT request_json,result_json FROM collab.network_attempt WHERE tenant_id=? AND recipient_id=? AND attempt_id=?",
                    TenantContext.require(),
                    recipient.get("id"),
                    request.path("attempt_id").asString());
            ObjectNode result;
            if (!previous.isEmpty()) {
              require(
                  canonical.equals(previous.getFirst().get("request_json")),
                  NetworkErrors.CONFLICT);
              result =
                  (ObjectNode) NetworkJson.parse(previous.getFirst().get("result_json").toString());
            } else {
              var entered =
                  mailbox.attemptBegin(
                      new NetworkMailboxService.AttemptBeginRequest(
                          TenantContext.require(),
                          c.getRealm(),
                          c.getNode(),
                          target.epoch(),
                          (UUID) recipient.get("delivery_id"),
                          (UUID) recipient.get("id"),
                          "process"));
              result =
                  base("attempt_result")
                      .put("attempt_id", request.path("attempt_id").asString())
                      .put("recipient_id", recipient.get("id").toString())
                      .put("result", entered.result())
                      .put("attempt_count", entered.attemptCount())
                      .put("max_attempts", entered.maxAttempts());
              jdbc.update(
                  "INSERT INTO collab.network_attempt(id,tenant_id,recipient_id,attempt_id,request_json,result_json) VALUES (?,?,?,?,?,?)",
                  auriga.common.persistence.UuidV7.next(),
                  TenantContext.require(),
                  recipient.get("id"),
                  request.path("attempt_id").asString(),
                  canonical,
                  NetworkJson.canonical(result));
            }
            return signed(result.put("request_id", target.requestId()), "AttemptResult", target);
          };
        });
  }

  public String executionAdmit(NetworkAuthorityConnection c, byte[] bytes) {
    var request = NetworkJson.parse(bytes);
    NetworkSchemas.validate("protocol", "ExecutionAdmit", request);
    require(
        "execution_admit".equals(request.path("type").asString()), NetworkErrors.PROTOCOL_ERROR);
    return associated(
        c,
        request.path("request_id").asString(),
        (row, target) -> {
          var permission = prepareExecution(c, request.path("recipient_id").asString());
          return () -> {
            require(
                decimal(request, "connection_epoch") == target.epoch()
                    && Objects.equals(row.get("local_boot_id"), request.path("boot_id").asString()),
                NetworkErrors.STALE_EPOCH);
            var recipient = executionRecipient(c, request.path("recipient_id").asString());
            require(
                permission.until().isAfter(clock.instant())
                    && permission.fingerprint().equals(executionFingerprint(recipient)),
                NetworkErrors.CONFLICT);
            var address = request.path("target");
            require(
                c.getRealm().equals(address.path("realm").asString())
                    && c.getNode().equals(address.path("node").asString())
                    && recipient.get("target_local").equals(address.path("local").asString())
                    && "session".equals(address.path("type").asString())
                    && request
                        .path("recipient_id")
                        .asString()
                        .equals(request.path("host_request_id").asString()),
                NetworkErrors.UNAUTHORIZED);
            require(
                jdbc.queryForObject(
                        "SELECT count(*) FROM collab.network_attempt WHERE tenant_id=? AND recipient_id=? AND attempt_id=?",
                        Integer.class,
                        TenantContext.require(),
                        recipient.get("id"),
                        request.path("attempt_id").asString())
                    == 1,
                NetworkErrors.CONFLICT);
            var admitted =
                mailbox.executionAdmit(
                    new NetworkMailboxService.ExecutionAdmitRequest(
                        TenantContext.require(),
                        c.getRealm(),
                        c.getNode(),
                        target.epoch(),
                        (UUID) recipient.get("id"),
                        request.path("boot_id").asString(),
                        request.path("host_request_id").asString()));
            return signed(
                base("admit_result")
                    .put("request_id", target.requestId())
                    .put("recipient_id", recipient.get("id").toString())
                    .put("result", admitted.result())
                    .put("admission_id", admitted.admissionId().toString())
                    .put("admitted_epoch", Long.toString(admitted.admittedEpoch()))
                    .put("state_version", recipient.get("state_version").toString()),
                "AdmitResult",
                target);
          };
        });
  }

  private record ExecutionPermission(Instant until, String fingerprint) {}

  private ExecutionPermission prepareExecution(NetworkAuthorityConnection c, String id) {
    var row = tx.execute(s -> executionRecipient(c, id, false));
    Instant until =
        source.authorizeDelivery(
            TenantContext.require(),
            row.get("message_json").toString(),
            row.get("source_context_json").toString(),
            c.getNode());
    return new ExecutionPermission(until, executionFingerprint(row));
  }

  private static String executionFingerprint(Map<String, Object> row) {
    var value = NetworkJson.object();
    for (String key :
        List.of(
            "id",
            "delivery_id",
            "target_local",
            "state_version",
            "message_json",
            "source_context_json",
            "registration_id",
            "receive_version",
            "registration_revision",
            "source_stop_version",
            "source_permission_version")) value.put(key, Objects.toString(row.get(key), ""));
    return NetworkJson.digest(value);
  }

  private Map<String, Object> executionRecipient(NetworkAuthorityConnection c, String id) {
    // Hold the source's local stop/permission fence through commit as well as the target lock.
    jdbc.queryForList(
        """
        SELECT sl.id FROM collab.network_source_lineage sl
        JOIN collab.network_source_binding sb ON sb.tenant_id=sl.tenant_id AND sb.lineage_id=sl.id
        JOIN collab.network_submit sub ON sub.tenant_id=sb.tenant_id AND sub.source_binding=sb.source_binding
        JOIN collab.network_mail_item i ON i.tenant_id=sub.tenant_id AND i.message_id=sub.message_id
        JOIN collab.network_recipient r ON r.tenant_id=i.tenant_id AND r.delivery_id=i.id
        WHERE sl.tenant_id=? AND r.id=? FOR UPDATE OF sl
        """,
        TenantContext.require(),
        uuid(id));
    return executionRecipient(c, id, true);
  }

  private Map<String, Object> executionRecipient(
      NetworkAuthorityConnection c, String id, boolean lock) {
    var rows =
        jdbc.queryForList(
            """
        SELECT r.*,m.message_json,m.source_context_json,l.id AS registration_id,l.receive_version,l.registration_revision,sl.stop_version AS source_stop_version,sl.permission_version AS source_permission_version FROM collab.network_recipient r JOIN collab.network_mail_item i ON i.tenant_id=r.tenant_id AND i.id=r.delivery_id
        JOIN collab.network_message m ON m.tenant_id=i.tenant_id AND m.id=i.message_id
        LEFT JOIN collab.network_submit sub ON sub.tenant_id=m.tenant_id AND sub.message_id=m.id
        LEFT JOIN collab.network_source_binding sb ON sb.tenant_id=sub.tenant_id AND sb.source_binding=sub.source_binding
        LEFT JOIN collab.network_source_lineage sl ON sl.tenant_id=sb.tenant_id AND sl.id=sb.lineage_id
        JOIN collab.network_local_registration l ON l.tenant_id=i.tenant_id AND l.realm=i.target_realm AND l.node_id=i.target_node AND l.local_id::text=r.target_local
        WHERE r.tenant_id=? AND r.id=? AND i.target_realm=? AND i.target_node=? AND r.transport_status='pending'
          AND m.kind='message' AND (sub.id IS NULL OR NOT sl.stopped) AND l.address_type='session' AND l.status='registered' AND l.receive_enabled
          AND (m.message_json::jsonb->>'expires_at')::timestamptz>now()
        """
                + (lock ? " FOR UPDATE OF r,l" : ""),
            TenantContext.require(),
            uuid(id),
            c.getRealm(),
            c.getNode());
    require(rows.size() == 1, NetworkErrors.UNAUTHORIZED);
    return rows.getFirst();
  }

  public String sourceRegister(NetworkAuthorityConnection c, byte[] frame) {
    return sources.handle(c, frame, "SourceRegister");
  }

  public String sourceGet(NetworkAuthorityConnection c, byte[] frame) {
    return sources.handle(c, frame, "SourceGet");
  }

  public String sourceRevoke(NetworkAuthorityConnection c, byte[] frame) {
    return sources.handle(c, frame, "SourceRevoke");
  }

  /** Durable outbox retry uses retained ownership, independent of a live mailbox association. */
  public int retrySourceRevocations() {
    return sources.retryRevocations();
  }

  record SourceGate(
      NetworkAuthorityConnection connection,
      JsonNode request,
      Map<String, Object> before,
      Target target,
      NetworkMailboxService.TargetDecision decision,
      NetworkConnectionAnchor anchor) {}

  SourceGate sourceGate(NetworkAuthorityConnection c, JsonNode request) {
    var caller =
        auriga.common.security.AuthorizationRules.currentPrincipal()
            .orElseThrow(
                () -> new AurigaException(auriga.common.error.CommonErrors.UNAUTHENTICATED));
    require(
        caller.isInternalService()
            && "svc:ai-gateway".equals(caller.subject())
            && TenantContext.PLATFORM_TENANT.equals(caller.tenantId()),
        auriga.common.error.CommonErrors.FORBIDDEN);
    NetworkAuthorityClient.binding(c);
    var verified = source.verifiedConnection(c);
    Instant deadline = verified.deadline();
    deadline =
        Collections.min(
            List.of(
                deadline,
                clock.instant().plusMillis(NetworkAuthorityClient.sourceRemainingMillis())));
    var before = tx.execute(s -> read(c, false));
    require(
        before != null && bindingJson(c).equals(before.get("wire_binding_json")),
        NetworkErrors.STALE_EPOCH);
    require("ready".equals(before.get("wire_status")), RECOVERY);
    var target = target(c, before, request.path("request_id").asString(), deadline);
    var decision =
        source.withTarget(
            c,
            target,
            () ->
                mailbox.authorizeTarget(
                    TenantContext.require(),
                    c.getRealm(),
                    c.getNode(),
                    Set.of(auriga.iam.api.NetworkAuthority.Operation.CONNECT)));
    return new SourceGate(c, request, before, target, decision, verified.anchor());
  }

  void lockSourceGate(SourceGate gate, boolean frameFence) {
    lockSourceErrorGate(gate);
    if (frameFence)
      registrationAssociation(
          gate.connection(),
          gate.request(),
          gate.before(),
          locked(gate.connection()),
          gate.target());
    locals.requireHealthy(
        TenantContext.require(), gate.connection().getRealm(), gate.connection().getNode());
  }

  void lockSourceErrorGate(SourceGate gate) {
    var row = locked(gate.connection());
    unchanged(gate.before(), row);
    require(gate.target().validUntil().isAfter(clock.instant()), NetworkErrors.UNAUTHORIZED);
    mailbox.withDecision(gate.decision(), () -> null);
  }

  Instant refreshSourceGate(SourceGate gate) {
    return refreshSourceGate(gate, false);
  }

  Instant refreshSourceGate(SourceGate gate, boolean requireAnchor) {
    var current = sourceGate(gate.connection(), gate.request());
    unchanged(gate.before(), current.before());
    if (requireAnchor)
      require(
          gate.anchor() != null && gate.anchor().equals(current.anchor()),
          ErrorCode.of("network.outcome_unknown", 503, Status.Code.FAILED_PRECONDITION));
    return Collections.min(
        List.of(
            gate.target().validUntil(),
            current.target().validUntil(),
            gate.decision().deadline,
            current.decision().deadline));
  }

  String signSource(SourceGate gate, ObjectNode result, String definition, Instant until) {
    var target = gate.target();
    require(until.isAfter(clock.instant()), NetworkErrors.UNAUTHORIZED);
    var signed =
        signer.sign(
            result.deepCopy().put("request_id", target.requestId()),
            "protocol_response",
            new NetworkEnvelopeSigner.Context(
                target.realm(),
                target.node(),
                target.incarnation(),
                target.epoch(),
                target.channelNonce(),
                target.requestId()),
            until);
    NetworkSchemas.validate(
        "Error".equals(definition) ? "protocol" : "source-control", definition, signed);
    return NetworkJson.canonical(signed);
  }

  public String ready(NetworkAuthorityConnection c, byte[] frame) {
    JsonNode request = frame(frame, "Ready");
    var requiredFeatures = NetworkWireCapabilities.verify(request, broadcast);
    NetworkAuthorityClient.binding(c);
    Instant deadline = source.verify(c); // Rejects inherited transactions before any mutation.
    UUID tenant = TenantContext.require();
    var before = tx.execute(status -> read(c, false));
    var proposed =
        new Target(
            c.getRealm(),
            c.getNode(),
            before == null || before.get("wire_incarnation") == null
                ? UUID.randomUUID().toString()
                : before.get("wire_incarnation").toString(),
            before == null
                ? 1
                : number(before, "connection_epoch")
                    + (c.getIngress().equals(before.get("ingress_id")) ? 0 : 1),
            request.path("channel_nonce").asString(),
            request.path("request_id").asString(),
            deadline);
    return source.withTarget(
        c,
        proposed,
        broadcast,
        () -> {
          var decision =
              mailbox.authorizeTarget(
                  tenant,
                  c.getRealm(),
                  c.getNode(),
                  Set.of(
                      auriga.iam.api.NetworkAuthority.Operation.CONNECT,
                      auriga.iam.api.NetworkAuthority.Operation.RECEIVE));
          // Phase 1 commits the association/epoch, but NEVER opens business. A crash leaves
          // reconciling.
          var prepared =
              mailbox.withDecision(
                  decision,
                  () ->
                      tx.execute(
                          status ->
                              mailbox.withDecision(
                                  decision, () -> prepareReady(c, request, proposed, before))));
          var target = target(c, prepared, proposed.requestId(), deadline);
          return source.withTarget(
              c,
              target,
              broadcast,
              () -> {
                boolean recovering = "reconciling".equals(prepared.get("wire_status"));
                // Phase 2 owns its commit: later signing/fencing failures cannot erase a diagnosis.
                // A completed identical READY is a transport replay, not an old backup after new
                // registers.
                var recovery =
                    recovering
                        ? locals.reconcileRegistration(tenant, NetworkJson.canonical(request))
                        : null;
                // Phase 3 has no RPC. Compare the exact association and durable registration
                // version under
                // the SAME datasource/node lock before signing or changing the business gate.
                return tx.execute(
                    status ->
                        mailbox.withDecision(
                            decision,
                            () -> {
                              var row = locked(c);
                              unchanged(prepared, row);
                              require(
                                  bindingJson(c).equals(row.get("wire_binding_json")),
                                  NetworkErrors.STALE_EPOCH);
                              require(
                                  target.epoch() == number(row, "connection_epoch"),
                                  NetworkErrors.STALE_EPOCH);
                              if (recovering) {
                                require(
                                    Objects.equals(
                                            row.get("local_boot_id"),
                                            request.path("boot_id").asString())
                                        && row.get("local_boot_epoch") != null
                                        && number(row, "local_boot_epoch") == target.epoch(),
                                    NetworkErrors.STALE_EPOCH);
                                require(
                                    counter(row, "local_high_water")
                                            .equals(recovery.localHighWater())
                                        && counter(row, "registration_revision")
                                            .equals(recovery.registrationRevision())
                                        && registrationStatus(row).equals(recovery.status()),
                                    NetworkErrors.CONFLICT);
                              }
                              var route = mailbox.head(tenant, c.getRealm(), c.getNode());
                              String state = registrationStatus(row);
                              if ("ready".equals(state))
                                state =
                                    recovering
                                        ? (receiptReady(request, route)
                                            ? "ready"
                                            : "recovery_conflict")
                                        : row.get("wire_status").toString();
                              var result =
                                  base("ready_result")
                                      .put("request_id", target.requestId())
                                      .put("status", state)
                                      .put("epoch", Long.toString(target.epoch()))
                                      .put("producer_epoch", row.get("producer_epoch").toString())
                                      .put("head_seq", Long.toString(route.headSeq()))
                                      .put(
                                          "committed_cursor",
                                          Long.toString(route.committedCursor()))
                                      .put("retention_low_seq", "0")
                                      .put("local_high_water", counter(row, "local_high_water"))
                                      .put(
                                          "registration_revision",
                                          counter(row, "registration_revision"));
                              result
                                  .putObject("node_identity")
                                  .put("realm", c.getRealm())
                                  .put("node", c.getNode())
                                  .put("incarnation", target.incarnation());
                              result.putNull("seen_checkpoint");
                              var features = result.putArray("required_features");
                              requiredFeatures.forEach(features::add);
                              // Lock waits must not extend the original scoped decision or source
                              // lifetime.
                              String signed =
                                  mailbox.withDecision(
                                      decision, () -> signed(result, "ReadyResult", target));
                              jdbc.update(
                                  "UPDATE collab.network_node SET wire_status=? WHERE tenant_id=? AND realm=? AND node_id=?",
                                  state,
                                  tenant,
                                  c.getRealm(),
                                  c.getNode());
                              return signed;
                            }));
              });
        });
  }

  private Map<String, Object> prepareReady(
      NetworkAuthorityConnection c, JsonNode request, Target proposed, Map<String, Object> before) {
    UUID tenant = TenantContext.require();
    jdbc.update(
        "INSERT INTO collab.network_node(tenant_id,realm,node_id) VALUES (?,?,?) ON CONFLICT DO NOTHING",
        tenant,
        c.getRealm(),
        c.getNode());
    var row = locked(c);
    // Simultaneous identical retries may have completed phase 1 already. Only that exact binding
    // and frame may join the prepared epoch; an old ingress can never join a takeover.
    if (c.getIngress().equals(row.get("ingress_id"))) {
      require(bindingJson(c).equals(row.get("wire_binding_json")), NetworkErrors.UNAUTHORIZED);
      require(
          NetworkJson.canonical(request).equals(row.get("wire_ready_json")),
          NetworkErrors.CONFLICT);
      require(number(row, "connection_epoch") == proposed.epoch(), NetworkErrors.STALE_EPOCH);
      return row;
    }
    unchanged(before, row);
    require(
        jdbc.queryForObject(
                "SELECT count(*) FROM collab.network_wire_ingress WHERE tenant_id=? AND ingress_id=?",
                Integer.class,
                tenant,
                c.getIngress())
            == 0,
        NetworkErrors.STALE_EPOCH);
    var route = mailbox.ready(tenant, c.getRealm(), c.getNode());
    require(route.connectionEpoch() == proposed.epoch(), NetworkErrors.STALE_EPOCH);
    jdbc.update(
        "UPDATE collab.network_node SET ingress_id=?,wire_binding_json=?,wire_ready_json=?,wire_incarnation=?,wire_nonce=?,wire_status='reconciling',route_expires_at=? WHERE tenant_id=? AND realm=? AND node_id=?",
        c.getIngress(),
        bindingJson(c),
        NetworkJson.canonical(request),
        proposed.incarnation(),
        proposed.channelNonce(),
        java.sql.Timestamp.from(proposed.validUntil()),
        tenant,
        c.getRealm(),
        c.getNode());
    jdbc.update(
        "INSERT INTO collab.network_wire_ingress(tenant_id,realm,node_id,ingress_id) VALUES (?,?,?,?)",
        tenant,
        c.getRealm(),
        c.getNode(),
        c.getIngress());
    return locked(c);
  }

  private static boolean receiptReady(JsonNode request, NetworkMailboxService.NodeRoute route) {
    return request.path("resume_cursor").asString().equals(Long.toString(route.committedCursor()))
        && request.path("receipt_checkpoint").path("checkpoint_seq").asString().equals("0")
        && request.path("receipt_checkpoint").path("checkpoint_revision").asString().equals("0")
        && (!request.has("evidence") || request.path("evidence").isEmpty());
  }

  private static String registrationStatus(Map<String, Object> row) {
    return switch (row.get("local_identity_status").toString()) {
      case "healthy" -> "ready";
      case "identity_rollback" -> "identity_rollback";
      default -> "recovery_conflict";
    };
  }

  private static String counter(Map<String, Object> row, String field) {
    return new java.math.BigDecimal(row.get(field).toString()).toBigIntegerExact().toString();
  }

  public String head(NetworkAuthorityConnection c) {
    return associated(
        c,
        null,
        (row, target) ->
            () -> {
              var route = mailbox.head(TenantContext.require(), c.getRealm(), c.getNode());
              return signed(
                  base("head")
                      .put("epoch", Long.toString(target.epoch()))
                      .put("head_seq", Long.toString(route.headSeq())),
                  "Head",
                  target);
            });
  }

  public String localRegister(NetworkAuthorityConnection c, byte[] bytes) {
    var request = frame(bytes, "LocalRegister");
    NetworkAuthorityClient.binding(c);
    Instant deadline = source.verify(c); // Reject inherited transactions before any IAM/mutation.
    UUID tenant = TenantContext.require();
    var before = tx.execute(status -> read(c, false));
    require(before != null, NetworkErrors.STALE_EPOCH);
    require(bindingJson(c).equals(before.get("wire_binding_json")), NetworkErrors.STALE_EPOCH);
    require("ready".equals(before.get("wire_status")), RECOVERY);
    var target = target(c, before, request.path("request_id").asString(), deadline);
    registrationAssociation(c, request, before, before, target);
    NetworkWireCapabilities.verify(
        NetworkJson.parse(before.get("wire_ready_json").toString()), broadcast);
    return source.withTarget(
        c,
        target,
        broadcast,
        () -> {
          var decision =
              mailbox.authorizeTarget(
                  tenant,
                  c.getRealm(),
                  c.getNode(),
                  Set.of(auriga.iam.api.NetworkAuthority.Operation.CONNECT));
          // Registry owns its commit and diagnostic outcome. Never wrap it in associated()'s tx.
          var registration =
              locals.register(
                  tenant,
                  NetworkJson.canonical(request),
                  () ->
                      mailbox.withDecision(
                          decision,
                          () -> {
                            registrationAssociation(c, request, before, locked(c), target);
                            return null;
                          }));
          // A crash/signing failure here leaves the witness committed; the original op recovers it.
          // Recheck revocation outside PG, retaining the original decision's lifetime as well.
          Instant currentDeadline = source.verify(c);
          var currentDecision =
              mailbox.authorizeTarget(
                  tenant,
                  c.getRealm(),
                  c.getNode(),
                  Set.of(auriga.iam.api.NetworkAuthority.Operation.CONNECT));
          Instant signingDeadline =
              Collections.min(
                  List.of(deadline, currentDeadline, decision.deadline, currentDecision.deadline));
          var audience = target(c, before, target.requestId(), signingDeadline);
          String response =
              tx.execute(
                  status -> {
                    registrationAssociation(c, request, before, locked(c), target);
                    var durable =
                        locals.requireRegistered(
                            tenant,
                            c.getRealm(),
                            c.getNode(),
                            registration.local(),
                            registration.registrationId(),
                            registration.allocationFingerprint());
                    require(
                        durable.allocationOpId().equals(registration.allocationOpId())
                            && durable.addressType().equals(registration.addressType())
                            && durable
                                .registrationRevision()
                                .equals(registration.registrationRevision())
                            && durable.localHighWater().equals(registration.localHighWater())
                            && durable.grantedEpoch() == registration.grantedEpoch()
                            && durable.grantedBootId().equals(registration.grantedBootId()),
                        NetworkErrors.CONFLICT);
                    var result =
                        base("register_result")
                            .put("request_id", target.requestId())
                            .put("allocation_op_id", registration.allocationOpId())
                            .put("local", registration.local())
                            .put("registration_id", registration.registrationId().toString())
                            .put("result", registration.result())
                            .put("registration_revision", registration.registrationRevision())
                            .put("local_high_water", registration.localHighWater())
                            .put("address_type", registration.addressType());
                    return mailbox.withDecision(
                        decision,
                        () ->
                            mailbox.withDecision(
                                currentDecision, () -> signed(result, "RegisterResult", audience)));
                  });
          // Local signing/key-lock waits and COMMIT must not turn an expired decision into success.
          return mailbox.withDecision(
              decision,
              () ->
                  mailbox.withDecision(
                      currentDecision,
                      () -> {
                        require(
                            signingDeadline.isAfter(clock.instant()), NetworkErrors.UNAUTHORIZED);
                        return response;
                      }));
        });
  }

  private void registrationAssociation(
      NetworkAuthorityConnection c,
      JsonNode request,
      Map<String, Object> before,
      Map<String, Object> row,
      Target target) {
    unchanged(before, row);
    require(bindingJson(c).equals(row.get("wire_binding_json")), NetworkErrors.STALE_EPOCH);
    require("ready".equals(row.get("wire_status")), RECOVERY);
    require(
        request.path("epoch").asString().equals(Long.toString(target.epoch()))
            && Objects.equals(row.get("local_boot_id"), request.path("boot_id").asString())
            && row.get("local_boot_epoch") != null
            && number(row, "local_boot_epoch") == target.epoch(),
        NetworkErrors.STALE_EPOCH);
    require(target.validUntil().isAfter(clock.instant()), NetworkErrors.UNAUTHORIZED);
  }

  public String fetch(NetworkAuthorityConnection c, byte[] bytes) {
    var request = frame(bytes, "Fetch");
    // Never discard proof/hold/barrier instructions.
    for (String field : List.of("held_ids", "paused", "state_barrier"))
      require(!request.has(field) || request.path(field).isEmpty(), NetworkErrors.PROTOCOL_ERROR);
    return associated(
        c,
        request.path("request_id").asString(),
        (row, target) -> {
          require(decimal(request, "epoch") == target.epoch(), NetworkErrors.STALE_EPOCH);
          long resume = decimal(request, "resume_cursor");
          long[] cursor =
              request.path("continuation").isNull()
                  ? new long[] {resume, -1}
                  : tx.execute(
                      s -> {
                        var rows =
                            jdbc.queryForList(
                                "SELECT scan_after,snapshot_head FROM collab.network_fetch_continuation WHERE tenant_id=? AND token=? AND realm=? AND node_id=? AND epoch=? AND resume_cursor=? AND valid_until>now()",
                                TenantContext.require(),
                                request.path("continuation").asString(),
                                c.getRealm(),
                                c.getNode(),
                                target.epoch(),
                                resume);
                        require(rows.size() == 1, NetworkErrors.CONFLICT);
                        return new long[] {
                          number(rows.getFirst(), "scan_after"),
                          number(rows.getFirst(), "snapshot_head")
                        };
                      });
          var prepared =
              mailbox.preparePage(
                  TenantContext.require(),
                  c.getRealm(),
                  c.getNode(),
                  cursor[0],
                  request.path("max_items").asInt(),
                  request.path("max_bytes").asInt(),
                  cursor[1],
                  resume);
          var locals = new ArrayList<auriga.collab.api.NetworkLocalTargets.Registration>();
          for (String local : prepared.targetLocals())
            locals.add(
                source.localSnapshot(TenantContext.require(), c.getRealm(), c.getNode(), local));
          return () -> {
            for (var local : locals) source.revalidateLocal(TenantContext.require(), local);
            return mailbox.finishPage(prepared);
          };
        });
  }

  public String receipt(NetworkAuthorityConnection c, byte[] bytes) {
    var request = frame(bytes, "Receipt");
    return associated(
        c,
        request.path("request_id").asString(),
        (row, target) -> {
          require(decimal(request, "epoch") == target.epoch(), NetworkErrors.STALE_EPOCH);
          var prepared = prepareReceipt(c, request);
          return () ->
              commitReceipt(
                  c,
                  request,
                  target,
                  mailbox.head(TenantContext.require(), c.getRealm(), c.getNode()),
                  prepared);
        });
  }

  private record ReceiptSnapshot(
      Map<UUID, Map<String, Object>> rows,
      Map<UUID, auriga.collab.api.NetworkLocalTargets.Registration> locals) {}

  private ReceiptSnapshot prepareReceipt(NetworkAuthorityConnection c, JsonNode request) {
    var records = new LinkedHashMap<UUID, Map<String, Object>>();
    tx.execute(
        status -> {
          for (var entry : request.path("results")) {
            UUID id = uuid(entry.path("recipient_id").asString());
            records.put(id, receiptRow(c, request, id, false));
          }
          return null;
        });
    var locals = new LinkedHashMap<UUID, auriga.collab.api.NetworkLocalTargets.Registration>();
    for (var entry : records.entrySet())
      locals.put(
          entry.getKey(),
          source.localSnapshot(
              TenantContext.require(),
              c.getRealm(),
              c.getNode(),
              entry.getValue().get("target_local").toString()));
    return new ReceiptSnapshot(Map.copyOf(records), Map.copyOf(locals));
  }

  private Map<String, Object> receiptRow(
      NetworkAuthorityConnection c, JsonNode request, UUID recipient, boolean lock) {
    var rows =
        jdbc.queryForList(
            """
        SELECT r.target_local,r.state_version,r.transport_status,m.id AS message_id,m.message_json,m.dispatch_context_json
          FROM collab.network_recipient r
          JOIN collab.network_mail_item i ON i.tenant_id=r.tenant_id AND i.id=r.delivery_id
          JOIN collab.network_message m ON m.tenant_id=i.tenant_id AND m.id=i.message_id
         WHERE r.tenant_id=? AND r.id=? AND i.id=? AND i.target_realm=? AND i.target_node=? AND i.mail_seq=?
        """
                + (lock ? " FOR UPDATE OF r" : ""),
            TenantContext.require(),
            recipient,
            uuid(request.path("delivery_id").asString()),
            c.getRealm(),
            c.getNode(),
            decimal(request, "mail_seq"));
    require(rows.size() == 1, NetworkErrors.UNAUTHORIZED);
    return rows.getFirst();
  }

  private String commitReceipt(
      NetworkAuthorityConnection c,
      JsonNode request,
      Target target,
      NetworkMailboxService.NodeRoute route,
      ReceiptSnapshot prepared) {
    UUID tenant = TenantContext.require();
    require(decimal(request, "epoch") == target.epoch(), NetworkErrors.STALE_EPOCH);
    String operation = request.path("operation_id").asString();
    String canonical = NetworkJson.canonical(request);
    var existing =
        jdbc.queryForList(
            "SELECT request_json,result_json FROM collab.network_wire_receipt WHERE tenant_id=? AND realm=? AND node_id=? AND operation_id=?",
            tenant,
            c.getRealm(),
            c.getNode(),
            operation);
    ObjectNode cached = null;
    if (!existing.isEmpty()) {
      require(canonical.equals(existing.getFirst().get("request_json")), NetworkErrors.CONFLICT);
      cached = (ObjectNode) NetworkJson.parse(existing.getFirst().get("result_json").toString());
    }
    UUID delivery = uuid(request.path("delivery_id").asString());
    var items = new ArrayList<NetworkMailboxService.ReceiptItem>();
    var newProofs = new LinkedHashMap<UUID, String>();
    var replays = new LinkedHashMap<UUID, NetworkMailboxService.ReceiptResultView>();
    var unique = new HashSet<UUID>();
    for (var entry : request.path("results")) {
      UUID recipient = uuid(entry.path("recipient_id").asString());
      require(unique.add(recipient), NetworkErrors.PROTOCOL_ERROR);
      var record = receiptRow(c, request, recipient, true);
      var snapshot = prepared.rows().get(recipient);
      // Recipient status may have advanced through an identical concurrent proof. Identity/body
      // cannot change.
      for (String key :
          List.of("target_local", "message_id", "message_json", "dispatch_context_json"))
        require(Objects.equals(record.get(key), snapshot.get(key)), NetworkErrors.CONFLICT);
      source.revalidateLocal(tenant, prepared.locals().get(recipient));
      var address = entry.path("target");
      require(
          c.getRealm().equals(address.path("realm").asString())
              && c.getNode().equals(address.path("node").asString())
              && record.get("target_local").equals(address.path("local").asString()),
          NetworkErrors.UNAUTHORIZED);
      require(record.get("message_json") != null, NetworkErrors.PROTOCOL_ERROR);
      var message = NetworkJson.parse(record.get("message_json").toString());
      require(
          NetworkJson.digest(message).equals(entry.path("message_digest").asString()),
          NetworkErrors.CONFLICT);
      String outcome = entry.path("status").asString();
      require(
          Set.of("host_accepted", "quarantined", "rejected").contains(outcome),
          NetworkErrors.PROTOCOL_ERROR);
      var dispatch =
          record.get("dispatch_context_json") == null
              ? null
              : NetworkJson.parse(record.get("dispatch_context_json").toString());
      var expected =
          NetworkReceiptProofs.target(
              message,
              dispatch,
              c.getRealm(),
              c.getNode(),
              record.get("target_local").toString(),
              prepared.locals().get(recipient).type());
      var contacts =
          jdbc.queryForList(
              "SELECT status,scope FROM collab.network_contact WHERE tenant_id=? AND message_id=?",
              tenant,
              record.get("message_id"));
      var contact = contacts.isEmpty() ? null : NetworkJson.JSON.valueToTree(contacts.getFirst());
      NetworkReceiptProofs.stage(entry, message, expected, contact);
      if (cached != null && !"host_accepted".equals(outcome)) {
        require(
            ("rejected".equals(outcome) ? "revoked" : outcome)
                .equals(record.get("transport_status")),
            NetworkErrors.CONFLICT);
        continue;
      }
      if ("host_accepted".equals(outcome)) {
        String proof = NetworkReceiptProofs.stable(entry);
        var old =
            jdbc.queryForList(
                "SELECT proof_json FROM collab.network_acceptance_proof WHERE tenant_id=? AND recipient_id=? AND accepted_by=?",
                tenant,
                recipient,
                entry.path("accepted_by").asString());
        if (!old.isEmpty()) {
          require(proof.equals(old.getFirst().get("proof_json")), NetworkErrors.CONFLICT);
          require("host_accepted".equals(record.get("transport_status")), NetworkErrors.CONFLICT);
          replays.put(
              recipient,
              new NetworkMailboxService.ReceiptResultView(
                  recipient, "host_accepted", number(record, "state_version")));
          continue;
        }
        require(!"host_accepted".equals(record.get("transport_status")), NetworkErrors.CONFLICT);
        newProofs.put(recipient, proof);
      }
      require(
          decimal(entry, "expected_state_version") == number(record, "state_version"),
          NetworkErrors.CONFLICT);
      items.add(
          new NetworkMailboxService.ReceiptItem(
              recipient, "rejected".equals(outcome) ? "revoked" : outcome));
    }
    if (cached != null) return signed(cached, "ReceiptCommitted", target);
    var commit =
        items.isEmpty()
            ? new NetworkMailboxService.ReceiptCommitResult(
                delivery,
                decimal(request, "mail_seq"),
                List.of(),
                jdbc.queryForObject(
                        "SELECT count(*) FROM collab.network_recipient WHERE tenant_id=? AND delivery_id=? AND transport_status NOT IN ('host_accepted','expired','canceled','revoked','skipped','unresolved')",
                        Integer.class,
                        tenant,
                        delivery)
                    == 0,
                route.committedCursor())
            : mailbox.receipt(
                new NetworkMailboxService.ReceiptRequest(
                    tenant,
                    c.getRealm(),
                    c.getNode(),
                    target.epoch(),
                    delivery,
                    decimal(request, "mail_seq"),
                    items));
    for (var entry : request.path("results")) {
      UUID id = uuid(entry.path("recipient_id").asString());
      if (newProofs.containsKey(id))
        jdbc.update(
            "INSERT INTO collab.network_acceptance_proof(tenant_id,recipient_id,delivery_id,accepted_by,proof_json) VALUES (?,?,?,?,?)",
            tenant,
            id,
            delivery,
            entry.path("accepted_by").asString(),
            newProofs.get(id));
    }
    var result =
        base("receipt_committed")
            .put("request_id", target.requestId())
            .put("operation_id", operation)
            .put("delivery_id", delivery.toString())
            .put("mail_seq", Long.toString(commit.mailSeq()))
            .put("mail_item_closed", commit.mailItemClosed())
            .put("committed_cursor", Long.toString(commit.committedCursor()));
    var results = result.putArray("results");
    var combined = new LinkedHashMap<UUID, NetworkMailboxService.ReceiptResultView>(replays);
    commit.results().forEach(value -> combined.put(value.recipientId(), value));
    for (var original : request.path("results")) {
      var entry = combined.get(uuid(original.path("recipient_id").asString()));
      results
          .addObject()
          .put("recipient_id", entry.recipientId().toString())
          .put("transport_status", entry.transportStatus())
          .put("state_version", Long.toString(entry.stateVersion()));
    }
    String signed = signed(result, "ReceiptCommitted", target);
    jdbc.update(
        "INSERT INTO collab.network_wire_receipt(tenant_id,realm,node_id,operation_id,request_json,result_json) VALUES (?,?,?,?,?,?)",
        tenant,
        c.getRealm(),
        c.getNode(),
        operation,
        canonical,
        NetworkJson.canonical(result));
    return signed;
  }

  private interface Work {
    java.util.function.Supplier<String> prepare(Map<String, Object> row, Target target);
  }

  private String associated(NetworkAuthorityConnection c, String requestId, Work work) {
    NetworkAuthorityClient.binding(c);
    Instant deadline = source.verify(c);
    var before = tx.execute(status -> read(c, false));
    require(before != null, NetworkErrors.STALE_EPOCH);
    require(bindingJson(c).equals(before.get("wire_binding_json")), NetworkErrors.STALE_EPOCH);
    require("ready".equals(before.get("wire_status")), RECOVERY);
    NetworkWireCapabilities.verify(
        NetworkJson.parse(before.get("wire_ready_json").toString()), broadcast);
    var target = target(c, before, requestId, deadline);
    return source.withTarget(
        c,
        target,
        broadcast,
        () -> {
          var decision =
              mailbox.authorizeTarget(
                  TenantContext.require(),
                  c.getRealm(),
                  c.getNode(),
                  Set.of(auriga.iam.api.NetworkAuthority.Operation.RECEIVE));
          return mailbox.withDecision(
              decision,
              () -> {
                var prepared = work.prepare(before, target);
                return tx.execute(
                    status -> {
                      var row = locked(c);
                      unchanged(before, row);
                      require(
                          bindingJson(c).equals(row.get("wire_binding_json")),
                          NetworkErrors.STALE_EPOCH);
                      require("ready".equals(row.get("wire_status")), RECOVERY);
                      locals.requireHealthy(TenantContext.require(), c.getRealm(), c.getNode());
                      return mailbox.withDecision(decision, prepared);
                    });
              });
        });
  }

  private static void unchanged(Map<String, Object> before, Map<String, Object> current) {
    if (before == null) {
      require(
          number(current, "connection_epoch") == 0 && current.get("wire_binding_json") == null,
          NetworkErrors.STALE_EPOCH);
      return;
    }
    for (String key :
        List.of(
            "connection_epoch",
            "ingress_id",
            "wire_binding_json",
            "wire_ready_json",
            "wire_incarnation",
            "wire_nonce",
            "wire_status"))
      require(Objects.equals(before.get(key), current.get(key)), NetworkErrors.STALE_EPOCH);
  }

  private Map<String, Object> locked(NetworkAuthorityConnection c) {
    return read(c, true);
  }

  private Map<String, Object> read(NetworkAuthorityConnection c, boolean lock) {
    var rows =
        jdbc.queryForList(
            "SELECT * FROM collab.network_node WHERE tenant_id=? AND realm=? AND node_id=?"
                + (lock ? " FOR UPDATE" : ""),
            TenantContext.require(),
            c.getRealm(),
            c.getNode());
    if (!lock && rows.isEmpty()) return null;
    require(rows.size() == 1, NetworkErrors.STALE_EPOCH);
    return rows.getFirst();
  }

  private Target target(
      NetworkAuthorityConnection c, Map<String, Object> row, String requestId, Instant deadline) {
    return new Target(
        c.getRealm(),
        c.getNode(),
        row.get("wire_incarnation").toString(),
        number(row, "connection_epoch"),
        row.get("wire_nonce").toString(),
        requestId,
        deadline);
  }

  private String signed(ObjectNode result, String definition, Target target) {
    require(target.validUntil().isAfter(clock.instant()), NetworkErrors.UNAUTHORIZED);
    var signed =
        signer.sign(
            result,
            "protocol_response",
            new NetworkEnvelopeSigner.Context(
                target.realm(),
                target.node(),
                target.incarnation(),
                target.epoch(),
                target.channelNonce(),
                target.requestId()),
            target.validUntil());
    NetworkSchemas.validate("protocol", definition, signed);
    return NetworkJson.canonical(signed);
  }

  private static JsonNode frame(byte[] bytes, String definition) {
    var frame = NetworkJson.parse(bytes);
    require(
        ("LocalRegister".equals(definition)
                ? "local_register"
                : "ReceiveSet".equals(definition)
                    ? "receive_set"
                    : definition.toLowerCase(Locale.ROOT))
            .equals(frame.path("type").asString()),
        NetworkErrors.PROTOCOL_ERROR);
    NetworkSchemas.validate("protocol", definition, frame);
    return frame;
  }

  private static ObjectNode base(String type) {
    return NetworkJson.object().put("app_version", "1.0").put("type", type);
  }

  private static String bindingJson(NetworkAuthorityConnection c) {
    return NetworkJson.canonical(
        NetworkJson.object()
            .put("tenant", c.getTenantId())
            .put("realm", c.getRealm())
            .put("node", c.getNode())
            .put("ingress", c.getIngress())
            .put("server", c.getServerId())
            .put("client", c.getClientId())
            .put("account", c.getClientAccount()));
  }

  private static long number(Map<String, Object> row, String key) {
    return ((Number) row.get(key)).longValue();
  }

  private static long decimal(JsonNode frame, String key) {
    try {
      return Long.parseLong(frame.path(key).asString());
    } catch (NumberFormatException e) {
      throw new AurigaException(NetworkErrors.PROTOCOL_ERROR);
    }
  }

  private static UUID uuid(String value) {
    try {
      return UUID.fromString(value);
    } catch (IllegalArgumentException e) {
      throw new AurigaException(NetworkErrors.PROTOCOL_ERROR);
    }
  }

  private static void require(boolean valid, ErrorCode error) {
    if (!valid) throw new AurigaException(error);
  }
}
