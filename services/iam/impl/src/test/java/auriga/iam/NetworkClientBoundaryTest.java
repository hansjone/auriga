package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.post;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;

import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.security.*;
import auriga.iam.api.NetworkAuthority;
import java.time.*;
import java.util.*;
import org.junit.jupiter.api.*;
import org.springframework.security.core.context.SecurityContextHolder;
import org.springframework.test.web.servlet.setup.MockMvcBuilders;

class NetworkClientBoundaryTest {
  final UUID tenant = UUID.randomUUID(), account = UUID.randomUUID(), device = UUID.randomUUID();
  final IamStore store = mock(IamStore.class);
  final NetworkAuthority authority = mock(NetworkAuthority.class);
  final Clock clock = Clock.fixed(Instant.parse("2026-01-01T00:00:00Z"), ZoneOffset.UTC);
  final IamNetworkClient client =
      new IamNetworkClient(authority, store, clock, "server-a", Set.of("server-a", "server-b"));

  void principal(
      PrincipalType type, TokenContext context, UUID principalTenant, Set<String> permissions) {
    SecurityContextHolder.getContext()
        .setAuthentication(
            new AurigaAuthentication(
                new AurigaPrincipal(
                    account.toString(),
                    principalTenant,
                    type,
                    context,
                    null,
                    device.toString(),
                    "member",
                    List.of(),
                    permissions,
                    null,
                    null,
                    null)));
  }

  void device() {
    principal(
        PrincipalType.DEVICE, TokenContext.DEVICE, tenant, Set.of("auriga.connect.collab.use"));
  }

  @AfterEach
  void clear() {
    SecurityContextHolder.clearContext();
  }

  static void error(String code, Runnable action) {
    assertEquals(code, assertThrows(AurigaException.class, action::run).errorCode().code());
  }

  @Test
  void rejectsMissingWrongTenantAndNonDeviceSubjectsBeforeAuthorityOrRateStorage() {
    TenantContext.run(
        tenant,
        () -> {
          error("common.unauthenticated", () -> client.register("access"));
          for (var type : PrincipalType.values()) {
            for (var context : TokenContext.values()) {
              if (type == PrincipalType.DEVICE && context == TokenContext.DEVICE) continue;
              principal(type, context, tenant, Set.of("auriga.connect.collab.use"));
              error("common.forbidden", () -> client.register("access"));
              error("common.forbidden", () -> client.prepare("access", "1"));
            }
          }
          principal(
              PrincipalType.DEVICE,
              TokenContext.DEVICE,
              UUID.randomUUID(),
              Set.of("auriga.connect.collab.use"));
          error("common.forbidden", () -> client.register("access"));
          principal(PrincipalType.DEVICE, TokenContext.DEVICE, tenant, Set.of());
          error("common.forbidden", () -> client.prepare("access", "1"));
        });
    verifyNoInteractions(authority, store);
  }

  @Test
  void selectsConfiguredAudienceAndRejectsUnconfiguredOrInvalidNode() {
    assertThrows(
        IllegalArgumentException.class,
        () -> new IamNetworkClient(authority, store, clock, "attacker", Set.of("server-a")));
    device();
    TenantContext.run(
        tenant,
        () -> {
          for (Set<String> servers : List.of(Set.<String>of(), Set.of("server-a", "server-b"))) {
            var unavailable = new IamNetworkClient(authority, store, clock, "", servers);
            error("common.unavailable", () -> unavailable.prepare("access", "1"));
          }
          for (String node : List.of("0", "01", "-1", "18446744073709551616"))
            error("common.validation_failed", () -> client.prepare("access", node));
          client.prepare("access", "1");
        });
    verify(authority).prepare("access", "1", "server-a");
  }

  @Test
  void singleAllowlistedServerIsSelectedWithoutClientInput() {
    device();
    TenantContext.run(
        tenant,
        () ->
            new IamNetworkClient(authority, store, clock, "", Set.of("server-b"))
                .prepare("access", "1"));
    verify(authority).prepare("access", "1", "server-b");
  }

