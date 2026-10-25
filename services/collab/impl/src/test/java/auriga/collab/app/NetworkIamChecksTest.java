package auriga.collab.app;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.error.AurigaException;
import auriga.common.persistence.TenantTransactionManager;
import auriga.iam.api.NetworkAuthority;
import auriga.iam.api.NetworkAuthority.*;
import java.util.Map;
import javax.sql.DataSource;
import org.junit.jupiter.api.Test;

class NetworkIamChecksTest {
  final NetworkAuthority iam = mock(NetworkAuthority.class);
  final NetworkIamChecks checks =
      new NetworkIamChecks(iam, new TenantTransactionManager(mock(DataSource.class)));

  @Test
  void unknownRevokedOrUnreachableAuthorityFailsClosed() {
    assertCode("network.unauthorized", () -> checks.active("2", "20"));
    when(iam.active("2", "20")).thenThrow(new IllegalStateException("offline"));
    assertCode("common.unavailable", () -> checks.active("2", "20"));
    assertCode(
        "common.unavailable",
        () ->
            new NetworkIamChecks(null, new TenantTransactionManager(mock(DataSource.class)))
                .active("2", "20"));
  }

  @Test
  void decisionsMustBeCurrentAndLimitsCannotRelaxPlatformCeilings() {
    var request = new Check("2", "10", "20", "1", Sender.AGENT, Operation.FIRST_CONTACT, 1, 1, 1);
    when(iam.check(request))
        .thenReturn(
            new Decision(
                true,
                "allowed",
                Map.of(Limit.FIRST_LOCAL_PENDING, 2L, Limit.FIRST_LOCAL_TARGETS_DAY, 9000L)));
    var decision = checks.check(request);
    assertEquals(2, NetworkIamChecks.limit(decision, Limit.FIRST_LOCAL_PENDING));
    assertEquals(40, NetworkIamChecks.limit(decision, Limit.FIRST_LOCAL_TARGETS_DAY));
    when(iam.check(request)).thenReturn(new Decision(false, "network_denied", Map.of()));
    assertCode("network.unauthorized", () -> checks.check(request));
    verify(iam, times(2)).check(request);
  }

  static void assertCode(String code, Runnable action) {
    assertEquals(code, assertThrows(AurigaException.class, action::run).errorCode().code());
  }

  @Test
  void durableIdempotencyResultRoundTripsRestrictedIdsAndDeadline() {
    var deadline = java.time.Instant.parse("2026-10-25T12:34:56Z");
    var submission =
        new NetworkMailboxService.StoreResult(
            null,
            "request_submitted",
            "request_submitted",
            true,
            java.util.List.of(),
            null,
            java.util.UUID.randomUUID(),
            deadline);
    var json = NetworkJson.JSON.writeValueAsString(submission);
    assertEquals(
        submission, NetworkJson.JSON.readValue(json, NetworkMailboxService.StoreResult.class));
    var request =
        NetworkMailboxService.StoreRequest.firstContact(
            java.util.UUID.randomUUID(), "2", "10", "1", "hello", "20", deadline, false);
    assertDoesNotThrow(() -> NetworkJson.canonical(NetworkJson.JSON.valueToTree(request)));
  }
}
