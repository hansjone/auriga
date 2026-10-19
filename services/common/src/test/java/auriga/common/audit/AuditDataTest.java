package auriga.common.audit;

import static org.junit.jupiter.api.Assertions.*;

import auriga.common.context.RequestContext;
import auriga.common.events.EventSchemas;
import auriga.common.security.AurigaPrincipal;
import auriga.common.security.PrincipalType;
import auriga.common.security.TokenContext;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.UUID;
import org.junit.jupiter.api.Test;
import tools.jackson.databind.json.JsonMapper;
import tools.jackson.databind.node.ObjectNode;

class AuditDataTest {
  private static final JsonMapper JSON = JsonMapper.builder().build();

  @Test
  void secretFieldsKeepOnlyTheirName() {
    AuditChanges changes =
        AuditChanges.create()
            .change("email", "a@example.com", "b@example.com")
            .change("password", "old-secret-value", "new-secret-value")
            .change("clientSecret", "s1", "s2")
            .change("totpSeed", "x", "y")
            .secret("recoveryCodes");
    String json = JSON.writeValueAsString(changes.asMap());
    assertFalse(json.contains("secret-value"), json);
    assertFalse(json.contains("\"s1\""), json);
    assertTrue(json.contains("\"password\":{\"redacted\":true}"), json);
    assertTrue(json.contains("\"recoveryCodes\":{\"redacted\":true}"), json);
    assertTrue(json.contains("b@example.com"), json);
  }

  @Test
  void secretNameDetection() {
    for (String s :
        List.of(
            "password",
            "newPassword",
            "api_key",
            "apiKey",
            "otp",
            "otp_code",
            "pin",
            "refreshToken",
            "privateKey")) {
      assertTrue(AuditChanges.isSecretName(s), s);
    }
    for (String s : List.of("email", "displayName", "option", "shipping", "spinner", "keyId")) {
      assertFalse(AuditChanges.isSecretName(s), s);
    }
  }

  @Test
  void dataMatchesContractAndUsesRequestActor() {
    var principal =
        new AurigaPrincipal(
            "0192a1b2-0000-7000-8000-0000000000a1",
            UUID.randomUUID(),
            PrincipalType.USER,
            TokenContext.ADMIN,
            UUID.randomUUID().toString(),
            null,
            null,
            List.of(),
            Set.of(),
            null,
            null,
            "jti");
    RequestContext request =
        RequestContext.start("req-7", null)
            .withPrincipal(principal)
            .withClient("192.0.2.10", "test-agent");
    AuditEntry entry =
        AuditEntry.builder("org.member.disable")
            .target("member", "m-1", "Alice")
            .changes(AuditChanges.create().change("status", "active", "disabled"))
            .detail("reason", "left")
            .detail("token", "should-not-appear")
            .build();
    Map<String, Object> data = OutboxAuditLogger.toData(entry, request);
    @SuppressWarnings("unchecked")
    Map<String, Object> actor = (Map<String, Object>) data.get("actor");
    assertEquals("user", actor.get("type"));
    assertEquals(principal.subject(), actor.get("id"));
    assertEquals("192.0.2.10", actor.get("ip"));
    assertEquals(principal.sessionId(), actor.get("sessionId"));
    assertEquals("req-7", data.get("requestId"));
    assertFalse(JSON.writeValueAsString(data).contains("should-not-appear"));

    ObjectNode envelope = JSON.createObjectNode();
    envelope.put("specversion", "1.0");
    envelope.put("id", UUID.randomUUID().toString());
    envelope.put("source", "auriga/org");
    envelope.put("type", "audit.recorded");
    envelope.put("time", "2026-10-19T08:00:00Z");
    envelope.put("datacontenttype", "application/json");
    envelope.put("dataversion", "1");
    envelope.put("tenantid", UUID.randomUUID().toString());
    envelope.set("data", JSON.valueToTree(data));
    assertDoesNotThrow(() -> new EventSchemas().validate(envelope));
  }

  @Test
  void systemActorWithoutRequest() {
    Map<String, Object> data =
        OutboxAuditLogger.toData(
            AuditEntry.builder("ops.backup.run").failure("common.internal").build(), null);
    @SuppressWarnings("unchecked")
    Map<String, Object> actor = (Map<String, Object>) data.get("actor");
    assertEquals("system", actor.get("type"));
    assertTrue(actor.containsKey("id"));
    assertNull(actor.get("id"));
    assertEquals("failure", data.get("result"));
  }
}
