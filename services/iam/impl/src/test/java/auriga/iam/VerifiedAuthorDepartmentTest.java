package auriga.iam;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.mockito.ArgumentMatchers.any;
import static org.mockito.Mockito.*;

import auriga.common.context.TenantContext;
import auriga.common.security.AurigaPrincipal;
import auriga.common.security.PrincipalType;
import auriga.common.security.TokenContext;
import java.time.Clock;
import java.time.Instant;
import java.time.ZoneOffset;
import java.util.UUID;
import java.util.function.Supplier;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import tools.jackson.databind.node.ObjectNode;

class VerifiedAuthorDepartmentTest {
  private final UUID tenant = UUID.randomUUID();
  private final String subject = UUID.randomUUID().toString();
  private final String sid = UUID.randomUUID().toString();
  private final String did = UUID.randomUUID().toString();
  private final Instant now = Instant.parse("2026-11-26T00:00:00Z");
  private final IamStore store = mock(IamStore.class);
  private final SessionManager sessions = mock(SessionManager.class);
  private final UacSessions uac =
      new UacSessions(store, null, null, null, sessions, Clock.fixed(now, ZoneOffset.UTC), null);
  private final ObjectNode binding =
      Json.object(
          "sourceSessionId", sid, "providerId", UUID.randomUUID().toString(), "authGeneration", 1);
  private final ObjectNode session =
      Json.object(
          "sessionId",
          sid,
          "accountId",
          subject,
          "audience",
          "portal",
          "uacCredentialId",
          UUID.randomUUID().toString(),
          "uacSessionBinding",
          binding,
          "uacLoginInfo",
          Json.object(
              "purpose",
              "login_info",
              "status",
              "available",
              "department",
              "部门甲",
              "updatedAt",
              now.toString()));

  @BeforeEach
  void sessionState() {
    when(store.tx(any())).thenAnswer(call -> call.<Supplier<?>>getArgument(0).get());
    when(store.byId("session", sid, true)).thenReturn(session);
    when(sessions.valid(session)).thenReturn(true);
  }

  @Test
  void userReadsOnlyVerifiedDepartmentOnItsLivingSession() {
    var user = actor(subject, PrincipalType.USER, TokenContext.PORTAL, sid, null);
    assertEquals("", uac.authorDepartment(user));
    TenantContext.run(UUID.randomUUID(), () -> assertEquals("", uac.authorDepartment(user)));
    verifyNoInteractions(store);
    assertEquals("部门甲", lookup(user));
    assertEquals(
        "", lookup(actor("someone-else", PrincipalType.USER, TokenContext.PORTAL, sid, null)));
    assertEquals("", lookup(actor(subject, PrincipalType.USER, TokenContext.ADMIN, sid, null)));
    assertEquals("", lookup(actor(subject, PrincipalType.USER, TokenContext.PORTAL, null, null)));
    assertEquals(
        "",
        lookup(
            actor(
                subject,
                PrincipalType.USER,
                TokenContext.PORTAL,
                UUID.randomUUID().toString(),
                null)));
    when(sessions.valid(session)).thenReturn(false);
    assertEquals("", lookup(user));
  }

  @Test
  void localAndPendingLoginHaveNoDepartment() {
    var user = actor(subject, PrincipalType.USER, TokenContext.PORTAL, sid, null);
    ((ObjectNode) session.path("uacLoginInfo")).put("status", "pending");
    assertEquals("", lookup(user));
    ((ObjectNode) session.path("uacLoginInfo")).put("status", "available");
    session.remove("uacCredentialId");
    session.remove("uacSessionBinding");
    assertEquals("", lookup(user));
  }

  @Test
  void deviceUsesItsFrozenPortalBindingAndRechecksItAfterLocking() {
    var deviceActor = actor(subject, PrincipalType.DEVICE, TokenContext.DEVICE, null, did);
    var device =
        Json.object(
            "sessionId",
            sid,
            "accountId",
            subject,
            "scope",
            "auriga.connect",
            "uacSessionBinding",
            binding.deepCopy());
    when(store.get("device", did, false)).thenReturn(device);
    when(store.get("device", did, true)).thenReturn(device);
    assertEquals("部门甲", lookup(deviceActor));

    var changed = device.deepCopy();
    ((ObjectNode) changed.path("uacSessionBinding")).put("authGeneration", 2);
    when(store.get("device", did, true)).thenReturn(changed);
    assertEquals("", lookup(deviceActor));
    when(store.get("device", did, true)).thenReturn(device);
    device.put("expiresAt", now.toString());
    assertEquals("", lookup(deviceActor));
    device.remove("expiresAt");
    device.put("revoked", true);
    assertEquals("", lookup(deviceActor));
    device.remove("revoked");
    device.remove("uacSessionBinding");
    assertEquals("", lookup(deviceActor));
  }

  private String lookup(AurigaPrincipal actor) {
    return TenantContext.call(tenant, () -> uac.authorDepartment(actor));
  }

  private AurigaPrincipal actor(
      String account, PrincipalType type, TokenContext context, String sessionId, String deviceId) {
    return new AurigaPrincipal(
        account, tenant, type, context, sessionId, deviceId, null, null, null, null, null, null);
  }
}
