package auriga.iam;

import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.contracts.org.v1.*;
import auriga.iam.api.InstanceAiAuthorization;
import java.time.Instant;
import java.util.Set;
import tools.jackson.databind.node.ObjectNode;

/**
 * The sole native refresh resource; online membership never falls back to configured permissions.
 */
final class DeviceAiResource {
  static final String RESOURCE = "urn:auriga:ai-gateway";
  static final String INVOKE = "auriga.connect.models.invoke";
  private final DeviceAccessAuthority devices;
  private final TokenService tokens;
  private final InstanceAiAuthorization authorization;

  DeviceAiResource(
      DeviceAccessAuthority devices, TokenService tokens, InstanceAiAuthorization authorization) {
    this.devices = devices;
    this.tokens = tokens;
    this.authorization = authorization;
  }

  TokenService.IssuedToken issue(OAuthClient client, ObjectNode grant, ObjectNode session) {
    var identity =
        devices.credential(
            client.tenant().toString(),
            session.path("accountId").asString(),
            grant.path("deviceId").asString());
    if (identity == null) return null;
    GetEffectivePermissionsResponse effective;
    try {
      effective =
          authorization.getEffectivePermissions(
              GetEffectivePermissionsRequest.newBuilder()
                  .setTenantId(identity.tenant())
                  .setAccountId(identity.account())
                  .build());
    } catch (AurigaException e) {
      if (Set.of("common.not_found", "common.forbidden").contains(e.errorCode().code()))
        return null;
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    } catch (RuntimeException e) {
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    }
    if (effective == null) throw new AurigaException(CommonErrors.UNAVAILABLE);
    if (effective.getStatus() != MemberStatus.MEMBER_STATUS_ACTIVE
        || (effective.getMemberType() != MemberType.MEMBER_TYPE_MEMBER
            && effective.getMemberType() != MemberType.MEMBER_TYPE_EXTERNAL)
        || !effective.getPermissionsList().contains(INVOKE)) return null;
    return tokens.deviceAi(
        identity,
        Instant.parse(session.path("authTime").asString()),
        effective.getMemberType() == MemberType.MEMBER_TYPE_EXTERNAL ? "external" : "member",
        Set.copyOf(effective.getPermissionsList()));
  }
}