  @Test
  void exhaustedPersistentRateBudgetStopsBothOperations() {
    device();
    doThrow(new AurigaException(auriga.common.error.CommonErrors.RATE_LIMITED))
        .when(store)
        .limit(anyString(), any(), anyInt());
    TenantContext.run(
        tenant,
        () -> {
          error("common.rate_limited", () -> client.register("access"));
          error("common.rate_limited", () -> client.prepare("access", "1"));
        });
    verifyNoInteractions(authority);
  }

  @Test
  void httpRejectsAudienceIdentityAndTokenInjectionAndNeverCachesSecrets() throws Exception {
    var controller = new IamNetworkClientController(client, new ContractSchemas());
    var mvc =
        MockMvcBuilders.standaloneSetup(controller)
            .setControllerAdvice(
                new auriga.common.error.ProblemExceptionHandler(
                    new auriga.common.error.Problems(
                        new auriga.common.error.ErrorMessages(List.of(), Locale.ENGLISH))))
            .build();
    device();
    var node = new NetworkAuthority.Node("1", "1", account.toString(), device.toString(), false);
    when(authority.register("access")).thenReturn(node);
    when(authority.prepare("access", "1", "server-a"))
        .thenReturn(
            new NetworkAuthority.Ticket(
                "t".repeat(43),
                node,
                "server-a",
                clock.instant().plusSeconds(30),
                clock.instant().plusSeconds(3600)));
    TenantContext.run(
        tenant,
        () -> {
          try {
            for (String path : List.of("register", "tickets")) {
              for (String field :
                  List.of("serverAudience", "tenant", "account", "device", "accessToken")) {
                var body = Json.object(field, "attacker");
                if (path.equals("tickets")) body.put("node", "1");
                var rejected =
                    mvc.perform(
                            post("/api/iam/v1/network/" + path)
                                .header("Authorization", "Bearer access")
                                .contentType("application/json")
                                .content(body.toString()))
                        .andExpect(status().isBadRequest())
                        .andReturn()
                        .getResponse();
                assertEquals(
                    "common.validation_failed",
                    Json.read(rejected.getContentAsString()).path("code").asString());
              }
            }
            verifyNoInteractions(authority, store);
            for (String authorization :
                List.of("", "Basic access", "Bearer ", "Bearer access extra")) {
              var rejected =
                  mvc.perform(
                          post("/api/iam/v1/network/register")
                              .header("Authorization", authorization)
                              .contentType("application/json")
                              .content("{}"))
                      .andExpect(status().isUnauthorized())
                      .andReturn()
                      .getResponse();
              assertEquals(
                  "common.unauthenticated",
                  Json.read(rejected.getContentAsString()).path("code").asString());
            }
            mvc.perform(
                    post("/api/iam/v1/network/register")
                        .header("Authorization", "Bearer access", "Bearer second")
                        .contentType("application/json")
                        .content("{}"))
                .andExpect(status().isUnauthorized());
            verifyNoInteractions(authority, store);
            var registration =
                mvc.perform(
                        post("/api/iam/v1/network/register")
                            .header("Authorization", "Bearer access")
                            .contentType("application/json")
                            .content("{}"))
                    .andExpect(status().isOk())
                    .andReturn()
                    .getResponse();
            assertEquals("no-store", registration.getHeader("Cache-Control"));
            assertEquals(
                "aur1:1.1.0",
                Json.read(registration.getContentAsString()).path("systemAddress").asString());
            var ticket =
                mvc.perform(
                        post("/api/iam/v1/network/tickets")
                            .header("Authorization", "Bearer access")
                            .contentType("application/json")
                            .content("{\"node\":\"1\"}"))
                    .andExpect(status().isOk())
                    .andReturn()
                    .getResponse();
            assertEquals("no-store", ticket.getHeader("Cache-Control"));
            var json = Json.read(ticket.getContentAsString());
            assertEquals("2026-01-01T01:00:00Z", json.path("connectionExpiresAt").asString());
            assertEquals("2026-01-01T00:00:30Z", json.path("expiresAt").asString());
            assertEquals("server-a", json.path("serverAudience").asString());
            assertFalse(ticket.getContentAsString().contains("Bearer access"));
          } catch (Exception failure) {
            throw new RuntimeException(failure);
          }
        });
  }
}
