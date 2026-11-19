package auriga.iam;

import static auriga.iam.SharedTaskTokens.*;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.contracts.iam.v1.*;
import auriga.contracts.node.v1.SharedDshCapability;
import auriga.contracts.node.v1.SharedDshSessionBinding;
import auriga.iam.api.PermissionResolver;
import auriga.workspace.api.SharedDshSessions;
import java.time.*;
import java.util.*;
import java.util.function.BooleanSupplier;
import java.util.function.Supplier;
import tools.jackson.databind.node.ObjectNode;

/** Persistent task/JTI authority. All methods are called behind the internal runtime RPC guard. */
final class SharedTasks {
  private final IamStore store;
  private final SessionManager sessions;
  private final UacSessions uac;
  private final PermissionResolver permissions;
  private final SharedDshSessions ownership;
  private final SharedTaskTokens tokens;
  private final Clock clock;
  private final BooleanSupplier allowRaw;

  SharedTasks(
      IamStore store,
      SessionManager sessions,
      UacSessions uac,
      PermissionResolver permissions,
      SharedDshSessions ownership,
      SharedTaskTokens tokens,
      Clock clock,
      BooleanSupplier allowRaw) {
    this.store = store;
    this.sessions = sessions;
    this.uac = uac;
    this.permissions = permissions;
    this.ownership = ownership;
    this.tokens = tokens;
    this.clock = clock;
    this.allowRaw = allowRaw;
  }

  BeginSharedTaskResponse begin(BeginSharedTaskRequest request) {
    var binding = request.getBinding();
    validate(binding);
    uuid(request.getTaskId());
    String audience = request.getAudience().isEmpty() ? ENTERPRISE : request.getAudience();
    var requested = scopes(audience, request.getScopesList());
    return withOwnership(
        binding,
        () -> {
          var session = session(binding);
          store.lock("shared-task:" + request.getTaskId());
          if (store.get("shared_task_end", endKey(binding, request.getTaskId()), false) != null)
            throw new AurigaException(CommonErrors.CONFLICT);
          var task = store.get("shared_task", request.getTaskId(), true);
          if (task == null) {
            task =
                Json.object(
                    "tenantId", binding.getTenantId(),
                    "taskId", request.getTaskId(),
                    "binding", bindingJson(binding),
                    "bindingWire", Base64.getEncoder().encodeToString(binding.toByteArray()),
                    "sessionAuthGeneration", session.path("authGeneration").asLong(),
                    "ended", false,
                    "expiresAt", session.path("expiresAt"));
            store.insert(
                "shared_task",
                request.getTaskId(),
                task,
                Instant.parse(session.path("expiresAt").asString()));
            store.uacTenant();
          }
          requireTask(task, binding, session);
          var capability = issue(task, session, binding, audience, requested);
          return BeginSharedTaskResponse.newBuilder()
              .setToken(tokens.sign(capability))
              .setCapability(capability)
              .build();
        });
  }

  IssueSharedCapabilityResponse issue(IssueSharedCapabilityRequest request) {
    var binding = request.getBinding();
    validate(binding);
    uuid(request.getTaskId());
    var requested = scopes(request.getAudience(), request.getScopesList());
    return withOwnership(
        binding,
        () -> {
          var session = session(binding);
          store.lock("shared-task:" + request.getTaskId());
          var task = store.get("shared_task", request.getTaskId(), true);
          requireTask(task, binding, session);
          var capability = issue(task, session, binding, request.getAudience(), requested);
          return IssueSharedCapabilityResponse.newBuilder()
              .setToken(tokens.sign(capability))
              .setCapability(capability)
              .build();
        });
  }

  FinishSharedTaskResponse finish(FinishSharedTaskRequest request) {
    var binding = request.getBinding();
    requireTenant(binding);
    validate(binding);
    uuid(request.getTaskId());
    return store.tx(
        () -> {
          store.lock("shared-task:" + request.getTaskId());
          var task = store.get("shared_task", request.getTaskId(), true);
          if (task != null && !task.path("binding").equals(bindingJson(binding)))
            throw new AurigaException(CommonErrors.NOT_FOUND);
          String endKey = endKey(binding, request.getTaskId());
          if (store.get("shared_task_end", endKey, true) == null)
            store.insert(
                "shared_task_end",
                endKey,
                Json.object(
                    "tenantId", binding.getTenantId(),
                    "taskId", request.getTaskId(),
                    "binding", bindingJson(binding),
                    "ended", true,
                    "endedAt", clock.instant().toString()),
                null);
          if (task != null && !task.path("ended").asBoolean()) {
            task.put("ended", true);
            task.put("endedAt", clock.instant().toString());
            store.save("shared_task", task);
          }
          return FinishSharedTaskResponse.newBuilder()
              .setFenced(true)
              .setBinding(binding)
              .setTaskId(request.getTaskId())
              .build();
        });
  }

