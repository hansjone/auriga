package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.*;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.*;

import auriga.common.error.*;
import auriga.contracts.deploymentconfig.DeploymentAccess;
import auriga.contracts.deploymentconfig.ValidationException;
import jakarta.servlet.http.Cookie;
import java.net.URI;
import java.nio.charset.StandardCharsets;
import java.nio.file.*;
import java.time.*;
import java.util.*;
import java.util.function.Supplier;
import org.junit.jupiter.api.Test;
import org.springframework.mock.web.MockHttpServletRequest;
import org.springframework.test.web.servlet.MockMvc;
import org.springframework.test.web.servlet.request.MockHttpServletRequestBuilder;
import org.springframework.test.web.servlet.setup.StandaloneMockMvcBuilder;
import tools.jackson.databind.node.ObjectNode;

class UacHttpPollCompatibilityTest {
  static final String HEADER = "Auriga-UAC-Poll";

  static MockHttpServletRequest poll(DeploymentAccess access, String role) {
    var request = HttpBoundaryTest.request(access, role);
    request.setMethod("GET");
    request.addHeader(HEADER, "same-origin");
    return request;
  }

  static RequestIdentity identity(DeploymentAccess access) {
    return new RequestIdentity(access, new RequestIdentity.Hop(Set.of("127.0.0.1"), null, true));
  }

  @Test
  void onlyAcknowledgedHttpIpSupportsTheNonSimplePollingProof() throws Exception {
    var access = HttpBoundaryTest.fixture("ip-http.json");
    var identity = identity(access);
    for (String role : List.of("portal", "admin")) {
      var request = poll(access, role);
      assertDoesNotThrow(() -> identity.sameOriginUacPoll(request));
      request.addHeader("Origin", role.equals("portal") ? access.portalUrl() : access.adminUrl());
      assertDoesNotThrow(() -> identity.sameOriginUacPoll(request));
      request.removeHeader(HEADER);
      HttpBoundaryTest.error("common.forbidden", () -> identity.sameOriginUacPoll(request));
    }
    var document =
        Json.read(Files.readString(Path.of("../../../contracts/deployment/fixtures/ip-http.json")));
    document.put("insecureHttpAcknowledged", false);
    assertThrows(
        ValidationException.class,
        () -> DeploymentAccess.load(document.toString().getBytes(StandardCharsets.UTF_8)));
    for (String entry : List.of("portalOrigin", "adminOrigin", "issuerOrigin"))
      document.withObject("/" + entry).put("scheme", "https");
    document.withObject("/listeners/portal").put("tls", true);
    document.withObject("/listeners/admin").put("tls", true);
    var secureIp = DeploymentAccess.load(document.toString().getBytes(StandardCharsets.UTF_8));
    for (var strict : List.of(secureIp, HttpBoundaryTest.fixture("domain-proxy.json")))
      HttpBoundaryTest.error(
          "common.forbidden", () -> identity(strict).sameOriginUacPoll(poll(strict, "portal")));
  }

  @Test
  void anyMetadataKeepsTheExistingStrictPolicyAndTheMarkerCannotOverrideIt() {
    for (String fixture : List.of("ip-http.json", "domain-proxy.json")) {
      var access = HttpBoundaryTest.fixture(fixture);
      var identity = identity(access);
      var request = poll(access, "portal");
      request.addHeader("Sec-Fetch-Site", "same-origin");
      request.addHeader("Sec-Fetch-Mode", "cors");
      request.addHeader("Sec-Fetch-Dest", "empty");
      assertDoesNotThrow(() -> identity.sameOriginUacPoll(request));
      request.removeHeader(HEADER);
      assertDoesNotThrow(() -> identity.sameOriginUacPoll(request));
      request.addHeader(HEADER, "same-origin");
      for (String site : List.of("same-site", "cross-site", "none")) {
        request.removeHeader("Sec-Fetch-Site");
        request.addHeader("Sec-Fetch-Site", site);
        HttpBoundaryTest.error("common.forbidden", () -> identity.sameOriginUacPoll(request));
      }
      request.removeHeader("Sec-Fetch-Site");
      HttpBoundaryTest.error("common.forbidden", () -> identity.sameOriginUacPoll(request));
      var partial = poll(access, "portal");
      partial.addHeader("Sec-Fetch-User", "?1");
      HttpBoundaryTest.error("common.forbidden", () -> identity.sameOriginUacPoll(partial));
    }
  }

