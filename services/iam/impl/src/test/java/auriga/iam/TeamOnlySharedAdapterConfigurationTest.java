package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.error.AurigaException;
import auriga.contracts.iam.v1.ExecuteSharedOutboundRequest;
import java.util.List;
import org.junit.jupiter.api.Test;

class TeamOnlySharedAdapterConfigurationTest {
  @Test
  void emptyPrivateAdapterInitializesButCannotApproveACompanyDestination() {
    var adapter = new UacConfiguredAdapter(Json.object(), false);
    assertEquals("uac", adapter.descriptor().path("type").asString());
    assertThrows(
        AurigaException.class,
        () ->
            adapter.validateConfig(
                Json.object(
                    "baseUrl", "https://fixture.example.invalid", "systemCode", "fixture")));
  }

  @Test
  void absentSharedOutboundRemainsUnavailableWithoutConsultingTaskAuthority() {
    var tasks = mock(SharedTasks.class);
    var outbound = new SharedOutbound(tasks, Json.object().path("sharedOutbound"), false);
    assertThrows(
        AurigaException.class,
        () -> outbound.execute(ExecuteSharedOutboundRequest.getDefaultInstance()));
    verifyNoInteractions(tasks);
  }

  @Test
  void existingPrivateCompanyMappingsAreNotMutatedOrReplacedByEmptyDefaults() {
    var settings =
        Json.object(
            "allowedDestinations",
            List.of(
                Json.object(
                    "origin",
                    "https://fixture.example.invalid",
                    "addresses",
                    List.of("192.0.2.8"))),
            "clientIP",
            "192.0.2.9",
            "qr",
            Json.object("path", "/fixture-qr", "timeoutSeconds", 10),
            "constants",
            Json.object("syntheticPrivate", "retain-this-approved-value"),
            "tokenVerifyUrl",
            "https://fixture.example.invalid/verify",
            "department",
            Json.object("url", "https://fixture.example.invalid/department"));
    var original = settings.deepCopy();
    assertNotNull(new UacConfiguredAdapter(settings, false));
    assertNotNull(
        new SharedOutbound(mock(SharedTasks.class), settings.path("sharedOutbound"), false));
    assertEquals(original, settings);
  }
}
