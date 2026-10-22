package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.contracts.deploymentconfig.DeploymentAccess;
import jakarta.servlet.http.Cookie;
import java.nio.file.*;
import java.util.*;
import org.junit.jupiter.api.Test;
import org.springframework.mock.web.*;

class HttpBoundaryTest {
  final DeploymentAccess access = fixture("domain-proxy.json");
  final UUID tenant = UUID.fromString(access.tenantId());
  final RequestIdentity identity =
      new RequestIdentity(access, new RequestIdentity.Hop(Set.of("127.0.0.1"), null, true));

  static DeploymentAccess fixture(String name) {
    try {
      return DeploymentAccess.load(
          Files.readAllBytes(Path.of("../../../contracts/deployment/fixtures/" + name)));
    } catch (java.io.IOException e) {
      throw new java.io.UncheckedIOException(e);
    }
  }

  static MockHttpServletRequest request(DeploymentAccess config, String role) {
    var r = new MockHttpServletRequest();
    String origin = role.equals("admin") ? config.adminUrl() : config.portalUrl();
    r.setRemoteAddr("127.0.0.1");
    r.setScheme("http");
    r.setSecure(false);
    r.addHeader("Host", java.net.URI.create(origin).getRawAuthority());
    r.addHeader("X-Auriga-Entry-Origin", origin);
    r.addHeader("X-Auriga-Entry-Role", role);
    r.addHeader("X-Auriga-Client-IP", "192.0.2.11");
    return r;
  }

  static void error(String code, Runnable work) {
    assertEquals(code, assertThrows(AurigaException.class, work::run).errorCode().code());
  }

  @Test
  void hostControlsTenantAudienceAndClientHeadersCannotOverrideIt() {
    var r = request(access, "portal");
    r.setServerName("admin.example.com");
    r.addHeader("X-Auriga-Tenant-ID", UUID.randomUUID().toString());
    r.addHeader("X-Auriga-Audience", "admin");
    r.addHeader("Forwarded", "proto=http;host=evil.example.com");
    assertEquals("portal", identity.host(r).audience());
    identity.call(
        r,
        () -> {
          assertEquals(tenant, TenantContext.require());
          return null;
        });
    TenantContext.run(
        UUID.randomUUID(), () -> error("common.forbidden", () -> identity.call(r, () -> null)));
    r.removeHeader("Host");
    r.addHeader("Host", "unknown.example.com");
    error("common.forbidden", () -> identity.host(r));
  }

  @Test
  void proxyClientAddressRequiresAuthenticatedExplicitPeer() {
    var r = request(access, "portal");
    assertEquals("192.0.2.11", identity.clientIp(r));
    r.setRemoteAddr("192.0.2.11");
    error("common.forbidden", () -> identity.clientIp(r));
    r.setRemoteAddr("127.0.0.1");
    r.removeHeader("X-Auriga-Client-IP");
    error("common.validation_failed", () -> identity.clientIp(r));
    var strict =
        new RequestIdentity(
            access, new RequestIdentity.Hop(Set.of("127.0.0.1"), "edge.internal", false));
    error("common.forbidden", () -> strict.host(r));
    r.setSecure(true);
    error("common.forbidden", () -> strict.host(r));
  }

  @Test
  void cookiesUseCorrectHostPrefixPathAndHttpFallback() {
    var response = new MockHttpServletResponse();
    identity.setCookie(
        request(access, "portal"), response, "auriga_portal_login_tx", "random", true, 300);
    String secure = response.getHeader("Set-Cookie");
    assertTrue(secure.startsWith("__Host-auriga_portal_login_tx="));
    assertTrue(secure.contains("Path=/;"));
    assertTrue(secure.contains("Secure"));
    assertTrue(secure.contains("HttpOnly"));
    assertTrue(secure.contains("SameSite=Lax"));
    var ip = fixture("ip-http.json");
    var ipIdentity =
        new RequestIdentity(ip, new RequestIdentity.Hop(Set.of("127.0.0.1"), null, true));
    response = new MockHttpServletResponse();
    ipIdentity.setCookie(request(ip, "portal"), response, "auriga_session", "random", true, -1);
    assertTrue(response.getHeader("Set-Cookie").startsWith("auriga_session="));
    assertFalse(response.getHeader("Set-Cookie").contains("Secure"));
  }

