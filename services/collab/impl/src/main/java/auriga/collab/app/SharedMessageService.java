package auriga.collab.app;

import auriga.collab.grpc.*;
import auriga.collab.network.keys.MsgnetRenewal;
import auriga.collab.network.local.*;
import auriga.common.context.TenantContext;
import auriga.common.error.*;
import auriga.common.persistence.TenantTransactionManager;
import auriga.contracts.collab.v1.CallSharedMessageRequest;
import auriga.contracts.iam.v1.*;
import auriga.contracts.node.v1.*;
import java.time.*;
import java.time.temporal.ChronoUnit;
import java.util.*;
import java.util.function.Supplier;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.transaction.support.TransactionTemplate;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

/** Shared HTTP adaptation over the existing typed-local, mailbox, attempts and receipt engines. */
public final class SharedMessageService {
  private final SharedMessageIamClient iam;
  private final SharedMessageSource source;
  private final NetworkMailboxService mailbox;
  private final NetworkMailboxWireService wire;
  private final LocalRegistry locals;
  private final JdbcTemplate jdbc;
  private final TransactionTemplate tx;
  private final NetworkEnvelopeSigner signer;
  private final Supplier<MsgnetRenewal> renewal;
  private final Clock clock;

  public SharedMessageService(
      SharedMessageIamClient iam,
      SharedMessageSource source,
      NetworkMailboxService mailbox,
      NetworkMailboxWireService wire,
      LocalRegistry locals,
      JdbcTemplate jdbc,
      TenantTransactionManager manager,
      NetworkSigningKeys keys,
      Supplier<MsgnetRenewal> renewal,
      Clock clock) {
    this.iam = iam;
    this.source = source;
    this.mailbox = mailbox;
    this.wire = wire;
    this.locals = locals;
    this.jdbc = jdbc;
    this.tx = new TransactionTemplate(manager);
    this.signer = new NetworkEnvelopeSigner(keys, clock);
    this.renewal = renewal;
    this.clock = clock;
  }

  public String call(CallSharedMessageRequest request) {
    var body = NetworkJson.parse(request.getRequestJson().toByteArray());
    var cap = request.getCapability();
    var binding = cap.getBinding();
    String op = request.getOperation();
    require(Set.of("context", "send", "receive").contains(op));
    require(
        cap.hasBinding()
            && "auriga.shared.message".equals(cap.getAudience())
            && TenantContext.require().toString().equals(binding.getTenantId())
            && binding.getTenantId().equals(text(body, "tenantId"))
            && binding.getSessionId().equals(text(body, "sessionId"))
            && cap.getTaskId().equals(text(body, "taskId")));
    String scope = "receive".equals(op) ? "message.receive" : "message.send";
    if ("context".equals(op))
      require(
          cap.getScopesList().stream()
              .anyMatch(s -> Set.of("message.send", "message.receive").contains(s)));
    else require(cap.getScopesList().contains(scope));
    var witness = body.path("nativeWitness");
    Instant until = witness(witness);
    UUID.fromString(request.getAuthenticatedNodeId());
    UUID.fromString(text(body, "bootId"));
    String id = "context".equals(op) ? text(body, "operationId") : text(body, "contextId");
    UUID.fromString(id);
    var context =
        iam.open(
            OpenSharedMessageContextRequest.newBuilder()
                .setCapability(cap)
                .setAuthenticatedNodeId(request.getAuthenticatedNodeId())
                .setOperationId(id)
                .setBootId(text(body, "bootId"))
                .setNativeBindingId(text(witness, "binding_id"))
                .setNativeBindingRevision(text(witness, "binding_revision"))
                .build());
    require(context.getBinding().equals(binding));
    until = Collections.min(List.of(until, Instant.parse(context.getExpiresAt())));
    var connection = connection(context);
    var target = target(context, text(witness, "witness_id"), until);
    Instant deadline = until;
    String result =
        source.withNative(
            context,
            deadline,
            () ->
                source.withTarget(
                    connection,
                    target,
                    () -> {
                      var registration = initialize(context, connection);
                      return switch (op) {
                        case "context" ->
                            contextResult(context, cap.getTaskId(), target, witness, registration);
                        case "send" -> send(context, body);
                        case "receive" -> receive(connection, body);
                        default -> throw new AurigaException(CommonErrors.VALIDATION_FAILED);
                      };
                    }));
    // No late successful response after cancellation or K ownership/version revocation.
    if (io.grpc.Context.current().isCancelled())
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    iam.open(
        OpenSharedMessageContextRequest.newBuilder()
            .setCapability(cap)
            .setAuthenticatedNodeId(request.getAuthenticatedNodeId())
            .setOperationId(id)
            .setBootId(context.getBootId())
            .setNativeBindingId(context.getNativeBindingId())
            .setNativeBindingRevision(context.getNativeBindingRevision())
            .build());
    require(deadline.isAfter(clock.instant()));
    return result;
  }

