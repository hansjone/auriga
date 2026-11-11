package auriga.org;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.mock;

import auriga.common.context.RequestContext;
import auriga.common.context.TenantContext;
import auriga.common.error.AurigaException;
import auriga.common.events.EventPublisher;
import auriga.common.security.*;
import auriga.contracts.deploymentconfig.DeploymentAccess;
import java.time.Clock;
import java.time.Instant;
import java.util.List;
import java.util.Set;
import java.util.UUID;
import org.junit.jupiter.api.Test;

class SecurityEntriesTest {
  private static OrgService service(DeploymentAccess access) {
    return new OrgService(null, mock(EventPublisher.class), Clock.systemUTC(), access);
  }

  private static AurigaPrincipal principal(
      TokenContext context, Set<String> permissions, Instant stepUp) {
    return new AurigaPrincipal(
        UUID.randomUUID().toString(),
        OrgIds.DEFAULT_TENANT,
        PrincipalType.USER,
        context,
        "sid",
        null,
        "member",
        List.of(),
        permissions,
        Instant.now(),
        stepUp,
        "id");
  }

  private static void error(String code, Runnable action) {
    assertEquals(code, assertThrows(AurigaException.class, action::run).errorCode().code());
  }

  @Test
  void noConfigurationFailsClosed() {
    error("common.unavailable", () -> service(null).securityEntries());
  }

  @Test
  void installationProjectionIsTenantBound() throws Exception {
    var access =
        DeploymentAccess.load(
            java.nio.file.Files.readAllBytes(
                java.nio.file.Path.of("../../../contracts/deployment/fixtures/ip-http.json")));
    var svc = service(access);
    var value = TenantContext.call(UUID.fromString(access.tenantId()), svc::securityEntries);
    assertEquals(access.adminUrl(), value.path("adminOrigin").asString());
    assertFalse(value.has("listeners"));
    assertFalse(value.has("instanceControl"));
    error("common.unavailable", () -> TenantContext.call(UUID.randomUUID(), svc::securityEntries));
  }

  @Test
  void entriesRequireAdminAndReadPermission() throws Exception {
    var rules =
        AuthorizationRules.of(
            SecurityEntriesController.class.getMethod("entries"), SecurityEntriesController.class);
    error(
        "common.forbidden",
        () ->
            rules.check(
                principal(TokenContext.PORTAL, Set.of("org.policies.read"), Instant.now()),
                Clock.systemUTC()));
    error(
        "common.forbidden",
        () ->
            rules.check(principal(TokenContext.ADMIN, Set.of(), Instant.now()), Clock.systemUTC()));
    assertDoesNotThrow(
        () ->
            rules.check(
                principal(TokenContext.ADMIN, Set.of("org.policies.read"), null),
                Clock.systemUTC()));
  }

  @Test
  void allPolicyWritersRequireRecentAdminIdentity() {
    var svc = service(null);
    error("common.unauthenticated", () -> svc.putPolicy("egress", OrgJson.object(), 1L));
    for (var actor :
        List.of(
            principal(TokenContext.PORTAL, Set.of("org.policies.write"), Instant.now()),
            principal(TokenContext.ADMIN, Set.of(), Instant.now())))
      RequestContext.start("test", null)
          .withPrincipal(actor)
          .run(
              () -> error("common.forbidden", () -> svc.putPolicy("egress", OrgJson.object(), 1L)));
    var actor =
        principal(
            TokenContext.ADMIN, Set.of("org.policies.write"), Instant.now().minusSeconds(601));
    RequestContext.start("test", null)
        .withPrincipal(actor)
        .run(
            () ->
                error("iam.step_up_required", () -> svc.putPolicy("egress", OrgJson.object(), 1L)));
  }
}
