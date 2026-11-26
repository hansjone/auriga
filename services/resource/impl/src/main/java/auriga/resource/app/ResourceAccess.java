package auriga.resource.app;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.security.AurigaPrincipal;
import auriga.common.security.PrincipalType;
import auriga.contracts.org.v1.MemberStatus;
import auriga.contracts.org.v1.MemberType;
import auriga.resource.api.ResourceAccessInputs;
import java.util.Optional;
import java.util.Set;
import java.util.UUID;
import java.util.stream.Collectors;

/**
 * A fresh authorization snapshot per operation, with no cached JWT membership/group assumptions.
 */
public final class ResourceAccess {
  public static final String READ = "resource.packages.read";
  public static final String UPLOAD = "resource.packages.upload";

  record AuthorSnapshot(String name, String department) {}

  AuthorSnapshot authorSnapshot(AurigaPrincipal actor, Reader reader) {
    return new AuthorSnapshot(
        member(reader.tenant(), reader.account()).getDisplayName(), inputs.authorDepartment(actor));
  }

  Optional<String> displayName(UUID tenant, UUID account) {
    if (tenant == null || account == null) return Optional.empty();
    try {
      String name = member(tenant, account).getDisplayName();
      return name != null && !name.isBlank() ? Optional.of(name) : Optional.empty();
    } catch (RuntimeException e) {
      return Optional.empty();
    }
  }

  private final ResourceAccessInputs inputs;
  private final auriga.iam.api.DevicePermissionAuthority devices;

  public ResourceAccess(ResourceAccessInputs inputs) {
    this(inputs, (tenant, account, device, permission) -> false);
  }

  public ResourceAccess(
      ResourceAccessInputs inputs, auriga.iam.api.DevicePermissionAuthority devices) {
    this.inputs = inputs;
    this.devices = devices;
  }

  public record Reader(
      UUID tenant, UUID account, boolean external, Set<UUID> groups, boolean personalPlugins) {}

  public Reader resolve(AurigaPrincipal principal) {
    return resolve(principal, READ);
  }

  public Reader writer(AurigaPrincipal principal) {
    if (principal == null) throw new AurigaException(CommonErrors.UNAUTHENTICATED);
    if (principal.type() != PrincipalType.USER
        || (principal.context() != auriga.common.security.TokenContext.PORTAL
            && principal.context() != auriga.common.security.TokenContext.ADMIN))
      throw new AurigaException(CommonErrors.FORBIDDEN);
    return resolve(principal, UPLOAD);
  }

  public Reader member(AurigaPrincipal principal) {
    if (principal == null) throw new AurigaException(CommonErrors.UNAUTHENTICATED);
    boolean device =
        principal.type() == PrincipalType.DEVICE
            && principal.context() == auriga.common.security.TokenContext.DEVICE
            && principal.deviceId() != null;
    boolean user =
        principal.type() == PrincipalType.USER
            && (principal.context() == auriga.common.security.TokenContext.PORTAL
                || principal.context() == auriga.common.security.TokenContext.ADMIN);
    if (!device && !user) throw new AurigaException(CommonErrors.FORBIDDEN);
    return resolve(principal, READ);
  }

  Optional<UUID> installationInstance(AurigaPrincipal principal) {
    if (principal.type() != PrincipalType.DEVICE) return Optional.empty();
    return installationInstance(principal.tenantId(), principal.subject(), principal.deviceId());
  }

  private Optional<UUID> installationInstance(UUID tenant, String owner, String device) {
    try {
      return devices.installationInstance(tenant, owner, device);
    } catch (UnsupportedOperationException unavailable) {
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    }
  }

  boolean currentInstallationDevice(Reader owner, UUID device, UUID instance) {
    String account = owner.account().toString(), did = device.toString();
    if (!devices.allowed(owner.tenant(), account, did, READ)) return false;
    try {
      return installationInstance(owner.tenant(), account, did)
          .filter(instance::equals)
          .isPresent();
    } catch (AurigaException e) {
      if (e.errorCode().equals(CommonErrors.FORBIDDEN)) return false;
      throw e;
    }
  }