  private LocalRegistry.Registration initialize(
      SharedMessageContext c, NetworkAuthorityConnection connection) {
    var ready = NetworkJson.object();
    var features = ready.putArray("features");
    var capabilities = ready.putArray("adapter_capabilities");
    NetworkWireCapabilities.BASE.forEach(
        value -> {
          features.add(value);
          capabilities.add(value);
        });
    tx.executeWithoutResult(
        s -> {
          jdbc.update(
              "INSERT INTO collab.network_node(tenant_id,realm,node_id) VALUES (?,?,?) ON CONFLICT DO NOTHING",
              TenantContext.require(),
              c.getRealm(),
              c.getNode());
          var row =
              jdbc.queryForMap(
                  "SELECT * FROM collab.network_node WHERE tenant_id=? AND realm=? AND node_id=? FOR UPDATE",
                  TenantContext.require(),
                  c.getRealm(),
                  c.getNode());
          String association = NetworkMailboxWireService.bindingJson(connection);
          if (row.get("wire_binding_json") == null) {
            require(
                ((Number) row.get("connection_epoch")).longValue() == 0
                    && ((java.math.BigDecimal) row.get("local_high_water")).signum() == 0);
            jdbc.update(
                """
            UPDATE collab.network_node SET connection_epoch=1,wire_binding_json=?,wire_ready_json=?,
              wire_incarnation=?,wire_nonce=?,wire_status='ready',local_boot_epoch=1,local_boot_id=?
            WHERE tenant_id=? AND realm=? AND node_id=?
            """,
                association,
                NetworkJson.canonical(ready),
                c.getIncarnation(),
                c.getChannelNonce(),
                c.getBootId(),
                TenantContext.require(),
                c.getRealm(),
                c.getNode());
          } else
            require(
                association.equals(row.get("wire_binding_json"))
                    && c.getBootId().equals(row.get("local_boot_id"))
                    && "ready".equals(row.get("wire_status")));
        });
    var register =
        NetworkJson.object()
            .put("app_version", "1.0")
            .put("type", "local_register")
            .put("request_id", c.getOperationId())
            .put("epoch", "1")
            .put("boot_id", c.getBootId())
            .put("candidate_local", "1")
            .put("allocation_op_id", c.getOperationId())
            .put(
                "allocation_fingerprint",
                NetworkJson.digest(
                    NetworkJson.object()
                        .put("context", c.getContextId())
                        .put("native", c.getNativeBindingId())
                        .put("revision", c.getNativeBindingRevision())))
            .put("expected_registration_revision", "0")
            .put("address_type", "session");
    // The actual collab registry allocates, persists, and checks its high-water mark.
    return locals.register(TenantContext.require(), NetworkJson.canonical(register));
  }

