package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import auriga.common.autoconfigure.AurigaProperties;
import auriga.common.context.*;
import auriga.common.error.AurigaException;
import auriga.common.security.*;
import auriga.contracts.deploymentconfig.DeploymentAccess;
import java.time.Clock;
import java.util.*;
import java.util.function.Supplier;
import org.junit.jupiter.api.Test;
import org.springframework.mock.web.*;

class UacNavigationBoundaryTest {
  static AurigaPrincipal principal(
      String subject, PrincipalType type, TokenContext context, UUID tenant, String sid) {
    return new AurigaPrincipal(
        subject,
        tenant,
        type,
        context,
        sid,
        null,
        "member",
        List.of(),
        Set.of("iam.providers.write"),
        null,
        null,
        UUID.randomUUID().toString());
  }

  @Test
  void internalNavigationRequiresEdgeDelegateAndExplicitInstallationTenant() {
    UUID tenant = UUID.randomUUID();
    String id = UUID.randomUUID().toString(), correlation = UUID.randomUUID().toString();
    var access = mock(DeploymentAccess.class);
    when(access.tenantId()).thenReturn(tenant.toString());
    when(access.portalUrl()).thenReturn("https://portal.example.com");
    var props = new AurigaProperties();
    props.getGrpc().getServer().setTenantDelegates(List.of("svc:edge"));
    var navigation = mock(UacNavigation.class);
    var store = mock(IamStore.class);
    when(store.tx(any())).thenAnswer(i -> ((Supplier<?>) i.getArgument(0)).get());
    when(store.get(eq("login_tx"), eq(id), eq(false)))
        .thenReturn(Json.object("correlationId", correlation));
    when(navigation.issue(any(), eq(false)))
        .thenAnswer(
            i -> {
              assertEquals(tenant, TenantContext.require());
              return Json.object("context", Json.object("transactionId", id));
            });
    var controller = new UacNavigationController(navigation, access, props, null, null, store);
    var request = new MockHttpServletRequest();
    request.addHeader("auriga-tenant-id", tenant.toString());
    var response = new MockHttpServletResponse();
    var body = Json.object("origin", "https://portal.example.com");
    var edge =
        principal(
            "svc:edge",
            PrincipalType.INTERNAL_SERVICE,
            TokenContext.SERVICE,
            TenantContext.PLATFORM_TENANT,
            null);
    RequestContext.start(null, null)
        .withPrincipal(edge)
        .run(
            () ->
                TenantContext.run(
                    TenantContext.PLATFORM_TENANT,
                    () -> controller.issuePortal(body, request, response)));
    assertEquals(correlation, response.getHeader(UacCorrelation.HEADER));
    assertEquals("no-store", response.getHeader("Cache-Control"));
    for (var caller :
        List.of(
            principal(
                UUID.randomUUID().toString(),
                PrincipalType.USER,
                TokenContext.ADMIN,
                tenant,
                UUID.randomUUID().toString()),
            principal(
                "svc:edge", PrincipalType.SERVICE_ACCOUNT, TokenContext.SERVICE, tenant, null),
            principal(
                "svc:other",
                PrincipalType.INTERNAL_SERVICE,
                TokenContext.SERVICE,
                TenantContext.PLATFORM_TENANT,
                null)))
      RequestContext.start(null, null)
          .withPrincipal(caller)
          .run(
              () ->
                  assertEquals(
                      "common.forbidden",
                      assertThrows(
                              AurigaException.class,
                              () -> controller.issuePortal(body, request, response))
                          .errorCode()
                          .code()));
    RequestContext.start(null, null)
        .withPrincipal(edge)
        .run(
            () -> {
              var wrong = new MockHttpServletRequest();
              wrong.addHeader("auriga-tenant-id", UUID.randomUUID().toString());
              assertEquals(
                  "common.forbidden",
                  assertThrows(
                          AurigaException.class,
                          () -> controller.issuePortal(body, wrong, response))
                      .errorCode()
                      .code());
              request.addHeader("auriga-tenant-id", tenant.toString());
              assertEquals(
                  "common.forbidden",
                  assertThrows(
                          AurigaException.class,
                          () -> controller.issuePortal(body, request, response))
                      .errorCode()
                      .code());
            });
    props.getGrpc().getServer().setTenantDelegates(List.of());
    var blocked = new UacNavigationController(navigation, access, props, null, null, store);
    RequestContext.start(null, null)
        .withPrincipal(edge)
        .run(
            () ->
                assertThrows(
                    AurigaException.class,
                    () -> blocked.issuePortal(body, new MockHttpServletRequest(), response)));
    verify(navigation, times(1)).issue(any(), anyBoolean());
  }

