package io.github.hansjone.auriga.common.observability;

import static org.junit.jupiter.api.Assertions.*;

import org.junit.jupiter.api.Test;

class SensitiveDataMaskerTest {
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