  @Test
  void missingDuplicateOrWrongMarkerOriginAndUntrustedEntryRemainForbidden() {
    var access = HttpBoundaryTest.fixture("ip-http.json");
    var identity = identity(access);
    for (String value : List.of("", "cors", "same-origin, same-origin")) {
      var request = poll(access, "portal");
      request.removeHeader(HEADER);
      request.addHeader(HEADER, value);
      HttpBoundaryTest.error("common.forbidden", () -> identity.sameOriginUacPoll(request));
    }
    var duplicate = poll(access, "portal");
    duplicate.addHeader(HEADER, "same-origin");
    HttpBoundaryTest.error("common.forbidden", () -> identity.sameOriginUacPoll(duplicate));
    for (String origin :
        List.of(
            "null", "https://attacker.example.com", access.adminUrl(), access.portalUrl() + "/")) {
      var request = poll(access, "portal");
      request.addHeader("Origin", origin);
      HttpBoundaryTest.error("common.forbidden", () -> identity.sameOriginUacPoll(request));
    }
    var untrusted = poll(access, "portal");
    untrusted.setRemoteAddr("192.0.2.11");
    HttpBoundaryTest.error("common.forbidden", () -> identity.sameOriginUacPoll(untrusted));
    var wrongHost = poll(access, "portal");
    wrongHost.removeHeader("Host");
    wrongHost.addHeader("Host", URI.create(access.adminUrl()).getRawAuthority());
    HttpBoundaryTest.error("common.forbidden", () -> identity.sameOriginUacPoll(wrongHost));
  }

  @Test
  void realControllerReadsLocalStatusWithoutRelaxingCookieAudienceOrOrigin() throws Exception {
    var f = new Fixture();
    f.mvc
        .perform(f.request("GET", "portal", f.binding))
        .andExpect(status().isOk())
        .andExpect(jsonPath("$.state").value("waiting"))
        .andExpect(header().string("Cache-Control", "no-store"))
        .andExpect(header().doesNotExist("Access-Control-Allow-Origin"));
    for (String binding : new String[] {null, "wrong-binding"})
      f.mvc.perform(f.request("GET", "portal", binding)).andExpect(status().isNotFound());
    f.mvc.perform(f.request("GET", "admin", f.binding)).andExpect(status().isNotFound());
    f.mvc
        .perform(f.request("GET", "portal", f.binding).header("Origin", f.access.adminUrl()))
        .andExpect(status().isForbidden());
    f.mvc
        .perform(f.request("GET", "portal", f.binding).header("Sec-Fetch-Site", "cross-site"))
        .andExpect(status().isForbidden());
    f.mvc
        .perform(
            f.request("GET", "portal", f.binding)
                .cookie(new Cookie("auriga_portal_login_tx", f.binding)))
        .andExpect(status().isNotFound());
    verifyNoInteractions(f.login);
    verify(f.store, atLeastOnce()).byId("uac_challenge", f.id, false);
    verify(f.store, never()).get(anyString(), anyString(), anyBoolean());
  }

  @Test
  void realMvcGrantsNoCrossOriginPreflightPermissionOrStatusLookup() throws Exception {
    var f = new Fixture();
    for (String origin : List.of("https://attacker.example.com", f.access.adminUrl()))
      f.mvc
          .perform(
              f.request("OPTIONS", "portal", null)
                  .header("Origin", origin)
                  .header("Access-Control-Request-Method", "GET")
                  .header("Access-Control-Request-Headers", HEADER))
          // Default MVC may answer OPTIONS with an empty 200. Without CORS permission the
          // browser still rejects the preflight and never sends the marked GET.
          .andExpect(status().isOk())
          .andExpect(content().string(""))
          .andExpect(header().doesNotExist("Access-Control-Allow-Origin"))
          .andExpect(header().doesNotExist("Access-Control-Allow-Headers"));
    verify(f.store, never()).get(anyString(), anyString(), anyBoolean());
    verify(f.store, never()).byId(anyString(), anyString(), anyBoolean());
  }

