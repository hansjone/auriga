package auriga.collab.app;

import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import auriga.common.persistence.TenantTransactionManager;
import auriga.iam.api.NetworkAuthority;
import auriga.iam.api.NetworkAuthority.*;
import java.util.function.Supplier;
import org.springframework.transaction.TransactionDefinition;
import org.springframework.transaction.support.AbstractPlatformTransactionManager;
import org.springframework.transaction.support.TransactionTemplate;

/** Online IAM calls own their transactions, including when the API is an in-process bean. */
final class NetworkIamChecks {
  private final NetworkAuthority iam;
  private final TransactionTemplate outside;

  NetworkIamChecks(NetworkAuthority iam, TenantTransactionManager manager) {
    this.iam = iam;
    // A separate manager suspends collab resources without changing the shared manager's policy.
    var suspension = new TenantTransactionManager(manager.getDataSource());
    suspension.setTransactionSynchronization(
        AbstractPlatformTransactionManager.SYNCHRONIZATION_NEVER);
    outside = new TransactionTemplate(suspension);
    outside.setPropagationBehavior(TransactionDefinition.PROPAGATION_NOT_SUPPORTED);
  }

  void active(String realm, String node) {
    if (!call(() -> iam.active(realm, node))) throw new AurigaException(NetworkErrors.UNAUTHORIZED);
  }

  Decision check(Check request) {
    Decision result = call(() -> iam.check(request));
    if (result == null) throw new AurigaException(CommonErrors.UNAVAILABLE);
    if (!result.allowed()) throw new AurigaException(NetworkErrors.UNAUTHORIZED);
    return result;
  }

  static long limit(Decision decision, Limit limit) {
    return Math.max(
        0,
        Math.min(
            limit.defaultValue(), decision.limits().getOrDefault(limit, limit.defaultValue())));
  }

  private <T> T call(Supplier<T> action) {
    if (iam == null) throw new AurigaException(CommonErrors.UNAVAILABLE);
    try {
      return outside.execute(status -> action.get());
    } catch (RuntimeException e) {
      throw new AurigaException(CommonErrors.UNAVAILABLE);
    }
  }
}