  private static String endKey(SharedDshSessionBinding binding, String task) {
    return task + ":" + Secrets.hash(Base64.getEncoder().encodeToString(binding.toByteArray()));
  }

  AuthorizeSharedCapabilityResponse authorize(AuthorizeSharedCapabilityRequest request) {
    var capability = request.getCapability();
    validate(capability);
    try {
      return withOwnership(capability.getBinding(), () -> snapshot(capability));
    } catch (AurigaException error) {
      if (Set.of("common.not_found", "common.forbidden", "common.unauthenticated")
          .contains(error.errorCode().code()))
        return AuthorizeSharedCapabilityResponse.getDefaultInstance();
      throw error;
    }
  }

  ResolveSharedTaskResponse resolve(ResolveSharedTaskRequest request) {
    uuid(request.getTenantId());
    uuid(request.getSessionId());
    uuid(request.getTaskId());
    if (!request.getJti().isEmpty()) uuid(request.getJti());
    if (!Set.of(MODEL, MESSAGE).contains(request.getAudience()))
      throw new AurigaException(CommonErrors.FORBIDDEN);
    if (!TenantContext.require().toString().equals(request.getTenantId()))
      throw new AurigaException(CommonErrors.NOT_FOUND);
    var stored = store.tx(() -> store.get("shared_task", request.getTaskId(), false));
    if (stored == null) return ResolveSharedTaskResponse.getDefaultInstance();
    SharedDshSessionBinding binding;
    try {
      binding =
          SharedDshSessionBinding.parseFrom(
              Base64.getDecoder().decode(stored.path("bindingWire").asString()));
    } catch (com.google.protobuf.InvalidProtocolBufferException | IllegalArgumentException error) {
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    }
    validate(binding);
    if (!binding.getSessionId().equals(request.getSessionId()))
      return ResolveSharedTaskResponse.getDefaultInstance();
    try {
      return withOwnership(
          binding,
          () -> {
            var session = session(binding);
            store.lock("shared-task:" + request.getTaskId());
            var task = store.get("shared_task", request.getTaskId(), true);
            if (!liveTask(task, binding, session))
              return ResolveSharedTaskResponse.getDefaultInstance();
            if (!request.getJti().isEmpty()) {
              var record = store.get("shared_capability", request.getJti(), false);
              if (record == null
                  || !record.path("claims").path("taskId").asString().equals(request.getTaskId())
                  || !record
                      .path("claims")
                      .path("audience")
                      .asString()
                      .equals(request.getAudience())
                  || !record.path("claims").path("binding").equals(bindingJson(binding)))
                return ResolveSharedTaskResponse.getDefaultInstance();
            }
            var allowed = allowedScopes(binding, request.getAudience());
            return ResolveSharedTaskResponse.newBuilder()
                .setActive(!allowed.isEmpty())
                .setCredentialVersion(
                    credentialVersion(task, session, request.getAudience(), allowed))
                .addAllScopes(allowed)
                .setBinding(binding)
                .build();
          });
    } catch (AurigaException error) {
      if (Set.of("common.not_found", "common.forbidden", "common.unauthenticated")
          .contains(error.errorCode().code()))
        return ResolveSharedTaskResponse.getDefaultInstance();
      throw error;
    }
  }

  ReadSharedCredentialsResponse credentials(ReadSharedCredentialsRequest request) {
    var capability = request.getCapability();
    validate(capability);
    if (!ENTERPRISE.equals(capability.getAudience())
        || !capability.getScopesList().contains("credentials"))
      throw new AurigaException(CommonErrors.FORBIDDEN);
    return withOwnership(
        capability.getBinding(),
        () -> {
          var state = snapshot(capability);
          if (!state.getActive() || !state.getAllowRawAgentToken())
            throw new AurigaException(CommonErrors.FORBIDDEN);
          var binding = capability.getBinding();
          var material =
              uac.sharedCredentials(binding.getMemberId(), binding.getPlatformSessionId());
          if (material.path("credentialVersion").asLong() != capability.getCredentialVersion())
            throw new AurigaException(CommonErrors.UNAUTHENTICATED);
          return ReadSharedCredentialsResponse.newBuilder()
              .setEmpNo(material.path("empNo").asString())
              .setToken(material.path("token").asString())
              .setUpdatedAt(material.path("updatedAt").asString())
              .setCredentialVersion(material.path("credentialVersion").asLong())
              .build();
        });
  }

