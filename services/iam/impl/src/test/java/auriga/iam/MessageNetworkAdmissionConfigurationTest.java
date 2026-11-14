package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;

import auriga.contracts.msgnet.model.IsolationRecord;
import auriga.iam.api.MessageNetworkAdmission;
import java.util.UUID;
import java.util.concurrent.atomic.AtomicReference;
import org.junit.jupiter.api.Test;
import org.springframework.context.annotation.AnnotationConfigApplicationContext;

class MessageNetworkAdmissionConfigurationTest {
  @Test
  void absentAndUnavailableLedgersDenyAndNoPositiveReadinessIsCached() {
    UUID tenant = UUID.randomUUID();
    try (var context =
        new AnnotationConfigApplicationContext(MessageNetworkAdmissionConfiguration.class)) {
      assertFalse(context.getBean(MessageNetworkAdmission.class).ready(tenant, "1", "target"));
    }
    var current = new AtomicReference<IsolationRecord>();
    try (var context = new AnnotationConfigApplicationContext()) {
      context.registerBean(MessageNetworkAdmission.Ledger.class, () -> (t, r, n) -> current.get());
      context.register(MessageNetworkAdmissionConfiguration.class);
      context.refresh();
      var admission = context.getBean(MessageNetworkAdmission.class);
      assertFalse(admission.ready(tenant, "1", "target"));
      var ready =
          new IsolationRecord()
              .tenantId(tenant.toString())
              .realm("1")
              .targetId("target")
              .state(IsolationRecord.StateEnum.READY)
              .identityCompromised(false)
              .newChainReady(true);
      current.set(ready);
      assertTrue(admission.ready(tenant, "1", "target"));
      ready.setIdentityCompromised(true);
      assertFalse(admission.ready(tenant, "1", "target"));
      current.set(null);
      assertFalse(admission.ready(tenant, "1", "target"));
    }
    try (var context = new AnnotationConfigApplicationContext()) {
      context.registerBean(
          MessageNetworkAdmission.Ledger.class,
          () ->
              (t, r, n) -> {
                throw new IllegalStateException("ledger unavailable");
              });
      context.register(MessageNetworkAdmissionConfiguration.class);
      context.refresh();
      assertFalse(context.getBean(MessageNetworkAdmission.class).ready(tenant, "1", "target"));
    }
  }
}
