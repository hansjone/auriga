package auriga.common.security;

import java.lang.annotation.Documented;
import java.lang.annotation.ElementType;
import java.lang.annotation.Inherited;
import java.lang.annotation.Retention;
import java.lang.annotation.RetentionPolicy;
import java.lang.annotation.Target;

/**
 * Any authenticated caller may invoke the endpoint, no specific permission (e.g. {@code GET /me}).
 * Every HTTP handler and gRPC method must carry this, {@link RequiresPermission} or {@link
 * PublicEndpoint}; unannotated endpoints stop the application from starting ({@link
 * EndpointAuthorizationCheck}).
 */
@Target({ElementType.METHOD, ElementType.TYPE})
@Retention(RetentionPolicy.RUNTIME)
@Inherited
@Documented
public @interface AuthenticatedOnly {}
