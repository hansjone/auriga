package auriga.org;

import auriga.common.error.AurigaException;
import auriga.iam.api.IdentityPolicyHooks;
import auriga.org.api.PendingAccount;
import java.util.Optional;
import java.util.UUID;
import tools.jackson.databind.JsonNode;

/**
 * Local-bean invitation redemption for iam login ({@code invite_only}). Prefer {@link
 * PendingAccount} or {@code profile.accountId} until iam passes the pending account id explicitly.
 */
public final class OrgIdentityPolicyHooks implements IdentityPolicyHooks {
  private final OrgService org;
  private final UacOrgPolicy uac;

  public OrgIdentityPolicyHooks(OrgService org) {
    this(org, null);
  }

  OrgIdentityPolicyHooks(OrgService org, UacOrgPolicy uac) {
    this.org = org;
    this.uac = uac;
  }

  @Override
  public Optional<UUID> resolveUacEmployee(UUID provider, String employee) {
    return uac == null ? Optional.empty() : uac.resolve(provider, employee);
  }

  @Override
  public Optional<UacMember> uacMember(UUID account) {
    return uac == null ? Optional.empty() : uac.member(account);
  }

  @Override
  public boolean consumeUacInvitation(String code, UUID provider, String employee, UUID account) {
    return uac != null && uac.consume(code, provider, employee, account);
  }

  @Override
  public boolean validateInvitation(String code, UUID provider) {
    return org.validateInvitation(code, provider);
  }

  @Override
  public boolean consumeInvitation(String code, UUID provider, JsonNode profile) {
    UUID accountId = PendingAccount.current().or(() -> accountIdFromProfile(profile)).orElse(null);
    if (accountId == null || code == null || code.isBlank()) return false;
    try {
      org.redeemInvitation(
          code,
          accountId,
          profile.path("displayName").asString(""),
          profile.path("email").asString(""),
          provider);
      return true;
    } catch (AurigaException e) {
      return false;
    }
  }

  private static Optional<UUID> accountIdFromProfile(JsonNode profile) {
    String raw = profile.path("accountId").asString("");
    if (raw.isBlank()) return Optional.empty();
    try {
      return Optional.of(UUID.fromString(raw));
    } catch (Exception e) {
      return Optional.empty();
    }
  }
}
