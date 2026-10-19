package auriga.common.security;

import java.lang.annotation.Documented;
import java.lang.annotation.ElementType;
import java.lang.annotation.Inherited;
import java.lang.annotation.Retention;
import java.lang.annotation.RetentionPolicy;
import java.lang.annotation.Target;

/**
 * The caller's access token must carry every listed permission ({@code perms}), e.g.
 * {@code @RequiresPermission("org.members.write")} matching the operation's {@code x-permission}.
 * Enforced for Spring beans (MVC controllers and gRPC services alike) by {@link
 * AuthorizationAdvisor}; on a type it applies to every public method.
 */
@Target({ElementType.METHOD, ElementType.TYPE})
@Retention(RetentionPolicy.RUNTIME)
@Inherited
@Documented
public @interface RequiresPermission {
  String[] value();
}
