package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.context.TenantContext;
import auriga.common.error.*;
import auriga.testsupport.PostgresExtension;
import java.util.*;
import java.util.function.Supplier;
import org.junit.jupiter.api.*;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.mock.web.*;
import tools.jackson.databind.node.ObjectNode;

@ExtendWith(PostgresExtension.class)
class DesktopDeviceReportsPostgresTest {
  NativeConnectPostgresTest fixture;
  DesktopDeviceReportController controller;
  String token, device;

  @BeforeAll
  static void migrate() {
    NativeConnectPostgresTest.migrate();
  }

  @BeforeEach
  void setup() {
    fixture = new NativeConnectPostgresTest();
    fixture.setup();
    var requests = mock(RequestIdentity.class);
    when(requests.call(any(), any()))
        .thenAnswer(
            i -> TenantContext.call(fixture.tenant, () -> ((Supplier<?>) i.getArgument(1)).get()));
    controller =
        new DesktopDeviceReportController(
            fixture.oauth,
            requests,
            NativeConnectPostgresTest.store,
            fixture.sessions,
            fixture.clock);
    TenantContext.run(
        fixture.tenant,
        () -> {
          token = fixture.connected().path("access_token").asString();
          device = fixture.oauth.nativeIdentity(token, false).path("deviceId").asString();
        });
  }

  ObjectNode body() {
    return Json.object(
        "installationId",
        "a2fb49ae-6bb0-4629-a7bc-868885134c98",
        "sequence",
        1,
        "clientVersion",
        "0.1.9",
        "channel",
        "stable",
        "lastUpdate",
        Json.object(
            "attemptId",
            "06437656-3150-4de6-9ae9-146a3b241310",
            "targetVersion",
            "0.2.0",
            "result",
            "rolled_back",
            "stage",
            "smoke",
            "reasonCode",
            "desktop.smoke_failed"));
  }

  void send(ObjectNode value) {
    var request = new MockHttpServletRequest();
    request.addHeader("Authorization", "Bearer " + token);
    request.setContentType("application/json");
    request.setContent(Json.MAPPER.writeValueAsBytes(value));
    var response = new MockHttpServletResponse();
    controller.report(request, response);
    assertEquals("no-store", response.getHeader("Cache-Control"));
  }

  ObjectNode row() {
    return TenantContext.call(
        fixture.tenant,
        () ->
            NativeConnectPostgresTest.store.tx(
                () -> NativeConnectPostgresTest.store.get("device", device, false)));
  }

  void denied(String code, Runnable action) {
    assertEquals(code, assertThrows(AurigaException.class, action::run).errorCode().code());
  }

  @Test
  void ownReportIsProjectedWithoutSecretsAndRetriesCannotOverwriteNewerFacts() {
    send(body());
    assertEquals("0.1.9", row().path("clientVersion").asString());
    var newer = body().put("sequence", 2).put("clientVersion", "0.2.0");
    newer.remove("lastUpdate");
    send(newer);
    send(body());
    send(newer);
    assertEquals(2, row().path("desktopReport").path("sequence").asInt());
    denied("common.conflict", () -> send(newer.deepCopy().put("clientVersion", "0.3.0")));
    TenantContext.run(
        fixture.tenant,
        () -> {
          var view =
              new IamAdminDevices(NativeConnectPostgresTest.store, fixture.clock, fixture.audit)
                  .get(device);
          assertEquals("0.2.0", view.path("desktopReport").path("clientVersion").asString());
          assertFalse(view.has("refreshHash"));
        });
  }

  @Test
  void selectorsOtherOwnersRevocationAndBrowserTokensCannotWriteDevices() {
    denied(
        "common.validation_failed",
        () -> send(body().put("deviceId", UUID.randomUUID().toString())));
    denied(
        "common.validation_failed",
        () -> send(body().put("accountId", fixture.account.toString())));
    TenantContext.run(
        fixture.tenant,
        () -> {
          var identity =
              fixture
                  .oauth
                  .nativeIdentity(token, false)
                  .put("accountId", UUID.randomUUID().toString());
          denied(
              "iam.invalid_token",
              () ->
                  new DesktopDeviceReports(
                          NativeConnectPostgresTest.store, fixture.sessions, fixture.clock)
                      .record(identity, body()));
          NativeConnectPostgresTest.store.tx(
              () -> {
                fixture.sessions.revokeDevice(row());
                return null;
              });
        });
    denied("iam.invalid_token", () -> send(body()));
    assertFalse(row().has("desktopReport"));
  }

  @Test
  void invalidFieldsAndPrivateDiagnosticsFailBeforePersistence() {
    for (var invalid :
        List.of(
            body().put("clientVersion", "x".repeat(129)),
            body().put("sequence", 1.5),
            body().put("sequence", -1),
            body().put("channel", "other"),
            body().put("installationId", "invalid")))
      denied("common.validation_failed", () -> send(invalid));
    for (String field : List.of("result", "stage", "reasonCode")) {
      var invalid = body();
      ((ObjectNode) invalid.path("lastUpdate")).put(field, "C:/private/token=secret");
      denied("common.validation_failed", () -> send(invalid));
    }
    assertFalse(row().has("desktopReport"));
  }

  @Test
  void deviceRequestsAreRateLimited() {
    for (int i = 0; i < 30; i++) send(body());
    denied("common.rate_limited", () -> send(body()));
  }

  @Test
  void standardDeviceUsesTheSameAuthoritativeSourceAndRejectsLostPermissions() throws Exception {
    StandardDevicePostgresTest.migrate();
    try (var h = new StandardDevicePostgresTest.Harness()) {
      var requests = mock(RequestIdentity.class);
      when(requests.call(any(), any()))
          .thenAnswer(i -> h.f.call(() -> ((Supplier<?>) i.getArgument(1)).get()));
      var api =
          new DesktopDeviceReportController(
              h.oauth, requests, StandardDevicePostgresTest.store, h.f.sessions, h.f.time);
      var issued = h.exchange(h.issue());
      var request = new MockHttpServletRequest();
      request.addHeader("Authorization", "Bearer " + issued.path("access_token").asString());
      request.setContent(Json.MAPPER.writeValueAsBytes(body()));
      api.report(request, new MockHttpServletResponse());
      h.effective.clear();
      request.setContent(Json.MAPPER.writeValueAsBytes(body().put("sequence", 2)));
      denied("common.unauthenticated", () -> api.report(request, new MockHttpServletResponse()));
    }
  }

  @Test
  void browserTokensAndQuerySelectorsAreRejected() {
    var old = token;
    token =
        TenantContext.call(
            fixture.tenant,
            () ->
                NativeConnectPostgresTest.store.tx(
                    () ->
                        fixture.tokens.access(
                            fixture.tenant,
                            fixture.account.toString(),
                            "auriga",
                            "user",
                            "portal",
                            UUID.randomUUID(),
                            null,
                            fixture.clock.instant(),
                            null,
                            "openid")));
    denied("common.unauthenticated", () -> send(body()));
    token = old;
    var request = new MockHttpServletRequest();
    request.addHeader("Authorization", "Bearer " + token);
    request.addParameter("deviceId", UUID.randomUUID().toString());
    request.setContent(Json.MAPPER.writeValueAsBytes(body()));
    denied(
        "common.validation_failed",
        () -> controller.report(request, new MockHttpServletResponse()));
  }
}
