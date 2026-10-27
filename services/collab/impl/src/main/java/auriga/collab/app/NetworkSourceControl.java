package auriga.collab.app;

import auriga.collab.app.NetworkMailboxWireService.SourceGate;
import auriga.collab.grpc.NetworkSourceGrpcAuthority;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.ErrorCode;
import auriga.common.persistence.TenantTransactionManager;
import auriga.common.persistence.UuidV7;
import auriga.contracts.iam.v1.*;
import io.grpc.Status;
import java.math.BigDecimal;
import java.sql.Timestamp;
import java.time.Clock;
import java.time.Instant;
import java.time.temporal.ChronoUnit;
import java.util.*;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.jdbc.core.namedparam.NamedParameterJdbcTemplate;
import org.springframework.transaction.support.TransactionSynchronizationManager;
import org.springframework.transaction.support.TransactionTemplate;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** Source control is a durable receipt/observation ledger; it does not admit SEND. */
final class NetworkSourceControl {
  private static final ErrorCode UNKNOWN = error("outcome_unknown", 503);
  private static final ErrorCode OVERLOADED = error("overloaded", 503);
  private static final ErrorCode EXPIRED = error("expired", 410);
  private static final ErrorCode REVOKED = error("revoked", 403);
  private static final ErrorCode REGISTRATION = error("local_registration_required", 412);
  private final NetworkMailboxWireService wire;
  private final JdbcTemplate jdbc;
  private final TransactionTemplate tx;
  private final NetworkSourceGrpcAuthority iam;
  private final Clock clock;

  NetworkSourceControl(
      NetworkMailboxWireService wire,
      JdbcTemplate jdbc,
      TenantTransactionManager manager,
      NetworkSourceGrpcAuthority iam,
      Clock clock) {
    this.wire = wire;
    this.jdbc = jdbc;
    tx = new TransactionTemplate(manager);
    this.iam = iam;
    this.clock = clock;
  }

  String handle(NetworkAuthorityConnection connection, byte[] bytes, String definition) {
    var frame = NetworkJson.parse(bytes);
    // Strict raw JSON budgets plus consumer validation retain future optional fields.
    boolean invalid;
    try {
      NetworkSchemas.validate("source-control", definition, frame);
      invalid = false;
    } catch (AurigaException e) {
      require(
          "SourceRegister".equals(definition)
              && "source_register".equals(text(frame, "type"))
              && text(frame, "operation_id").matches("[A-Za-z0-9][A-Za-z0-9._:-]{0,127}")
              && text(frame, "request_id").matches("[A-Za-z0-9][A-Za-z0-9._:-]{0,127}")
              && frame.path("registration").isObject()
              && frame.has("scope")
              && frame.has("expected_revision")
              && frame.has("predecessor_binding"),
          NetworkErrors.PROTOCOL_ERROR);
      invalid = true;
    }
    outside();
    boolean invalidFrame = invalid;
    return iam.sourceRequest(
        () -> handleRequest(connection, bytes, definition, frame, invalidFrame));
  }

  private String handleRequest(
      NetworkAuthorityConnection connection,
      byte[] bytes,
      String definition,
      JsonNode frame,
      boolean invalid) {
    var gate = wire.sourceGate(connection, frame);
    UUID holder = UuidV7.next();
    String slot = "SourceRevoke".equals(definition) ? "revoke" : "regular";
    boolean acquired = false;
    try {
      acquire(gate, slot, holder);
      acquired = true;
      SourceResult response =
          switch (definition) {
            case "SourceRegister" -> register(gate, holder, bytes, invalid);
            case "SourceGet" -> new SourceResult(get(gate), true);
            case "SourceRevoke" -> new SourceResult(revoke(gate, bytes), true);
            default -> throw new AurigaException(NetworkErrors.PROTOCOL_ERROR);
          };
      ObjectNode result = response.frame();
      Instant until;
      try {
        until = wire.refreshSourceGate(gate, response.history() != null);
      } catch (AurigaException e) {
        if (response.history() != null && "common.unavailable".equals(e.errorCode().code()))
          throw new AurigaException(UNKNOWN);
        throw e;
      }
      return transaction(
          s -> {
            if (response.history() != null) {
              lockOp("Register", text(frame, "operation_id"));
              response
                  .history()
                  .requireUnchanged(operation("Register", text(frame, "operation_id")));
              wire.lockSourceGate(gate, response.frameFence());
            } else if (response.frameFence()) wire.lockSourceGate(gate, true);
            else wire.lockSourceErrorGate(gate);
            requireSlot(gate, slot, holder);
            if ("SourceGet".equals(definition)) finalObservation(gate, result);
            Instant signingUntil =
                result.has("valid_until")
                    ? Collections.min(List.of(until, instant(text(result, "valid_until"))))
                    : until;
            return wire.signSource(gate, result, definition + "Result", signingUntil);
          });
    } catch (AurigaException e) {
      // Authentication/association failures before a gate exists stay at the gRPC boundary.
      return transaction(
          s -> {
            wire.lockSourceErrorGate(gate);
            String code =
                e.errorCode().code().startsWith("network.")
                    ? e.errorCode().code()
                    : "network.overloaded";
            var result =
                base("error")
                    .put("code", code)
                    .put("scope", "request")
                    .put("retryable", "network.overloaded".equals(code))
                    .putNull("retry_after");
            return wire.signSource(gate, result, "Error", gate.target().validUntil());
          });
    } finally {
      if (acquired)
        transactionWithoutResult(
            s ->
                jdbc.update(
                    "DELETE FROM collab.network_source_slot WHERE tenant_id=? AND holder=?",
                    tenant(),
                    holder));
    }
  }

  private void finalObservation(SourceGate gate, ObjectNode result) {
    wire.lockSourceGate(gate, true);
    registration(gate);
    var binding = binding(gate, result.path("source").path("source_binding").asString());
    var lineage = lockLineage((UUID) binding.get("lineage_id"));
    witness(gate, "Get:" + result.path("source").path("source_binding").asString());
    nativeBinding(lineage, gate.request().path("native_witness"));
    if (Boolean.TRUE.equals(lineage.get("stopped"))
        && "active".equals(result.path("source").path("state").asString())) {
      ((ObjectNode) result.path("source")).put("state", "stopped");
      result.remove("valid_until");
    }
    if (result.has("valid_until")) {
      require(
          Objects.equals(binding.get("permission_version"), lineage.get("permission_version"))
              && Objects.equals(binding.get("stop_version"), lineage.get("stop_version")),
          NetworkErrors.CONFLICT);
      require(instant(text(result, "valid_until")).isAfter(clock.instant()), EXPIRED);
    }
  }

