package io.github.hansjone.auriga.common.security;

import java.lang.annotation.Documented;
import java.lang.annotation.ElementType;
import java.lang.annotation.Inherited;
import java.lang.annotation.Retention;
import java.lang.annotation.RetentionPolicy;
import java.lang.annotation.Target;

/**
 * The session must have completed a second factor recently: token claim {@code stepup_time} within
 * {@link #maxAgeSeconds()} (default 10 minutes). Fails with 403 {@code iam.step_up_required} so the
 * client can start a step-up transaction.
 */
@Target({ElementType.METHOD, ElementType.TYPE})
@Retention(RetentionPolicy.RUNTIME)
@Inherited
@Documented
public @interface RequiresRecentStepUp {
  long maxAgeSeconds() default 600;
}
