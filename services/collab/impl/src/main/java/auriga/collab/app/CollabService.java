package auriga.collab.app;

import auriga.collab.api.CollabAdmission;
import auriga.collab.api.CollabAdmission.Operation;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.persistence.TenantTransactionManager;
import auriga.common.persistence.UuidV7;
import auriga.common.security.AuthorizationRules;
import auriga.contracts.collab.v1.*;
import com.google.protobuf.Timestamp;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.sql.ResultSet;
import java.sql.SQLException;
import java.time.Instant;
import java.util.*;
import java.util.function.Supplier;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.transaction.support.TransactionTemplate;

/** Persistent communication only. All entry points repeat internal actor/admission checks. */
public class CollabService {
  private final JdbcTemplate jdbc;
  private final TransactionTemplate tx;
  private final CollabAdmission admission;

  public CollabService(
      JdbcTemplate jdbc, TenantTransactionManager manager, CollabAdmission admission) {
    this.jdbc = jdbc;
    this.tx = new TransactionTemplate(manager);
    this.admission = Objects.requireNonNull(admission);
  }

  public Endpoint registerEndpoint(RegisterEndpointRequest request) {
    Actor a = request.getActor();
    return run(
        a,
        Operation.REGISTER,
        () -> {
          text(request.getLabel(), 128);
          // Serialize the initial registration before a row exists. Hash collision only serializes.
          jdbc.queryForObject(
              "SELECT pg_advisory_xact_lock(hashtextextended(?,0)) IS NULL",
              Boolean.class,
              a.getTenantId() + ":" + a.getCredentialKind() + ":" + a.getCredentialSubject());
          var existing =
              rows(
                  "SELECT * FROM collab.endpoint WHERE tenant_id=? AND credential_kind=? AND credential_subject=? FOR UPDATE",
                  tenant(a),
                  a.getCredentialKind(),
                  uuid(a.getCredentialSubject()));
          UUID id;
          if (existing.isEmpty()) {
            id = UuidV7.next();
            jdbc.update(
                "INSERT INTO collab.endpoint(id,tenant_id,account_id,credential_kind,credential_subject,label,receive_enabled) VALUES (?,?,?,?,?,?,?)",
                id,
                tenant(a),
                uuid(a.getAccountId()),
                a.getCredentialKind(),
                uuid(a.getCredentialSubject()),
                request.getLabel(),
                request.getReceiveEnabled());
          } else {
            var row = existing.getFirst();
            require(row.get("account_id").equals(uuid(a.getAccountId())), CommonErrors.FORBIDDEN);
            require(!Boolean.TRUE.equals(row.get("revoked")), CommonErrors.FORBIDDEN);
            id = (UUID) row.get("id");
            jdbc.update(
                "UPDATE collab.endpoint SET label=?,receive_enabled=? WHERE tenant_id=? AND id=?",
                request.getLabel(),
                request.getReceiveEnabled(),
                tenant(a),
                id);
          }
          return endpoint(
              one("SELECT * FROM collab.endpoint WHERE tenant_id=? AND id=?", tenant(a), id));
        });
  }

  public SessionBinding bindSession(BindSessionRequest request) {
    Actor a = request.getActor();
    return run(
        a,
        Operation.BIND,
        () -> {
          actorEndpoint(a);
          text(a.getHostSessionId(), 512);
          UUID endpointId = uuid(a.getEndpointId());
          jdbc.update(
              "INSERT INTO collab.session_binding(id,tenant_id,endpoint_id,host_session_id) VALUES (?,?,?,?) ON CONFLICT (tenant_id,endpoint_id,host_session_id) DO NOTHING",
              UuidV7.next(),
              tenant(a),
              endpointId,
              a.getHostSessionId());
          var binding =
              one(
                  "SELECT * FROM collab.session_binding WHERE tenant_id=? AND endpoint_id=? AND host_session_id=?",
                  tenant(a),
                  endpointId,
                  a.getHostSessionId());
          if (!request.getThreadId().isEmpty()) {
            UUID thread = uuid(request.getThreadId());
            one(
                "SELECT id FROM collab.thread WHERE tenant_id=? AND id=? FOR UPDATE",
                tenant(a),
                thread);
            var participant =
                one(
                    "SELECT binding_id FROM collab.thread_participant WHERE tenant_id=? AND thread_id=? AND endpoint_id=?",
                    tenant(a),
                    thread,
                    endpointId);
            require(
                participant.get("binding_id") == null
                    || participant.get("binding_id").equals(binding.get("id")),
                CommonErrors.FORBIDDEN);
            jdbc.update(
                "UPDATE collab.thread_participant SET binding_id=? WHERE tenant_id=? AND thread_id=? AND endpoint_id=?",
                binding.get("id"),
                tenant(a),
                thread,
                endpointId);
          }
          return SessionBinding.newBuilder()
              .setId(binding.get("id").toString())
              .setEndpointId(a.getEndpointId())
              .setHostSessionId(a.getHostSessionId())
              .build();
        });
  }