  @Test
  void transactionAndSessionCookiesRemainIndependentAndIpAdminNeverWritesCsrfCookie() {
    var r = request(access, "admin");
    r.setCookies(
        new Cookie("__Host-auriga_session", "portal"),
        new Cookie("__Host-auriga_admin_session", "admin"),
        new Cookie("__Host-auriga_admin_login_tx", "at"),
        new Cookie("__Host-auriga_portal_login_tx", "pt"));
    assertEquals("admin", identity.sessionCookie(r));
    assertEquals("at", identity.binding(r));
    var ip = fixture("ip-http.json");
    var ii = new RequestIdentity(ip, new RequestIdentity.Hop(Set.of("127.0.0.1"), null, true));
    var response = new MockHttpServletResponse();
    ii.issued(
        request(ip, "admin"),
        response,
        new LoginManager.Result(
            Json.object("status", "completed"),
            null,
            new SessionManager.Issued(Json.object(), "admin-cookie", "csrf-raw")));
    assertTrue(
        response.getHeaders("Set-Cookie").stream().noneMatch(c -> c.contains("auriga_csrf")));
    assertTrue(
        response.getHeaders("Set-Cookie").stream().noneMatch(c -> c.startsWith("auriga_session=")));
    assertTrue(
        response.getHeaders("Set-Cookie").stream()
            .anyMatch(c -> c.startsWith("auriga_admin_login_tx=") && c.contains("Max-Age=0")));
  }

  @Test
  void consentCannotBeGrantedWithoutSessionBoundCsrf() {
    IamStore store = mock(IamStore.class);
    when(store.tx(any()))
        .thenAnswer(i -> ((java.util.function.Supplier<?>) i.getArgument(0)).get());
    SessionManager sessions = mock(SessionManager.class);
    OAuthProvider provider = mock(OAuthProvider.class);
    var controller =
        new OAuthController(provider, mock(SigningKeys.class), identity, store, sessions);
    var r = request(access, "portal");
    r.addHeader("Origin", access.portalUrl());
    when(sessions.activeByCookie(nullable(String.class), eq("portal")))
        .thenReturn(Json.object("csrfHash", Secrets.hash("example-csrf")));
    assertThrows(
        RuntimeException.class, () -> controller.authorize(Map.of("csrf_token", "incorrect"), r));
    verifyNoInteractions(provider);
    when(provider.authorize(anyMap(), nullable(String.class), eq("portal")))
        .thenReturn("https://client.example.com/callback?code=example");
    assertEquals(
        302, controller.authorize(Map.of("csrf_token", "example-csrf"), r).getStatusCode().value());
  }

  @Test
  void completeAuthorityOriginAndSingleHeadersAreRequired() {
    var ip = fixture("ip-http.json");
    var ii = new RequestIdentity(ip, new RequestIdentity.Hop(Set.of("127.0.0.1"), null, true));
    var r = request(ip, "admin");
    r.addHeader("Origin", ip.adminUrl());
    ii.sameOrigin(r);
    for (String bad :
        List.of(
            "192.0.2.10:",
            "192.0.2.010:8082",
            "192.0.2.10:0",
            "192.0.2.10:65536",
            "192.0.2.10:8082,evil.example",
            "192.0.2.10%3a8082")) {
      var q = request(ip, "admin");
      q.removeHeader("Host");
      q.addHeader("Host", bad);
      error("common.validation_failed", () -> ii.host(q));
    }
    for (String bad :
        List.of(
            ip.portalUrl(),
            "null",
            ip.adminUrl() + "/",
            "https://192.0.2.10:8082",
            ip.adminUrl() + ", " + ip.portalUrl(),
            "http://192.0.2.10%3a8082")) {
      var q = request(ip, "admin");
      q.addHeader("Origin", bad);
      error("common.forbidden", () -> ii.sameOrigin(q));
    }
    var wrongPort = request(ip, "admin");
    wrongPort.removeHeader("Host");
    wrongPort.addHeader("Host", "192.0.2.10:8080");
    error("common.forbidden", () -> ii.host(wrongPort));
    var duplicate = request(ip, "admin");
    duplicate.addHeader("X-Auriga-Entry-Role", "portal");
    error("common.forbidden", () -> ii.host(duplicate));
  }

  @Test
  void postCallbackFailsClosedWithoutCreatingResumeOrSession() {
    var store = mock(IamStore.class);
    var login = mock(LoginManager.class);
    var controller = new LoginCallbackController(login, identity, store);
    var req = request(access, "portal");
    req.setMethod("POST");
    error(
        "common.forbidden",
        () ->
            controller.post(
                UUID.randomUUID(), Map.of("state", "arbitrary", "code", "arbitrary"), req));
    verifyNoInteractions(store, login);
  }
}
