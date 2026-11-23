package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.error.AurigaException;
import com.sun.net.httpserver.HttpServer;
import java.net.*;
import java.nio.charset.StandardCharsets;
import java.util.*;
import java.util.concurrent.atomic.AtomicInteger;
import org.junit.jupiter.api.Test;

class UacHttpTest {

  private static InetAddress ipv4(int a, int b, int c, int d) {
    try {
      return InetAddress.getByAddress(new byte[] {(byte) a, (byte) b, (byte) c, (byte) d});
    } catch (UnknownHostException e) {
      throw new AssertionError(e);
    }
  }

  @Test
  void staticPinnedModeAllowsMatchingOriginAndAddressesAndRejectsMismatches() throws Exception {
    var settings =
        Json.object(
            "allowedDestinations",
            List.of(
                Json.object(
                    "origin",
                    "https://identity.example.com",
                    "addresses",
                    List.of("192.0.2.10", "192.0.2.11"))));
    var http =
        new UacHttp(
            settings,
            false,
            host ->
                new InetAddress[] {
                  InetAddress.ofLiteral("192.0.2.10"), InetAddress.ofLiteral("192.0.2.11")
                });

    InetAddress[] resolved = http.resolve(URI.create("https://identity.example.com/verify"));
    assertEquals(2, resolved.length);

    assertThrows(
        AurigaException.class, () -> http.resolve(URI.create("https://other.example.com/verify")));

    var drifted =
        new UacHttp(
            settings,
            false,
            host ->
                new InetAddress[] {
                  InetAddress.ofLiteral("192.0.2.10"), InetAddress.ofLiteral("192.0.2.99")
                });
    assertThrows(
        UnknownHostException.class,
        () -> drifted.resolve(URI.create("https://identity.example.com/verify")));
  }

  @Test
  void dynamicAddressesModeSupportsMultiIpRotationAcrossIntranetAndPublicIps() throws Exception {
    var rotation = new AtomicInteger();
    List<InetAddress[]> responses =
        List.of(
            new InetAddress[] {ipv4(10, 20, 30, 10), ipv4(10, 20, 30, 11)},
            new InetAddress[] {ipv4(172, 16, 4, 20), ipv4(192, 168, 10, 21)},
            new InetAddress[] {
              InetAddress.ofLiteral("192.0.2.30"),
              InetAddress.ofLiteral("fd00::10"),
              InetAddress.ofLiteral("2001:db8::20")
            });
    var settings =
        Json.object(
            "allowedDestinations",
            List.of(
                Json.object("origin", "https://uac.corp.example", "allowDynamicAddresses", true)));
    var http =
        new UacHttp(
            settings, false, host -> responses.get(rotation.getAndIncrement() % responses.size()));

    for (InetAddress[] expected : responses) {
      InetAddress[] actual = http.resolve(URI.create("https://uac.corp.example/verify"));
      assertArrayEquals(expected, actual);
    }

    // Explicit empty addresses array is also accepted with allowDynamicAddresses=true.
    var withEmptyArray =
        Json.object(
            "allowedDestinations",
            List.of(
                Json.object(
                    "origin",
                    "https://uac.corp.example",
                    "allowDynamicAddresses",
                    true,
                    "addresses",
                    List.of())));
    var httpEmpty =
        new UacHttp(withEmptyArray, false, host -> new InetAddress[] {ipv4(10, 1, 2, 3)});
    assertEquals(1, httpEmpty.resolve(URI.create("https://uac.corp.example/info")).length);

    // Unapproved origin, wrong port, and non-HTTPS are still rejected.
    assertThrows(
        AurigaException.class,
        () -> http.validate(URI.create("https://other.corp.example/verify"), false));
    assertThrows(
        AurigaException.class,
        () -> http.validate(URI.create("https://uac.corp.example:8443/verify"), false));
    assertThrows(
        AurigaException.class,
        () -> http.validate(URI.create("http://uac.corp.example/verify"), false));
  }

