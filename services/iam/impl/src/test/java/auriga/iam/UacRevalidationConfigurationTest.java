package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;

import java.util.List;
import org.junit.jupiter.api.Test;
import org.springframework.mock.env.MockEnvironment;

class UacRevalidationConfigurationTest {
  @Test
  void replicasUseSameRevisionAndChangedConfigurationGetsDifferentRevision() {
    var env = new MockEnvironment();
    var first = new UacRevalidationConfiguration(env);
    var second = new UacRevalidationConfiguration(env);
    assertEquals(first.policy(), second.policy());
    first.policy().put("intervalSeconds", 1);
    assertEquals(7200, first.policy().path("intervalSeconds").asInt());
    var changed =
        new UacRevalidationConfiguration(
            env.withProperty("auriga.iam.uac.revalidation.interval-seconds", "3600"));
    assertNotEquals(first.policy().path("configRevision"), changed.policy().path("configRevision"));
    new ContractSchemas()
        .validate("auth/uac-control.schema.json#/$defs/UacRevalidationPolicy", changed.policy());
    assertThrows(
        IllegalArgumentException.class,
        () ->
            new IamRuntimeConfiguration()
                .iamUacRevalidationConfiguration(
                    new MockEnvironment()
                        .withProperty("auriga.iam.uac.revalidation.strategy", "invalid")));
  }

  @Test
  void defaultsAndInclusiveBoundariesMatchFrozenContract() {
    var defaults = UacRevalidationConfiguration.read(new MockEnvironment());
    assertEquals("token-verify", defaults.path("strategy").asString());
    assertEquals(7200, defaults.path("intervalSeconds").asInt());
    assertEquals(30, defaults.path("retryIntervalSeconds").asInt());
    assertEquals(3, defaults.path("maxRetries").asInt());
    new ContractSchemas()
        .validate("auth/uac-control.schema.json#/$defs/UacRevalidationPolicy", defaults);
    for (var bounds : List.of(new int[] {60, 5, 0}, new int[] {86400, 300, 10})) {
      var env =
          new MockEnvironment()
              .withProperty("auriga.iam.uac.revalidation.strategy", "department")
              .withProperty("auriga.iam.uac.revalidation.interval-seconds", "" + bounds[0])
              .withProperty("auriga.iam.uac.revalidation.retry-interval-seconds", "" + bounds[1])
              .withProperty("auriga.iam.uac.revalidation.max-retries", "" + bounds[2]);
      assertEquals(
          "department", UacRevalidationConfiguration.read(env).path("strategy").asString());
    }
  }

  @Test
  void invalidValuesExposeOnlyConfigurationKey() {
    for (String name : List.of("interval-seconds", "retry-interval-seconds", "max-retries")) {
      for (String raw : List.of("", " ", "-1", "1.5", "NaN", "1e3", "2147483648", "secret-value")) {
        var env = new MockEnvironment().withProperty("auriga.iam.uac.revalidation." + name, raw);
        var error =
            assertThrows(
                IllegalArgumentException.class, () -> UacRevalidationConfiguration.read(env));
        assertTrue(error.getMessage().endsWith(name));
        assertFalse(error.getMessage().contains("secret-value"));
      }
    }
    for (var invalid :
        List.of(
            new String[] {"interval-seconds", "59"},
            new String[] {"interval-seconds", "86401"},
            new String[] {"retry-interval-seconds", "4"},
            new String[] {"retry-interval-seconds", "301"},
            new String[] {"max-retries", "11"},
            new String[] {"strategy", "unknown"}))
      assertThrows(
          IllegalArgumentException.class,
          () ->
              UacRevalidationConfiguration.read(
                  new MockEnvironment()
                      .withProperty("auriga.iam.uac.revalidation." + invalid[0], invalid[1])));
  }
}
