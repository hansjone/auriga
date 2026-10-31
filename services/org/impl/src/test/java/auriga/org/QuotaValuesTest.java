package auriga.org;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.error.AurigaException;
import org.junit.jupiter.api.Test;

class QuotaValuesTest {
  @Test
  void removedModelQuotaCannotBeSilentlyAccepted() {
    var error =
        assertThrows(
            AurigaException.class,
            () -> QuotaValues.validate(OrgJson.object("model", OrgJson.object())));
    assertEquals("common.validation_failed", error.errorCode().code());
  }

  @Test
  void instanceQuotasKeepTheirExistingValidation() {
    assertDoesNotThrow(
        () -> QuotaValues.validate(OrgJson.object("instance", OrgJson.object("maxInstances", 0))));
    assertThrows(
        AurigaException.class,
        () -> QuotaValues.validate(OrgJson.object("instance", OrgJson.object("cpuMillis", 99))));
  }
}
