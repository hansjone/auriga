package io.github.hansjone.auriga.common.security;

import java.lang.annotation.Documented;
import java.lang.annotation.ElementType;
import java.lang.annotation.Inherited;
import java.lang.annotation.Retention;
import java.lang.annotation.RetentionPolicy;
import java.lang.annotation.Target;

/**
 * OpenAPI {@code x-admin-only: true}: only tokens with {@code ctx=admin} (admin console sessions)
 * are accepted. Every {@code /api/<service>/v1/admin/**} path is additionally guarded by {@link
 * AurigaWebSecurity} (path rule), so a forgotten annotation on an admin controller still fails
 * closed.
 */
@Target({ElementType.METHOD, ElementType.TYPE})
@Retention(RetentionPolicy.RUNTIME)
@Inherited
@Documented
public @interface AdminOnly {}
