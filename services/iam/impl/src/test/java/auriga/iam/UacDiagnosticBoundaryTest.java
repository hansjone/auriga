package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import auriga.common.context.RequestContext;
import auriga.common.error.AurigaException;
import auriga.common.security.*;
import auriga.iam.api.PermissionResolver;
import java.util.*;
import java.util.function.Supplier;
import org.junit.jupiter.api.Test;

class UacDiagnosticBoundaryTest {
  @Test
  void downloadRequiresLiveAdminCookiePermissionsOriginAndCsrf() {
    var access = HttpBoundaryTest.fixture("domain-proxy.json");
    var identity =
        new RequestIdentity(access, new RequestIdentity.Hop(Set.of("127.0.0.1"), null, true));
    var store = mock(IamStore.class);
    when(store.tx(any())).thenAnswer(i -> ((Supplier<?>) i.getArgument(0)).get());
    var sessions = mock(SessionManager.class);
    var permissions = mock(PermissionResolver.class);
    var bundles = mock(UacDiagnosticBundles.class);
    when(bundles.build(any(), any()))
        .thenAnswer(
            i -> {
              ((Runnable) i.getArgument(1)).run();
              return new UacDiagnosticBundles.Bundle("uac-test.zip", new byte[] {1, 2}, true);
            });
    var controller =
        new UacDiagnosticController(store, identity, sessions, null, permissions, bundles);
    String account = UUID.randomUUID().toString(),
        sid = UUID.randomUUID().toString(),
        csrf = Secrets.random(),
        cookie = Secrets.random();
    UUID tenant = UUID.fromString(access.tenantId());
    var required = Set.of("iam.providers.read", "audit.events.read", "ops.alerts.read");
    when(permissions.resolve(tenant, account, "admin")).thenReturn(required);
    var row =
        Json.object(
            "accountId",
            account,
            "sessionId",
            sid,
            "audience",
            "admin",
            "csrfHash",
            Secrets.hash(csrf));
    when(sessions.activeByCookie(cookie, "admin")).thenReturn(row);
    var caller =
        new AurigaPrincipal(
            account,
            tenant,
            PrincipalType.USER,
            TokenContext.ADMIN,
            sid,
            null,
            "member",
            List.of(),
            required,
            null,
            null,
            UUID.randomUUID().toString());
    Supplier<org.springframework.mock.web.MockHttpServletRequest> request =
        () -> {
          var r = HttpBoundaryTest.request(access, "admin");
          r.removeHeader("Origin");
          r.addHeader("Origin", access.adminUrl());
          r.addHeader("X-Auriga-CSRF", csrf);
          r.setCookies(
              new jakarta.servlet.http.Cookie(
                  identity.cookieName(r, "auriga_admin_session"), cookie),
              new jakarta.servlet.http.Cookie(identity.cookieName(r, "auriga_csrf"), csrf));
          return r;
        };
    RequestContext.start(null, null)
        .withPrincipal(caller)
        .run(
            () -> {
              var result = controller.download(Json.object(), request.get());
              assertEquals("application/zip", result.getHeaders().getContentType().toString());
              assertEquals("no-store", result.getHeaders().getFirst("Cache-Control"));
              assertEquals("true", result.getHeaders().getFirst("X-Auriga-Diagnostic-Partial"));
              assertArrayEquals(new byte[] {1, 2}, result.getBody());
              var missing = request.get();
              missing.removeHeader("X-Auriga-CSRF");
              assertThrows(
                  AurigaException.class, () -> controller.download(Json.object(), missing));
              var foreign = request.get();
              foreign.removeHeader("Origin");
              foreign.addHeader("Origin", "https://other.example.com");
              assertThrows(
                  AurigaException.class, () -> controller.download(Json.object(), foreign));
              when(permissions.resolve(tenant, account, "admin"))
                  .thenReturn(Set.of("iam.providers.read"));
              assertThrows(
                  AurigaException.class, () -> controller.download(Json.object(), request.get()));
              when(permissions.resolve(tenant, account, "admin")).thenReturn(required);
              when(sessions.activeByCookie(cookie, "admin")).thenReturn(null);
              assertThrows(
                  AurigaException.class, () -> controller.download(Json.object(), request.get()));
            });
    for (var type :
        List.of(
            PrincipalType.USER, PrincipalType.INTERNAL_SERVICE, PrincipalType.SERVICE_ACCOUNT)) {
      var denied =
          new AurigaPrincipal(
              account,
              tenant,
              type,
              TokenContext.PORTAL,
              sid,
              null,
              "member",
              List.of(),
              required,
              null,
              null,
              UUID.randomUUID().toString());
      RequestContext.start(null, null)
          .withPrincipal(denied)
          .run(
              () ->
                  assertThrows(
                      AurigaException.class,
                      () -> controller.download(Json.object(), request.get())));
    }
    RequestContext.start(null, null)
        .run(
            () ->
                assertThrows(
                    AurigaException.class,
                    () -> controller.download(Json.object(), request.get())));
  }
}
