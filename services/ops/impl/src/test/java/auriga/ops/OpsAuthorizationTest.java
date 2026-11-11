package auriga.ops;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.error.AurigaException;
import auriga.common.security.*;
import java.time.*;
import java.util.*;
import org.junit.jupiter.api.Test;

class OpsAuthorizationTest {
  @Test
  void requiresAdminPermissionAndRecentStepUp() throws Exception {
    var method = OpsController.class.getMethod("test", UUID.class);
    var rules = AuthorizationRules.of(method, OpsController.class);
    var clock = Clock.systemUTC();
    assertEquals(
        "common.unauthenticated",
        assertThrows(AurigaException.class, () -> rules.check(null, clock)).errorCode().code());
    var perms = Set.of("ops.notifications.write");
    assertEquals(
        "common.forbidden",
        assertThrows(
                AurigaException.class,
                () -> rules.check(principal(TokenContext.PORTAL, perms, Instant.now()), clock))
            .errorCode()
            .code());
    assertEquals(
        "common.forbidden",
        assertThrows(
                AurigaException.class,
                () -> rules.check(principal(TokenContext.ADMIN, Set.of(), Instant.now()), clock))
            .errorCode()
            .code());
    assertEquals(
        "iam.step_up_required",
        assertThrows(
                AurigaException.class,
                () -> rules.check(principal(TokenContext.ADMIN, perms, null), clock))
            .errorCode()
            .code());
    assertDoesNotThrow(
        () -> rules.check(principal(TokenContext.ADMIN, perms, Instant.now()), clock));
  }

  private AurigaPrincipal principal(TokenContext context, Set<String> permissions, Instant stepup) {
    return new AurigaPrincipal(
        "alice",
        UUID.randomUUID(),
        PrincipalType.USER,
        context,
        "session",
        null,
        "member",
        List.of(),
        permissions,
        Instant.now(),
        stepup,
        "token");
  }
}