  public EndpointPage listEndpoints(ListEndpointsRequest request) {
    Actor a = request.getActor();
    return run(
        a,
        Operation.DISCOVER,
        () -> {
          actorEndpoint(a);
          int limit = limit(request.getLimit());
          UUID after =
              request.getAfterEndpointId().isEmpty()
                  ? new UUID(0, 0)
                  : uuid(request.getAfterEndpointId());
          var found =
              rows(
                  "SELECT * FROM collab.endpoint WHERE tenant_id=? AND id>? AND NOT revoked AND receive_enabled ORDER BY id LIMIT ?",
                  tenant(a),
                  after,
                  limit + 1);
          var page = EndpointPage.newBuilder();
          for (var row : found.subList(0, Math.min(limit, found.size()))) {
            try {
              admission.verify(endpointActor(a, row), Operation.RECEIVE);
              page.addItems(endpoint(row));
            } catch (AurigaException denied) {
              if (!Set.of("common.forbidden", "common.not_found")
                  .contains(denied.errorCode().code())) throw denied;
            }
          }
          if (found.size() > limit)
            page.setNextEndpointId(found.get(limit - 1).get("id").toString());
          return page.build();
        });
  }

  public Message openThread(OpenThreadRequest request) {
    Actor a = request.getActor();
    return run(
        a,
        Operation.OPEN,
        () -> {
          UUID source = uuid(a.getEndpointId()), target = uuid(request.getTargetEndpointId());
          require(!source.equals(target), CommonErrors.VALIDATION_FAILED);
          // Reject an impersonated source before acquiring any recipient row lock.
          actorEndpoint(a, false, false);
          binding(a);
          lockEndpoints(a, List.of(source, target));
          actorEndpoint(a);
          binding(a);
          admission.verify(a, Operation.OPEN);
          require(
              Boolean.TRUE.equals(
                  one(
                          "SELECT receive_enabled FROM collab.endpoint WHERE tenant_id=? AND id=?",
                          tenant(a),
                          target)
                      .get("receive_enabled")),
              CommonErrors.FORBIDDEN);
          String fingerprint = fingerprint(request.getTargetEndpointId(), request.getText());
          Message previous = replay(a, "open", request.getIdempotencyKey(), fingerprint);
          if (previous != null) return previous;
          body(request.getText());
          UUID thread = UuidV7.next();
          jdbc.update("INSERT INTO collab.thread(id,tenant_id) VALUES (?,?)", thread, tenant(a));
          jdbc.update(
              "INSERT INTO collab.thread_participant(id,tenant_id,thread_id,endpoint_id,binding_id) VALUES (?,?,?,?,?)",
              UuidV7.next(),
              tenant(a),
              thread,
              source,
              uuid(a.getBindingId()));
          jdbc.update(
              "INSERT INTO collab.thread_participant(id,tenant_id,thread_id,endpoint_id) VALUES (?,?,?,?)",
              UuidV7.next(),
              tenant(a),
              thread,
              target);
          Message result = append(a, thread, request.getText());
          remember(a, "open", request.getIdempotencyKey(), fingerprint, result);
          return result;
        });
  }

