package io.github.hansjone.auriga.common.security;

import java.lang.reflect.Method;
import java.time.Clock;
import java.util.Map;
import java.util.Objects;
import java.util.concurrent.ConcurrentHashMap;
import org.aopalliance.aop.Advice;
import org.aopalliance.intercept.MethodInterceptor;
import org.springframework.aop.Pointcut;
import org.springframework.aop.support.AbstractPointcutAdvisor;
import org.springframework.aop.support.StaticMethodMatcherPointcut;
import org.springframework.core.Ordered;

/**
 * Enforces {@link RequiresPermission}, {@link AdminOnly} and {@link RequiresRecentStepUp} on any
 * Spring bean, so MVC controllers and gRPC service implementations share one implementation.
 * Registered as an infrastructure advisor by the auto-configuration.
 */
public final class AuthorizationAdvisor extends AbstractPointcutAdvisor {
  private final transient Map<Method, Map<Class<?>, AuthorizationRules>> cache =
      new ConcurrentHashMap<>();
  private final transient Clock clock;

  public AuthorizationAdvisor(Clock clock) {
    this.clock = Objects.requireNonNull(clock);
    setOrder(Ordered.HIGHEST_PRECEDENCE + 100);
  }

  private AuthorizationRules rules(Method method, Class<?> targetClass) {
    Class<?> key = targetClass == null ? method.getDeclaringClass() : targetClass;
    return cache
        .computeIfAbsent(method, m -> new ConcurrentHashMap<>())
        .computeIfAbsent(key, c -> AuthorizationRules.of(method, c));
  }

  @Override
  public Pointcut getPointcut() {
    return new StaticMethodMatcherPointcut() {
      @Override
      public boolean matches(Method method, Class<?> targetClass) {
        return !rules(method, targetClass).isEmpty();
      }
    };
  }

  @Override
  public Advice getAdvice() {
    return (MethodInterceptor)
        invocation -> {
          Class<?> target = invocation.getThis() == null ? null : invocation.getThis().getClass();
          rules(invocation.getMethod(), target)
              .check(AuthorizationRules.currentPrincipal().orElse(null), clock);
          return invocation.proceed();
        };
  }
}
