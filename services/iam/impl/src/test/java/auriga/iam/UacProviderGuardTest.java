package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import auriga.common.audit.AuditLogger;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.iam.api.IdentityConnector;
import java.time.Clock;
import java.util.List;
import java.util.UUID;
import java.util.function.Supplier;
import org.junit.jupiter.api.Test;
import tools.jackson.databind.node.ObjectNode;

class UacProviderGuardTest {
  @Test
  void genericCreateOnlyAllowsDisabledUnconfiguredUacPlaceholder() {
    TenantContext.run(
        UUID.randomUUID(),
        () -> {
          var store = mock(IamStore.class);
          var registry = mock(ConnectorRegistry.class);
          var connector = mock(IdentityConnector.class);
          when(registry.get("uac")).thenReturn(connector);
          when(connector.descriptor()).thenReturn(Json.object("configSchema", Json.object()));
          when(store.tx(any())).thenAnswer(i -> ((Supplier<?>) i.getArgument(0)).get());
          when(store.insert(eq("idp_provider"), anyString(), any(), isNull()))
              .thenAnswer(i -> i.getArgument(2));
          var manager =
              new ProviderManager(
                  store,
                  registry,
                  new Secrets(Secrets.random().replace('-', '+').replace('_', '/')),
                  mock(AuditLogger.class),
                  Clock.systemUTC(),
                  "https://portal.example.com");
          for (ObjectNode input :
              List.of(
                  Json.object("connectorType", "uac", "config", Json.object(), "enabled", true),
                  Json.object(
                      "connectorType",
                      "uac",
                      "config",
                      Json.object("baseUrl", "https://idp.example.com")),
                  Json.object("connectorType", "uac"),
                  Json.object(
                      "connectorType", "uac", "config", Json.object(), "enabled", "false"))) {
            assertEquals(
                "common.validation_failed",
                assertThrows(AurigaException.class, () -> manager.create(input))
                    .errorCode()
                    .code());
          }
          verify(store, never()).insert(anyString(), anyString(), any(), any());
          var result =
              manager.create(
                  Json.object(
                      "connectorType", "uac", "config", Json.object(), "display", Json.object()));
          assertFalse(result.path("enabled").asBoolean(true));
          verify(registry, never()).validate(anyString(), any());
        });
  }

  @Test
  void genericPatchRejectsEvenFalseEmptyAndNullWritesBeforeAnyMutation() {
    TenantContext.run(
        UUID.randomUUID(),
        () -> {
          var store = mock(IamStore.class);
          when(store.tx(any())).thenAnswer(i -> ((Supplier<?>) i.getArgument(0)).get());
          var row = Json.object("providerId", UUID.randomUUID().toString(), "connectorType", "uac");
          when(store.get(eq("idp_provider"), anyString(), eq(true))).thenReturn(row);
          var manager =
              new ProviderManager(
                  store,
                  mock(ConnectorRegistry.class),
                  null,
                  mock(AuditLogger.class),
                  Clock.systemUTC(),
                  "https://portal.example.com");
          for (var patch :
              List.of(
                  Json.object("enabled", false),
                  Json.object("enabled", (Object) null),
                  Json.object("config", Json.object()),
                  Json.object("config", (Object) null),
                  Json.object("display", Json.object(), "config", Json.object())))
            assertEquals(
                "common.validation_failed",
                assertThrows(
                        AurigaException.class,
                        () -> manager.update(row.path("providerId").asString(), patch))
                    .errorCode()
                    .code());
          assertFalse(row.has("display"));
          verify(store, never()).save(anyString(), any());
        });
  }

  @Test
  void httpCreateKeepsOmittedEnabledDistinctFromExplicitTrue() {
    var manager = mock(ProviderManager.class);
    when(manager.create(any())).thenAnswer(i -> i.getArgument(0));
    var controller = new IamAdminController(manager, null, null);
    var input =
        Json.object("connectorType", "uac", "display", Json.object(), "config", Json.object());
    assertFalse(controller.create(input).has("enabled"));
    input.put("enabled", true);
    assertTrue(controller.create(input).path("enabled").asBoolean());
    input.put("enabled", "true");
    assertEquals(
        "common.validation_failed",
        assertThrows(AurigaException.class, () -> controller.create(input)).errorCode().code());
    input.remove("enabled");
    input.set("display", Json.object("order", Json.object("invalid", true)));
    assertEquals(
        "common.validation_failed",
        assertThrows(AurigaException.class, () -> controller.create(input)).errorCode().code());
  }
}
