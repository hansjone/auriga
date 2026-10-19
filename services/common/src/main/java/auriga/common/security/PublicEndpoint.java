package auriga.common.security;

import java.lang.annotation.Documented;
import java.lang.annotation.ElementType;
import java.lang.annotation.Retention;
import java.lang.annotation.RetentionPolicy;
import java.lang.annotation.Target;

/**
 * The HTTP handler is reachable without a token (e.g. login transaction start). Must be placed on
 * the handler method itself (not inherited from the type), so opening an endpoint is always a
 * visible, local decision. Not allowed on {@code /admin/} paths or on gRPC methods (use {@code
 * auriga.grpc.server.public-methods} for health checks).
 */
@Target(ElementType.METHOD)
@Retention(RetentionPolicy.RUNTIME)
@Documented
public @interface PublicEndpoint {}
