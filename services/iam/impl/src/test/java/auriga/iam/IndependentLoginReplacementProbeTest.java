package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.audit.AuditLogger;
import auriga.common.events.EventPublisher;
import auriga.iam.api.IdentityConnector;
import auriga.iam.api.IdentityPolicyHooks;
import java.time.*;
import java.util.*;
import java.util.concurrent.*;
import org.junit.jupiter.api.Test;

class IndependentLoginReplacementProbeTest extends AdminCsrfPostgresTest {
  @Test
  void twoAlreadyInFlightReplacementsOfSameCookieHaveOnlyOneSuccessor() throws Exception {
    var secrets = new Secrets(Base64.getEncoder().encodeToString(new byte[32]));
    var registry = mock(ConnectorRegistry.class);
    var connector = mock(IdentityConnector.class);
    when(registry.get("review-form")).thenReturn(connector);
    when(registry.result(any())).thenAnswer(i -> i.getArgument(0));
    when(connector.begin(any(), any()))
        .thenReturn(
            Json.object(
                "state",
                Json.object(),
                "step",
                Json.object(
                    "type",
                    "form",
                    "formId",
                    "review",
                    "title",
                    Json.object("en", "Review"),
                    "fields",
                    List.of(
                        Json.object(
                            "name",
                            "username",
                            "kind",
                            "text",
                            "label",
                            Json.object("en", "Username"))))));
    store.tenant(
        tenant,
        () -> {
          var p = store.get("idp_provider", provider.toString(), true);
          p.put("enabled", true);
          p.put("connectorType", "review-form");
          p.put("config", secrets.encrypt("{}", "provider:" + tenant + ":" + provider));
          p.set("display", Json.object("name", "review"));
          store.save("idp_provider", p);
          return null;
        });
    var login =
        new LoginManager(
            store,
            secrets,
            schemas,
            registry,
            mock(EmergencyAccounts.class),
            mock(TeamAccounts.class),
            sessions,
            mock(IdentityPolicyHooks.class),
            mock(EventPublisher.class),
            mock(AuditLogger.class),
            time,
            "https://portal.example.com");
    var request = Json.object("providerId", provider, "purpose", "login");
    String origin = "https://admin.example.com";
    var first =
        store.tenant(
            tenant, () -> login.begin(request, "admin", "192.0.2.8", "review", origin, null));
    var start = new CountDownLatch(1);
    try (var threads = Executors.newVirtualThreadPerTaskExecutor()) {
      Callable<LoginManager.Result> replace =
          () -> {
            assertTrue(start.await(5, TimeUnit.SECONDS));
            return store.tenant(
                tenant,
                () ->
                    login.begin(
                        request, "admin", "192.0.2.8", "review", origin, first.bindingCookie()));
          };
      var a = threads.submit(replace);
      var b = threads.submit(replace);
      start.countDown();
      var successors = new ArrayList<LoginManager.Result>();
      int rejected = 0;
      for (var future : List.of(a, b)) {
        try {
          successors.add(future.get(10, TimeUnit.SECONDS));
        } catch (ExecutionException e) {
          var error = assertInstanceOf(auriga.common.error.AurigaException.class, e.getCause());
          assertEquals("iam.login_tx_finished", error.errorCode().code());
          rejected++;
        }
      }
      assertEquals(1, successors.size());
      assertEquals(1, rejected);
      var second = successors.getFirst();
      var statuses =
          store.tenant(
              tenant,
              () ->
                  List.of(
                      login
                          .get(first.transaction().path("id").asString(), first.bindingCookie())
                          .transaction()
                          .path("status")
                          .asString(),
                      login
                          .get(second.transaction().path("id").asString(), second.bindingCookie())
                          .transaction()
                          .path("status")
                          .asString()));
      System.out.println(
          "REPAIRED_REPLACEMENT_PROBE statuses=" + statuses + " rejected=" + rejected);
      assertEquals(List.of("cancelled", "pending"), statuses);
      error(
          "iam.login_tx_finished",
          () ->
              store.tenant(
                  tenant,
                  () ->
                      login.begin(
                          request, "admin", "192.0.2.8", "review", origin, first.bindingCookie())));
      store.tenant(
          tenant,
          () -> {
            var portal =
                login.begin(
                    request,
                    "portal",
                    "192.0.2.8",
                    "review",
                    "https://portal.example.com",
                    first.bindingCookie());
            assertEquals("pending", portal.transaction().path("status").asString());
            login.cancel(second.transaction().path("id").asString(), second.bindingCookie());
            var restarted =
                login.begin(
                    request, "admin", "192.0.2.8", "review", origin, second.bindingCookie());
            assertEquals("pending", restarted.transaction().path("status").asString());
            assertEquals(
                "cancelled",
                login
                    .get(second.transaction().path("id").asString(), second.bindingCookie())
                    .transaction()
                    .path("status")
                    .asString());
            assertEquals(
                "pending",
                login
                    .get(portal.transaction().path("id").asString(), portal.bindingCookie())
                    .transaction()
                    .path("status")
                    .asString());
            // Completed transactions can start a new flow once, but never reuse an older
            // generation.
            var completed =
                store.get("login_tx", restarted.transaction().path("id").asString(), true);
            completed.put("status", "completed");
            store.save("login_tx", completed);
            var afterCompletion =
                login.begin(
                    request, "admin", "192.0.2.8", "review", origin, restarted.bindingCookie());
            assertEquals("pending", afterCompletion.transaction().path("status").asString());
            assertEquals(
                "pending",
                login
                    .begin(request, "admin", "192.0.2.8", "other-browser", origin, null)
                    .transaction()
                    .path("status")
                    .asString());
            return null;
          });
      error(
          "iam.login_tx_finished",
          () ->
              store.tenant(
                  tenant,
                  () ->
                      login.begin(
                          request,
                          "admin",
                          "192.0.2.8",
                          "review",
                          origin,
                          second.bindingCookie())));
      for (var stale : List.of(first, second)) {
        error(
            "iam.login_tx_finished",
            () ->
                store.tenant(
                    tenant,
                    () ->
                        login.advance(
                            stale.transaction().path("id").asString(),
                            stale.bindingCookie(),
                            Json.object("type", "callback", "params", Json.object()),
                            true)));
      }
      assertEquals(1, store.tenant(tenant, () -> store.all("session").size()));
      org.mockito.Mockito.verify(connector, never()).advance(any(), any(), any());
    }
  }
}
