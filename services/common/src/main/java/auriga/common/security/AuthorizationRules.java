package auriga.common.security;

import auriga.common.context.RequestContext;
import auriga.common.error.AurigaException;
import auriga.common.error.CommonErrors;
import java.lang.reflect.Method;
import java.time.Clock;
import java.time.Duration;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Optional;
import java.util.Set;
import org.springframework.aop.support.AopUtils;
import org.springframework.core.annotation.AnnotatedElementUtils;
import org.springframework.security.core.Authentication;
import org.springframework.security.core.context.SecurityContextHolder;

/**
 * The declarative checks resolved for one method: union of type-level and method-level {@link
 * RequiresPermission}, {@link AdminOnly} on either, and the strictest {@link RequiresRecentStepUp}.
 */
public record AuthorizationRules(
    Set<String> permissions, boolean adminOnly, Duration stepUpMaxAge, boolean authenticated) {
  public static final AuthorizationRules NONE =
      new AuthorizationRules(Set.of(), false, null, false);

  public boolean isEmpty() {
    return permissions.isEmpty() && !adminOnly && stepUpMaxAge == null && !authenticated;
  }

  /** True when the method declares how it is authorized (default deny check). */
  public static boolean isDeclared(Method method, Class<?> targetClass) {
    Method specific =
        targetClass == null ? method : AopUtils.getMostSpecificMethod(method, targetClass);
    Class<?> type = targetClass == null ? method.getDeclaringClass() : targetClass;
    if (specific.isAnnotationPresent(PublicEndpoint.class)) return true;
    for (var element : List.of(specific, (java.lang.reflect.AnnotatedElement) type)) {
      if (declares(element)) return true;
    }
    return false;
  }

  /**
   * True when the element carries an annotation that requires an authenticated caller: {@link
   * RequiresPermission}, {@link AuthenticatedOnly}, {@link AdminOnly} or {@link
   * RequiresRecentStepUp}.
   */
  public static boolean declares(java.lang.reflect.AnnotatedElement element) {
    return AnnotatedElementUtils.hasAnnotation(element, RequiresPermission.class)
        || AnnotatedElementUtils.hasAnnotation(element, AuthenticatedOnly.class)
        || AnnotatedElementUtils.hasAnnotation(element, AdminOnly.class)
        || AnnotatedElementUtils.hasAnnotation(element, RequiresRecentStepUp.class);
  }

  public static boolean isPublic(Method method, Class<?> targetClass) {
    Method specific =
        targetClass == null ? method : AopUtils.getMostSpecificMethod(method, targetClass);
    return specific.isAnnotationPresent(PublicEndpoint.class);
  }

  public static AuthorizationRules of(Method method, Class<?> targetClass) {
    Method specific =
        targetClass == null ? method : AopUtils.getMostSpecificMethod(method, targetClass);
    Class<?> type = targetClass == null ? method.getDeclaringClass() : targetClass;
    Set<String> permissions = new LinkedHashSet<>();
    boolean adminOnly = false;
    boolean authenticated = false;
    Duration stepUp = null;
    for (var element : List.of(specific, (java.lang.reflect.AnnotatedElement) type)) {
      RequiresPermission rp =
          AnnotatedElementUtils.findMergedAnnotation(element, RequiresPermission.class);
      if (rp != null) permissions.addAll(List.of(rp.value()));
      if (AnnotatedElementUtils.hasAnnotation(element, AdminOnly.class)) adminOnly = true;
      if (AnnotatedElementUtils.hasAnnotation(element, AuthenticatedOnly.class)) {
        authenticated = true;
      }
      RequiresRecentStepUp su =
          AnnotatedElementUtils.findMergedAnnotation(element, RequiresRecentStepUp.class);
      if (su != null) {
        Duration age = Duration.ofSeconds(su.maxAgeSeconds());
        stepUp = stepUp == null || age.compareTo(stepUp) < 0 ? age : stepUp;
      }
    }
    if (permissions.isEmpty() && !adminOnly && stepUp == null && !authenticated) return NONE;
    return new AuthorizationRules(Set.copyOf(permissions), adminOnly, stepUp, authenticated);
  }

  /**
   * @throws AurigaException 401 without principal, 403 {@code common.forbidden} for a missing
   *     permission or non-admin context, 403 {@code iam.step_up_required} for a stale step-up
   */
  public void check(AurigaPrincipal principal, Clock clock) {
    if (isEmpty()) return;
    if (principal == null) throw new AurigaException(CommonErrors.UNAUTHENTICATED);
    if (adminOnly && !principal.isAdminContext()) {
      throw AurigaException.of(CommonErrors.FORBIDDEN, "reason", "admin_only");
    }
    for (String permission : permissions) {
      if (!principal.hasPermission(permission)) {
        throw AurigaException.of(CommonErrors.FORBIDDEN, "permission", permission);
      }
    }
    if (stepUpMaxAge != null && !principal.steppedUpWithin(stepUpMaxAge, clock.instant())) {
      throw AurigaException.of(
          CommonErrors.STEP_UP_REQUIRED, "maxAgeSeconds", stepUpMaxAge.toSeconds());
    }
  }

  /** The principal of the current request (scoped context first, then Spring Security). */
  public static Optional<AurigaPrincipal> currentPrincipal() {
    Optional<AurigaPrincipal> scoped = RequestContext.currentPrincipal();
    if (scoped.isPresent()) return scoped;
    Authentication auth = SecurityContextHolder.getContext().getAuthentication();
    return auth instanceof AurigaAuthentication a
        ? Optional.of(a.getPrincipal())
        : Optional.empty();
  }
}
