package io.github.hansjone.auriga.common.security;

import java.util.List;
import org.springframework.security.authorization.AuthorizationManager;
import org.springframework.security.config.annotation.web.builders.HttpSecurity;
import org.springframework.security.config.annotation.web.configurers.AbstractHttpConfigurer;
import org.springframework.security.config.http.SessionCreationPolicy;
import org.springframework.security.core.Authentication;
import org.springframework.security.oauth2.jwt.JwtDecoder;
import org.springframework.security.oauth2.server.resource.InvalidBearerTokenException;
import org.springframework.security.oauth2.server.resource.web.authentication.BearerTokenAuthenticationFilter;
import org.springframework.security.web.SecurityFilterChain;
import org.springframework.security.web.access.intercept.RequestAuthorizationContext;
import org.springframework.security.web.util.matcher.RequestMatcher;

/**
 * The resource server filter chain shared by all services: Bearer access tokens only (no cookies,
 * no sessions, no CSRF; edge handles browser concerns), problem+json errors, principal and tenant
 * binding, and {@code ctx=admin} for every {@code /api/<service>/v<n>/admin/**} path (OpenAPI
 * {@code x-admin-only}).
 */
public final class AurigaWebSecurity {
  /** Paths reachable without a token unless a service configures otherwise. */
  public static final List<String> DEFAULT_PUBLIC_PATHS =
      List.of("/actuator/health", "/actuator/health/**", "/error");

  public static final String ADMIN_PATHS = "/api/*/v*/admin/**";

  private AurigaWebSecurity() {}

  public static SecurityFilterChain build(
      HttpSecurity http,
      JwtDecoder decoder,
      ProblemSecurityHandlers handlers,
      List<String> publicPaths,
      RequestMatcher publicEndpoints)
      throws Exception {
    AuthorizationManager<RequestAuthorizationContext> adminContext =
        (authentication, context) ->
            new ProblemSecurityHandlers.AdminContextDecision(isAdmin(authentication.get()));
    http.csrf(AbstractHttpConfigurer::disable)
        .httpBasic(AbstractHttpConfigurer::disable)
        .formLogin(AbstractHttpConfigurer::disable)
        .logout(AbstractHttpConfigurer::disable)
        .requestCache(AbstractHttpConfigurer::disable)
        .sessionManagement(s -> s.sessionCreationPolicy(SessionCreationPolicy.STATELESS))
        .oauth2ResourceServer(
            o ->
                o.jwt(
                        j ->
                            j.decoder(decoder)
                                .jwtAuthenticationConverter(
                                    jwt -> {
                                      try {
                                        return AurigaAuthentication.converter().convert(jwt);
                                      } catch (RuntimeException e) {
                                        throw new InvalidBearerTokenException(
                                            "Invalid access token claims", e);
                                      }
                                    }))
                    .authenticationEntryPoint(handlers)
                    .accessDeniedHandler(handlers))
        .exceptionHandling(e -> e.authenticationEntryPoint(handlers).accessDeniedHandler(handlers))
        .addFilterAfter(new PrincipalBindingFilter(), BearerTokenAuthenticationFilter.class)
        .authorizeHttpRequests(
            a -> {
              // Admin rule first: a misconfigured public path can never open an admin path.
              a.requestMatchers(ADMIN_PATHS).access(adminContext);
              if (!publicPaths.isEmpty()) {
                a.requestMatchers(publicPaths.toArray(String[]::new)).permitAll();
              }
              if (publicEndpoints != null) a.requestMatchers(publicEndpoints).permitAll();
              a.anyRequest().authenticated();
            });
    return http.build();
  }

  private static boolean isAdmin(Authentication authentication) {
    return authentication instanceof AurigaAuthentication a && a.getPrincipal().isAdminContext();
  }
}
