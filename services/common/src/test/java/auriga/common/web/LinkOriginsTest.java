package auriga.common.web;

import static org.junit.jupiter.api.Assertions.*;

import java.util.List;
import org.junit.jupiter.api.Test;
import org.springframework.mock.web.MockHttpServletRequest;

class LinkOriginsTest {
  private static final String IP = "http://192.0.2.10:8080";
  private static final String DOMAIN = "https://portal.example.com";
  private final LinkOrigins links = new LinkOrigins(List.of(IP, DOMAIN), List.of("192.0.2.20"));

  private MockHttpServletRequest request(String peer) {
    var request = new MockHttpServletRequest();
    request.setRemoteAddr(peer);
    request.setScheme("http");
    request.addHeader("Host", "192.0.2.10:8080");
    return request;
  }

  @Test
  void directHostAndTrustedProxySelectAllowedPresentation() {
    var request = request("192.0.2.30");
    assertEquals(IP, links.call(request, () -> LinkOrigins.currentOr(IP)));
    request = request("192.0.2.20");
    request.addHeader("X-Forwarded-Host", "portal.example.com");
    request.addHeader("X-Forwarded-Proto", "https");
    assertEquals(DOMAIN, links.call(request, () -> LinkOrigins.currentOr(IP)));
    assertEquals(IP, LinkOrigins.currentOr(IP));
  }

  @Test
  void unconfiguredInstallationOnlyUsesDefaultOrigin() {
    var request = request("192.0.2.20");
    request.removeHeader("Host");
    request.addHeader("Host", "portal.example.com");
    request.setScheme("https");
    request.addHeader("X-Forwarded-Host", "portal.example.com");
    request.addHeader("X-Forwarded-Proto", "https");
    var disabled = new LinkOrigins(List.of(), List.of());
    assertEquals(IP, disabled.call(request, () -> LinkOrigins.currentOr(IP)));
  }

  @Test
  void untrustedForwardingAndForgedInternalHeadersCannotOverrideDirectHost() {
    var request = request("192.0.2.30");
    request.addHeader("X-Forwarded-Host", "portal.example.com");
    request.addHeader("X-Forwarded-Proto", "https");
    request.addHeader("X-Auriga-Link-Origin", DOMAIN);
    request.addHeader("Forwarded", "host=portal.example.com;proto=https");
    assertEquals(IP, links.call(request, () -> LinkOrigins.currentOr(IP)));
  }

  @Test
  void unlistedOrAmbiguousSourcesFallBackAndScopesDoNotLeak() {
    for (String host :
        List.of(
            "attacker.example.com",
            "portal.example.com, attacker.example.com",
            "alice@portal.example.com",
            "portal.example.com/path")) {
      var request = request("192.0.2.20");
      request.addHeader("X-Forwarded-Host", host);
      request.addHeader("X-Forwarded-Proto", "https");
      assertEquals(IP, links.call(request, () -> LinkOrigins.currentOr(IP)));
    }
    var request = request("192.0.2.20");
    request.addHeader("X-Forwarded-Host", "portal.example.com");
    request.addHeader("X-Forwarded-Host", "attacker.example.com");
    request.addHeader("X-Forwarded-Proto", "https");
    assertEquals(IP, links.call(request, () -> LinkOrigins.currentOr(IP)));
    request = request("192.0.2.30");
    request.removeHeader("Host");
    request.addHeader("Host", "attacker.example.com");
    assertEquals(IP, links.call(request, () -> LinkOrigins.currentOr(IP)));
    var trusted = request("192.0.2.20");
    trusted.addHeader("X-Forwarded-Host", "portal.example.com");
    trusted.addHeader("X-Forwarded-Proto", "https");
    assertThrows(
        IllegalStateException.class,
        () ->
            links.call(
                trusted,
                () -> {
                  throw new IllegalStateException();
                }));
    assertEquals(IP, LinkOrigins.currentOr(IP));
  }

  @Test
  void servletFilterBindsOnlyItsRequest() throws Exception {
    var request = request("192.0.2.20");
    request.addHeader("X-Forwarded-Host", "portal.example.com");
    request.addHeader("X-Forwarded-Proto", "https");
    links.doFilter(
        request,
        new org.springframework.mock.web.MockHttpServletResponse(),
        (req, res) -> assertEquals(DOMAIN, LinkOrigins.currentOr(IP)));
    assertEquals(IP, LinkOrigins.currentOr(IP));
  }

  @Test
  void servletAutoConfigurationBindsListsAndRequiresRawTransportPeers() {
    var runner =
        new org.springframework.boot.test.context.runner.WebApplicationContextRunner()
            .withConfiguration(
                org.springframework.boot.autoconfigure.AutoConfigurations.of(
                    auriga.common.autoconfigure.AurigaCoreAutoConfiguration.class))
            .withPropertyValues(
                "auriga.links.allowed-origins=" + IP + "," + DOMAIN,
                "auriga.links.trusted-proxies=192.0.2.20");
    runner.run(
        context -> {
          assertNull(context.getStartupFailure());
          var registration =
              context.getBean(
                  "aurigaLinkOrigins",
                  org.springframework.boot.web.servlet.FilterRegistrationBean.class);
          var filter = (LinkOrigins) registration.getFilter();
          var request = request("192.0.2.20");
          request.addHeader("X-Forwarded-Host", "portal.example.com");
          request.addHeader("X-Forwarded-Proto", "https");
          assertEquals(DOMAIN, filter.call(request, () -> LinkOrigins.currentOr(IP)));
        });
    runner
        .withPropertyValues("server.forward-headers-strategy=native")
        .run(context -> assertNotNull(context.getStartupFailure()));
  }

  @Test
  void invalidInstallationDeclarationsAreRejected() {
    for (String origin :
        List.of(
            "https://portal.example.com/",
            "https://portal.example.com?x=1",
            "https://alice@portal.example.com",
            "https://portal.example.com:443",
            "https://PORTAL.example.com",
            "https://*.example.com",
            "https://portal.example.com:0",
            "https://portal.example.com:65536",
            "https://portal.example.com\n")) {
      assertThrows(
          IllegalArgumentException.class, () -> new LinkOrigins(List.of(origin), List.of()));
    }
    for (String peer : List.of("proxy.example.com", "*", "0.0.0.0/0", "192.0.2.20, 192.0.2.21")) {
      assertThrows(
          IllegalArgumentException.class, () -> new LinkOrigins(List.of(DOMAIN), List.of(peer)));
    }
  }
}
