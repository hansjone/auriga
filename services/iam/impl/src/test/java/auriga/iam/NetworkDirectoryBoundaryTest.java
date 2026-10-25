package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.get;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.status;

import auriga.common.context.TenantContext;
import auriga.common.security.*;
import auriga.contracts.iam.v1.*;
import auriga.iam.api.NetworkDirectory;
import java.time.Clock;
import java.util.*;
import org.junit.jupiter.api.*;
import org.springframework.security.core.context.SecurityContextHolder;
import org.springframework.test.web.servlet.setup.MockMvcBuilders;

/** Authorization and HTTP projection tests; the separate PostgreSQL suite covers query/RLS. */
class NetworkDirectoryBoundaryTest {
  final UUID tenant = UUID.randomUUID();
  final IamStore store = mock(IamStore.class);
  final SessionManager sessions = mock(SessionManager.class);
  final DeviceAccessAuthority devices = mock(DeviceAccessAuthority.class);
  final IamNetworkDirectory directory =
      new IamNetworkDirectory(store, sessions, devices, Clock.systemUTC());

  void principal(
      String subject,
      UUID principalTenant,
      PrincipalType type,
      TokenContext context,
      Set<String> permissions) {
    SecurityContextHolder.getContext()
        .setAuthentication(
            new AurigaAuthentication(
                new AurigaPrincipal(
                    subject,
                    principalTenant,
                    type,
                    context,
                    UUID.randomUUID().toString(),
                    UUID.randomUUID().toString(),
                    "member",
                    List.of(),
                    permissions,
                    null,
                    null,
                    null)));
  }

  void user() {
    principal(
        UUID.randomUUID().toString(), tenant, PrincipalType.USER, TokenContext.PORTAL, Set.of());
  }

  @AfterEach
  void clear() {
    SecurityContextHolder.clearContext();
  }

  @Test
  void
      directoryRejectsUnauthenticatedWrongTenantAndServiceOrUnexpectedContextPrincipalsBeforeStorage() {
    clearInvocations(store);
    TenantContext.run(
        tenant,
        () -> {
          NetworkDirectoryPostgresTest.error(
              "common.unauthenticated", () -> directory.list(50, ""));
          principal(
              "svc:collab",
              TenantContext.PLATFORM_TENANT,
              PrincipalType.INTERNAL_SERVICE,
              TokenContext.SERVICE,
              Set.of());
          NetworkDirectoryPostgresTest.error("common.forbidden", () -> directory.list(50, ""));
          principal(
              UUID.randomUUID().toString(),
              UUID.randomUUID(),
              PrincipalType.USER,
              TokenContext.PORTAL,
              Set.of());
          NetworkDirectoryPostgresTest.error("common.forbidden", () -> directory.list(50, ""));
          for (var type :
              List.of(PrincipalType.SERVICE_ACCOUNT, PrincipalType.USER, PrincipalType.DEVICE)) {
            principal(
                UUID.randomUUID().toString(),
                tenant,
                type,
                type == PrincipalType.USER ? TokenContext.DEVICE : TokenContext.SERVICE,
                Set.of());
            NetworkDirectoryPostgresTest.error("common.forbidden", () -> directory.list(50, ""));
          }
          principal(
              UUID.randomUUID().toString(),
              tenant,
              PrincipalType.DEVICE,
              TokenContext.DEVICE,
              Set.of());
          NetworkDirectoryPostgresTest.error("common.forbidden", () -> directory.list(50, ""));
        });
    verifyNoInteractions(store, sessions, devices);
  }

  @Test
  void invalidPagingAndForeignOrNonCanonicalCursorNeverReachStorage() {
    user();
    clearInvocations(store);
    TenantContext.run(
        tenant,
        () -> {
          for (int size : new int[] {-1, 0, 201, Integer.MAX_VALUE})
            NetworkDirectoryPostgresTest.error(
                "common.validation_failed", () -> directory.list(size, ""));
          for (String raw :
              List.of(
                  "x".repeat(129),
                  "garbage!",
                  "01",
                  "-1",
                  Base64.getUrlEncoder()
                      .withoutPadding()
                      .encodeToString(
                          (UUID.randomUUID() + ":1")
                              .getBytes(java.nio.charset.StandardCharsets.US_ASCII)),
                  Base64.getUrlEncoder()
                      .withoutPadding()
                      .encodeToString(
                          (tenant + ":01").getBytes(java.nio.charset.StandardCharsets.US_ASCII))))
            NetworkDirectoryPostgresTest.error(
                "common.validation_failed", () -> directory.list(50, raw));
        });
    verifyNoInteractions(store, sessions, devices);
  }

  @Test
  void nodeLookupRejectsCallerSpoofingAndInvalidUnsignedIdsBeforeStorage() {
    clearInvocations(store);
    TenantContext.run(
        tenant,
        () -> {
          for (UUID claimedTenant : List.of(tenant, TenantContext.PLATFORM_TENANT)) {
            principal(
                "svc:collab",
                claimedTenant,
                PrincipalType.SERVICE_ACCOUNT,
                TokenContext.SERVICE,
                Set.of());
            NetworkDirectoryPostgresTest.error(
                "common.forbidden", () -> directory.resolve("1", "1"));
          }
          principal(
              "svc:collab",
              TenantContext.PLATFORM_TENANT,
              PrincipalType.INTERNAL_SERVICE,
              TokenContext.SERVICE,
              Set.of());
          for (String bad : List.of("0", "01", "1.0", "1.2.3", "18446744073709551616")) {
            NetworkDirectoryPostgresTest.error(
                "common.validation_failed", () -> directory.resolve("1", bad));
            NetworkDirectoryPostgresTest.error(
                "common.validation_failed", () -> directory.resolve(bad, "1"));
          }
        });
    verifyNoInteractions(store, sessions, devices);
  }

  @Test
  void httpDefaultsAndProjectionFollowContractAndRejectMalformedLimit() throws Exception {
    var api = mock(NetworkDirectory.class);
    var page =
        ListNetworkSystemsResponse.newBuilder()
            .addItems(
                NetworkSystemEntry.newBuilder()
                    .setSystemAddress("aur1:ffffffffffffffff.ffffffffffffffff.0")
                    .setDisplayName("DSH")
                    .setOwnerUserId(UUID.randomUUID().toString())
                    .setOwnerDisplayName("Alice"))
            .build();
    when(api.list(50, "")).thenReturn(page);
    var mvc = MockMvcBuilders.standaloneSetup(new IamNetworkDirectoryController(api)).build();
    var response =
        mvc.perform(get("/api/iam/v1/network/systems"))
            .andExpect(status().isOk())
            .andReturn()
            .getResponse();
    var json = Json.read(response.getContentAsString());
    new ContractSchemas().validate("network/system-directory.schema.json", json);
    assertEquals(Set.of("items"), json.propertyNames());
    assertEquals(
        Set.of("systemAddress", "displayName", "ownerUserId", "ownerDisplayName"),
        json.path("items").get(0).propertyNames());
    verify(api).list(50, "");
    mvc.perform(get("/api/iam/v1/network/systems").param("limit", "NaN"))
        .andExpect(status().isBadRequest());
    verifyNoMoreInteractions(api);
  }
}
