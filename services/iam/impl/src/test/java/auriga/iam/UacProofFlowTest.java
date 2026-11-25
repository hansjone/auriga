package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.iam.api.UacEnterpriseAdapter;
import java.time.*;
import java.util.*;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.function.Supplier;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.CsvSource;
import tools.jackson.databind.JsonNode;
import tools.jackson.databind.node.ObjectNode;

class UacProofFlowTest {
  final UUID tenant = UUID.randomUUID();
  final Instant now = Instant.parse("2026-11-25T00:00:00Z");
  final IamStore store = mock(IamStore.class);
  final UacEnterpriseAdapter adapter = mock(UacEnterpriseAdapter.class);
  final UacProviderService providers = mock(UacProviderService.class);
  final ProviderManager generic = mock(ProviderManager.class);
  final LoginManager login = mock(LoginManager.class);
  final UacSessions uac = mock(UacSessions.class);
  final AtomicBoolean inTx = new AtomicBoolean();
  final Map<String, ObjectNode> rows = new HashMap<>();
  final ObjectNode provider =
      Json.object("id", UUID.randomUUID().toString(), "enabled", true, "uacConfigured", true);
  final ObjectNode body =
      Json.object(
          "providerId",
          provider.path("id"),
          "nativeConnect",
          Json.object(
              "clientId",
              "native-test",
              "codeChallenge",
              Secrets.random(),
              "codeChallengeMethod",
              "S256"),
          "uacProof",
          Json.object(
              "empNo",
              "001Ab",
              "token",
              "synthetic-token",
              "nonce",
              Secrets.random(),
              "issuedAt",
              now.toString()));
  final UacFlow flow =
      new UacFlow(
          store,
          providers,
          generic,
          adapter,
          uac,
          login,
          mock(SessionManager.class),
          Clock.fixed(now, ZoneOffset.UTC));

  UacProofFlowTest() {
    when(store.tx(any()))
        .thenAnswer(
            i -> {
              assertFalse(inTx.getAndSet(true));
              try {
                return ((Supplier<?>) i.getArgument(0)).get();
              } finally {
                inTx.set(false);
              }
            });
    when(providers.row(anyString(), anyBoolean())).thenAnswer(i -> provider.deepCopy());
    when(generic.decryptedConfig(any())).thenReturn(Json.object());
    doAnswer(
            i -> {
              LoginManager.validateNative(i.getArgument(0), i.getArgument(1));
              return null;
            })
        .when(login)
        .validateUacNative(any(), anyString());
    when(login.beginUacNative(any(), any(), anyString()))
        .thenReturn(
            Json.object(
                "deviceCode",
                "synthetic-device",
                "expiresAt",
                now.plusSeconds(300).toString(),
                "interval",
                5));
    when(store.get(anyString(), anyString(), anyBoolean()))
        .thenAnswer(i -> rows.get(i.getArgument(0) + ":" + i.getArgument(1)));
    when(store.insert(anyString(), anyString(), any(), any()))
        .thenAnswer(
            i -> {
              ObjectNode value = i.getArgument(2);
              value.put("id", UUID.randomUUID().toString());
              rows.put(i.getArgument(0) + ":" + i.getArgument(1), value);
              return value;
            });
    when(uac.seal(any(), anyString()))
        .thenAnswer(i -> Json.object("testEnvelope", i.getArgument(0)));
    when(uac.open(any(), anyString()))
        .thenAnswer(i -> ((JsonNode) i.getArgument(0)).path("testEnvelope").deepCopy());
    when(adapter.verifyProof(any(), any()))
        .thenAnswer(
            i -> {
              assertFalse(inTx.get());
              assertEquals(Set.of("empNo", "token"), ((JsonNode) i.getArgument(1)).propertyNames());
              return Json.object(
                  "success", true, "empNo", "001Ab", "token", "replacement-synthetic");
            });
  }

  UacFlow.Created create(ObjectNode request, String audience, String origin) {
    return TenantContext.call(
        tenant, () -> flow.create(request, audience, origin, "192.0.2.1", null, null));
  }

  UacFlow.Created create() {
    return create(body, "portal", "https://portal.example.com");
  }

  @Test
  void repeatsOnlyTheSameScopedChallengeWithoutRevalidatingOrIssuingAnotherGrant() {
    var first = create();
    for (int i = 0; i < 4; i++) {
      var repeat = create();
      assertEquals(first.challenge(), repeat.challenge());
      assertEquals(first.binding(), repeat.binding());
    }
    verify(adapter).verifyProof(any(), any());
    verify(login).beginUacNative(any(), any(), anyString());
    assertEquals(2, rows.size());
    assertThrows(AurigaException.class, () -> create(body, "portal", "https://other.example.com"));
    body.withObject("/uacProof").put("empNo", "002Cd");
    assertThrows(AurigaException.class, this::create);
  }

  @Test
  void rejectsUntrustedFieldsPortalMismatchAndStaleOrFutureNonceBeforeUpstream() {
    for (String field : List.of("clientIP", "accountId", "sessionId", "generation")) {
      var request = body.deepCopy();
      request.withObject("/uacProof").put(field, "synthetic");
      assertThrows(
          AurigaException.class, () -> create(request, "portal", "https://portal.example.com"));
    }
    assertThrows(AurigaException.class, () -> create(body, "admin", "https://admin.example.com"));
    for (long seconds : List.of(-121L, 31L)) {
      var request = body.deepCopy();
      request.withObject("/uacProof").put("issuedAt", now.plusSeconds(seconds).toString());
      assertThrows(
          AurigaException.class, () -> create(request, "portal", "https://portal.example.com"));
    }
    verifyNoInteractions(adapter);
  }

  @ParameterizedTest
  @CsvSource({
    "business_rejected,,iam.uac_proof_rejected",
    "identity_mismatch,,iam.uac_proof_rejected",
    "upstream_fault,missing_subject,iam.uac_proof_subject_required",
    "upstream_fault,invalid_success_payload,iam.uac_proof_subject_required",
    "upstream_fault,transport_error,iam.uac_proof_unavailable",
    "rate_limited,,common.rate_limited"
  })
  void proofFailuresNeverCreateTransactions(String failure, String reason, String code) {
    doReturn(Json.object("success", false, "failureClass", failure, "faultReason", reason))
        .when(adapter)
        .verifyProof(any(), any());
    assertEquals(code, assertThrows(AurigaException.class, this::create).errorCode().code());
    assertTrue(rows.isEmpty());
  }

  @Test
  void disabledProviderAndConfigurationRaceFailClosed() {
    provider.put("enabled", false);
    assertEquals(
        "iam.provider_disabled",
        assertThrows(AurigaException.class, this::create).errorCode().code());
    verifyNoInteractions(adapter);
    provider.put("enabled", true);
    doAnswer(
            i -> {
              assertFalse(inTx.get());
              provider.put("revision", 2);
              return Json.object(
                  "success", true, "empNo", "001Ab", "token", "replacement-synthetic");
            })
        .when(adapter)
        .verifyProof(any(), any());
    assertEquals(
        "iam.provider_disabled",
        assertThrows(AurigaException.class, this::create).errorCode().code());
    assertTrue(rows.isEmpty());
  }
}