  public Message replyThread(ReplyThreadRequest request) {
    Actor a = request.getActor();
    return run(
        a,
        Operation.REPLY,
        () -> {
          UUID thread = uuid(request.getThreadId());
          // Preflight must not lock the source first: that would invert the
          // globally sorted endpoint order for legitimate callers as well.
          actorEndpoint(a, false, false);
          participant(a, thread);
          var participants =
              rows(
                  "SELECT endpoint_id FROM collab.thread_participant WHERE tenant_id=? AND thread_id=?",
                  tenant(a),
                  thread);
          var endpointIds = new ArrayList<UUID>();
          endpointIds.add(uuid(a.getEndpointId()));
          participants.forEach(row -> endpointIds.add((UUID) row.get("endpoint_id")));
          lockEndpoints(a, endpointIds);
          actorEndpoint(a);
          participant(a, thread);
          admission.verify(a, Operation.REPLY);
          String fingerprint = fingerprint(request.getThreadId(), request.getText());
          Message previous = replay(a, "reply", request.getIdempotencyKey(), fingerprint);
          if (previous != null) return previous;
          body(request.getText());
          Message result = append(a, thread, request.getText());
          remember(a, "reply", request.getIdempotencyKey(), fingerprint, result);
          return result;
        });
  }

  public MessagePage readThread(ReadThreadRequest request) {
    Actor a = request.getActor();
    return run(
        a,
        Operation.READ,
        () -> {
          actorEndpoint(a);
          UUID thread = uuid(request.getThreadId());
          participant(a, thread);
          require(
              request.getAfterSequence() >= 0 && request.getAfterSequence() <= 9007199254740991L,
              CommonErrors.VALIDATION_FAILED);
          return MessagePage.newBuilder()
              .addAllItems(
                  jdbc.query(
                      "SELECT * FROM collab.message WHERE tenant_id=? AND thread_id=? AND sequence>? ORDER BY sequence LIMIT ?",
                      CollabService::message,
                      tenant(a),
                      thread,
                      request.getAfterSequence(),
                      limit(request.getLimit())))
              .build();
        });
  }

  public DeliveryPage pullDeliveries(PullDeliveriesRequest request) {
    Actor a = request.getActor();
    return run(
        a,
        Operation.PULL,
        () -> {
          var endpoint = actorEndpoint(a);
          require(Boolean.TRUE.equals(endpoint.get("receive_enabled")), CommonErrors.FORBIDDEN);
          // The endpoint row serializes pull/ack/revocation; never skip a locked predecessor.
          var candidates =
              rows(
                  "SELECT d.id FROM collab.delivery d JOIN collab.message m ON m.tenant_id=d.tenant_id AND m.id=d.message_id WHERE d.tenant_id=? AND d.target_endpoint_id=? AND d.status<>'host_accepted' AND (d.status='pending' OR d.lease_expires_at<=clock_timestamp()) AND NOT EXISTS (SELECT 1 FROM collab.delivery prior JOIN collab.message pm ON pm.tenant_id=prior.tenant_id AND pm.id=prior.message_id WHERE prior.tenant_id=d.tenant_id AND prior.thread_id=d.thread_id AND prior.target_endpoint_id=d.target_endpoint_id AND prior.status<>'host_accepted' AND pm.sequence<m.sequence) ORDER BY m.created_at,m.id LIMIT ?",
                  tenant(a),
                  uuid(a.getEndpointId()),
                  limit(request.getLimit()));
          var result = DeliveryPage.newBuilder();
          for (var candidate : candidates) {
            UUID id = (UUID) candidate.get("id");
            jdbc.update(
                "UPDATE collab.delivery SET status='leased',lease_token=?,lease_expires_at=clock_timestamp()+interval '30 seconds' WHERE tenant_id=? AND id=?",
                UuidV7.next(),
                tenant(a),
                id);
            result.addItems(delivery(a, id));
          }
          return result.build();
        });
  }

