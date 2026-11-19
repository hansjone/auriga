package auriga.iam;

import static auriga.iam.SharedTaskTokens.*;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.contracts.iam.v1.*;
import java.time.*;
import java.time.temporal.ChronoUnit;
import java.util.*;
import tools.jackson.databind.node.ObjectNode;

/** Stable member/native identity; every operation still rechecks current K/member authority. */
final class SharedMessages {
  private final IamStore store;
  private final SharedTasks tasks;
  private final IamNetworkAuthority network;
  private final IamNetworkPolicyRevision policy;
  private final Clock clock;

  SharedMessages(IamStore store, SharedTasks tasks, IamNetworkAuthority network, Clock clock) {
    this.store = store;
    this.tasks = tasks;
    this.network = network;
    this.policy = new IamNetworkPolicyRevision(store);
    this.clock = clock;
  }

  SharedMessageContext open(OpenSharedMessageContextRequest r) {
    validate(r.getCapability());
    uuid(r.getAuthenticatedNodeId());
    uuid(r.getOperationId());
    uuid(r.getBootId());
    uuid(r.getNativeBindingId());
    if (!IamNetworkAuthority.validNumber(r.getNativeBindingRevision(), false)
        || !MESSAGE.equals(r.getCapability().getAudience())
        || !tasks
            .authorize(
                AuthorizeSharedCapabilityRequest.newBuilder()
                    .setCapability(r.getCapability())
                    .build())
            .getActive()) denied();
    var binding = r.getCapability().getBinding();
    var expiry = tasks.messageBinding(binding, r.getCapability().getScopes(0));
    var result =
        store.tx(
            () -> {
              // Same allocator lock as ordinary devices: no duplicate addresses across identity
              // classes.
              store.lock("network-authority");
              store.lock("shared-message:" + r.getOperationId());
              var saved = store.get("shared_message_context", r.getOperationId(), true);
              if (saved != null) {
                if (saved.path("revoked").asBoolean()) denied();
                var context = decode(saved);
                if (!context.getBinding().equals(binding)
                    || !context.getAuthenticatedNodeId().equals(r.getAuthenticatedNodeId())
                    || !context.getBootId().equals(r.getBootId())
                    || !context.getNativeBindingId().equals(r.getNativeBindingId())
                    || !context.getNativeBindingRevision().equals(r.getNativeBindingRevision()))
                  denied();
                return context;
              }
              var jdbc = store.networkJdbc();
              String nativeKey = nativeKey(r);
              if (!jdbc.queryForList(
                      "SELECT id FROM iam.shared_message_context WHERE tenant_id=? AND native_key=?",
                      TenantContext.require(),
                      nativeKey)
                  .isEmpty()) throw new AurigaException(CommonErrors.CONFLICT);
              String sessionKey = Secrets.hash(bindingJson(binding).toString());
              // A new trusted native incarnation permanently fences retained old operations.
              jdbc.update(
                  "UPDATE iam.shared_message_context SET data=jsonb_set(data,'{revoked}','true'::jsonb) WHERE tenant_id=? AND session_key=? AND data->>'revoked'='false'",
                  TenantContext.require(),
                  sessionKey);
              jdbc.update(
                  """
          INSERT INTO iam.network_realm(tenant_id,realm)
          SELECT ?,nextval('iam.network_realm_number')::numeric+9223372036854775809
          WHERE NOT EXISTS(SELECT 1 FROM iam.network_realm WHERE tenant_id=?)
          ON CONFLICT(tenant_id) DO NOTHING
          """,
                  TenantContext.require(),
                  TenantContext.require());
              var nodes =
                  jdbc.query(
                      """
          UPDATE iam.network_realm SET next_node=next_node+1
          WHERE tenant_id=? AND next_node<=18446744073709551615
          RETURNING realm::text,(next_node-1)::text
          """,
                      (rs, i) -> List.of(rs.getString(1), rs.getString(2)),
                      TenantContext.require());
              if (nodes.size() != 1) throw new AurigaException(CommonErrors.UNAVAILABLE);
              var context =
                  SharedMessageContext.newBuilder()
                      .setBinding(binding)
                      .setContextId(r.getOperationId())
                      .setOperationId(r.getOperationId())
                      .setAuthenticatedNodeId(r.getAuthenticatedNodeId())
                      .setBootId(r.getBootId())
                      .setNativeBindingId(r.getNativeBindingId())
                      .setNativeBindingRevision(r.getNativeBindingRevision())
                      .setRealm(nodes.getFirst().get(0))
                      .setNode(nodes.getFirst().get(1))
                      .setIncarnation(UUID.randomUUID().toString())
                      .setChannelNonce(UUID.randomUUID().toString())
                      .setSourceBinding(UUID.randomUUID().toString())
                      .setSourceRevision(1)
                      .setNotBefore(clock.instant().truncatedTo(ChronoUnit.SECONDS).toString())
                      .setExpiresAt(expiry.truncatedTo(ChronoUnit.SECONDS).toString())
                      .setPolicyRevision(policy.current().longValue())
                      .build();
              store.insert(
                  "shared_message_context",
                  r.getOperationId(),
                  Json.object(
                      "binding",
                      bindingJson(binding),
                      "realm",
                      context.getRealm(),
                      "node",
                      context.getNode(),
                      "nativeKey",
                      nativeKey,
                      "sessionKey",
                      sessionKey,
                      "revoked",
                      false,
                      "wire",
                      Base64.getEncoder().encodeToString(context.toByteArray())),
                  expiry);
              return context;
            });
    // Ownership may change during allocation; saved identity never grants a lease.
    tasks.messageBinding(result.getBinding(), r.getCapability().getScopes(0));
    return result.toBuilder().setPolicyRevision(store.tx(policy::current).longValue()).build();
  }

