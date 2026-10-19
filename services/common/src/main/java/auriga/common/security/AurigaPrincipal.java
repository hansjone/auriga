package auriga.common.security;

import java.time.Duration;
import java.time.Instant;
import java.util.List;
import java.util.Objects;
import java.util.Set;
import java.util.UUID;

/**
 * The authenticated caller, mapped from the access token claims (contracts/auth).
 *
 * @param subject {@code sub}: account id or service account id
 * @param tenantId {@code tid}
 * @param type {@code ptyp}
 * @param context {@code ctx}: where the token came from (portal/admin/device/service)
 * @param sessionId {@code sid} for portal/admin tokens, else null
 * @param deviceId {@code did} for device tokens, else null
 * @param memberType {@code mtyp} (member/external), may be null
 * @param roles {@code roles}
 * @param permissions {@code perms}
 * @param authTime {@code auth_time}, may be null
 * @param stepUpTime {@code stepup_time}, null when the session never stepped up
 * @param tokenId {@code jti}
 */
public record AurigaPrincipal(
    String subject,
    UUID tenantId,
    PrincipalType type,
    TokenContext context,
    String sessionId,
    String deviceId,
    String memberType,
    List<String> roles,
    Set<String> permissions,
    Instant authTime,
    Instant stepUpTime,
    String tokenId) {
  public AurigaPrincipal {
    Objects.requireNonNull(subject, "subject");
    Objects.requireNonNull(tenantId, "tenantId");
    Objects.requireNonNull(type, "type");
    Objects.requireNonNull(context, "context");
    roles = List.copyOf(roles == null ? List.of() : roles);
    permissions = Set.copyOf(permissions == null ? Set.of() : permissions);
  }

  public boolean hasPermission(String permission) {
    return permissions.contains(permission);
  }

  public boolean isAdminContext() {
    return context == TokenContext.ADMIN;
  }

  /**
   * Internal service token: {@code ptyp=internal_service}, {@code ctx=service}, {@code svc:} sub.
   */
  public boolean isInternalService() {
    return type == PrincipalType.INTERNAL_SERVICE
        && context == TokenContext.SERVICE
        && subject.startsWith("svc:");
  }

  /** True when a step-up happened within {@code maxAge} of {@code now} (not in the future). */
  public boolean steppedUpWithin(Duration maxAge, Instant now) {
    if (stepUpTime == null) return false;
    return !stepUpTime.isAfter(now.plusSeconds(30)) && !stepUpTime.isBefore(now.minus(maxAge));
  }
}