  public Delivery ackDelivery(AckDeliveryRequest request) {
    Actor a = request.getActor();
    return run(
        a,
        Operation.ACK,
        () -> {
          actorEndpoint(a);
          UUID id = uuid(request.getDeliveryId());
          var row =
              one(
                  "SELECT * FROM collab.delivery WHERE tenant_id=? AND id=? AND target_endpoint_id=? FOR UPDATE",
                  tenant(a),
                  id,
                  uuid(a.getEndpointId()));
          participant(a, (UUID) row.get("thread_id"));
          text(request.getHostRequestId(), 128);
          require(
              uuid(request.getLeaseToken()).equals(row.get("lease_token")),
              CommonErrors.PRECONDITION_FAILED);
          // DSH deduplicates by requestId within the session. A delivery's
          // server-generated identity is its one stable prompt requestId.
          require(request.getHostRequestId().equals(id.toString()), CommonErrors.CONFLICT);
          if ("host_accepted".equals(row.get("status"))) {
            require(
                request.getHostRequestId().equals(row.get("host_request_id")),
                CommonErrors.CONFLICT);
          } else {
            require("leased".equals(row.get("status")), CommonErrors.PRECONDITION_FAILED);
            int changed =
                jdbc.update(
                    "UPDATE collab.delivery SET status='host_accepted',host_request_id=? WHERE tenant_id=? AND id=? AND lease_expires_at>clock_timestamp()",
                    request.getHostRequestId(),
                    tenant(a),
                    id);
            require(changed == 1, CommonErrors.PRECONDITION_FAILED);
          }
          return delivery(a, id);
        });
  }

  public Endpoint revokeEndpoint(RevokeEndpointRequest request) {
    Actor a = request.getActor();
    return run(
        a,
        Operation.REVOKE,
        () -> {
          var row = actorEndpoint(a, true);
          jdbc.update(
              "UPDATE collab.endpoint SET revoked=true WHERE tenant_id=? AND id=?",
              tenant(a),
              uuid(a.getEndpointId()));
          row.put("revoked", true);
          return endpoint(row);
        });
  }

  private <T> T run(Actor a, Operation operation, Supplier<T> work) {
    var caller =
        AuthorizationRules.currentPrincipal()
            .orElseThrow(() -> new AurigaException(CommonErrors.UNAUTHENTICATED));
    require(
        caller.isInternalService()
            && "svc:ai-gateway".equals(caller.subject())
            && TenantContext.PLATFORM_TENANT.equals(caller.tenantId()),
        CommonErrors.FORBIDDEN);
    UUID tenant = tenant(a);
    require(
        TenantContext.current().filter(tenant::equals).isPresent()
            && !TenantContext.PLATFORM_TENANT.equals(tenant),
        CommonErrors.FORBIDDEN);
    uuid(a.getAccountId());
    uuid(a.getCredentialSubject());
    require(
        Set.of("DEVICE", "INSTANCE").contains(a.getCredentialKind()),
        CommonErrors.VALIDATION_FAILED);
    return tx.execute(
        status -> {
          TenantTransactionManager.verifyTransactionTenant();
          admission.verify(a, operation);
          return work.get();
        });
  }

  private Map<String, Object> actorEndpoint(Actor a) {
    return actorEndpoint(a, false);
  }

  private Map<String, Object> actorEndpoint(Actor a, boolean allowRevoked) {
    return actorEndpoint(a, allowRevoked, true);
  }

  private Map<String, Object> actorEndpoint(Actor a, boolean allowRevoked, boolean lock) {
    var row =
        one(
            "SELECT * FROM collab.endpoint WHERE tenant_id=? AND id=?"
                + (lock ? " FOR UPDATE" : ""),
            tenant(a),
            uuid(a.getEndpointId()));
    require(
        row.get("account_id").equals(uuid(a.getAccountId()))
            && row.get("credential_kind").equals(a.getCredentialKind())
            && row.get("credential_subject").equals(uuid(a.getCredentialSubject())),
        CommonErrors.FORBIDDEN);
    require(allowRevoked || !Boolean.TRUE.equals(row.get("revoked")), CommonErrors.FORBIDDEN);
    return row;
  }