  SharedMessageContext read(ReadSharedMessageContextRequest r) {
    ObjectNode saved;
    if (!r.getContextId().isEmpty() && r.getRealm().isEmpty() && r.getNode().isEmpty()) {
      uuid(r.getContextId());
      saved = store.tx(() -> store.get("shared_message_context", r.getContextId(), false));
    } else {
      if (!r.getContextId().isEmpty()
          || !IamNetworkAuthority.validNumber(r.getRealm(), false)
          || !IamNetworkAuthority.validNumber(r.getNode(), false)) denied();
      saved =
          store.tx(
              () -> {
                var rows =
                    store
                        .networkJdbc()
                        .query(
                            """
            SELECT data::text FROM iam.shared_message_context WHERE tenant_id=? AND realm=?::numeric AND node=?::numeric
            """,
                            (rs, i) -> Json.read(rs.getString(1)),
                            TenantContext.require(),
                            r.getRealm(),
                            r.getNode());
                return rows.isEmpty() ? null : rows.getFirst();
              });
    }
    if (saved == null || saved.path("revoked").asBoolean()) denied();
    var context = decode(saved);
    if (!context.getBinding().getTenantId().equals(TenantContext.require().toString())
        || !Instant.parse(context.getExpiresAt()).isAfter(clock.instant())) denied();
    // A finished individual call does not revoke this native session's durable mailbox.
    try {
      tasks.messageBinding(context.getBinding(), "message.receive");
    } catch (AurigaException failure) {
      tasks.messageBinding(context.getBinding(), "message.send");
    }
    return context.toBuilder().setPolicyRevision(store.tx(policy::current).longValue()).build();
  }

  CheckSharedMessageNodesResponse check(CheckSharedMessageNodesRequest r) {
    var check = r.getCheck();
    var limits = network.effectiveLimits();
    boolean allowed = false;
    try {
      var from =
          read(
              ReadSharedMessageContextRequest.newBuilder()
                  .setRealm(check.getRealm())
                  .setNode(check.getSourceNode())
                  .build());
      var operation = check.getOperation();
      String scope =
          operation == NetworkAuthorityOperation.NETWORK_AUTHORITY_OPERATION_SEND
              ? "message.send"
              : "message.receive";
      tasks.messageBinding(from.getBinding(), scope);
      if (operation != NetworkAuthorityOperation.NETWORK_AUTHORITY_OPERATION_CONNECT) {
        var to =
            read(
                ReadSharedMessageContextRequest.newBuilder()
                    .setRealm(check.getRealm())
                    .setNode(check.getTargetNode())
                    .build());
        tasks.messageBinding(to.getBinding(), "message.receive");
      }
      allowed = store.tx(() -> network.sharedMessagePolicy(check, limits));
    } catch (AurigaException failure) {
      if (!Set.of("common.forbidden", "common.not_found", "common.unauthenticated")
          .contains(failure.errorCode().code())) throw failure;
    }
    var response = CheckSharedMessageNodesResponse.newBuilder().setAllowed(allowed);
    limits.forEach((key, value) -> response.putLimits(key.name(), value));
    return response.build();
  }

  private SharedMessageContext decode(ObjectNode value) {
    try {
      return SharedMessageContext.parseFrom(
          Base64.getDecoder().decode(value.path("wire").asString()));
    } catch (Exception failure) {
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    }
  }

  private static String nativeKey(OpenSharedMessageContextRequest r) {
    var value =
        Json.object(
            "binding",
            bindingJson(r.getCapability().getBinding()),
            "node",
            r.getAuthenticatedNodeId(),
            "boot",
            r.getBootId(),
            "native",
            r.getNativeBindingId(),
            "revision",
            r.getNativeBindingRevision());
    return Secrets.hash(value.toString());
  }

  private static void denied() {
    throw new AurigaException(CommonErrors.FORBIDDEN);
  }
}
