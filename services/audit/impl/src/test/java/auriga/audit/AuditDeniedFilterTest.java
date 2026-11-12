package auriga.audit;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

import auriga.common.context.RequestContext;
import auriga.common.security.*;
import java.time.Instant;
import java.util.*;
import org.junit.jupiter.api.*;
import org.springframework.mock.web.*;
import org.springframework.security.core.context.SecurityContextHolder;

class AuditDeniedFilterTest {
  @AfterEach
  void clear() {
    SecurityContextHolder.clearContext();
  }

  void denied(AuditDeniedFilter filter, String ip, String subject, int status) throws Exception {
    SecurityContextHolder.clearContext();
    if (subject != null)
      SecurityContextHolder.getContext()
          .setAuthentication(
              new AurigaAuthentication(
                  new AurigaPrincipal(
                      subject,
                      UUID.fromString("00000000-0000-0000-0000-000000000001"),
                      PrincipalType.USER,
                      TokenContext.ADMIN,
                      null,
                      null,
                      null,
                      List.of(),
                      Set.of(),
                      Instant.now(),
                      null,
                      "test")));
    var request = new MockHttpServletRequest("GET", "/api/audit/v1/admin/events");
    request.setRemoteAddr(ip);
    var response = new MockHttpServletResponse();
    filter.doFilter(
        request,
        response,
        (req, res) -> {
          res.getWriter().write("original-denial");
          ((jakarta.servlet.http.HttpServletResponse) res).setStatus(status);
        });
    assertEquals(status, response.getStatus());
    assertEquals("original-denial", response.getContentAsString());
  }

  @Test
  void repeatedDenialsAreBoundedBySourceAndAuthenticatedSubject() throws Exception {
    var store = mock(AuditStore.class);
    var filter = new AuditDeniedFilter(store);
    for (int i = 0; i < 20; i++) denied(filter, "192.0.2.10", "alice-" + i, 403);
    verify(store, atMost(5)).recordDenied();
    reset(store);
    filter = new AuditDeniedFilter(store);
    for (int i = 0; i < 20; i++) denied(filter, "192.0.2." + (i + 1), "alice", 403);
    verify(store, times(5)).recordDenied();
  }

  @Test
  void globalBoundAndNextMinuteRecovery() throws Exception {
    var store = mock(AuditStore.class);
    var clock = new java.util.concurrent.atomic.AtomicLong();
    var filter = new AuditDeniedFilter(store, clock::get);
    for (int i = 0; i < 150; i++) denied(filter, "peer-" + i, null, 401);
    verify(store, times(100)).recordDenied();
    clock.addAndGet(java.time.Duration.ofMinutes(1).toNanos());
    denied(filter, "peer-0", null, 401);
    verify(store, times(101)).recordDenied();
  }

  @Test
  void storageFailureDoesNotReplace401Or403() throws Exception {
    var store = mock(AuditStore.class);
    doThrow(new IllegalStateException("database unavailable")).when(store).recordDenied();
    denied(new AuditDeniedFilter(store), "192.0.2.10", null, 401);
    denied(new AuditDeniedFilter(store), "192.0.2.10", "alice", 403);
  }

  @Test
  void anonymousIdentityComesFromPeerNotUntrustedHeaders() throws Exception {
    var store = mock(AuditStore.class);
    doAnswer(
            call -> {
              var ctx = RequestContext.current().orElseThrow();
              assertNull(ctx.principal());
              assertEquals("192.0.2.10", ctx.clientIp());
              return null;
            })
        .when(store)
        .recordDenied();
    denied(new AuditDeniedFilter(store), "192.0.2.10", null, 401);
    verify(store).recordDenied();
  }
}