  private void acquire(SourceGate gate, String slot, UUID holder) {
    transactionWithoutResult(
        s -> {
          wire.lockSourceErrorGate(gate);
          int changed =
              jdbc.update(
                  """
          INSERT INTO collab.network_source_slot(id,tenant_id,realm,node_id,slot,holder,valid_until)
          VALUES (?,?,?,?,?,?,?) ON CONFLICT (tenant_id,realm,node_id,slot) DO UPDATE
          SET holder=EXCLUDED.holder,valid_until=EXCLUDED.valid_until
          WHERE network_source_slot.valid_until<=?
          """,
                  UuidV7.next(),
                  tenant(),
                  gate.connection().getRealm(),
                  gate.connection().getNode(),
                  slot,
                  holder,
                  Timestamp.from(gate.target().validUntil()),
                  Timestamp.from(clock.instant()));
          require(changed == 1, OVERLOADED);
        });
  }

  private void requireSlot(SourceGate gate, String slot, UUID holder) {
    var row =
        one(
            "SELECT * FROM collab.network_source_slot WHERE tenant_id=? AND realm=? AND node_id=? AND slot=? FOR UPDATE",
            tenant(),
            gate.connection().getRealm(),
            gate.connection().getNode(),
            slot);
    require(
        row != null
            && holder.equals(row.get("holder"))
            && ((Timestamp) row.get("valid_until")).toInstant().isAfter(clock.instant()),
        EXPIRED);
  }

  private record PreparedRegister(
      Map<String, Object> row, boolean existing, NetworkLocalSourceHistory.Snapshot history) {
    PreparedRegister(Map<String, Object> row, boolean existing) {
      this(
          row,
          existing,
          "not_accepted".equals(row.get("status")) && row.get("iam_request") == null
              ? NetworkLocalSourceHistory.freeze(row)
              : null);
    }
  }

  private record SourceResult(
      ObjectNode frame, boolean frameFence, NetworkLocalSourceHistory.Snapshot history) {
    SourceResult(ObjectNode frame, boolean frameFence) {
      this(frame, frameFence, null);
    }
  }

