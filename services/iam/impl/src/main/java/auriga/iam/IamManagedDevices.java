package auriga.iam;

import auriga.common.context.TenantContext;
import auriga.common.error.*;
import auriga.common.persistence.UuidV7;
import auriga.contracts.iam.v1.*;
import auriga.contracts.org.v1.*;
import auriga.contracts.workspace.v1.*;
import auriga.iam.api.*;
import auriga.workspace.api.ManagedInstanceBindings;
import com.google.protobuf.Timestamp;
import java.time.*;
import java.util.*;
import tools.jackson.databind.node.ObjectNode;

/** IAM-owned managed device lineage. Instance state and membership remain online authorities. */
public final class IamManagedDevices implements ManagedDeviceCredentials {
  public static final String GRANT = "urn:auriga:params:oauth:grant-type:managed-device";
  private final IamStore store;
  private final SessionManager sessions;
  private final TokenService tokens;
  private final Secrets secrets;
  private final ManagedInstanceBindings bindings;
  private final InstanceAiAuthorization permissions;
  private final Clock clock;

  public IamManagedDevices(
      IamStore store,
      SessionManager sessions,
      TokenService tokens,
      Secrets secrets,
      ManagedInstanceBindings bindings,
      InstanceAiAuthorization permissions,
      Clock clock) {
    this.store = store;
    this.sessions = sessions;
    this.tokens = tokens;
    this.secrets = secrets;
    this.bindings = bindings;
    this.permissions = permissions;
    this.clock = clock;
  }

  private static void require(boolean value) {
    if (!value) throw new AurigaException(CommonErrors.FORBIDDEN);
  }

  private static void uuid(String value) {
    try {
      IamError.validate(UUID.fromString(value).toString().equals(value));
    } catch (IllegalArgumentException | NullPointerException e) {
      throw new AurigaException(CommonErrors.VALIDATION_FAILED);
    }
  }

  private void tenant(String tenant) {
    require(
        TenantContext.require().toString().equals(tenant)
            && !TenantContext.PLATFORM_TENANT.equals(TenantContext.require()));
  }

  private boolean active(String instance, String owner, long version) {
    try {
      return activeBinding(instance, owner, version);
    } catch (AurigaException error) {
      if (Set.of("common.not_found", "common.forbidden").contains(error.errorCode().code()))
        return false;
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    } catch (RuntimeException unavailable) {
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    }
  }

  private boolean activeBinding(String instance, String owner, long version) {
    var binding =
        bindings.get(
            GetManagedInstanceBindingRequest.newBuilder()
                .setTenantId(TenantContext.require().toString())
                .setInstanceId(instance)
                .build());
    if (binding == null
        || !binding.getActive()
        || !TenantContext.require().toString().equals(binding.getTenantId())
        || !instance.equals(binding.getInstanceId())
        || !owner.equals(binding.getOwnerId())
        || version != binding.getCredentialVersion()) return false;
    var effective = effective(owner);
    return effective != null
        && effective.getStatus() == MemberStatus.MEMBER_STATUS_ACTIVE
        && (effective.getMemberType() == MemberType.MEMBER_TYPE_MEMBER
            || effective.getMemberType() == MemberType.MEMBER_TYPE_EXTERNAL)
        && effective.getPermissionsList().contains("auriga.connect.collab.use");
  }

  private GetEffectivePermissionsResponse effective(String owner) {
    try {
      return permissions.getEffectivePermissions(
          GetEffectivePermissionsRequest.newBuilder()
              .setTenantId(TenantContext.require().toString())
              .setAccountId(owner)
              .build());
    } catch (RuntimeException error) {
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    }
  }

  public boolean validSession(ObjectNode session) {
    return active(
        session.path("managedInstance").asString(),
        session.path("accountId").asString(),
        session.path("managedVersion").asLong());
  }

  @Override
  public IssueManagedDeviceResponse issue(IssueManagedDeviceRequest request) {
    tenant(request.getTenantId());
    uuid(request.getOwnerId());
    uuid(request.getInstanceId());
    IamError.validate(
        request.getUnknownFields().asMap().isEmpty()
            && request.getCredentialVersion() >= 1
            && request.getCredentialVersion() <= 9007199254740991L);
    require(active(request.getInstanceId(), request.getOwnerId(), request.getCredentialVersion()));
    return store.tx(
        () -> {
          store.lock("managed:" + request.getInstanceId());
          var account = store.get("account", request.getOwnerId(), false);
          require(account != null && "active".equals(account.path("status").asString()));
          var previous = store.get("managed_device", request.getInstanceId(), true);
          if (previous != null) {
            long version = previous.path("version").asLong();
            require(version <= request.getCredentialVersion());
            if (version == request.getCredentialVersion()) {
              require(
                  !previous.path("revoked").asBoolean()
                      && request.getOwnerId().equals(previous.path("owner").asString()));
              var device = store.get("device", previous.path("device").asString(), false);
              require(device != null && !device.path("revoked").asBoolean());
              require(sessions.activeById(device.path("sessionId").asString()) != null);
              return response(previous);
            }
            retire(previous);
          }
          Instant expiry = clock.instant().plus(Duration.ofDays(30));
          UUID device = UuidV7.next(clock);
          // No browser cookie, user JWT, refresh grant or KV projection is ever issued.
          var session =
              store.insert(
                  "session",
                  Secrets.hash(Secrets.random()),
                  Json.object(
                      "kind",
                      "managed_device",
                      "accountId",
                      request.getOwnerId(),
                      "tenantId",
                      request.getTenantId(),
                      "audience",
                      "portal",
                      "cookieHash",
                      Secrets.hash(Secrets.random()),
                      "authTime",
                      clock.instant().toString(),
                      "expiresAt",
                      expiry.toString(),
                      "idleExpiresAt",
                      expiry.toString(),
                      "revoked",
                      false,
                      "managedInstance",
                      request.getInstanceId(),
                      "managedVersion",
                      request.getCredentialVersion()),
                  expiry);
          // Store generates the row primary key; its canonical session id must be that key.
          session.put("sessionId", session.path("id").asString());
          store.save("session", session);
          String credential = Secrets.random();
          store.insert(
              "device",
              device.toString(),
              Json.object(
                  "deviceId",
                  device.toString(),
                  "accountId",
                  request.getOwnerId(),
                  "sessionId",
                  session.path("sessionId").asString(),
                  "deviceType",
                  "managed",
                  "deviceName",
                  "Managed DSH",
                  "createdAt",
                  clock.instant().toString(),
                  "managedInstance",
                  request.getInstanceId(),
                  "managedVersion",
                  request.getCredentialVersion(),
                  "scope",
                  "auriga.connect",
                  "revoked",
                  false,
                  "credentialHash",
                  Secrets.hash(credential)),
              expiry);
          var lineage =
              Json.object(
                  "owner",
                  request.getOwnerId(),
                  "device",
                  device.toString(),
                  "version",
                  request.getCredentialVersion(),
                  "revoked",
                  false,
                  "expiresAt",
                  expiry.toString(),
                  "credential",
                  secrets.encrypt(
                      credential,
                      purpose(request.getInstanceId(), request.getCredentialVersion())));
          if (previous == null)
            store.insert("managed_device", request.getInstanceId(), lineage, null);
          else {
            lineage.put("id", previous.path("id").asString());
            store.save("managed_device", lineage);
          }
          return response(lineage);
        });
  }

