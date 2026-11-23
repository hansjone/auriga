package auriga.resource;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.context.RequestContext;
import auriga.common.error.*;
import auriga.common.security.*;
import auriga.resource.app.DesktopReleases;
import auriga.resource.web.DesktopController;
import auriga.resource.web.ResourceBody;
import java.time.*;
import java.util.*;
import org.junit.jupiter.api.Test;
import org.springframework.aop.framework.ProxyFactory;
import org.springframework.mock.web.MockHttpServletRequest;

class DesktopControllerAuthorizationTest {
  final Instant now = Instant.parse("2026-11-17T00:00:00Z");
  final DesktopReleases releases = mock(DesktopReleases.class);

  DesktopController controller() {
    var factory = new ProxyFactory(new DesktopController(releases));
    factory.setProxyTargetClass(true);
    factory.addAdvisor(new AuthorizationAdvisor(Clock.fixed(now, ZoneOffset.UTC)));
    return (DesktopController) factory.getProxy();
  }

  AurigaPrincipal actor(TokenContext context, Set<String> permissions, Instant stepUp) {
    return new AurigaPrincipal(
        UUID.randomUUID().toString(),
        UUID.randomUUID(),
        PrincipalType.USER,
        context,
        "session",
        null,
        "member",
        List.of(),
        permissions,
        now,
        stepUp,
        "jti");
  }

  void denied(AurigaPrincipal principal, ErrorCode code, Runnable call) {
    var failure =
        assertThrows(
            AurigaException.class,
            () -> RequestContext.start(null, null).withPrincipal(principal).run(call));
    assertEquals(code, failure.errorCode());
  }

  @Test
  void readsPreflightAndWritesWorkForAuthenticatedAdminWithoutStepUp() throws Exception {
    when(releases.list(any())).thenReturn(ResourceBody.JSON.createObjectNode());
    when(releases.preflight(any(), any())).thenReturn(ResourceBody.JSON.createObjectNode());
    var api = controller();
    var actor = actor(TokenContext.ADMIN, Set.of(DesktopReleases.MANAGE), now.minusSeconds(601));
    RequestContext.start(null, null)
        .withPrincipal(actor)
        .run(
            () -> {
              assertEquals(200, api.list().getStatusCode().value());
              var request = new MockHttpServletRequest();
              request.setContent(
                  "{\"signature\":{}}".getBytes(java.nio.charset.StandardCharsets.UTF_8));
              assertEquals(200, api.preflight(request).getStatusCode().value());
            });
    var noStepUp = actor(TokenContext.ADMIN, Set.of(DesktopReleases.MANAGE), null);
    RequestContext.start(null, null).withPrincipal(noStepUp).run(() -> api.list());
    RequestContext.start(null, null).withPrincipal(noStepUp).run(() -> api.withdraw(2));
    verify(releases).withdraw(noStepUp, 2);
    var request = new MockHttpServletRequest();
    request.addHeader(
        "X-Desktop-Manifest",
        Base64.getUrlEncoder()
            .withoutPadding()
            .encodeToString(
                "{\"signature\":{}}".getBytes(java.nio.charset.StandardCharsets.UTF_8)));
    when(releases.publish(any(), any(), any())).thenReturn(ResourceBody.JSON.createObjectNode());
    assertEquals(
        201,
        RequestContext.start(null, null)
            .withPrincipal(noStepUp)
            .call(() -> api.publish(request))
            .getStatusCode()
            .value());
    verify(releases).publish(eq(noStepUp), any(), any());
  }

  @Test
  void readStillRequiresAdminPermission() {
    var api = controller();
    denied(actor(TokenContext.ADMIN, Set.of(), now), CommonErrors.FORBIDDEN, api::list);
    denied(
        actor(TokenContext.PORTAL, Set.of(DesktopReleases.MANAGE), now),
        CommonErrors.FORBIDDEN,
        api::list);
    denied(null, CommonErrors.UNAUTHENTICATED, api::list);
    verifyNoInteractions(releases);
  }
}