  private String contextResult(
      SharedMessageContext c,
      String task,
      auriga.collab.api.NetworkSourceAuthority.Target target,
      JsonNode witness,
      LocalRegistry.Registration r) {
    var registration = registration(r);
    var fact =
        NetworkJson.object()
            .put("source_binding", c.getSourceBinding())
            .put("scope", "durable_unicast_send")
            .put("revision", Long.toUnsignedString(c.getSourceRevision()))
            .put("state", "active")
            .put("not_before", c.getNotBefore())
            .put("expires_at", c.getExpiresAt())
            .put("predecessor_binding", "");
    fact.set("registration", registration.deepCopy());
    fact.putObject("native_binding")
        .put("binding_id", c.getNativeBindingId())
        .put("binding_revision", c.getNativeBindingRevision());
    var get =
        NetworkJson.object()
            .put("app_version", "1.0")
            .put("type", "source_get_result")
            .put("request_id", target.requestId())
            .put("policy_revision", Long.toUnsignedString(c.getPolicyRevision()))
            .put("observed_at", clock.instant().truncatedTo(ChronoUnit.SECONDS).toString())
            .put("witness_id", text(witness, "witness_id"))
            .put("valid_until", target.validUntil().toString());
    get.set("source", fact);
    var signed =
        signer.sign(
            get,
            "protocol_response",
            new NetworkEnvelopeSigner.Context(
                c.getRealm(),
                c.getNode(),
                c.getIncarnation(),
                1,
                c.getChannelNonce(),
                target.requestId()),
            target.validUntil());
    NetworkSchemas.validate("source-control", "SourceGetResult", signed);
    var result = NetworkJson.object().put("taskId", task).put("contextId", c.getContextId());
    result.set("binding", binding(c.getBinding()));
    result
        .putObject("connection")
        .put("transport", "shared_node_http")
        .put("realm", c.getRealm())
        .put("node", c.getNode())
        .put("incarnation", c.getIncarnation())
        .put("epoch", "1")
        .put("bootId", c.getBootId())
        .put("channelNonce", c.getChannelNonce())
        .put("requestId", target.requestId())
        .put("validUntil", target.validUntil().toString());
    registration.put("realm", c.getRealm()).put("node", c.getNode());
    result.set("registration", registration);
    result.set("sourceGet", signed);
    var row =
        tx.execute(
            s ->
                jdbc.queryForMap(
                    """
                        SELECT n.producer_epoch,n.head_seq,n.committed_cursor,l.receive_enabled,l.receive_version
                        FROM collab.network_node n JOIN collab.network_local_registration l
                          ON l.tenant_id=n.tenant_id AND l.realm=n.realm AND l.node_id=n.node_id
                        WHERE n.tenant_id=? AND n.realm=? AND n.node_id=? AND l.local_id=1
                          AND l.status='registered' AND l.address_type='session'
                        """,
                    TenantContext.require(),
                    c.getRealm(),
                    c.getNode()));
    result.put("producerEpoch", row.get("producer_epoch").toString());
    result
        .putObject("mailbox")
        .put("receiveEnabled", Boolean.TRUE.equals(row.get("receive_enabled")))
        .put("receiveVersion", row.get("receive_version").toString())
        .put("headSeq", row.get("head_seq").toString())
        .put("committedCursor", row.get("committed_cursor").toString());
    // Only already committed official public materials. A missing/expired chain fails this call,
    // while the installed handler and independent model service remain available.
    var publicState = renewal.get();
    var trust = result.putObject("messageTrust");
    trust.set("control", NetworkJson.parse(publicState.publicControl(), 1048576));
    trust.set("publication", NetworkJson.parse(publicState.publication(), 1048576));
    trust.set("announcement", NetworkJson.parse(publicState.announcement(), 1048576));
    return NetworkJson.canonical(result);
  }

  private String send(SharedMessageContext c, JsonNode body) {
    UUID messageId = UUID.fromString(text(body, "messageId"));
    var to = body.path("target");
    require(c.getRealm().equals(text(to, "realm")) && "1".equals(text(to, "local")));
    var fields = NetworkJson.object().put("intent", "request").put("ttl", 3600);
    fields.set("body", body.path("body"));
    var request =
        NetworkMailboxService.StoreRequest.unicast(
            TenantContext.require(),
            c.getRealm(),
            c.getNode(),
            "1",
            NetworkJson.canonical(body.path("body")),
            text(to, "node"),
            text(to, "local"));
    var stored =
        mailbox.storeShared(
            request, NetworkJson.canonical(fields), UUID.fromString(c.getContextId()), messageId);
    var message =
        tx.execute(
            s ->
                NetworkJson.parse(
                    jdbc.queryForObject(
                        "SELECT message_json FROM collab.network_message WHERE tenant_id=? AND id=?",
                        String.class,
                        TenantContext.require(),
                        stored.messageId())));
    var result =
        NetworkJson.object()
            .put("status", "platform_stored")
            .put("message_id", stored.messageId().toString())
            .put("thread_id", message.path("thread_id").asString())
            .put("message_class", "unicast")
            .put("dispatch_status", "pending")
            .put("expires_at", message.path("expires_at").asString());
    result.set("from", message.path("from"));
    result.set("to", message.path("to"));
    NetworkSchemas.validate("send", "SendResult", result);
    return NetworkJson.canonical(result);
  }

