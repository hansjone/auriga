package io.github.hansjone.auriga.common.security;

import static org.junit.jupiter.api.Assertions.*;

import io.github.hansjone.auriga.common.context.RequestContext;
import io.github.hansjone.auriga.common.error.AurigaException;
import io.github.hansjone.auriga.common.error.CommonErrors;
import java.time.Clock;
import java.time.Instant;
import java.time.ZoneOffset;
import java.util.List;
import java.util.Set;
import java.util.UUID;
import org.junit.jupiter.api.Test;
import org.springframework.aop.framework.ProxyFactory;

class AuthorizationAdvisorTest {
  private static final Instant NOW = Instant.parse("2026-10-19T08:00:00Z");
  private static final Clock CLOCK = Clock.fixed(NOW, ZoneOffset.UTC);

  public static class MembersApi {
    @RequiresPermission("org.members.write")
    public String update() {
      return "updated";
    }

    @RequiresPermission({"org.members.write", "org.members.offboard"})
    public String offboard() {
      return "offboarded";
    }

    @RequiresRecentStepUp
    public String sensitive() {
      return "ok";
    }

    @RequiresRecentStepUp(maxAgeSeconds = 60)
    public String verySensitive() {
      return "ok";
    }

    public String open() {
      return "open";
    }
  }

  @AdminOnly
  @RequiresPermission("org.members.read")
  public static class AdminApi {
    public String list() {
      return "list";
    }

    @RequiresPermission("org.members.write")
    public String write() {
      return "write";
    }
  }

  @SuppressWarnings("unchecked")
  private static <T> T proxy(T target) {
    ProxyFactory factory = new ProxyFactory(target);
    factory.setProxyTargetClass(true);
    factory.addAdvisor(new AuthorizationAdvisor(CLOCK));
    return (T) factory.getProxy();
  }

  private static AurigaPrincipal principal(TokenContext ctx, Set<String> perms, Instant stepUp) {
    return new AurigaPrincipal(
        "alice",
        UUID.randomUUID(),
        PrincipalType.USER,
        ctx,
        null,
        null,
        null,
        List.of(),
        perms,
        null,
        stepUp,
        "jti");
  }

  private static <T> T as(AurigaPrincipal principal, java.util.function.Supplier<T> call) {
    return RequestContext.start(null, null).withPrincipal(principal).call(call::get);
  }

  private static void assertDenied(
      io.github.hansjone.auriga.common.error.ErrorCode code, Runnable call) {
    AurigaException e = assertThrows(AurigaException.class, call::run);
    assertEquals(code, e.errorCode());
  }

  @Test
  void permissionPresentOrMissing() {
    MembersApi api = proxy(new MembersApi());
    var writer = principal(TokenContext.PORTAL, Set.of("org.members.write"), null);
    var reader = principal(TokenContext.PORTAL, Set.of("org.members.read"), null);
    assertEquals("updated", as(writer, api::update));
    assertDenied(CommonErrors.FORBIDDEN, () -> as(reader, api::update));
    // all listed permissions are required
    assertDenied(CommonErrors.FORBIDDEN, () -> as(writer, api::offboard));
    assertEquals("open", as(reader, api::open));
  }

  @Test
  void noPrincipalIsUnauthenticated() {
    MembersApi api = proxy(new MembersApi());
    assertDenied(CommonErrors.UNAUTHENTICATED, api::update);
    assertEquals("open", api.open());
  }

  @Test
  void adminOnlyRequiresAdminContextEvenWithPermissions() {
    AdminApi api = proxy(new AdminApi());
    var all = Set.of("org.members.read", "org.members.write");
    assertEquals("list", as(principal(TokenContext.ADMIN, all, null), api::list));
    for (TokenContext ctx :
        List.of(TokenContext.PORTAL, TokenContext.DEVICE, TokenContext.SERVICE)) {
      assertDenied(CommonErrors.FORBIDDEN, () -> as(principal(ctx, all, null), api::list));
    }
    // class and method permissions combine
    assertDenied(
        CommonErrors.FORBIDDEN,
        () -> as(principal(TokenContext.ADMIN, Set.of("org.members.read"), null), api::write));
    assertEquals("write", as(principal(TokenContext.ADMIN, all, null), api::write));
  }

  @Test
  void stepUpMustBeRecent() {
    MembersApi api = proxy(new MembersApi());
    assertEquals(
        "ok", as(principal(TokenContext.PORTAL, Set.of(), NOW.minusSeconds(599)), api::sensitive));
    assertDenied(
        CommonErrors.STEP_UP_REQUIRED,
        () -> as(principal(TokenContext.PORTAL, Set.of(), NOW.minusSeconds(601)), api::sensitive));
    assertDenied(
        CommonErrors.STEP_UP_REQUIRED,
        () -> as(principal(TokenContext.PORTAL, Set.of(), null), api::sensitive));
    // a step-up time far in the future is not trusted
    assertDenied(
        CommonErrors.STEP_UP_REQUIRED,
        () -> as(principal(TokenContext.PORTAL, Set.of(), NOW.plusSeconds(3600)), api::sensitive));
    assertDenied(
        CommonErrors.STEP_UP_REQUIRED,
        () ->
            as(
                principal(TokenContext.PORTAL, Set.of(), NOW.minusSeconds(120)),
                api::verySensitive));
  }

  @Test
  void stepUpErrorUsesIamCode() {
    assertEquals("iam.step_up_required", CommonErrors.STEP_UP_REQUIRED.code());
    assertEquals(403, CommonErrors.STEP_UP_REQUIRED.httpStatus());
  }
}