  /** Internal-only material for a server-side delegated operation; never an RPC response. */
  ObjectNode outboundMaterial(SharedDshCapability capability) {
    validate(capability);
    if (!ENTERPRISE.equals(capability.getAudience())
        || !capability.getScopesList().contains("outbound"))
      throw new AurigaException(CommonErrors.FORBIDDEN);
    return withOwnership(
        capability.getBinding(),
        () -> {
          if (!snapshot(capability).getActive()) throw new AurigaException(CommonErrors.FORBIDDEN);
          var binding = capability.getBinding();
          var material =
              uac.sharedCredentials(binding.getMemberId(), binding.getPlatformSessionId());
          if (material.path("credentialVersion").asLong() != capability.getCredentialVersion())
            throw new AurigaException(CommonErrors.UNAUTHENTICATED);
          return material;
        });
  }

  void checkOutbound(SharedDshCapability capability) {
    if (!ENTERPRISE.equals(capability.getAudience())
        || !capability.getScopesList().contains("outbound")
        || !authorize(
                AuthorizeSharedCapabilityRequest.newBuilder().setCapability(capability).build())
            .getActive()) throw new AurigaException(CommonErrors.FORBIDDEN);
  }

  void requireNoTransaction() {
    store.requireTransactionReleased();
  }

  /**
   * Stable native message identity follows the current member login, never a finished tool task.
   */
  Instant messageBinding(SharedDshSessionBinding binding, String scope) {
    validate(binding);
    if (!Set.of("message.send", "message.receive").contains(scope))
      throw new AurigaException(CommonErrors.FORBIDDEN);
    return withOwnership(
        binding,
        () -> {
          var session = session(binding);
          if (!allowedScopes(binding, MESSAGE).contains(scope))
            throw new AurigaException(CommonErrors.FORBIDDEN);
          return Instant.parse(session.path("expiresAt").asString());
        });
  }

  private SharedDshCapability issue(
      ObjectNode task,
      ObjectNode session,
      SharedDshSessionBinding binding,
      String audience,
      List<String> requested) {
    var allowed = allowedScopes(binding, audience);
    if (!allowed.containsAll(requested)
        || (requested.contains("credentials") && !allowRaw.getAsBoolean()))
      throw new AurigaException(CommonErrors.FORBIDDEN);
    long version =
        credentialVersion(task, session, audience, allowed, requested.contains("task.execute"));
    long now = clock.instant().getEpochSecond();
    long expiry =
        Math.min(now + 600, Instant.parse(task.path("expiresAt").asString()).getEpochSecond());
    if (expiry <= now) throw new AurigaException(CommonErrors.UNAUTHENTICATED);
    var capability =
        SharedDshCapability.newBuilder()
            .setSchemaVersion(1)
            .setBinding(binding)
            .setTaskId(task.path("taskId").asString())
            .setAudience(audience)
            .addAllScopes(requested)
            .setCredentialVersion(version)
            .setIssuedAt(now)
            .setExpiresAt(expiry)
            .setJti(UUID.randomUUID().toString())
            .build();
    store.insert(
        "shared_capability",
        capability.getJti(),
        Json.object("claims", claimsJson(capability)),
        Instant.ofEpochSecond(expiry + 150));
    return capability;
  }

  private AuthorizeSharedCapabilityResponse snapshot(SharedDshCapability capability) {
    long now = clock.instant().getEpochSecond();
    if (capability.getIssuedAt() > now || capability.getExpiresAt() <= now)
      return AuthorizeSharedCapabilityResponse.getDefaultInstance();
    var binding = capability.getBinding();
    var session = session(binding);
    store.lock("shared-task:" + capability.getTaskId());
    var task = store.get("shared_task", capability.getTaskId(), true);
    if (!liveTask(task, binding, session))
      return AuthorizeSharedCapabilityResponse.getDefaultInstance();
    var recorded = store.get("shared_capability", capability.getJti(), false);
    if (recorded == null || !recorded.path("claims").equals(claimsJson(capability)))
      return AuthorizeSharedCapabilityResponse.getDefaultInstance();
    var allowed = allowedScopes(binding, capability.getAudience());
    long version =
        credentialVersion(
            task,
            session,
            capability.getAudience(),
            allowed,
            capability.getScopesList().contains("task.execute"));
    return AuthorizeSharedCapabilityResponse.newBuilder()
        .setActive(
            version == capability.getCredentialVersion()
                && allowed.containsAll(capability.getScopesList()))
        .setCredentialVersion(version)
        .setAllowRawAgentToken(allowRaw.getAsBoolean())
        .addAllScopes(allowed)
        .setBinding(binding)
        .setEnterpriseCredentialState(
            ENTERPRISE.equals(capability.getAudience())
                ? enterpriseState(session)
                : SharedEnterpriseCredentialState.SHARED_ENTERPRISE_CREDENTIAL_STATE_UNSPECIFIED)
        .build();
  }