  @Test
  void dynamicAddressesModeRejectsDangerousResolvedAddressesIncludingMixedSets() {
    var settings =
        Json.object(
            "allowedDestinations",
            List.of(
                Json.object("origin", "https://uac.corp.example", "allowDynamicAddresses", true)));
    List<String> dangerous =
        List.of(
            "127.0.0.1",
            "127.0.0.2",
            "::1",
            "169.254.169.254",
            "fe80::1",
            "0.0.0.0",
            "0.1.2.3",
            "::",
            "224.0.0.1",
            "ff02::1",
            "240.0.0.1",
            "255.255.255.255");

    for (String badIp : dangerous) {
      var http =
          new UacHttp(settings, false, host -> new InetAddress[] {InetAddress.ofLiteral(badIp)});
      assertThrows(
          UnknownHostException.class,
          () -> http.resolve(URI.create("https://uac.corp.example/verify")),
          "Expected dangerous IP to be rejected: " + badIp);

      // A single dangerous IP in a multi-IP response must reject the whole lookup.
      var mixed =
          new UacHttp(
              settings,
              false,
              host -> new InetAddress[] {ipv4(10, 0, 1, 10), InetAddress.ofLiteral(badIp)});
      assertThrows(
          UnknownHostException.class,
          () -> mixed.resolve(URI.create("https://uac.corp.example/verify")),
          "Expected mixed response containing dangerous IP to be rejected: " + badIp);
    }
  }

  @Test
  void dynamicAddressesModeWorksEndToEndWithLoopbackInTestsWhileStillBlockingOtherUnsafeIps()
      throws Exception {
    var server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
    server.createContext(
        "/verify",
        exchange -> {
          byte[] body = "{\"ok\":true}".getBytes(StandardCharsets.UTF_8);
          exchange.sendResponseHeaders(200, body.length);
          exchange.getResponseBody().write(body);
          exchange.close();
        });
    server.start();
    try {
      String origin = "http://localhost:" + server.getAddress().getPort();
      var settings =
          Json.object(
              "allowedDestinations",
              List.of(Json.object("origin", origin, "allowDynamicAddresses", true)));
      var http =
          new UacHttp(
              settings, true, host -> new InetAddress[] {InetAddress.ofLiteral("127.0.0.1")});
      var response = http.post(URI.create(origin + "/verify"), Json.object(), Map.of(), 3);
      assertEquals(200, response.status());

      // Even with testLoopback=true, link-local / unspecified / multicast remain blocked.
      for (String badIp : List.of("169.254.169.254", "0.0.0.0", "224.0.0.1")) {
        var blocked =
            new UacHttp(settings, true, host -> new InetAddress[] {InetAddress.ofLiteral(badIp)});
        assertThrows(
            UnknownHostException.class,
            () -> blocked.post(URI.create(origin + "/verify"), Json.object(), Map.of(), 3),
            "Expected non-loopback dangerous IP to remain rejected in testLoopback mode: " + badIp);
      }
    } finally {
      server.stop(0);
    }
  }