  private SourceResult register(SourceGate gate, UUID holder, byte[] bytes, boolean invalid) {
    var frame = gate.request();
    String op = text(frame, "operation_id");
    String business = NetworkJson.canonical(business(frame));
    PreparedRegister prepared;
    try {
      prepared =
          transaction(
              s -> {
                lockOp("Register", op);
                var original = operation("Register", op);
                if (original != null) {
                  owned(gate, original);
                  require(business.equals(original.get("business_json")), NetworkErrors.CONFLICT);
                  if ("not_accepted".equals(original.get("status"))) {
                    NetworkLocalSourceHistory.freeze(original);
                    require(gate.anchor() != null, UNKNOWN);
                    wire.lockSourceGate(gate, true);
                    requireSlot(gate, "regular", holder);
                  }
                  if (invalid && "pending".equals(original.get("status")))
                    throw new AurigaException(UNKNOWN);
                  return new PreparedRegister(original, true);
                }
                // Only the primary ledger, while holding the same key lock as every writer, can
                // prove non-acceptance. Restored/uncertain node histories cannot produce
                // tombstones.
                var node = node(gate);
                require(!Boolean.TRUE.equals(node.get("source_operation_recovery_gap")), UNKNOWN);
                require(
                    one(
                            """
          SELECT o.id FROM collab.network_source_outbox b JOIN collab.network_source_operation o
          ON o.tenant_id=b.tenant_id AND o.id=b.operation_id
          WHERE o.tenant_id=? AND o.method='Register' AND o.operation_id=?
          """,
                            tenant(),
                            op)
                        == null,
                    UNKNOWN);
                if ("reconcile".equals(text(frame, "mode")))
                  return new PreparedRegister(notAccepted(gate, holder, bytes, business), false);
                if (invalid)
                  return new PreparedRegister(notAccepted(gate, holder, bytes, business), false);
                NetworkAgentSourceRegistration registration;
                Map<String, Object> lineage;
                try {
                  wire.lockSourceGate(gate, true);
                  registration = registration(gate);
                  witness(gate, "Register:" + op);
                  lineage = lineage(gate, registration, true);
                  require(!Boolean.TRUE.equals(lineage.get("stopped")), REVOKED);
                  if (!matchesNative(lineage, frame.path("native_witness"))) {
                    stopAndEnqueue(gate, lineage);
                    return new PreparedRegister(notAccepted(gate, holder, bytes, business), false);
                  }
                  // An unresolved mutation cannot be bypassed by another local or operation id.
                  require(
                      one(
                              "SELECT id FROM collab.network_source_operation WHERE tenant_id=? AND realm=? AND node_id=? AND method='Register' AND status='pending'",
                              tenant(),
                              gate.connection().getRealm(),
                              gate.connection().getNode())
                          == null,
                      UNKNOWN);
                } catch (AurigaException e) {
                  if (e.errorCode().equals(UNKNOWN) || e.errorCode().equals(OVERLOADED)) throw e;
                  return new PreparedRegister(notAccepted(gate, holder, bytes, business), false);
                }
                var request =
                    RegisterAgentSourceRequest.newBuilder()
                        .setConnection(gate.connection())
                        .setRegistration(registration)
                        .setOperationId(op)
                        .setScope(
                            NetworkAgentSourceScope.NETWORK_AGENT_SOURCE_SCOPE_DURABLE_UNICAST_SEND)
                        .setExpectedRevision(unsigned(text(frame, "expected_revision")))
                        .setPredecessorBinding(text(frame, "predecessor_binding"))
                        .build();
                String json = NetworkJson.canonical(requestJson(request));
                insertOperation(
                    gate,
                    "Register",
                    business,
                    bytes,
                    request.toByteArray(),
                    json,
                    NetworkJson.digest(NetworkJson.parse(json)),
                    lineage,
                    "pending",
                    null);
                return new PreparedRegister(operation("Register", op), false);
              });
    } catch (AurigaException e) {
      throw e;
    } catch (RuntimeException e) {
      // A commit acknowledgement failure is never a definitive pre-pending rejection.
      throw new AurigaException(UNKNOWN);
    }
    var pending = prepared.row();
    if (prepared.history() != null) {
      if (prepared.existing()) {
        try {
          // Pure provenance read, outside the op transaction. Never use any source RPC here.
          var proof = iam.readConnectionAnchor(prepared.history().anchor(), gate.anchor());
          require(
              proof != null
                  && proof.getUnknownFields().asMap().isEmpty()
                  && proof.hasOriginal()
                  && proof.hasCurrent()
                  && prepared.history().anchor().equals(proof.getOriginal())
                  && gate.anchor().equals(proof.getCurrent()),
              UNKNOWN);
        } catch (RuntimeException e) {
          throw new AurigaException(UNKNOWN);
        }
      }
      return new SourceResult(prepared.history().result(), prepared.existing(), prepared.history());
    }
    boolean sameConnection =
        Arrays.equals(gate.connection().toByteArray(), (byte[]) pending.get("original_connection"));
    boolean receiptRead =
        prepared.existing()
            && pending.get("iam_request") != null
            && ("pending".equals(pending.get("status"))
                || "reconcile".equals(text(frame, "mode"))
                || !sameConnection);
    if (!"pending".equals(pending.get("status")) && !receiptRead) {
      require(sameConnection, UNKNOWN);
      return new SourceResult(result(pending), true);
    }
    try {
      var original = frozenRegister(pending);
      RegisterAgentSourceResponse response;
      if (receiptRead) {
        transactionWithoutResult(s -> wire.lockSourceGate(gate, true));
        // A miss never falls back to Register, even if the old C might still be live.
        response = iam.readSourceReceipt(original);
      } else {
        require(gate.connection().equals(original.getConnection()), UNKNOWN);
        require(
            text(frame, "epoch").equals(pending.get("epoch").toString())
                && text(frame, "boot_id").equals(pending.get("boot_id")),
            UNKNOWN);
        transactionWithoutResult(
            s -> {
              lockOp("Register", op);
              wire.lockSourceGate(gate, true);
              require(
                  registration(gate).equals(original.getRegistration()), NetworkErrors.CONFLICT);
              var lineage = lockLineage((UUID) pending.get("lineage_id"));
              fences(pending, lineage);
              require(!Boolean.TRUE.equals(lineage.get("stopped")), REVOKED);
              witness(gate, "Register:" + op);
              nativeBinding(lineage, frame.path("native_witness"));
            });
        response = iam.registerSource(original);
      }
      validateRegister(original, response, pending.get("request_digest").toString());
      // Commit the complete original receipt before attempting any transport/freshness CAS.
      var value =
          transaction(
              s -> {
                lockOp("Register", op);
                var current = operation("Register", op);
                require(
                    current != null
                        && Arrays.equals(
                            (byte[]) current.get("iam_request"),
                            (byte[]) pending.get("iam_request"))
                        && Objects.equals(
                            current.get("request_digest"), pending.get("request_digest"))
                        && Objects.equals(
                            current.get("business_json"), pending.get("business_json")),
                    UNKNOWN);
                if (!"pending".equals(current.get("status"))) {
                  require(
                      current.get("iam_response") != null
                          && response.equals(registerResponse(current.get("iam_response"))),
                      UNKNOWN);
                  return result(current);
                }
                var lineage = lockLineage((UUID) current.get("lineage_id"));
                ObjectNode responseFrame;
                String state;
                if (response.getDefinitivelyUncommitted()) {
                  state = "not_committed";
                  responseFrame =
                      registerResult(
                          op,
                          state,
                          null,
                          NetworkJson.object()
                              .put("authority", "iam")
                              .put("reason", response.getRejectionReason())
                              .put("request_digest", response.getRequestDigest()));
                } else {
                  state = "registered";
                  var source = response.getSource();
                  jdbc.update(
                      """
              INSERT INTO collab.network_source_binding(id,tenant_id,source_binding,lineage_id,operation_id,creation_receipt)
              VALUES (?,?,?,?,?,?)
              """,
                      UuidV7.next(),
                      tenant(),
                      source.getSourceBinding(),
                      lineage.get("id"),
                      current.get("id"),
                      source.toByteArray());
                  jdbc.update(
                      "UPDATE collab.network_source_lineage SET head_binding=? WHERE tenant_id=? AND id=?",
                      source.getSourceBinding(),
                      tenant(),
                      lineage.get("id"));
                  responseFrame = registerResult(op, state, fact(source, lineage, false), null);
                }
                complete(current, state, response.toByteArray(), responseFrame);
                return responseFrame;
              });
      return new SourceResult(value, true);
    } catch (RuntimeException e) {
      // No ordinary RPC, storage or post-pending validation failure is a negative receipt.
      throw new AurigaException(UNKNOWN);
    }
  }

  private Map<String, Object> notAccepted(
      SourceGate gate, UUID holder, byte[] bytes, String business) {
    require(gate.anchor() != null, UNKNOWN);
    wire.lockSourceGate(gate, false);
    requireSlot(gate, "regular", holder);
    String digest = NetworkJson.digest(NetworkJson.parse(business));
    var value =
        registerResult(
            text(gate.request(), "operation_id"),
            "not_accepted",
            null,
            NetworkJson.object()
                .put("authority", "collab")
                .put("reason", "rejected_before_pending")
                .put("request_digest", digest));
    insertOperation(
        gate, "Register", business, bytes, null, null, digest, null, "not_accepted", value);
    return operation("Register", text(gate.request(), "operation_id"));
  }

