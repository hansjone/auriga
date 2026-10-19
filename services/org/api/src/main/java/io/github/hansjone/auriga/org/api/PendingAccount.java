package io.github.hansjone.auriga.org.api;

import java.util.Optional;
import java.util.UUID;

/**
 * Bridges iam {@code IdentityPolicyHooks#consumeInvitation} (which does not pass account id) to org
 * redemption. Callers that know the pending account id (tests, or a future iam one-line profile
 * enrichment) bind it here before invoking the hook.
 */
public final class PendingAccount {
  private static final ScopedValue<UUID> ACCOUNT = ScopedValue.newInstance();

  private PendingAccount() {}

  public static <T, X extends Throwable> T call(UUID accountId, ScopedValue.CallableOp<T, X> op)
      throws X {
    return ScopedValue.where(ACCOUNT, accountId).call(op);
  }

  public static Optional<UUID> current() {
    return ACCOUNT.isBound() ? Optional.of(ACCOUNT.get()) : Optional.empty();
  }
}
