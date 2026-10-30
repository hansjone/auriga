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
    var modelPermissions = effective(request.getOwnerId());
    boolean models =
        modelPermissions != null
            && modelPermissions.getStatus() == MemberStatus.MEMBER_STATUS_ACTIVE
            && modelPermissions.getPermissionsList().contains(DeviceAiResource.INVOKE);
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
              if (sessions.activeById(device.path("sessionId").asString()) == null)
                throw new AurigaException(
                    auriga.common.error.ErrorCode.of(
                        "network.revoked", 403, io.grpc.Status.Code.PERMISSION_DENIED));
              if (!previous.has("modelCredential")) {
                addModelCredential(previous, request.getInstanceId());
                store.save("managed_device", previous);
              }
              return response(previous, models);
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
          addModelCredential(lineage, request.getInstanceId());
          if (previous == null)
            store.insert("managed_device", request.getInstanceId(), lineage, null);
          else {
            lineage.put("id", previous.path("id").asString());
            store.save("managed_device", lineage);
          }
          return response(lineage, models);
        });
  }

  private String purpose(String instance, long version) {
    return TenantContext.require() + "/managed-device/" + instance + "/" + version;
  }

  private void addModelCredential(ObjectNode row, String instance) {
    String credential =
        "amc."
            + TenantContext.require()
            + "."
            + row.path("owner").asString()
            + "."
            + instance
            + "."
            + row.path("device").asString()
            + "."
            + Secrets.random();
    row.put("modelCredentialHash", Secrets.hash(credential));
    row.put(
        "modelCredential",
        secrets.encrypt(credential, purpose(instance, row.path("version").asLong()) + "/model"));
  }

  private IssueManagedDeviceResponse response(ObjectNode row, boolean models) {
    return IssueManagedDeviceResponse.newBuilder()
        .setDeviceId(row.path("device").asString())
        .setCredential(
            secrets.decrypt(
                row.path("credential").asString(),
                purpose(row.path("id").asString(), row.path("version").asLong())))
        .setModelCredential(
            models
                ? secrets.decrypt(
                    row.path("modelCredential").asString(),
                    purpose(row.path("id").asString(), row.path("version").asLong()) + "/model")
                : "")
        .setExpiresAt(
            Timestamp.newBuilder()
                .setSeconds(Instant.parse(row.path("expiresAt").asString()).getEpochSecond()))
        .build();
  }

  /** Dedicated model key admission; no OAuth grant, network token or browser session is issued. */
  public DeviceAccessAuthority.Identity modelAccess(String raw) {
    if (raw == null || raw.length() > 512) return null;
    String[] parts = raw.split("\\.", -1);
    if (parts.length != 6 || !"amc".equals(parts[0]) || !parts[5].matches("[A-Za-z0-9_-]{43}"))
      return null;
    for (int i = 1; i <= 4; i++) {
      try {
        if (!UUID.fromString(parts[i]).toString().equals(parts[i])) return null;
      } catch (IllegalArgumentException invalid) {
        return null;
      }
    }
    tenant(parts[1]);
    store.requireTransactionReleased();
    boolean matches =
        store.tx(
            () -> {
              var row = store.get("managed_device", parts[3], false);
              return row != null
                  && !row.path("revoked").asBoolean()
                  && parts[2].equals(row.path("owner").asString())
                  && parts[4].equals(row.path("device").asString())
                  && clock.instant().isBefore(Instant.parse(row.path("expiresAt").asString()))
                  && Secrets.same(row.path("modelCredentialHash").asString(), Secrets.hash(raw));
            });
    if (!matches) return null;
    var authority = new DeviceAccessAuthority(store, sessions, tokens);
    if (authority.credential(parts[1], parts[2], parts[4]) == null) return null;
    var effective = effective(parts[2]);
    if (effective == null
        || effective.getStatus() != MemberStatus.MEMBER_STATUS_ACTIVE
        || !effective.getPermissionsList().contains(DeviceAiResource.INVOKE)) return null;
    // Re-read after remote authorization, so rotation/revocation cannot revive a stale key.
    var current = authority.credential(parts[1], parts[2], parts[4]);
    if (current == null) return null;
    Instant now = clock.instant();
    if (!now.isBefore(current.expiresAt())) return null;
    Instant lease = now.plusSeconds(300);
    return new DeviceAccessAuthority.Identity(
        current.tenant(),
        current.account(),
        current.device(),
        current.expiresAt().isBefore(lease) ? current.expiresAt() : lease);
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
    return exchange(input, "internal");
  }

  private static final class InvalidCredential extends RuntimeException {
    InvalidCredential() {
      super(null, null, false, false);
    }
  }

  public ObjectNode exchange(Map<String, String> input, String trustedSource) {
    IamError.validate(
        Set.of("grant_type", "client_id", "instance_id", "owner_id", "credential")
            .containsAll(input.keySet()));
    String instance = input.getOrDefault("instance_id", ""),
        owner = input.getOrDefault("owner_id", "");
    uuid(instance);
    uuid(owner);
    try {
      return store.tx(
          () -> {
            var row = store.get("managed_device", instance, false);
            if (!(row != null
                && !row.path("revoked").asBoolean()
                && owner.equals(row.path("owner").asString())
                && clock.instant().isBefore(Instant.parse(row.path("expiresAt").asString()))))
              throw new InvalidCredential();
            var device = store.get("device", row.path("device").asString(), false);
            if (!(device != null
                && !device.path("revoked").asBoolean()
                && Secrets.same(
                    device.path("credentialHash").asString(),
                    Secrets.hash(input.getOrDefault("credential", "")))))
              throw new InvalidCredential();
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
    } catch (InvalidCredential rejected) {
      // Only failed credential checks consume these independent, persisted failure buckets.
      // Authenticated callers do not consult or increment them, even when a bucket is full.
      store.limit("managed-exchange:failure:source:" + trustedSource, clock.instant(), 300);
      store.limit("managed-exchange:failure:instance:" + instance, clock.instant(), 60);
      store.limit("managed-exchange:failure:tenant", clock.instant(), 3000);
      throw new AurigaException(CommonErrors.FORBIDDEN);
    }
  }
}