  private void insertOperation(
      SourceGate gate,
      String method,
      String business,
      byte[] bytes,
      byte[] request,
      String requestJson,
      String digest,
      Map<String, Object> lineage,
      String state,
      ObjectNode result) {
    UUID id = UuidV7.next();
    var row = new LinkedHashMap<String, Object>();
    row.put("id", id);
    row.put("tenant_id", tenant());
    row.put("method", method);
    row.put("operation_id", text(gate.request(), "operation_id"));
    row.put("realm", gate.connection().getRealm());
    row.put("node_id", gate.connection().getNode());
    row.put("business_json", business);
    row.put("request_digest", digest);
    row.put("original_frame", bytes);
    row.put("original_connection", gate.connection().toByteArray());
    row.put("iam_request", request);
    row.put("iam_request_json", requestJson);
    row.put("iam_response", null);
    row.put("lineage_id", lineage == null ? null : lineage.get("id"));
    row.put(
        "witness_json",
        gate.request().hasNonNull("native_witness")
            ? NetworkJson.canonical(gate.request().path("native_witness"))
            : null);
    row.put("epoch", gate.target().epoch());
    row.put("boot_id", text(gate.request(), "boot_id"));
    row.put("stop_version", lineage == null ? null : lineage.get("stop_version"));
    row.put("permission_version", lineage == null ? null : lineage.get("permission_version"));
    row.put("status", state);
    row.put("result_json", result == null ? null : NetworkJson.canonical(result));
    row.put(
        "created_at",
        Timestamp.from(clock.instant().truncatedTo(java.time.temporal.ChronoUnit.MICROS)));
    boolean localHistory = "not_accepted".equals(state) && request == null;
    row.put("local_history_version", localHistory ? 1 : null);
    row.put("original_connection_anchor", localHistory ? gate.anchor().toByteArray() : null);
    row.put("local_history_digest", localHistory ? NetworkLocalSourceHistory.digest(row) : null);
    if (localHistory) NetworkLocalSourceHistory.freeze(row);
    new NamedParameterJdbcTemplate(jdbc)
        .update(
            """
        INSERT INTO collab.network_source_operation(id,tenant_id,method,operation_id,realm,node_id,
        business_json,request_digest,original_frame,original_connection,iam_request,iam_request_json,lineage_id,witness_json,
        epoch,boot_id,stop_version,permission_version,status,result_json,created_at,
        local_history_version,original_connection_anchor,local_history_digest)
        VALUES (:id,:tenant_id,:method,:operation_id,:realm,:node_id,:business_json,:request_digest,
        :original_frame,:original_connection,:iam_request,:iam_request_json,:lineage_id,:witness_json,
        :epoch,:boot_id,:stop_version,:permission_version,:status,:result_json,:created_at,
        :local_history_version,:original_connection_anchor,:local_history_digest)
        """,
            row);
    if (request != null)
      jdbc.update(
          "INSERT INTO collab.network_source_outbox(id,tenant_id,operation_id) VALUES (?,?,?)",
          UuidV7.next(),
          tenant(),
          id);
  }

  private void complete(Map<String, Object> op, String state, byte[] response, ObjectNode result) {
    jdbc.update(
        "UPDATE collab.network_source_operation SET status=?,iam_response=?,result_json=? WHERE tenant_id=? AND id=? AND status='pending'",
        state,
        response,
        NetworkJson.canonical(result),
        tenant(),
        op.get("id"));
    jdbc.update(
        "UPDATE collab.network_source_outbox SET completed=true WHERE tenant_id=? AND operation_id=?",
        tenant(),
        op.get("id"));
  }

  private record Observation(
      Map<String, Object> binding,
      Map<String, Object> lineage,
      NetworkAgentSource original,
      NetworkAgentSourceRegistration registration) {}

  private ObjectNode get(SourceGate gate) {
    var snapshot =
        transaction(
            s -> {
              wire.lockSourceGate(gate, true);
              var binding = binding(gate, text(gate.request(), "source_binding"));
              var lineage = lockLineage((UUID) binding.get("lineage_id"));
              var registration = registration(gate);
              var original = sourceBytes(binding.get("creation_receipt"));
              require(original.getRegistration().equals(registration), NetworkErrors.CONFLICT);
              witness(gate, "Get:" + original.getSourceBinding());
              if (!matchesNative(lineage, gate.request().path("native_witness"))) {
                stopAndEnqueue(gate, lineage);
                return null;
              }
              return new Observation(binding, lineage, original, registration);
            });
    require(snapshot != null, NetworkErrors.CONFLICT);
    var request =
        GetAgentSourceRequest.newBuilder()
            .setSourceBinding(snapshot.original().getSourceBinding())
            .setConnection(gate.connection())
            .setRegistration(snapshot.registration())
            .build();
    GetAgentSourceResponse response;
    try {
      response = iam.getSource(request);
    } catch (AurigaException e) {
      throw e;
    }
    validateGet(gate, snapshot.original(), response);
    Instant deadline = wire.refreshSourceGate(gate);
    return transaction(
        s -> {
          wire.lockSourceGate(gate, true);
          require(registration(gate).equals(snapshot.registration()), NetworkErrors.CONFLICT);
          var lineage = lockLineage((UUID) snapshot.lineage().get("id"));
          require(
              Objects.equals(
                  lineage.get("permission_version"), snapshot.lineage().get("permission_version")),
              NetworkErrors.CONFLICT);
          witness(gate, "Get:" + snapshot.original().getSourceBinding());
          nativeBinding(lineage, gate.request().path("native_witness"));
          if (response.getLineageStopped() && !Boolean.TRUE.equals(lineage.get("stopped"))) {
            stop(lineage);
            lineage = lockLineage((UUID) lineage.get("id"));
          }
          boolean stopped =
              Boolean.TRUE.equals(lineage.get("stopped"))
                  || !Objects.equals(
                      snapshot.lineage().get("stop_version"), lineage.get("stop_version"));
          var result =
              base("source_get_result")
                  .put("policy_revision", Long.toUnsignedString(response.getPolicyRevision()))
                  .put("observed_at", response.getObservedAt())
                  .put("witness_id", text(gate.request().path("native_witness"), "witness_id"));
          result.set("source", fact(response.getSource(), lineage, stopped));
          Instant until = null;
          if ("active".equals(result.path("source").path("state").asString())) {
            // The request budget may end between seconds; wire Time is UTC whole seconds.
            // Round the earliest bound down before both persistence and the expiry check.
            until =
                Collections.min(
                        List.of(
                            deadline,
                            instant(response.getValidUntil()),
                            instant(response.getSource().getExpiresAt()),
                            instant(text(gate.request().path("native_witness"), "valid_until"))))
                    .truncatedTo(ChronoUnit.SECONDS);
            require(until.isAfter(clock.instant()), EXPIRED);
            result.put("valid_until", until.toString());
          }
          jdbc.update(
              """
          UPDATE collab.network_source_binding SET observation=?,observation_request=?,observation_connection=?,observation_witness=?,
          observed_at=?,valid_until=?,epoch=?,boot_id=?,stop_version=?,permission_version=?
          WHERE tenant_id=? AND id=?
          """,
              response.toByteArray(),
              request.toByteArray(),
              gate.connection().toByteArray(),
              NetworkJson.canonical(gate.request().path("native_witness")),
              Timestamp.from(instant(response.getObservedAt())),
              until == null ? null : Timestamp.from(until),
              gate.target().epoch(),
              text(gate.request(), "boot_id"),
              lineage.get("stop_version"),
              lineage.get("permission_version"),
              tenant(),
              snapshot.binding().get("id"));
          return result;
        });
  }

