package auriga.collab.network.local;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.collab.api.NetworkSourceAuthority;
import auriga.collab.api.NetworkSourceAuthority.Target;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import java.time.Clock;
import java.time.Instant;
import java.time.ZoneOffset;
import java.util.UUID;
import org.junit.jupiter.api.Test;

class LocalRegistryAuthorityTest {
  private static final Instant NOW = Instant.parse("2026-10-01T00:00:00Z");
  private static final Clock CLOCK = Clock.fixed(NOW, ZoneOffset.UTC);
  private final UUID tenant = UUID.randomUUID();
  private final NetworkSourceAuthority authority = mock(NetworkSourceAuthority.class);

  @Test
  void validTrustedTargetKeepsItsIdentity() {
    var target = target("2", "20", NOW.plusSeconds(60));
    when(authority.currentTarget(tenant)).thenReturn(target);
    assertSame(target, LocalRegistry.resolveTarget(tenant, authority, CLOCK));
    verify(authority).currentTarget(tenant);
  }

  @Test
  void missingAuthorityIsUnavailable() {
    error("common.unavailable", () -> LocalRegistry.resolveTarget(tenant, null, CLOCK));
  }

  @Test
  void missingOrExpiredTargetNeverFallsBackToAnIdentity() {
    error("network.unauthorized", () -> LocalRegistry.resolveTarget(tenant, authority, CLOCK));
    when(authority.currentTarget(tenant)).thenReturn(target("2", "20", NOW));
    error("network.unauthorized", () -> LocalRegistry.resolveTarget(tenant, authority, CLOCK));
    when(authority.currentTarget(tenant)).thenReturn(target("2", "20", null));
    error("network.unauthorized", () -> LocalRegistry.resolveTarget(tenant, authority, CLOCK));
  }

  @Test
  void runtimeFailureBecomesUnavailableWithoutInternalDetails() {
    when(authority.currentTarget(tenant))
        .thenThrow(new IllegalStateException("offline-private-detail"));
    var failure =
        error("common.unavailable", () -> LocalRegistry.resolveTarget(tenant, authority, CLOCK));
    assertNull(failure.getCause());
    assertTrue(failure.params().isEmpty());
    assertFalse(failure.toString().contains("offline-private-detail"));
  }

  @Test
  void controlledDenialIsPreserved() {
    var denied = new AurigaException(CommonErrors.FORBIDDEN);
    when(authority.currentTarget(tenant)).thenThrow(denied);
    assertSame(
        denied,
        error("common.forbidden", () -> LocalRegistry.resolveTarget(tenant, authority, CLOCK)));
  }

  @Test
  void malformedTrustedIdentityIsRejected() {
    for (String id : new String[] {"0", "01", "18446744073709551616", "bad", ""}) {
      when(authority.currentTarget(tenant)).thenReturn(target(id, "20", NOW.plusSeconds(60)));
      error("network.unauthorized", () -> LocalRegistry.resolveTarget(tenant, authority, CLOCK));
      when(authority.currentTarget(tenant)).thenReturn(target("2", id, NOW.plusSeconds(60)));
      error("network.unauthorized", () -> LocalRegistry.resolveTarget(tenant, authority, CLOCK));
    }
  }

  private static Target target(String realm, String node, Instant until) {
    return new Target(realm, node, "incarnation", 1, "nonce", "request", until);
  }

  private static AurigaException error(String code, Runnable work) {
    var failure = assertThrows(AurigaException.class, work::run);
    assertEquals(code, failure.errorCode().code());
    return failure;
  }
}
