package auriga.iam;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.ArgumentMatchers.*;
import static org.mockito.Mockito.*;

import auriga.common.context.*;
import auriga.common.error.AurigaException;
import auriga.common.security.*;
import auriga.iam.api.*;
import java.time.*;
import java.util.*;
import java.util.function.Supplier;
import org.junit.jupiter.api.Test;

class PlacementAuthorizationTest {
  final UUID tenant = UUID.randomUUID();
  final Instant now = Instant.parse("2026-11-15T00:00:00Z");
  final IamStore store = mock(IamStore.class);
  final SessionManager sessions = mock(SessionManager.class);
  final PermissionResolver permissions = mock(PermissionResolver.class);
  final IamPlacementAuthorization auth =
      new IamPlacementAuthorization(store, sessions, permissions, Clock.fixed(now, ZoneOffset.UTC));

  AurigaPrincipal caller(TokenContext context, UUID tid) {
    return new AurigaPrincipal(
        "actor",
        tid,
        PrincipalType.USER,
        context,
        "sid",
        null,
        "member",
        List.of("admin"),
        Set.of("workspace.placements.write"),
        now,
        now,
        "token");
  }

  void run(AurigaPrincipal caller, Runnable work) {
    TenantContext.run(
        tenant, () -> RequestContext.start("test", null).withPrincipal(caller).run(work));
  }

  void denied(String code, Runnable work) {
    assertEquals(code, assertThrows(AurigaException.class, work::run).errorCode().code());
  }

  @Test
  void builtinCatalogAndNonAdminTokenFilters() {
    var expected = Set.of("workspace.placements.read", "workspace.placements.write");
    for (String role : List.of("owner", "admin"))
      assertTrue(M3PermissionCatalog.forRole(role).containsAll(expected));
    assertTrue(Collections.disjoint(M3PermissionCatalog.forRole("member"), expected));
    for (String permission : expected) {
      assertTrue(TokenService.allowedPermission(permission, "admin"));
      for (String context : List.of("portal", "device", "instance"))
        assertFalse(TokenService.allowedPermission(permission, context));
    }
  }

  @Test
  void wSubstituteRequiresLiveAdminPermissionSessionAndStrictStepUp() {
    when(store.tx(any())).thenAnswer(i -> ((Supplier<?>) i.getArgument(0)).get());
    var session = Json.object("accountId", "actor", "audience", "admin");
    when(sessions.activeById("sid")).thenReturn(session);
    when(permissions.resolve(tenant, "actor", "admin"))
        .thenReturn(Set.of("workspace.placements.read", "workspace.placements.write"));
    run(
        caller(TokenContext.ADMIN, tenant),
        () -> {
          auth.requireRead();
          denied("iam.step_up_required", auth::requireWrite);
          for (long seconds : List.of(-1L, 600L, 601L)) {
            session.put("stepUpAt", now.minusSeconds(seconds).toString());
            denied("iam.step_up_required", auth::requireWrite);
          }
          for (long seconds : List.of(0L, 599L)) {
            session.put("stepUpAt", now.minusSeconds(seconds).toString());
            auth.requireWrite();
          }
          when(permissions.resolve(tenant, "actor", "admin")).thenReturn(Set.of());
          denied("iam.forbidden", auth::requireWrite);
          when(sessions.activeById("sid")).thenReturn(null);
          denied("iam.unauthenticated", auth::requireRead);
        });
    for (TokenContext context : List.of(TokenContext.PORTAL, TokenContext.DEVICE))
      run(caller(context, tenant), () -> denied("iam.forbidden", auth::requireWrite));
    run(
        caller(TokenContext.ADMIN, UUID.randomUUID()),
        () -> denied("iam.forbidden", auth::requireWrite));
  }
}