  @Test
  void correlationHeaderIsGeneratedByServerAndCannotBeChosenByClient() throws Exception {
    var request = new MockHttpServletRequest("POST", "/api/iam/v1/login/uac/sso");
    request.addHeader(UacCorrelation.HEADER, "client-value");
    var response = new MockHttpServletResponse();
    new UacCorrelation().doFilter(request, response, new MockFilterChain());
    assertDoesNotThrow(() -> UUID.fromString(response.getHeader(UacCorrelation.HEADER)));
    assertNotEquals("client-value", response.getHeader(UacCorrelation.HEADER));
  }

  @Test
  void portalLogoutSetsOnlySessionScopedHostOnlySuppressionWhileAdminLogoutDoesNot() {
    var access = HttpBoundaryTest.fixture("domain-proxy.json");
    var tenant = UUID.fromString(access.tenantId());
    for (String role : List.of("portal", "admin")) {
      var identity =
          new RequestIdentity(access, new RequestIdentity.Hop(Set.of("127.0.0.1"), null, true));
      var sessions = mock(SessionManager.class);
      var store = mock(IamStore.class);
      when(sessions.response(any())).thenAnswer(i -> ((Supplier<?>) i.getArgument(0)).get());
      String account = UUID.randomUUID().toString(),
          sid = UUID.randomUUID().toString(),
          csrf = Secrets.random(),
          cookie = Secrets.random();
      var row =
          Json.object(
              "accountId",
              account,
              "sessionId",
              sid,
              "audience",
              role,
              "csrfHash",
              Secrets.hash(csrf));
      when(sessions.activeByCookie(cookie, role)).thenReturn(row);
      var controller =
          new IamLoginController(
              mock(LoginManager.class),
              identity,
              store,
              sessions,
              mock(QrChallenges.class),
              mock(Secrets.class));
      controller.uacNavigation(
          new UacNavigation(store, null, null, null, null, Clock.systemUTC(), "true"));
      var request = HttpBoundaryTest.request(access, role);
      request.removeHeader("Origin");
      request.addHeader("Origin", role.equals("portal") ? access.portalUrl() : access.adminUrl());
      request.addHeader("X-Auriga-CSRF", csrf);
      request.setCookies(
          new jakarta.servlet.http.Cookie(
              identity.cookieName(
                  request, role.equals("portal") ? "auriga_session" : "auriga_admin_session"),
              cookie),
          new jakarta.servlet.http.Cookie(identity.cookieName(request, "auriga_csrf"), csrf));
      var response = new MockHttpServletResponse();
      var caller =
          principal(
              account,
              PrincipalType.USER,
              role.equals("portal") ? TokenContext.PORTAL : TokenContext.ADMIN,
              tenant,
              sid);
      RequestContext.start(null, null)
          .withPrincipal(caller)
          .run(() -> controller.logout(request, response));
      var values =
          response.getHeaders("Set-Cookie").stream()
              .filter(v -> v.contains("auriga_portal_sso_suppression="))
              .toList();
      if (role.equals("portal")) {
        assertEquals(1, values.size());
        String value = values.getFirst();
        assertTrue(value.startsWith("__Host-auriga_portal_sso_suppression="));
        for (String attribute : List.of("HttpOnly", "Secure", "SameSite=Lax", "Path=/"))
          assertTrue(value.contains(attribute));
        for (String attribute : List.of("Domain=", "Max-Age=", "Expires="))
          assertFalse(value.contains(attribute));
        verify(store).insert(eq("uac_suppression"), anyString(), any(), any());
      } else {
        assertTrue(values.isEmpty());
        verify(store, never()).insert(eq("uac_suppression"), anyString(), any(), any());
      }
      verify(sessions).revokeOne(row, "logout");
    }
  }
}
