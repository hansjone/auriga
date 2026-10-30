package auriga.common.observability;

import static org.junit.jupiter.api.Assertions.*;

import java.time.Duration;
import org.junit.jupiter.api.Test;

class SensitiveDataMaskerTest {
  @Test
  void boundsLongUntrustedLogTokens() {
    for (String prefix : new String[] {"", "prefix-", "file.name."})
      assertHidden(prefix + "eyJabcde.abcdefgh.signature", "eyJabcde");
    assertHidden("db-password=secret-regression", "secret-regression");
    SensitiveDataMasker.mask("warmup password=hidden");
    for (String input :
        new String[] {
          "/api/org/v1/invitations/preview/" + "a".repeat(8000),
          "x".repeat(65536),
          "a_".repeat(32768),
          "a.".repeat(32768),
          "eyJ-".repeat(16384),
          "eyJ" + "x".repeat(65000) + ".payload.signature",
          "-----BEGIN PRIVATE KEY-----\n".repeat(2048)
        }) {
      assertTimeout(Duration.ofMillis(50), () -> SensitiveDataMasker.mask(input));
    }
    assertTrue(SensitiveDataMasker.mask("x".repeat(65537)).length() <= 65536);
    assertHidden("AURIGA_" + "A".repeat(8000) + "_PASSWORD=hidden-value", "hidden-value");
  }

  private static void assertHidden(String input, String... secrets) {
    String masked = SensitiveDataMasker.mask(input);
    for (String secret : secrets) {
      assertFalse(masked.contains(secret), "leaked " + secret + " in " + masked);
    }
  }

  @Test
  void masksEveryCookiePair() {
    assertHidden("Cookie: theme=dark; SESSION=abc123secret; csrf=tok999", "abc123secret", "tok999");
    assertHidden("set-cookie: SESSION=abc123secret; Path=/; HttpOnly", "abc123secret");
    assertHidden("{\"cookie\":\"a=1; sid=s3cr3tvalue\",\"x\":1}", "s3cr3tvalue");
    // the rest of a JSON line survives
    assertTrue(SensitiveDataMasker.mask("{\"cookie\":\"a=1; b=2\",\"x\":1}").endsWith(",\"x\":1}"));
  }

  @Test
  void masksQuotedValuesWithSpaces() {
    String masked =
        SensitiveDataMasker.mask("{\"password\": \"correct horse battery\", \"user\":\"bo\"}");
    assertFalse(masked.contains("horse"), masked);
    assertFalse(masked.contains("battery"), masked);
    assertTrue(masked.contains("\"user\":\"bo\""), masked);
    assertHidden("{\"client_secret\":\"with \\\" escaped quote\"}", "escaped");
  }

  @Test
  void masksUrlUserinfoPassword() {
    String masked = SensitiveDataMasker.mask("connect postgres://svc:p4ssw0rd@db:5432/org failed");
    assertFalse(masked.contains("p4ssw0rd"), masked);
    assertTrue(masked.contains("postgres://svc:****@db:5432/org"), masked);
    assertHidden("nats://user:hunter2@nats.internal:4222", "hunter2");
    // URLs without credentials are untouched
    assertEquals(
        "see https://example.com/a@b", SensitiveDataMasker.mask("see https://example.com/a@b"));
  }

  @Test
  void keepsEarlierRules() {
    assertHidden("Authorization: Bearer abcdef123456", "abcdef123456");
    assertHidden("password=hunter2&next=/", "hunter2");
  }
}
