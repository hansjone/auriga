package auriga.iam.api;

import auriga.contracts.msgnet.model.IsolationRecord;
import java.util.UUID;

/**
 * Authoritative message-only admission. Unknown, offline, unconfirmed and compromised targets deny.
 */
@FunctionalInterface
public interface MessageNetworkAdmission {
  /** Implementations read the control ledger; a device credential never implies readiness. */
  boolean ready(UUID tenant, String realm, String target);

  @FunctionalInterface
  interface Ledger {
    IsolationRecord read(UUID tenant, String realm, String target);
  }

  /** Adapt C's authoritative ledger without trusting receipt fields as readiness. */
  static MessageNetworkAdmission fromLedger(Ledger ledger) {
    java.util.Objects.requireNonNull(ledger);
    return (tenant, realm, target) -> {
      IsolationRecord record = ledger.read(tenant, realm, target);
      return record != null
          && tenant.toString().equals(record.getTenantId())
          && realm.equals(record.getRealm())
          && target.equals(record.getTargetId())
          && record.getState() == IsolationRecord.StateEnum.READY
          && Boolean.FALSE.equals(record.getIdentityCompromised())
          && Boolean.TRUE.equals(record.getNewChainReady());
    };
  }
}