  public Reader administrator(AurigaPrincipal principal, String permission) {
    if (principal == null) throw new AurigaException(CommonErrors.UNAUTHENTICATED);
    if (principal.type() != PrincipalType.USER
        || principal.context() != auriga.common.security.TokenContext.ADMIN)
      throw new AurigaException(CommonErrors.FORBIDDEN);
    return resolve(principal, permission);
  }

  public Reader memberWriter(AurigaPrincipal principal, String permission) {
    if (principal == null) throw new AurigaException(CommonErrors.UNAUTHENTICATED);
    if (principal.type() != PrincipalType.USER
        || (principal.context() != auriga.common.security.TokenContext.ADMIN
            && principal.context() != auriga.common.security.TokenContext.PORTAL))
      throw new AurigaException(CommonErrors.FORBIDDEN);
    return resolve(principal, permission);
  }

  Reader catalogWriter(
      AurigaPrincipal principal, String permission, java.time.Instant now, boolean stepUp) {
    if (principal != null && principal.type() == PrincipalType.DEVICE)
      return desktopContributor(principal);
    var reader = memberWriter(principal, permission);
    if (stepUp
        && !principal.isAdminContext()
        && !principal.steppedUpWithin(java.time.Duration.ofMinutes(10), now))
      throw AurigaException.of(CommonErrors.STEP_UP_REQUIRED, "maxAgeSeconds", 600L);
    return reader;
  }

  Reader desktopContributor(AurigaPrincipal principal) {
    if (principal == null) throw new AurigaException(CommonErrors.UNAUTHENTICATED);
    UUID tenant = principal.tenantId();
    if (principal.type() != PrincipalType.DEVICE
        || principal.context() != auriga.common.security.TokenContext.DEVICE
        || !tenant.equals(TenantContext.require())
        || principal.deviceId() == null
        || !devices.desktopSessionActive(tenant, principal.subject(), principal.deviceId()))
      throw new AurigaException(CommonErrors.FORBIDDEN);
    UUID account = UUID.fromString(principal.subject());
    try {
      var member = member(tenant, account);
      if (!member.getTenantId().equals(tenant.toString())
          || !member.getAccountId().equals(account.toString())
          || member.getStatus() != MemberStatus.MEMBER_STATUS_ACTIVE
          || member.getMemberType() != MemberType.MEMBER_TYPE_MEMBER)
        throw new AurigaException(CommonErrors.FORBIDDEN);
      return new Reader(
          tenant,
          account,
          false,
          member.getGroupIdsList().stream()
              .map(UUID::fromString)
              .collect(Collectors.toUnmodifiableSet()),
          false);
    } catch (AurigaException e) {
      if (e.errorCode().equals(CommonErrors.NOT_FOUND))
        throw new AurigaException(CommonErrors.FORBIDDEN);
      throw e;
    } catch (RuntimeException e) {
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    }
  }

  public Reader owner(AurigaPrincipal principal) {
    var reader = administrator(principal, "resource.levels.admin");
    if (!member(reader.tenant(), reader.account()).getRoleKeysList().contains("owner"))
      throw new AurigaException(CommonErrors.FORBIDDEN);
    return reader;
  }

  public Reader target(UUID tenant, UUID account) {
    return target(tenant, account, READ);
  }

  public Reader target(UUID tenant, UUID account, String permission) {
    return target(tenant, account, permission, false);
  }

  /** Null means Org confirmed that this active market owner has lost read permission. */
  Reader targetForPendingMarket(UUID tenant, UUID account) {
    return target(tenant, account, READ, true);
  }