  private ObjectNode revoke(SourceGate gate, byte[] bytes) {
    String op = text(gate.request(), "operation_id");
    var business =
        NetworkJson.object().put("source_binding", text(gate.request(), "source_binding"));
    var pending =
        transaction(
            s -> {
              lockOp("Revoke", op);
              var existing = operation("Revoke", op);
              if (existing != null) {
                owned(gate, existing);
                require(
                    NetworkJson.canonical(business).equals(existing.get("business_json")),
                    NetworkErrors.CONFLICT);
                return existing;
              }
              wire.lockSourceGate(gate, true);
              var binding = binding(gate, text(gate.request(), "source_binding"));
              var lineage = lockLineage((UUID) binding.get("lineage_id"));
              stop(lineage);
              var earlier =
                  one(
                      "SELECT * FROM collab.network_source_operation WHERE tenant_id=? AND lineage_id=? AND method='Revoke' AND status='pending'",
                      tenant(),
                      lineage.get("id"));
              if (earlier != null) return earlier;
              var original = sourceBytes(binding.get("creation_receipt"));
              var request =
                  RevokeAgentSourceRequest.newBuilder()
                      .setSourceBinding(original.getSourceBinding())
                      .setConnection(original.getOriginalConnection())
                      .setOperationId(op)
                      .build();
              insertOperation(
                  gate,
                  "Revoke",
                  NetworkJson.canonical(business),
                  bytes,
                  request.toByteArray(),
                  NetworkJson.canonical(revokeRequestJson(request)),
                  NetworkJson.digest(revokeRequestJson(request)),
                  lockLineage((UUID) lineage.get("id")),
                  "pending",
                  null);
              return operation("Revoke", op);
            });
    require(op.equals(pending.get("operation_id")), UNKNOWN);
    if (!"pending".equals(pending.get("status"))) return result(pending);
    return deliverRevoke(pending);
  }

  private ObjectNode deliverRevoke(Map<String, Object> pending) {
    try {
      var request = RevokeAgentSourceRequest.parseFrom((byte[]) pending.get("iam_request"));
      var response = iam.revokeSource(request);
      var receipt = response.getSource();
      var original =
          transaction(
              s ->
                  sourceBytes(
                      one(
                              "SELECT creation_receipt FROM collab.network_source_binding WHERE tenant_id=? AND source_binding=?",
                              tenant(),
                              request.getSourceBinding())
                          .get("creation_receipt")));
      immutable(original, receipt);
      require(
          receipt.getState() == NetworkAgentSourceState.NETWORK_AGENT_SOURCE_STATE_REVOKED
              || receipt.getState() == NetworkAgentSourceState.NETWORK_AGENT_SOURCE_STATE_EXPIRED,
          OVERLOADED);
      require(receipt.getRevision() != 0, OVERLOADED);
      return transaction(
          s -> {
            lockOp("Revoke", request.getOperationId());
            var current = operation("Revoke", request.getOperationId());
            if (!"pending".equals(current.get("status"))) return result(current);
            var value =
                base("source_revoke_result")
                    .put("operation_id", request.getOperationId())
                    .put("source_binding", receipt.getSourceBinding())
                    .put("revision", Long.toUnsignedString(receipt.getRevision()))
                    .put("state", state(receipt))
                    .put("lineage_stopped", true);
            complete(current, "revoked", response.toByteArray(), value);
            return value;
          });
    } catch (RuntimeException | com.google.protobuf.InvalidProtocolBufferException e) {
      throw new AurigaException(UNKNOWN);
    }
  }

  int retryRevocations() {
    outside();
    require(!TenantContext.PLATFORM_TENANT.equals(tenant()), NetworkErrors.UNAUTHORIZED);
    var pending =
        transaction(
            s ->
                jdbc.queryForList(
                    """
        SELECT o.* FROM collab.network_source_operation o JOIN collab.network_source_outbox b
        ON o.tenant_id=b.tenant_id AND o.id=b.operation_id
        WHERE o.tenant_id=? AND o.method='Revoke' AND NOT b.completed AND b.next_attempt_at<=? ORDER BY b.next_attempt_at,o.created_at LIMIT 32
        """,
                    tenant(),
                    Timestamp.from(clock.instant())));
    int completed = 0;
    for (var op : pending) {
      UUID holder = UuidV7.next();
      boolean acquired =
          transaction(
              s ->
                  jdbc.update(
                          """
          INSERT INTO collab.network_source_slot(id,tenant_id,realm,node_id,slot,holder,valid_until)
          VALUES (?,?,?,?,'revoke',?,?) ON CONFLICT (tenant_id,realm,node_id,slot) DO UPDATE
          SET holder=EXCLUDED.holder,valid_until=EXCLUDED.valid_until WHERE network_source_slot.valid_until<=?
          """,
                          UuidV7.next(),
                          tenant(),
                          op.get("realm"),
                          op.get("node_id"),
                          holder,
                          Timestamp.from(clock.instant().plusSeconds(30)),
                          Timestamp.from(clock.instant()))
                      == 1);
      if (!acquired) continue;
      try {
        iam.sourceRequest(() -> deliverRevoke(op));
        completed++;
      } catch (AurigaException e) {
        transactionWithoutResult(
            s ->
                jdbc.update(
                    "UPDATE collab.network_source_outbox SET attempts=attempts+1,next_attempt_at=? WHERE tenant_id=? AND operation_id=?",
                    Timestamp.from(clock.instant().plusSeconds(5)),
                    tenant(),
                    op.get("id")));
      } finally {
        transactionWithoutResult(
            s ->
                jdbc.update(
                    "DELETE FROM collab.network_source_slot WHERE tenant_id=? AND holder=?",
                    tenant(),
                    holder));
      }
    }
    return completed;
  }

  private void stop(Map<String, Object> lineage) {
    jdbc.update(
        "UPDATE collab.network_source_lineage SET stopped=true,stop_version=stop_version+1 WHERE tenant_id=? AND id=? AND NOT stopped",
        tenant(),
        lineage.get("id"));
    jdbc.update(
        "UPDATE collab.network_source_binding SET valid_until=NULL WHERE tenant_id=? AND lineage_id=?",
        tenant(),
        lineage.get("id"));
  }

