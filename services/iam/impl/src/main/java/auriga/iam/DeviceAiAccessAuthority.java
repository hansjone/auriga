package auriga.iam;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.contracts.org.v1.*;
import auriga.iam.api.InstanceAiAuthorization;
import java.time.Clock;
import java.time.Instant;
import java.util.Set;
import java.util.UUID;
import tools.jackson.databind.node.ObjectNode;

/** Online AI device admission only; never touches idle, refresh grants or permission caches. */
public final class DeviceAiAccessAuthority {
  private final DeviceAccessAuthority devices;
  private final TokenService tokens;
  private final InstanceAiAuthorization authorization;
  private final Clock clock;
  private IamManagedDevices managed;

  public DeviceAiAccessAuthority managedModels(IamManagedDevices value) {
    managed = java.util.Objects.requireNonNull(value);
    return this;
  }

  public DeviceAiAccessAuthority(
      DeviceAccessAuthority devices,
      TokenService tokens,
      InstanceAiAuthorization authorization,
      Clock clock) {
    this.devices = devices;
    this.tokens = tokens;
    this.authorization = authorization;
    this.clock = clock;
  }

  public DeviceAccessAuthority.Identity access(String raw) {
    if (raw == null || raw.isEmpty() || raw.length() > 16384) return null;
    if (raw.startsWith("amc.")) return managed == null ? null : managed.modelAccess(raw);
    ObjectNode claims;
    try {
      // Reuse the authoritative signature, schema, issuer, typ, alg and kid validation.
      claims = tokens.verify(raw);
    } catch (AurigaException invalid) {
      int status = invalid.errorCode().httpStatus();
      if (status == 400 || status == 401 || status == 403) return null;
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    } catch (RuntimeException unavailable) {
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    }
    if (!aiDevice(claims)) return null;
    String tenant = claims.path("tid").asString(),
        account = claims.path("sub").asString(),
        device = claims.path("did").asString();
    if (!TenantContext.require().toString().equals(tenant))
      throw new AurigaException(CommonErrors.FORBIDDEN);
    Instant tokenExpiry = Instant.ofEpochSecond(claims.path("exp").asLong());
    try {
      // A surrounding transaction would retain credential's locks across the org call.
      if (org.springframework.transaction.support.TransactionSynchronizationManager
              .isActualTransactionActive()
          || org.springframework.transaction.support.TransactionSynchronizationManager
              .isSynchronizationActive()) throw new AurigaException(CommonErrors.UNAVAILABLE);
      if (devices.credential(tenant, account, device) == null) return null;
      // credential owns and releases its IAM locks before the org call.
      GetEffectivePermissionsResponse effective;
      try {
        effective =
            authorization.getEffectivePermissions(
                GetEffectivePermissionsRequest.newBuilder()
                    .setTenantId(tenant)
                    .setAccountId(account)
                    .build());
      } catch (AurigaException invalidMember) {
        if (Set.of("common.not_found", "common.forbidden")
            .contains(invalidMember.errorCode().code())) return null;
        throw invalidMember;
      }
      if (effective == null) throw new AurigaException(CommonErrors.UNAVAILABLE);
      if (effective.getStatus() != MemberStatus.MEMBER_STATUS_ACTIVE
          || (effective.getMemberType() != MemberType.MEMBER_TYPE_MEMBER
              && effective.getMemberType() != MemberType.MEMBER_TYPE_EXTERNAL)
          || !effective.getPermissionsList().contains(DeviceAiResource.INVOKE)) return null;
      // Re-read device/account/session state after the remote check, without extending activity.
      var current = devices.credential(tenant, account, device);
      if (current == null) return null;
      Instant expiry =
          tokenExpiry.isBefore(current.expiresAt()) ? tokenExpiry : current.expiresAt();
      if (!clock.instant().isBefore(expiry)) return null;
      return new DeviceAccessAuthority.Identity(
          current.tenant(), current.account(), current.device(), expiry);
    } catch (RuntimeException unavailable) {
      // Do not propagate remote params, raw tokens, implementation messages or causes.
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    }
  }

  private boolean aiDevice(ObjectNode c) {
    var aud = c.path("aud");
    boolean aiAudience =
        aud.isString() && aud.asString().equals("auriga.ai-gateway")
            || aud.isArray()
                && aud.size() == 1
                && aud.get(0).asString().equals("auriga.ai-gateway");
    if (!aiAudience
        || !c.path("ptyp").asString().equals("device")
        || !c.path("ctx").asString().equals("device")
        || !uuid(c.path("tid").asString())
        || !uuid(c.path("sub").asString())
        || !uuid(c.path("did").asString())
        || c.has("sid")
        || c.has("instanceId")
        || c.has("credentialVersion")
        || c.has("roles") && (!c.path("roles").isArray() || !c.path("roles").isEmpty())
        || !c.path("perms").isArray()
        || c.path("perms").size() != 1
        || !c.path("perms").get(0).asString().equals(DeviceAiResource.INVOKE)
        || !c.path("iat").isIntegralNumber()
        || !c.path("iat").canConvertToLong()
        || !c.path("exp").isIntegralNumber()
        || !c.path("exp").canConvertToLong()) return false;
    long issued = c.path("iat").asLong(), expires = c.path("exp").asLong();
    long now = clock.instant().getEpochSecond();
    return issued <= now + 30
        && expires > now
        && expires > issued
        && issued >= expires - 300
        && (!c.has("nbf")
            || c.path("nbf").isIntegralNumber()
                && c.path("nbf").canConvertToLong()
                && c.path("nbf").asLong() < expires);
  }

  private static boolean uuid(String value) {
    try {
      return UUID.fromString(value).toString().equals(value);
    } catch (IllegalArgumentException invalid) {
      return false;
    }
  }
}