  private String receive(NetworkAuthorityConnection c, JsonNode body) {
    byte[] frame =
        NetworkJson.canonical(body.path("frame")).getBytes(java.nio.charset.StandardCharsets.UTF_8);
    return switch (text(body, "operation")) {
      case "receiveSet" -> wire.receiveSet(c, frame);
      case "fetch" -> wire.fetch(c, frame);
      case "attemptBegin" -> wire.attemptBegin(c, frame);
      case "executionAdmit" -> wire.executionAdmit(c, frame);
      case "receipt" -> wire.receipt(c, frame);
      default -> throw new AurigaException(CommonErrors.VALIDATION_FAILED);
    };
  }

  static NetworkAuthorityConnection connection(SharedMessageContext c) {
    return NetworkAuthorityConnection.newBuilder()
        .setTenantId(c.getBinding().getTenantId())
        .setRealm(c.getRealm())
        .setNode(c.getNode())
        .setSharedContextId(c.getContextId())
        .build();
  }

  private static auriga.collab.api.NetworkSourceAuthority.Target target(
      SharedMessageContext c, String request, Instant until) {
    return new auriga.collab.api.NetworkSourceAuthority.Target(
        c.getRealm(), c.getNode(), c.getIncarnation(), 1, c.getChannelNonce(), request, until);
  }

  private Instant witness(JsonNode w) {
    require(w.isObject() && w.size() == 5);
    UUID.fromString(text(w, "witness_id"));
    UUID.fromString(text(w, "binding_id"));
    String revision = text(w, "binding_revision");
    require(
        revision.matches("[1-9][0-9]{0,19}")
            && new java.math.BigInteger(revision).bitLength() <= 64);
    String observed = text(w, "observed_at"), expiry = text(w, "valid_until");
    require(
        observed.matches("[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z")
            && expiry.matches("[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z"));
    Instant start = Instant.parse(observed), end = Instant.parse(expiry), now = clock.instant();
    require(
        !start.isAfter(now.plusSeconds(2))
            && end.isAfter(now)
            && end.isAfter(start)
            && !end.isAfter(start.plusSeconds(30)));
    return end;
  }

  private static ObjectNode registration(LocalRegistry.Registration r) {
    require(r.registrationId() != null && "session".equals(r.addressType()));
    return NetworkJson.object()
        .put("registration_id", r.registrationId().toString())
        .put("allocation_op_id", r.allocationOpId())
        .put("allocation_fingerprint", r.allocationFingerprint())
        .put("registration_revision", r.registrationRevision())
        .put("local", r.local())
        .put("address_type", r.addressType());
  }

  private static ObjectNode binding(SharedDshSessionBinding b) {
    return NetworkJson.object()
        .put("tenantId", b.getTenantId())
        .put("memberId", b.getMemberId())
        .put("placementId", b.getPlacementId())
        .put("physicalInstanceId", b.getPhysicalInstanceId())
        .put(
            "mode",
            b.getMode() == DshInstanceMode.DSH_INSTANCE_MODE_SHARED ? "shared" : "dedicated")
        .put("epoch", b.getEpoch())
        .put("revision", b.getRevision())
        .put("sessionId", b.getSessionId())
        .put("platformSessionId", b.getPlatformSessionId())
        .put("authGeneration", b.getAuthGeneration());
  }

  private static String text(JsonNode node, String field) {
    require(node.path(field).isString());
    return node.path(field).asString();
  }

  private static void require(boolean value) {
    if (!value) throw new AurigaException(NetworkErrors.UNAUTHORIZED);
  }
}