  private NetworkAgentSourceRegistration registration(SourceGate gate) {
    var requested = gate.request().path("registration");
    var row =
        one(
            """
        SELECT * FROM collab.network_local_registration WHERE tenant_id=? AND realm=? AND node_id=? AND local_id=? FOR UPDATE
        """,
            tenant(),
            gate.connection().getRealm(),
            gate.connection().getNode(),
            new BigDecimal(text(requested, "local")));
    require(row != null && "registered".equals(row.get("status")), REGISTRATION);
    var actual =
        NetworkAgentSourceRegistration.newBuilder()
            .setRegistrationId(row.get("id").toString())
            .setAllocationOpId(row.get("allocation_op_id").toString())
            .setAllocationFingerprint(row.get("allocation_fingerprint").toString())
            .setRegistrationRevision(unsigned(decimal(row, "registration_revision")))
            .setRealm(row.get("realm").toString())
            .setNode(row.get("node_id").toString())
            .setLocal(decimal(row, "local_id"))
            .setAddressType(Objects.toString(row.get("address_type"), ""))
            .build();
    require("session".equals(actual.getAddressType()), NetworkErrors.CONFLICT);
    for (var entry : registrationJson(actual).properties())
      require(entry.getValue().equals(requested.path(entry.getKey())), NetworkErrors.CONFLICT);
    return actual;
  }

  private Map<String, Object> lineage(
      SourceGate gate, NetworkAgentSourceRegistration registration, boolean create) {
    var row =
        one(
            "SELECT * FROM collab.network_source_lineage WHERE tenant_id=? AND realm=? AND node_id=? AND registration_id=? FOR UPDATE",
            tenant(),
            registration.getRealm(),
            registration.getNode(),
            UUID.fromString(registration.getRegistrationId()));
    if (row == null && create) {
      require(text(gate.request(), "predecessor_binding").isEmpty(), NetworkErrors.CONFLICT);
      UUID id = UuidV7.next();
      jdbc.update(
          "INSERT INTO collab.network_source_lineage(id,tenant_id,realm,node_id,registration_id,native_binding_json) VALUES (?,?,?,?,?,?)",
          id,
          tenant(),
          registration.getRealm(),
          registration.getNode(),
          UUID.fromString(registration.getRegistrationId()),
          NetworkJson.canonical(nativeProjection(gate.request().path("native_witness"))));
      row = lockLineage(id);
    }
    return row;
  }

  private Map<String, Object> lockLineage(UUID id) {
    var row =
        one(
            "SELECT * FROM collab.network_source_lineage WHERE tenant_id=? AND id=? FOR UPDATE",
            tenant(),
            id);
    require(row != null, UNKNOWN);
    return row;
  }

  private void nativeBinding(Map<String, Object> lineage, JsonNode witness) {
    require(matchesNative(lineage, witness), NetworkErrors.CONFLICT);
  }

  private static boolean matchesNative(Map<String, Object> lineage, JsonNode witness) {
    return NetworkJson.parse(lineage.get("native_binding_json").toString())
        .equals(nativeProjection(witness));
  }

  private void stopAndEnqueue(SourceGate gate, Map<String, Object> lineage) {
    stop(lineage);
    if (lineage.get("head_binding") == null
        || one(
                "SELECT id FROM collab.network_source_operation WHERE tenant_id=? AND lineage_id=? AND method='Revoke' AND status='pending'",
                tenant(),
                lineage.get("id"))
            != null) return;
    var row =
        one(
            "SELECT creation_receipt FROM collab.network_source_binding WHERE tenant_id=? AND source_binding=?",
            tenant(),
            lineage.get("head_binding"));
    require(row != null, UNKNOWN);
    var original = sourceBytes(row.get("creation_receipt"));
    String op = "stop-" + UuidV7.next();
    lockOp("Revoke", op);
    var frame =
        base("source_revoke")
            .put("operation_id", op)
            .put("source_binding", original.getSourceBinding())
            .put("request_id", gate.target().requestId())
            .put("epoch", Long.toString(gate.target().epoch()))
            .put("boot_id", text(gate.request(), "boot_id"));
    var revokeGate =
        new SourceGate(
            gate.connection(), frame, gate.before(), gate.target(), gate.decision(), gate.anchor());
    var request =
        RevokeAgentSourceRequest.newBuilder()
            .setSourceBinding(original.getSourceBinding())
            .setOperationId(op)
            .setConnection(original.getOriginalConnection())
            .build();
    var body = NetworkJson.object().put("source_binding", original.getSourceBinding());
    insertOperation(
        revokeGate,
        "Revoke",
        NetworkJson.canonical(body),
        NetworkJson.canonical(frame).getBytes(java.nio.charset.StandardCharsets.UTF_8),
        request.toByteArray(),
        NetworkJson.canonical(revokeRequestJson(request)),
        NetworkJson.digest(revokeRequestJson(request)),
        lockLineage((UUID) lineage.get("id")),
        "pending",
        null);
  }

  private void witness(SourceGate gate, String action) {
    var frame = gate.request();
    var witness = frame.path("native_witness");
    Instant observed = instant(text(witness, "observed_at"));
    Instant until = instant(text(witness, "valid_until"));
    require(
        !observed.isAfter(clock.instant().plusSeconds(60))
            && until.isAfter(observed)
            && !until.isAfter(observed.plusSeconds(30)),
        NetworkErrors.PROTOCOL_ERROR);
    require(until.isAfter(clock.instant()), EXPIRED);
    var full =
        NetworkJson.object()
            .put("action", action)
            .put("epoch", text(frame, "epoch"))
            .put("boot_id", text(frame, "boot_id"));
    full.set("registration", frame.path("registration"));
    full.set("native_witness", witness);
    full.set("connection", connectionJson(gate.connection()));
    String digest = NetworkJson.digest(full);
    var row =
        one(
            "SELECT * FROM collab.network_source_witness WHERE tenant_id=? AND realm=? AND node_id=? AND witness_id=? FOR UPDATE",
            tenant(),
            gate.connection().getRealm(),
            gate.connection().getNode(),
            text(witness, "witness_id"));
    if (row != null) require(digest.equals(row.get("witness_digest")), NetworkErrors.CONFLICT);
    else
      jdbc.update(
          "INSERT INTO collab.network_source_witness(id,tenant_id,realm,node_id,witness_id,witness_digest,witness_json) VALUES (?,?,?,?,?,?,?)",
          UuidV7.next(),
          tenant(),
          gate.connection().getRealm(),
          gate.connection().getNode(),
          text(witness, "witness_id"),
          digest,
          NetworkJson.canonical(full));
  }