  private String purpose(String instance, long version) {
    return TenantContext.require() + "/managed-device/" + instance + "/" + version;
  }

  private IssueManagedDeviceResponse response(ObjectNode row) {
    return IssueManagedDeviceResponse.newBuilder()
        .setDeviceId(row.path("device").asString())
        .setCredential(
            secrets.decrypt(
                row.path("credential").asString(),
                purpose(row.path("id").asString(), row.path("version").asLong())))
        .setExpiresAt(
            Timestamp.newBuilder()
                .setSeconds(Instant.parse(row.path("expiresAt").asString()).getEpochSecond()))
        .build();
  }

  private void retire(ObjectNode lineage) {
    var device = store.get("device", lineage.path("device").asString(), false);
    if (device != null) {
      var session = store.byId("session", device.path("sessionId").asString(), true);
      device = store.get("device", lineage.path("device").asString(), true);
      if (session != null) {
        session.put("revoked", true);
        store.save("session", session);
      }
      device.put("revoked", true);
      store.save("device", device);
    }
  }

  @Override
  public void revoke(RevokeManagedDeviceRequest request) {
    tenant(request.getTenantId());
    uuid(request.getInstanceId());
    IamError.validate(
        request.getCredentialVersion() >= 1
            && request.getCredentialVersion() <= 9007199254740991L
            && request.getUnknownFields().asMap().isEmpty());
    store.tx(
        () -> {
          store.lock("managed:" + request.getInstanceId());
          var row = store.get("managed_device", request.getInstanceId(), true);
          if (row != null && row.path("version").asLong() <= request.getCredentialVersion()) {
            retire(row);
            row.put("revoked", true);
            store.save("managed_device", row);
          }
          return null;
        });
  }

  public ObjectNode exchange(Map<String, String> input) {
    IamError.validate(
        Set.of("grant_type", "client_id", "instance_id", "owner_id", "credential")
            .containsAll(input.keySet()));
    String instance = input.getOrDefault("instance_id", ""),
        owner = input.getOrDefault("owner_id", "");
    uuid(instance);
    uuid(owner);
    store.limit("managed-exchange:tenant", clock.instant(), 3000);
    store.limit("managed-exchange:" + instance, clock.instant(), 60);
    return store.tx(
        () -> {
          var row = store.get("managed_device", instance, false);
          require(
              row != null
                  && !row.path("revoked").asBoolean()
                  && owner.equals(row.path("owner").asString())
                  && clock.instant().isBefore(Instant.parse(row.path("expiresAt").asString())));
          var device = store.get("device", row.path("device").asString(), false);
          require(
              device != null
                  && Secrets.same(
                      device.path("credentialHash").asString(),
                      Secrets.hash(input.getOrDefault("credential", ""))));
          var identity =
              new DeviceAccessAuthority(store, sessions, tokens)
                  .credential(
                      TenantContext.require().toString(), owner, row.path("device").asString());
          require(identity != null);
          var effective = effective(owner);
          require(
              effective != null
                  && effective.getStatus() == MemberStatus.MEMBER_STATUS_ACTIVE
                  && effective.getPermissionsList().contains("auriga.connect.collab.use")
                  && (effective.getMemberType() == MemberType.MEMBER_TYPE_MEMBER
                      || effective.getMemberType() == MemberType.MEMBER_TYPE_EXTERNAL));
          var token =
              tokens.managedDevice(
                  TenantContext.require(),
                  owner,
                  UUID.fromString(identity.device()),
                  identity.expiresAt(),
                  effective.getMemberType() == MemberType.MEMBER_TYPE_EXTERNAL
                      ? "external"
                      : "member");
          return Json.object(
              "access_token",
              token.accessToken(),
              "token_type",
              "Bearer",
              "scope",
              "auriga.connect",
              "expires_in",
              token.expiresAt().getEpochSecond() - clock.instant().getEpochSecond());
        });
  }
}
