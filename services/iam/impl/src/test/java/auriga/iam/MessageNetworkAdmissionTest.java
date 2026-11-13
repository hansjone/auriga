package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;

import auriga.contracts.msgnet.model.IsolationRecord;
import auriga.iam.api.MessageNetworkAdmission;
import java.util.UUID;
import org.junit.jupiter.api.Test;

class MessageNetworkAdmissionTest {
  @Test
  void onlyExactReadyUncompromisedLedgerEntryAdmits() {
    UUID tenant = UUID.randomUUID();
    var record =
        new IsolationRecord()
            .tenantId(tenant.toString())
            .realm("1")
            .targetId("node")
            .state(IsolationRecord.StateEnum.READY)
            .identityCompromised(false)
            .newChainReady(true);
    var admission = MessageNetworkAdmission.fromLedger((t, r, n) -> record);
    assertTrue(admission.ready(tenant, "1", "node"));
    assertFalse(admission.ready(UUID.randomUUID(), "1", "node"));
    assertFalse(admission.ready(tenant, "2", "node"));
    assertFalse(admission.ready(tenant, "1", "other"));
    record.setIdentityCompromised(true);
    assertFalse(admission.ready(tenant, "1", "node"));
    record.setIdentityCompromised(false);
    record.setNewChainReady(false);
    assertFalse(admission.ready(tenant, "1", "node"));
    record.setNewChainReady(true);
    for (var state :
        new IsolationRecord.StateEnum[] {
          IsolationRecord.StateEnum.QUARANTINED, IsolationRecord.StateEnum.CONFIRMED
        }) {
      record.setState(state);
      assertFalse(admission.ready(tenant, "1", "node"));
    }
    assertFalse(MessageNetworkAdmission.fromLedger((t, r, n) -> null).ready(tenant, "1", "node"));
  }
}