  private Map<String, Object> binding(SourceGate gate, String id) {
    var row =
        one(
            """
        SELECT b.* FROM collab.network_source_binding b JOIN collab.network_source_lineage l
        ON b.tenant_id=l.tenant_id AND b.lineage_id=l.id
        WHERE b.tenant_id=? AND b.source_binding=? AND l.realm=? AND l.node_id=?
        """,
            tenant(),
            id,
            gate.connection().getRealm(),
            gate.connection().getNode());
    require(row != null, NetworkErrors.UNAUTHORIZED);
    return row;
  }

  private Map<String, Object> node(SourceGate gate) {
    return one(
        "SELECT * FROM collab.network_node WHERE tenant_id=? AND realm=? AND node_id=? FOR UPDATE",
        tenant(),
        gate.connection().getRealm(),
        gate.connection().getNode());
  }

  private void lockOp(String method, String op) {
    jdbc.queryForList(
        "SELECT pg_advisory_xact_lock(hashtextextended(?,0))",
        tenant() + ":source:" + method + ":" + op);
  }

  private Map<String, Object> operation(String method, String op) {
    return one(
        "SELECT * FROM collab.network_source_operation WHERE tenant_id=? AND method=? AND operation_id=? FOR UPDATE",
        tenant(),
        method,
        op);
  }

  private void owned(SourceGate gate, Map<String, Object> original) {
    require(
        gate.connection().getRealm().equals(original.get("realm"))
            && gate.connection().getNode().equals(original.get("node_id")),
        NetworkErrors.UNAUTHORIZED);
  }

  private static void fences(Map<String, Object> pending, Map<String, Object> lineage) {
    require(
        Objects.equals(pending.get("stop_version"), lineage.get("stop_version"))
            && Objects.equals(pending.get("permission_version"), lineage.get("permission_version")),
        REVOKED);
  }

  private static void validateRegister(
      RegisterAgentSourceRequest request, RegisterAgentSourceResponse response, String digest) {
    if (response.getDefinitivelyUncommitted()) {
      require(
          !response.hasSource()
              && request.getOperationId().equals(response.getOperationId())
              && digest.equals(response.getRequestDigest())
              && Set.of(
                      "original_connection_invalid_uncommitted",
                      "revision_mismatch",
                      "predecessor_not_head",
                      "lineage_stopped")
                  .contains(response.getRejectionReason()),
          UNKNOWN);
      return;
    }
    var source = response.getSource();
    require(
        response.hasSource()
            && source.getRevision() == 1
            && source.getState() == NetworkAgentSourceState.NETWORK_AGENT_SOURCE_STATE_ACTIVE
            && source.getRegistration().equals(request.getRegistration())
            && source.getOriginalConnection().equals(request.getConnection())
            && source.getScope() == request.getScope()
            && source.getPredecessorBinding().equals(request.getPredecessorBinding())
            && !source.getSourceBinding().equals(request.getPredecessorBinding())
            && source.getSourceBinding().matches("[A-Za-z0-9][A-Za-z0-9._:-]{0,127}")
            && !source.getOwnerUserId().isBlank()
            && instant(source.getExpiresAt()).isAfter(instant(source.getNotBefore())),
        UNKNOWN);
  }

  private static RegisterAgentSourceRequest frozenRegister(Map<String, Object> row) {
    try {
      var original = RegisterAgentSourceRequest.parseFrom((byte[]) row.get("iam_request"));
      String json = NetworkJson.canonical(requestJson(original));
      var business = NetworkJson.parse(row.get("business_json").toString());
      require(
          original.getOperationId().equals(row.get("operation_id"))
              && original.getConnection().getTenantId().equals(tenant().toString())
              && original.getConnection().getRealm().equals(row.get("realm"))
              && original.getConnection().getNode().equals(row.get("node_id"))
              && Arrays.equals(
                  original.getConnection().toByteArray(), (byte[]) row.get("original_connection"))
              && original.getRegistration().getRealm().equals(original.getConnection().getRealm())
              && original.getRegistration().getNode().equals(original.getConnection().getNode())
              && original.getScope()
                  == NetworkAgentSourceScope.NETWORK_AGENT_SOURCE_SCOPE_DURABLE_UNICAST_SEND
              && "durable_unicast_send".equals(text(business, "scope"))
              && original.getPredecessorBinding().equals(text(business, "predecessor_binding"))
              && Long.toUnsignedString(original.getExpectedRevision())
                  .equals(text(business, "expected_revision"))
              && json.equals(row.get("iam_request_json"))
              && NetworkJson.digest(NetworkJson.parse(json)).equals(row.get("request_digest")),
          UNKNOWN);
      for (var field : registrationJson(original.getRegistration()).properties())
        require(
            field.getValue().equals(business.path("registration").path(field.getKey())), UNKNOWN);
      return original;
    } catch (com.google.protobuf.InvalidProtocolBufferException e) {
      throw new AurigaException(UNKNOWN);
    }
  }

  private static RegisterAgentSourceResponse registerResponse(Object bytes) {
    try {
      return RegisterAgentSourceResponse.parseFrom((byte[]) bytes);
    } catch (com.google.protobuf.InvalidProtocolBufferException e) {
      throw new AurigaException(UNKNOWN);
    }
  }

  private void validateGet(
      SourceGate gate, NetworkAgentSource original, GetAgentSourceResponse response) {
    require(
        response.hasSource()
            && response.hasLineageStopped()
            && response.getConnection().equals(gate.connection())
            && response.getPolicyRevision() != 0,
        OVERLOADED);
    immutable(original, response.getSource());
    require(response.getSource().getRevision() != 0, OVERLOADED);
    Instant observed = instant(response.getObservedAt());
    require(
        !observed.isAfter(clock.instant()) && !observed.isBefore(clock.instant().minusSeconds(60)),
        EXPIRED);
    if (response.getSource().getState()
        == NetworkAgentSourceState.NETWORK_AGENT_SOURCE_STATE_ACTIVE) {
      require(!response.getLineageStopped(), OVERLOADED);
      require(
          instant(response.getValidUntil()).isAfter(clock.instant())
              && !instant(response.getSource().getNotBefore()).isAfter(clock.instant()),
          EXPIRED);
    } else {
      require(
          response.getValidUntil().isEmpty()
              && Set.of(
                      NetworkAgentSourceState.NETWORK_AGENT_SOURCE_STATE_EXPIRED,
                      NetworkAgentSourceState.NETWORK_AGENT_SOURCE_STATE_REVOKED)
                  .contains(response.getSource().getState()),
          OVERLOADED);
    }
  }

  private static void immutable(NetworkAgentSource original, NetworkAgentSource current) {
    require(
        original.toBuilder()
            .clearRevision()
            .clearState()
            .build()
            .equals(current.toBuilder().clearRevision().clearState().build()),
        OVERLOADED);
  }