  private void lockEndpoints(Actor a, List<UUID> ids) {
    for (UUID id : ids.stream().distinct().sorted().toList()) {
      var row =
          one("SELECT * FROM collab.endpoint WHERE tenant_id=? AND id=? FOR UPDATE", tenant(a), id);
      require(!Boolean.TRUE.equals(row.get("revoked")), CommonErrors.FORBIDDEN);
      if (!id.toString().equals(a.getEndpointId())) {
        require(Boolean.TRUE.equals(row.get("receive_enabled")), CommonErrors.FORBIDDEN);
        admission.verify(endpointActor(a, row), Operation.RECEIVE);
      }
    }
  }

  private static Actor endpointActor(Actor caller, Map<String, Object> row) {
    return Actor.newBuilder()
        .setTenantId(caller.getTenantId())
        .setAccountId(row.get("account_id").toString())
        .setCredentialKind(row.get("credential_kind").toString())
        .setCredentialSubject(row.get("credential_subject").toString())
        .setEndpointId(row.get("id").toString())
        .build();
  }

  private void binding(Actor a) {
    var row =
        one(
            "SELECT host_session_id FROM collab.session_binding WHERE tenant_id=? AND id=? AND endpoint_id=?",
            tenant(a),
            uuid(a.getBindingId()),
            uuid(a.getEndpointId()));
    require(row.get("host_session_id").equals(a.getHostSessionId()), CommonErrors.FORBIDDEN);
  }

  private void participant(Actor a, UUID thread) {
    binding(a);
    var row =
        one(
            "SELECT binding_id FROM collab.thread_participant WHERE tenant_id=? AND thread_id=? AND endpoint_id=?",
            tenant(a),
            thread,
            uuid(a.getEndpointId()));
    require(uuid(a.getBindingId()).equals(row.get("binding_id")), CommonErrors.FORBIDDEN);
  }

  private Message append(Actor a, UUID thread, String text) {
    one("SELECT id FROM collab.thread WHERE tenant_id=? AND id=? FOR UPDATE", tenant(a), thread);
    long sequence =
        jdbc.queryForObject(
            "UPDATE collab.thread SET next_sequence=next_sequence+1 WHERE tenant_id=? AND id=? RETURNING next_sequence-1",
            Long.class,
            tenant(a),
            thread);
    UUID id = UuidV7.next();
    jdbc.update(
        "INSERT INTO collab.message(id,tenant_id,thread_id,sequence,author_endpoint_id,author_binding_id,text) VALUES (?,?,?,?,?,?,?)",
        id,
        tenant(a),
        thread,
        sequence,
        uuid(a.getEndpointId()),
        uuid(a.getBindingId()),
        text);
    for (var p :
        rows(
            "SELECT endpoint_id FROM collab.thread_participant WHERE tenant_id=? AND thread_id=? AND endpoint_id<>?",
            tenant(a),
            thread,
            uuid(a.getEndpointId()))) {
      jdbc.update(
          "INSERT INTO collab.delivery(id,tenant_id,message_id,thread_id,target_endpoint_id) VALUES (?,?,?,?,?)",
          UuidV7.next(),
          tenant(a),
          id,
          thread,
          p.get("endpoint_id"));
    }
    return loadMessage(a, id);
  }

  private Message replay(Actor a, String operation, String key, String fingerprint) {
    text(key, 128);
    var found =
        rows(
            "SELECT fingerprint,message_id FROM collab.request_dedup WHERE tenant_id=? AND endpoint_id=? AND binding_id=? AND operation=? AND key=?",
            tenant(a),
            uuid(a.getEndpointId()),
            uuid(a.getBindingId()),
            operation,
            key);
    if (found.isEmpty()) return null;
    require(fingerprint.equals(found.getFirst().get("fingerprint")), CommonErrors.CONFLICT);
    return loadMessage(a, (UUID) found.getFirst().get("message_id"));
  }

  private void remember(
      Actor a, String operation, String key, String fingerprint, Message message) {
    jdbc.update(
        "INSERT INTO collab.request_dedup(id,tenant_id,endpoint_id,binding_id,operation,key,fingerprint,message_id) VALUES (?,?,?,?,?,?,?,?)",
        UuidV7.next(),
        tenant(a),
        uuid(a.getEndpointId()),
        uuid(a.getBindingId()),
        operation,
        key,
        fingerprint,
        uuid(message.getId()));
  }