  @Test
  void expiredAndCancelledChallengesDoNotResumeThroughHttpCompatibility() throws Exception {
    var f = new Fixture();
    f.row.put("expiresAt", f.now.minusSeconds(1).toString());
    f.mvc
        .perform(f.request("GET", "portal", f.binding))
        .andExpect(status().isOk())
        .andExpect(jsonPath("$.state").value("expired"));
    f.mvc
        .perform(f.request("GET", "portal", f.binding))
        .andExpect(status().isOk())
        .andExpect(jsonPath("$.state").value("expired"));
    var cancelled = new Fixture();
    cancelled
        .mvc
        .perform(cancelled.request("DELETE", "portal", cancelled.binding))
        .andExpect(status().isForbidden());
    cancelled
        .mvc
        .perform(
            cancelled
                .request("DELETE", "portal", cancelled.binding)
                .header("Origin", cancelled.access.portalUrl()))
        .andExpect(status().isNoContent());
    for (int i = 0; i < 2; i++)
      cancelled
          .mvc
          .perform(cancelled.request("GET", "portal", cancelled.binding))
          .andExpect(status().isOk())
          .andExpect(jsonPath("$.state").value("cancelled"));
    verify(cancelled.login).cancel(cancelled.transaction, cancelled.binding);
  }

  static final class Fixture {
    final DeploymentAccess access = HttpBoundaryTest.fixture("ip-http.json");
    final Instant now = Instant.parse("2026-11-01T00:00:00Z");
    final String id = UUID.randomUUID().toString(), transaction = UUID.randomUUID().toString();
    final String path = "/api/iam/v1/login/uac/challenges/" + id, binding = Secrets.random();
    final IamStore store = mock(IamStore.class);
    final LoginManager login = mock(LoginManager.class);
    final ObjectNode row =
        Json.object(
            "id",
            id,
            "transactionId",
            transaction,
            "bindingHash",
            Secrets.hash(binding),
            "audience",
            "portal",
            "origin",
            access.portalUrl(),
            "state",
            "waiting",
            "consumed",
            false,
            "expiresAt",
            now.plusSeconds(120).toString(),
            "correlationId",
            UUID.randomUUID().toString());
    final MockMvc mvc;

    Fixture() {
      when(store.tx(any())).thenAnswer(call -> ((Supplier<?>) call.getArgument(0)).get());
      // Browser endpoints resolve the public challenge id, not a proof nonce lookup key.
      when(store.byId(eq("uac_challenge"), eq(id), anyBoolean())).thenReturn(row);
      var flow =
          new UacFlow(store, null, null, null, null, login, null, Clock.fixed(now, ZoneOffset.UTC));
      mvc =
          new StandaloneMockMvcBuilder(
              new UacLoginController(flow, null, login, identity(access), null, store, null)) {
            @Override
            protected Map<String, Object> extendMvcSingletons(
                jakarta.servlet.ServletContext servletContext) {
              // Standalone MVC omits the adapter used by its real preflight request handler.
              return Map.of(
                  "httpRequestHandlerAdapter",
                  new org.springframework.web.servlet.mvc.HttpRequestHandlerAdapter());
            }
          }.setControllerAdvice(
                  new ProblemExceptionHandler(
                      new Problems(new ErrorMessages(List.of(), Locale.ENGLISH))))
              .build();
    }

    MockHttpServletRequestBuilder request(String method, String role, String cookie) {
      String origin = role.equals("admin") ? access.adminUrl() : access.portalUrl();
      var request =
          org.springframework.test.web.servlet.request.MockMvcRequestBuilders.request(
                  org.springframework.http.HttpMethod.valueOf(method), path)
              .with(
                  r -> {
                    r.setRemoteAddr("127.0.0.1");
                    return r;
                  })
              .header("Host", URI.create(origin).getRawAuthority())
              .header("X-Auriga-Entry-Origin", origin)
              .header("X-Auriga-Entry-Role", role)
              .header("X-Auriga-Client-IP", "192.0.2.11")
              .header(HEADER, "same-origin");
      if (cookie != null)
        request.cookie(
            new Cookie(
                role.equals("admin") ? "auriga_admin_login_tx" : "auriga_portal_login_tx", cookie));
      return request;
    }
  }
}
