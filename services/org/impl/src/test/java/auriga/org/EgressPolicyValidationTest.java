package auriga.org;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.error.AurigaException;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.ValueSource;

class EgressPolicyValidationTest {
  @ParameterizedTest
  @ValueSource(
      strings = {
        "127.0.0.1",
        "10.0.0.1", // gitleaks:allow synthetic target that must be rejected
        "169.254.169.254",
        "100.64.0.1",
        "::1",
        "0x7f000001",
        "2130706433",
        "metadata.google.internal",
        "localhost",
        "*.internal",
        "example.com:443",
        "https://example.com",
        "*.com"
      })
  void rejectsUnsafeTargets(String domain) {
    var value = OrgJson.object("allowDomains", java.util.List.of(domain));
    var error = assertThrows(AurigaException.class, () -> EgressPolicyValidation.validate(value));
    assertEquals("common.validation_failed", error.errorCode().code());
  }

  @ParameterizedTest
  @ValueSource(
      strings = {
        "{\"allowPrivate\":true}",
        "{\"categories\":[\"unknown\"]}",
        "{\"denyDomains\":null}",
        "{\"allowDomains\":\"example.com\"}"
      })
  void rejectsUnsupportedShape(String raw) {
    assertThrows(
        AurigaException.class, () -> EgressPolicyValidation.validate(OrgJson.MAPPER.readTree(raw)));
  }

  @Test
  void acceptsDomains() {
    EgressPolicyValidation.validate(
        OrgJson.object(
            "allowDomains",
            java.util.List.of("api.example.com", "*.example.org"),
            "denyDomains",
            java.util.List.of("blocked.example.com"),
            "categories",
            java.util.List.of()));
  }
}