  private Message loadMessage(Actor a, UUID id) {
    var found =
        jdbc.query(
            "SELECT * FROM collab.message WHERE tenant_id=? AND id=?",
            CollabService::message,
            tenant(a),
            id);
    require(found.size() == 1, CommonErrors.NOT_FOUND);
    return found.getFirst();
  }

  private Delivery delivery(Actor a, UUID id) {
    var r = one("SELECT * FROM collab.delivery WHERE tenant_id=? AND id=?", tenant(a), id);
    var b =
        Delivery.newBuilder()
            .setId(id.toString())
            .setTargetEndpointId(r.get("target_endpoint_id").toString())
            .setStatus(r.get("status").toString())
            .setMessage(loadMessage(a, (UUID) r.get("message_id")));
    if (r.get("lease_token") != null) b.setLeaseToken(r.get("lease_token").toString());
    if (r.get("lease_expires_at") != null)
      b.setLeaseExpiresAt(timestamp(((java.sql.Timestamp) r.get("lease_expires_at")).toInstant()));
    return b.build();
  }

  private static Message message(ResultSet r, int n) throws SQLException {
    return Message.newBuilder()
        .setId(r.getString("id"))
        .setThreadId(r.getString("thread_id"))
        .setSequence(r.getLong("sequence"))
        .setAuthorEndpointId(r.getString("author_endpoint_id"))
        .setText(r.getString("text"))
        .setCreatedAt(timestamp(r.getTimestamp("created_at").toInstant()))
        .build();
  }

  private static Timestamp timestamp(Instant i) {
    return Timestamp.newBuilder().setSeconds(i.getEpochSecond()).setNanos(i.getNano()).build();
  }

  private static Endpoint endpoint(Map<String, Object> r) {
    return Endpoint.newBuilder()
        .setId(r.get("id").toString())
        .setAccountId(r.get("account_id").toString())
        .setLabel(r.get("label").toString())
        .setReceiveEnabled((Boolean) r.get("receive_enabled"))
        .setRevoked((Boolean) r.get("revoked"))
        .build();
  }

  private List<Map<String, Object>> rows(String sql, Object... args) {
    return jdbc.queryForList(sql, args);
  }

  private Map<String, Object> one(String sql, Object... args) {
    var r = rows(sql, args);
    require(r.size() == 1, CommonErrors.NOT_FOUND);
    return r.getFirst();
  }

  private static UUID tenant(Actor a) {
    return uuid(a.getTenantId());
  }

  private static UUID uuid(String s) {
    try {
      UUID id = UUID.fromString(s);
      if (!id.toString().equals(s)) throw new IllegalArgumentException();
      return id;
    } catch (IllegalArgumentException e) {
      throw new AurigaException(CommonErrors.VALIDATION_FAILED);
    }
  }

  private static void text(String s, int max) {
    require(!s.isBlank() && s.length() <= max, CommonErrors.VALIDATION_FAILED);
  }

  private static void body(String s) {
    require(
        !s.isBlank() && s.getBytes(StandardCharsets.UTF_8).length <= 16384,
        CommonErrors.VALIDATION_FAILED);
  }

  private static int limit(int n) {
    require(n >= 1 && n <= 100, CommonErrors.VALIDATION_FAILED);
    return n;
  }

  private static String fingerprint(String target, String body) {
    try {
      MessageDigest d = MessageDigest.getInstance("SHA-256");
      d.update(target.getBytes(StandardCharsets.UTF_8));
      d.update((byte) 0);
      return HexFormat.of().formatHex(d.digest(body.getBytes(StandardCharsets.UTF_8)));
    } catch (NoSuchAlgorithmException e) {
      throw new IllegalStateException(e);
    }
  }

  private static void require(boolean ok, auriga.common.error.ErrorCode error) {
    if (!ok) throw new AurigaException(error);
  }
}
