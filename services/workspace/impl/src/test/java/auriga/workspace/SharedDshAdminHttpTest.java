package auriga.workspace;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.*;
import static org.springframework.test.web.servlet.result.MockMvcResultMatchers.*;

import auriga.common.error.*;
import auriga.common.security.*;
import auriga.iam.api.PlacementAuthorization;
import auriga.workspace.app.*;
import auriga.workspace.web.SharedDshController;
import java.time.*;
import java.util.*;
import org.junit.jupiter.api.*;
import org.springframework.security.core.context.SecurityContextHolder;
import org.springframework.test.web.servlet.MockMvc;
import org.springframework.test.web.servlet.setup.MockMvcBuilders;

class SharedDshAdminHttpTest {
  final UUID member = UUID.randomUUID(), actor = UUID.randomUUID(), tenant = UUID.randomUUID();
  final Instant now = Instant.ofEpochSecond(1000);
  final SharedDshTickets tickets = mock(SharedDshTickets.class);
  final SharedDshAdmin admin = mock(SharedDshAdmin.class);
  final SharedDshController.RequestGate gate = mock(SharedDshController.RequestGate.class);
  final PlacementAuthorization authorization = mock(PlacementAuthorization.class);
  MockMvc mvc;

  void login(TokenContext context, Set<String> permissions, Instant step) {
    SecurityContextHolder.getContext()
        .setAuthentication(
            new AurigaAuthentication(
                new AurigaPrincipal(
                    actor.toString(),
                    tenant,
                    PrincipalType.USER,
                    context,
                    UUID.randomUUID().toString(),
                    null,
                    "member",
                    List.of(),
                    permissions,
                    now,
                    step,
                    "test")));
  }

  @BeforeEach
  void setup() {
    var controller =
        new SharedDshController(
            mock(SharedDshPlacements.class),
            tickets,
            gate,
            Clock.fixed(now, ZoneOffset.UTC),
            admin,
            authorization);
    mvc =
        MockMvcBuilders.standaloneSetup(controller)
            .setControllerAdvice(
                new ProblemExceptionHandler(
                    new Problems(new ErrorMessages(List.of(), Locale.ENGLISH))))
            .build();
    login(
        TokenContext.ADMIN, Set.of("workspace.placements.read", "workspace.placements.write"), now);
  }

  @AfterEach
  void cleanup() {
    SecurityContextHolder.clearContext();
  }

  @Test
  void portalOpenReturnsLegacyMigrationProblem() throws Exception {
    login(TokenContext.PORTAL, Set.of(), now);
    when(tickets.open(eq(actor), any(UUID.class), eq("open-key")))
        .thenThrow(
            new AurigaException(auriga.workspace.domain.SharedDshErrors.LEGACY_MIGRATION_REQUIRED));
    mvc.perform(post("/api/workspace/v1/me/placement/open").header("Idempotency-Key", "open-key"))
        .andExpect(status().isConflict())
        .andExpect(jsonPath("$.code").value("shared.legacy_migration_required"));
  }

  @Test
  void unavailableAuthorizationFailsClosedAs503() throws Exception {
    doThrow(new IllegalStateException("IAM unavailable")).when(authorization).requireRead();
    mvc.perform(get("/api/workspace/v1/admin/members/{id}/placement", member))
        .andExpect(status().isServiceUnavailable());
    verifyNoInteractions(admin);
  }

  @Test
  void memberReadReturnsOpaqueTagAndNeverCachesAndRechecksIam() throws Exception {
    when(admin.member(member))
        .thenReturn(new SharedDshAdmin.Result(Map.of("memberId", member), "\"opaque\""));
    mvc.perform(get("/api/workspace/v1/admin/members/{id}/placement", member))
        .andExpect(status().isOk())
        .andExpect(header().string("ETag", "\"opaque\""))
        .andExpect(header().string("Cache-Control", "no-store"));
    verify(authorization).requireRead();
    doThrow(new AurigaException(CommonErrors.FORBIDDEN)).when(authorization).requireRead();
    mvc.perform(get("/api/workspace/v1/admin/members/{id}/placement", member))
        .andExpect(status().isForbidden());
  }

  @Test
  void portalAndMissingPermissionsCannotReadAndUnavailableCapacityIs503() throws Exception {
    login(TokenContext.PORTAL, Set.of("workspace.placements.read"), now);
    mvc.perform(get("/api/workspace/v1/admin/placement-capacity"))
        .andExpect(status().isForbidden());
    login(TokenContext.ADMIN, Set.of(), now);
    mvc.perform(get("/api/workspace/v1/admin/placement-capacity"))
        .andExpect(status().isForbidden());
    login(TokenContext.ADMIN, Set.of("workspace.placements.read"), now);
    mvc.perform(get("/api/workspace/v1/admin/placement-capacity"))
        .andExpect(status().isServiceUnavailable());
    verifyNoInteractions(admin);
  }

  @Test
  void writesEnforceOriginCsrfGateWithoutStepUp() throws Exception {
    login(TokenContext.ADMIN, Set.of("workspace.placements.write"), null);
    doThrow(new AurigaException(CommonErrors.FORBIDDEN)).when(gate).require(any(), any(), eq(true));
    mvc.perform(
            put("/api/workspace/v1/admin/members/{id}/dedicated-entitlement", member)
                .header("Idempotency-Key", "key")
                .contentType("application/json")
                .content("{\"granted\":true,\"reason\":\"grant\"}"))
        .andExpect(status().isForbidden());
    verifyNoInteractions(admin);
  }

  @Test
  void lookupRequiresHeaderAndStartRequiresExplicitExportConfirmation() throws Exception {
    mvc.perform(
            get("/api/workspace/v1/admin/members/{id}/placement-switches/lookup", member)
                .param("Idempotency-Key", "key"))
        .andExpect(status().isBadRequest());
    mvc.perform(
            post("/api/workspace/v1/admin/placement-rebuild-plans/{id}/start", member)
                .header("Idempotency-Key", "key")
                .header("If-Match", "\"opaque\"")
                .contentType("application/json")
                .content(
                    "{\"exportConfirmed\":false,\"sessionPolicy\":\"discard\",\"filePolicy\":\"archive-and-reset\"}"))
        .andExpect(status().isBadRequest());
    verifyNoInteractions(admin);
  }
}