  private Reader target(UUID tenant, UUID account, String permission, boolean confirmReadLoss) {
    try {
      var member = member(tenant, account);
      var permissions = inputs.permissions(tenant, account);
      if (!member.getTenantId().equals(tenant.toString())
          || !member.getAccountId().equals(account.toString())
          || member.getStatus() != MemberStatus.MEMBER_STATUS_ACTIVE
          || permissions.getStatus() != MemberStatus.MEMBER_STATUS_ACTIVE
          || (member.getMemberType() != MemberType.MEMBER_TYPE_MEMBER
              && member.getMemberType() != MemberType.MEMBER_TYPE_EXTERNAL)
          || member.getMemberType() != permissions.getMemberType())
        throw new AurigaException(CommonErrors.FORBIDDEN);
      if (!permissions.getPermissionsList().contains(permission)) {
        if (confirmReadLoss) return null;
        throw new AurigaException(CommonErrors.FORBIDDEN);
      }
      return new Reader(
          tenant,
          account,
          member.getMemberType() == MemberType.MEMBER_TYPE_EXTERNAL,
          member.getGroupIdsList().stream()
              .map(UUID::fromString)
              .collect(Collectors.toUnmodifiableSet()),
          inputs.allowPersonalPlugins(tenant));
    } catch (AurigaException e) {
      throw e;
    } catch (RuntimeException e) {
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    }
  }

  public boolean groupExists(UUID tenant, UUID group) {
    return inputs.groupExists(tenant, group);
  }

  private Reader resolve(AurigaPrincipal principal, String permission) {
    if (principal == null) throw new AurigaException(CommonErrors.UNAUTHENTICATED);
    if (!principal.tenantId().equals(TenantContext.require())
        || !principal.hasPermission(permission)
        || (principal.type() != PrincipalType.USER && principal.type() != PrincipalType.DEVICE))
      throw new AurigaException(CommonErrors.FORBIDDEN);
    UUID account;
    try {
      account = UUID.fromString(principal.subject());
    } catch (IllegalArgumentException e) {
      throw new AurigaException(CommonErrors.FORBIDDEN);
    }
    if (principal.type() == PrincipalType.DEVICE
        && (principal.context() != auriga.common.security.TokenContext.DEVICE
            || principal.deviceId() == null
            || !devices.allowed(
                principal.tenantId(), principal.subject(), principal.deviceId(), permission)))
      throw new AurigaException(CommonErrors.FORBIDDEN);
    try {
      var member = member(principal.tenantId(), account);
      var permissions = inputs.permissions(principal.tenantId(), account);
      if (!member.getTenantId().equals(principal.tenantId().toString())
          || !member.getAccountId().equals(account.toString())
          || member.getStatus() != MemberStatus.MEMBER_STATUS_ACTIVE
          || permissions.getStatus() != MemberStatus.MEMBER_STATUS_ACTIVE
          || !permissions.getPermissionsList().contains(permission)
          || (member.getMemberType() != MemberType.MEMBER_TYPE_MEMBER
              && member.getMemberType() != MemberType.MEMBER_TYPE_EXTERNAL)
          || member.getMemberType() != permissions.getMemberType())
        throw new AurigaException(CommonErrors.FORBIDDEN);
      return new Reader(
          principal.tenantId(),
          account,
          member.getMemberType() == MemberType.MEMBER_TYPE_EXTERNAL,
          member.getGroupIdsList().stream()
              .map(UUID::fromString)
              .collect(Collectors.toUnmodifiableSet()),
          inputs.allowPersonalPlugins(principal.tenantId()));
    } catch (AurigaException e) {
      if (e.errorCode().equals(CommonErrors.FORBIDDEN))
        throw new AurigaException(CommonErrors.FORBIDDEN);
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    } catch (RuntimeException e) {
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    }
  }

  private auriga.contracts.org.v1.Member member(UUID tenant, UUID account) {
    try {
      return inputs.member(tenant, account);
    } catch (AurigaException e) {
      if (e.errorCode().httpStatus() == 404) throw new AurigaException(CommonErrors.FORBIDDEN);
      throw e;
    }
  }
}