  static ObjectNode requestJson(RegisterAgentSourceRequest request) {
    var result =
        NetworkJson.object()
            .put("operation_id", request.getOperationId())
            .put("scope", request.getScope().name())
            .put("expected_revision", Long.toUnsignedString(request.getExpectedRevision()))
            .put("predecessor_binding", request.getPredecessorBinding());
    result.set("connection", connectionJson(request.getConnection()));
    var registration =
        registrationJson(request.getRegistration())
            .put("realm", request.getRegistration().getRealm())
            .put("node", request.getRegistration().getNode());
    result.set("registration", registration);
    return result;
  }

  private static ObjectNode revokeRequestJson(RevokeAgentSourceRequest request) {
    var result =
        NetworkJson.object()
            .put("source_binding", request.getSourceBinding())
            .put("expected_revision", "0")
            .put("operation_id", request.getOperationId());
    result.set("connection", connectionJson(request.getConnection()));
    return result;
  }

  private static ObjectNode connectionJson(NetworkAuthorityConnection c) {
    return NetworkJson.object()
        .put("tenant_id", c.getTenantId())
        .put("realm", c.getRealm())
        .put("node", c.getNode())
        .put("ingress", c.getIngress())
        .put("server_id", c.getServerId())
        .put("client_id", c.getClientId())
        .put("client_account", c.getClientAccount());
  }

  private static ObjectNode registrationJson(NetworkAgentSourceRegistration r) {
    return NetworkJson.object()
        .put("registration_id", r.getRegistrationId())
        .put("allocation_op_id", r.getAllocationOpId())
        .put("allocation_fingerprint", r.getAllocationFingerprint())
        .put("registration_revision", Long.toUnsignedString(r.getRegistrationRevision()))
        .put("local", r.getLocal())
        .put("address_type", r.getAddressType());
  }

  private static ObjectNode business(JsonNode frame) {
    var result = NetworkJson.object();
    for (String field :
        List.of("registration", "scope", "expected_revision", "predecessor_binding"))
      result.set(field, frame.path(field));
    return result;
  }

  private static ObjectNode registerResult(
      String op, String result, ObjectNode source, ObjectNode failure) {
    var value = base("source_register_result").put("operation_id", op).put("result", result);
    value.set("source", source == null ? NetworkJson.JSON.nullNode() : source);
    value.set("failure_evidence", failure == null ? NetworkJson.JSON.nullNode() : failure);
    return value;
  }

  private static ObjectNode fact(
      NetworkAgentSource source, Map<String, Object> lineage, boolean stopped) {
    var result =
        NetworkJson.object()
            .put("source_binding", source.getSourceBinding())
            .put("scope", "durable_unicast_send")
            .put("revision", Long.toUnsignedString(source.getRevision()))
            .put(
                "state",
                source.getState() == NetworkAgentSourceState.NETWORK_AGENT_SOURCE_STATE_REVOKED
                    ? "revoked"
                    : stopped ? "stopped" : state(source))
            .put("not_before", source.getNotBefore())
            .put("expires_at", source.getExpiresAt())
            .put("predecessor_binding", source.getPredecessorBinding());
    result.set("registration", registrationJson(source.getRegistration()));
    result.set("native_binding", NetworkJson.parse(lineage.get("native_binding_json").toString()));
    return result;
  }

  private static String state(NetworkAgentSource source) {
    return switch (source.getState()) {
      case NETWORK_AGENT_SOURCE_STATE_ACTIVE -> "active";
      case NETWORK_AGENT_SOURCE_STATE_REVOKED -> "revoked";
      case NETWORK_AGENT_SOURCE_STATE_EXPIRED -> "expired";
      default -> throw new AurigaException(OVERLOADED);
    };
  }

  private static ObjectNode nativeProjection(JsonNode witness) {
    return NetworkJson.object()
        .put("binding_id", text(witness, "binding_id"))
        .put("binding_revision", text(witness, "binding_revision"));
  }

  private static NetworkAgentSource sourceBytes(Object bytes) {
    try {
      return NetworkAgentSource.parseFrom((byte[]) bytes);
    } catch (com.google.protobuf.InvalidProtocolBufferException e) {
      throw new AurigaException(UNKNOWN);
    }
  }

  private Map<String, Object> one(String sql, Object... args) {
    var rows = jdbc.queryForList(sql, args);
    return rows.isEmpty() ? null : rows.getFirst();
  }

  private static ObjectNode result(Map<String, Object> row) {
    return (ObjectNode) NetworkJson.parse(row.get("result_json").toString());
  }

  private static String decimal(Map<String, Object> row, String key) {
    return new BigDecimal(row.get(key).toString()).toBigIntegerExact().toString();
  }

  private static long unsigned(String value) {
    return Long.parseUnsignedLong(value);
  }

  private static String text(JsonNode node, String field) {
    return node.path(field).asString();
  }

  private static UUID tenant() {
    return TenantContext.require();
  }

  private static ObjectNode base(String type) {
    return NetworkJson.object().put("app_version", "1.0").put("type", type);
  }

  private static Instant instant(String value) {
    try {
      return Instant.parse(value);
    } catch (java.time.format.DateTimeParseException e) {
      throw new AurigaException(OVERLOADED);
    }
  }

  private static ErrorCode error(String name, int status) {
    return ErrorCode.of("network." + name, status, Status.Code.FAILED_PRECONDITION);
  }

  private static void require(boolean value, ErrorCode error) {
    if (!value) throw new AurigaException(error);
  }

  private void outside() {
    require(
        !TransactionSynchronizationManager.isActualTransactionActive()
            && !TransactionSynchronizationManager.isSynchronizationActive()
            && !TransactionSynchronizationManager.hasResource(jdbc.getDataSource()),
        OVERLOADED);
  }

  private <T> T transaction(org.springframework.transaction.support.TransactionCallback<T> work) {
    long remaining = auriga.collab.grpc.NetworkAuthorityClient.sourceRemainingMillis();
    require(remaining > 0, EXPIRED);
    var bounded = new TransactionTemplate(Objects.requireNonNull(tx.getTransactionManager()));
    bounded.setTimeout((int) ((remaining + 999) / 1000));
    return bounded.execute(work);
  }

  private void transactionWithoutResult(
      java.util.function.Consumer<org.springframework.transaction.TransactionStatus> work) {
    transaction(
        status -> {
          work.accept(status);
          return null;
        });
  }
}