  private List<String> allowedScopes(SharedDshSessionBinding binding, String audience) {
    return switch (audience) {
      case ENTERPRISE -> List.of("credentials", "outbound", "task.execute");
      case MODEL ->
          permissions
                  .resolve(TenantContext.require(), binding.getMemberId(), "portal")
                  .contains("ai.models.invoke")
              ? List.of("model.invoke")
              : List.of();
      case MESSAGE ->
          permissions
                  .resolve(TenantContext.require(), binding.getMemberId(), "portal")
                  .contains("collab.messages.use")
              ? List.of("message.receive", "message.send")
              : List.of();
      default -> throw new AurigaException(CommonErrors.VALIDATION_FAILED);
    };
  }

  private long credentialVersion(
      ObjectNode task, ObjectNode session, String audience, List<String> allowed) {
    return credentialVersion(task, session, audience, allowed, false);
  }

  private SharedEnterpriseCredentialState enterpriseState(ObjectNode session) {
    if (!session.has("uacCredentialId")) {
      if (session.has("uacSessionBinding")) throw new AurigaException(CommonErrors.UNAUTHENTICATED);
      return SharedEnterpriseCredentialState.SHARED_ENTERPRISE_CREDENTIAL_STATE_ABSENT;
    }
    enterpriseVersion(session);
    return SharedEnterpriseCredentialState.SHARED_ENTERPRISE_CREDENTIAL_STATE_AVAILABLE;
  }

  private long enterpriseVersion(ObjectNode session) {
    if (!session.has("uacCredentialId")) throw new AurigaException(CommonErrors.UNAUTHENTICATED);
    var credential =
        store.byId("uac_credential", session.path("uacCredentialId").asString(), false);
    if (credential == null
        || credential.path("invalid").asBoolean()
        || !credential.path("accountId").equals(session.path("accountId"))
        || !credential.path("providerId").equals(session.path("providerId")))
      throw new AurigaException(CommonErrors.UNAUTHENTICATED);
    long version = credential.path("version").asLong();
    positive(version);
    return version;
  }

  private long credentialVersion(
      ObjectNode task,
      ObjectNode session,
      String audience,
      List<String> allowed,
      boolean executionOnly) {
    if (ENTERPRISE.equals(audience) && !executionOnly) return enterpriseVersion(session);
    var versions = task.withObject("/audienceVersions");
    String versionKey = executionOnly ? audience + ":task.execute" : audience;
    var current = versions.path(versionKey);
    String fingerprint = Secrets.hash(String.join("\n", allowed));
    long version = current.path("version").asLong(0);
    if (!fingerprint.equals(current.path("fingerprint").asString())) {
      version++;
      positive(version);
      versions.set(versionKey, Json.object("fingerprint", fingerprint, "version", version));
      store.save("shared_task", task);
    }
    positive(version);
    return version;
  }

  private ObjectNode session(SharedDshSessionBinding binding) {
    var session = sessions.activeById(binding.getPlatformSessionId());
    if (session == null
        || !binding.getMemberId().equals(session.path("accountId").asString())
        || !binding.getTenantId().equals(session.path("tenantId").asString())
        || !"portal".equals(session.path("audience").asString())
        || session.has("managedInstance")) throw new AurigaException(CommonErrors.UNAUTHENTICATED);
    return session;
  }

  private void requireTask(ObjectNode task, SharedDshSessionBinding binding, ObjectNode session) {
    if (!liveTask(task, binding, session)) throw new AurigaException(CommonErrors.CONFLICT);
  }

  private boolean liveTask(ObjectNode task, SharedDshSessionBinding binding, ObjectNode session) {
    return task != null
        && !task.path("ended").asBoolean()
        && task.path("binding").equals(bindingJson(binding))
        && task.path("sessionAuthGeneration").asLong() == session.path("authGeneration").asLong()
        && clock.instant().isBefore(Instant.parse(task.path("expiresAt").asString()));
  }

  private <T> T withOwnership(SharedDshSessionBinding binding, Supplier<T> work) {
    requireTenant(binding);
    // Workspace checks IAM while holding its own locks. Resolve outside IAM's
    // transaction to preserve that lock order, then fence any changed binding
    // again before a capability or material can leave this service.
    store.requireTransactionReleased();
    requireOwnership(binding);
    T result = store.tx(work);
    requireOwnership(binding);
    return result;
  }

  private void requireOwnership(SharedDshSessionBinding binding) {
    try {
      var registered =
          ownership.lookup(uuid(binding.getPhysicalInstanceId()), uuid(binding.getSessionId()));
      if (!binding.equals(registered)) throw new AurigaException(CommonErrors.UNAUTHENTICATED);
    } catch (AurigaException error) {
      throw error;
    } catch (RuntimeException error) {
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    }
  }

  private void requireTenant(SharedDshSessionBinding binding) {
    if (TenantContext.PLATFORM_TENANT.equals(TenantContext.require())
        || !TenantContext.require().toString().equals(binding.getTenantId()))
      throw new AurigaException(CommonErrors.NOT_FOUND);
  }
}