  @Test
  void rejectsAmbiguousOrInvalidAllowlistConfigurations() {
    // Missing or empty addresses without allowDynamicAddresses=true
    assertThrows(
        IllegalArgumentException.class,
        () ->
            new UacHttp(
                Json.object(
                    "allowedDestinations",
                    List.of(Json.object("origin", "https://identity.example.com"))),
                false));
    assertThrows(
        IllegalArgumentException.class,
        () ->
            new UacHttp(
                Json.object(
                    "allowedDestinations",
                    List.of(
                        Json.object(
                            "origin", "https://identity.example.com", "addresses", List.of()))),
                false));
    assertThrows(
        IllegalArgumentException.class,
        () ->
            new UacHttp(
                Json.object(
                    "allowedDestinations",
                    List.of(
                        Json.object(
                            "origin",
                            "https://identity.example.com",
                            "allowDynamicAddresses",
                            false,
                            "addresses",
                            List.of()))),
                false));

    // allowDynamicAddresses=true combined with non-empty static addresses
    assertThrows(
        IllegalArgumentException.class,
        () ->
            new UacHttp(
                Json.object(
                    "allowedDestinations",
                    List.of(
                        Json.object(
                            "origin",
                            "https://uac.corp.example",
                            "allowDynamicAddresses",
                            true,
                            "addresses",
                            List.of("192.0.2.10")))),
                false));

    // allowDynamicAddresses=true with literal IPv4 / IPv6 origin
    assertThrows(
        IllegalArgumentException.class,
        () ->
            new UacHttp(
                Json.object(
                    "allowedDestinations",
                    List.of(
                        Json.object(
                            "origin", "https://192.0.2.10", "allowDynamicAddresses", true))),
                false));
    assertThrows(
        IllegalArgumentException.class,
        () ->
            new UacHttp(
                Json.object(
                    "allowedDestinations",
                    List.of(
                        Json.object(
                            "origin", "https://[2001:db8::1]", "allowDynamicAddresses", true))),
                false));

    // Non-boolean allowDynamicAddresses
    assertThrows(
        IllegalArgumentException.class,
        () ->
            new UacHttp(
                Json.object(
                    "allowedDestinations",
                    List.of(
                        Json.object(
                            "origin",
                            "https://uac.corp.example",
                            "allowDynamicAddresses",
                            "true"))),
                false));

    // Duplicate origin
    assertThrows(
        IllegalArgumentException.class,
        () ->
            new UacHttp(
                Json.object(
                    "allowedDestinations",
                    List.of(
                        Json.object(
                            "origin", "https://uac.corp.example", "allowDynamicAddresses", true),
                        Json.object(
                            "origin",
                            "https://UAC.CORP.EXAMPLE:443",
                            "addresses",
                            List.of("192.0.2.10")))),
                false));
  }

  @Test
  void mixedStaticAndDynamicOriginsEnforceEachOriginsOwnPolicy() throws Exception {
    var settings =
        Json.object(
            "allowedDestinations",
            List.of(
                Json.object(
                    "origin", "https://pinned.example.com", "addresses", List.of("192.0.2.10")),
                Json.object(
                    "origin", "https://rotating.corp.example", "allowDynamicAddresses", true)));
    InetAddress rotatingIp = ipv4(10, 0, 2, 20);
    var http =
        new UacHttp(
            settings,
            false,
            host ->
                host.equals("pinned.example.com")
                    ? new InetAddress[] {InetAddress.ofLiteral("192.0.2.99")}
                    : new InetAddress[] {rotatingIp});

    assertThrows(
        UnknownHostException.class,
        () -> http.resolve(URI.create("https://pinned.example.com/verify")));
    assertEquals(rotatingIp, http.resolve(URI.create("https://rotating.corp.example/verify"))[0]);
  }

  @Test
  void unrestrictedModeDoesNotThrowNpeOnLiteralIpTargets() throws Exception {
    var server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
    server.createContext(
        "/verify",
        exchange -> {
          byte[] body = "{\"ok\":true}".getBytes(StandardCharsets.UTF_8);
          exchange.sendResponseHeaders(200, body.length);
          exchange.getResponseBody().write(body);
          exchange.close();
        });
    server.start();
    try {
      String origin = "http://127.0.0.1:" + server.getAddress().getPort();
      for (var settings : List.of(Json.object(), Json.object("allowedDestinations", List.of()))) {
        var http = new UacHttp(settings, true);
        var response = http.post(URI.create(origin + "/verify"), Json.object(), Map.of(), 3);
        assertEquals(200, response.status());

        var prodHttp = new UacHttp(settings, false);
        assertEquals(
            "192.0.2.10",
            prodHttp.resolve(URI.create("https://192.0.2.10/verify"))[0].getHostAddress());
        assertEquals(
            "2001:db8:0:0:0:0:0:1",
            prodHttp.resolve(URI.create("https://[2001:db8::1]/verify"))[0].getHostAddress());
      }
    } finally {
      server.stop(0);
    }
  }
}
