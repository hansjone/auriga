package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import auriga.common.context.TenantContext;
import jakarta.servlet.http.Cookie;
import java.util.*;
import org.junit.jupiter.api.Test;
import org.springframework.mock.web.*;

class HttpBoundaryTest {
  private final UUID tenant = UUID.randomUUID();
  private final RequestIdentity identity =
      new RequestIdentity(
          Map.of(
              "portal.example.com",
              new RequestIdentity.Host(tenant, "portal", true),
              "admin.example.com",
              new RequestIdentity.Host(tenant, "admin", true),
              "192.0.2.10",
              new RequestIdentity.Host(tenant, "portal", false)),
          Set.of("127.0.0.1"));

  private MockHttpServletRequest request(String host) {
    var r = new MockHttpServletRequest();
    r.setServerName(host);
    return r;
  }

  @Test
  void hostControlsTenantAudienceAndClientHeadersCannotOverrideIt() {
    var r = request("portal.example.com");
    r.addHeader("X-Auriga-Tenant-ID", UUID.randomUUID().toString());
    r.addHeader("X-Auriga-Audience", "admin");
    assertEquals("portal", identity.host(r).audience());
    identity.call(
        r,
        () -> {
          assertEquals(tenant, TenantContext.require());
          return null;
        });
    TenantContext.run(
        UUID.randomUUID(),
        () -> assertThrows(RuntimeException.class, () -> identity.call(r, () -> null)));
    assertThrows(RuntimeException.class, () -> identity.host(request("unknown.example.com")));
  }

  @Test
  void proxyClientAddressRequiresExplicitTrustedPeer() {
    var r = request("portal.example.com");
    r.setRemoteAddr("192.0.2.11");
    r.addHeader("X-Auriga-Client-IP", "127.0.0.1");
    assertEquals("192.0.2.11", identity.clientIp(r));
    r.setRemoteAddr("127.0.0.1");
    assertEquals("127.0.0.1", identity.clientIp(r));
    var missing = request("portal.example.com");
    missing.setRemoteAddr("127.0.0.1");
    assertThrows(RuntimeException.class, () -> identity.clientIp(missing));
  }

  @Test
  void cookiesUseCorrectHostPrefixPathAndHttpFallback() {
    var response = new MockHttpServletResponse();
    identity.setCookie(
        request("portal.example.com"), response, "auriga_login_tx", "example-random", true, 300);
    String secure = response.getHeader("Set-Cookie");
    assertTrue(secure.startsWith("__Host-auriga_login_tx="));
    assertTrue(secure.contains("Path=/;"));
    assertTrue(secure.contains("Secure"));
    assertTrue(secure.contains("HttpOnly"));
    assertTrue(secure.contains("SameSite=Lax"));
    response = new MockHttpServletResponse();
    identity.setCookie(
        request("192.0.2.10"), response, "auriga_session", "example-random", true, -1);
    String plain = response.getHeader("Set-Cookie");
    assertTrue(plain.startsWith("auriga_session="));
    assertFalse(plain.contains("Secure"));
  }

  @Test
  void csrfCookieAndSessionAreSeparateBetweenPortalAndAdminHosts() {
    var r = request("admin.example.com");
    r.setCookies(
        new Cookie("__Host-auriga_session", "portal"),
        new Cookie("__Host-auriga_admin_session", "admin"));
    assertEquals("admin", identity.sessionCookie(r));
    r.setServerName("portal.example.com");
    assertEquals("portal", identity.sessionCookie(r));
  }

  @Test
  void consentCannotBeGrantedWithoutSessionBoundCsrf() {
    IamStore store = mock(IamStore.class);
    when(store.tx(any()))
        .thenAnswer(i -> ((java.util.function.Supplier<?>) i.getArgument(0)).get());
    SessionManager sessions = mock(SessionManager.class);
    OAuthProvider provider = mock(OAuthProvider.class);
    SigningKeys keys = mock(SigningKeys.class);
    var controller = new OAuthController(provider, keys, identity, store, sessions);
    var r = request("portal.example.com");
    when(sessions.activeByCookie(nullable(String.class), eq("portal")))
        .thenReturn(Json.object("csrfHash", Secrets.hash("example-csrf")));
    assertThrows(
        RuntimeException.class,
        () ->
            controller.authorize(
                Map.of("client_id", "example-client", "csrf_token", "incorrect"), r));
    verifyNoInteractions(provider);
    when(provider.authorize(anyMap(), nullable(String.class), eq("portal")))
        .thenReturn("https://client.example.com/callback?code=example");
    assertEquals(
        302, controller.authorize(Map.of("csrf_token", "example-csrf"), r).getStatusCode().value());
  }
}
