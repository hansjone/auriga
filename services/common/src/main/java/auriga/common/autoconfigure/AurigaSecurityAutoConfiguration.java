package auriga.common.autoconfigure;

import auriga.common.error.Problems;
import auriga.common.security.AccessTokenDecoders;
import auriga.common.security.AccessTokenSettings;
import auriga.common.security.AccessTokenVerifier;
import auriga.common.security.AurigaWebSecurity;
import auriga.common.security.EndpointAuthorizationCheck;
import auriga.common.security.InternalTokenVerifier;
import auriga.common.security.ProblemSecurityHandlers;
import java.net.URI;
import java.time.Clock;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.boot.autoconfigure.AutoConfiguration;
import org.springframework.boot.autoconfigure.condition.ConditionalOnMissingBean;
import org.springframework.boot.autoconfigure.condition.ConditionalOnWebApplication;
import org.springframework.boot.security.autoconfigure.web.servlet.ServletWebSecurityAutoConfiguration;
import org.springframework.boot.security.oauth2.server.resource.autoconfigure.OAuth2ResourceServerAutoConfiguration;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.security.config.annotation.web.builders.HttpSecurity;
import org.springframework.security.config.annotation.web.configuration.EnableWebSecurity;
import org.springframework.security.oauth2.jwt.BadJwtException;
import org.springframework.security.oauth2.jwt.JwtDecoder;
import org.springframework.security.web.SecurityFilterChain;
import org.springframework.web.servlet.mvc.method.annotation.RequestMappingHandlerMapping;

/** Resource server: access token validation and the shared filter chain. */
@AutoConfiguration(
    after = AurigaCoreAutoConfiguration.class,
    before = {
      ServletWebSecurityAutoConfiguration.class,
      OAuth2ResourceServerAutoConfiguration.class
    },
    beforeName =
        "org.springframework.boot.security.autoconfigure.UserDetailsServiceAutoConfiguration")
public class AurigaSecurityAutoConfiguration {
  private static final Logger log = LoggerFactory.getLogger(AurigaSecurityAutoConfiguration.class);

  private static AccessTokenSettings settings(AurigaProperties properties, Clock clock) {
    AurigaProperties.Jwt jwt = properties.getSecurity().getJwt();
    if (jwt.getIssuer() == null || jwt.getJwkSetUri() == null) return null;
    return new AccessTokenSettings(
        jwt.getIssuer(),
        URI.create(jwt.getJwkSetUri()),
        jwt.getAudience(),
        jwt.getClockSkew(),
        jwt.getJwksCacheTtl(),
        jwt.getJwksMinRefreshInterval(),
        clock,
        jwt.isAllowInsecureJwksUri());
  }

  private static JwtDecoder rejectAll() {
    return token -> {
      throw new BadJwtException("Access token validation is not configured");
    };
  }

  /** Platform tokens for the HTTP API ({@code aud} contains {@code auriga}). */
  @Bean
  @ConditionalOnMissingBean
  JwtDecoder aurigaAccessTokenDecoder(AurigaProperties properties, Clock clock) {
    AccessTokenSettings settings = settings(properties, clock);
    if (settings == null) {
      log.warn(
          "auriga.security.jwt.issuer / jwk-set-uri not configured: all access tokens are rejected");
      return rejectAll();
    }
    return AccessTokenDecoders.create(settings);
  }

  @Bean
  @ConditionalOnMissingBean
  AccessTokenVerifier aurigaAccessTokenVerifier(JwtDecoder decoder) {
    return new AccessTokenVerifier(decoder);
  }

  /** Internal service tokens for gRPC ({@code aud} = {@code auriga.internal}). */
  @Bean
  @ConditionalOnMissingBean
  InternalTokenVerifier aurigaInternalTokenVerifier(AurigaProperties properties, Clock clock) {
    AccessTokenSettings settings = settings(properties, clock);
    return new InternalTokenVerifier(
        new AccessTokenVerifier(
            settings == null ? rejectAll() : AccessTokenDecoders.createInternal(settings)));
  }

  @Configuration(proxyBeanMethods = false)
  @ConditionalOnWebApplication(type = ConditionalOnWebApplication.Type.SERVLET)
  @EnableWebSecurity
  static class Web {
    @Bean
    @ConditionalOnMissingBean
    ProblemSecurityHandlers aurigaProblemSecurityHandlers(Problems problems) {
      return new ProblemSecurityHandlers(problems);
    }

    @Bean
    @ConditionalOnMissingBean(SecurityFilterChain.class)
    SecurityFilterChain aurigaSecurityFilterChain(
        HttpSecurity http,
        JwtDecoder decoder,
        ProblemSecurityHandlers handlers,
        AurigaProperties properties,
        ObjectProvider<RequestMappingHandlerMapping> mappings)
        throws Exception {
      return AurigaWebSecurity.build(
          http,
          decoder,
          handlers,
          properties.getSecurity().getPublicPaths(),
          EndpointAuthorizationCheck.publicEndpoints(mappings));
    }

    /** Default deny: unannotated handlers stop startup. */
    @Bean
    static EndpointAuthorizationCheck aurigaEndpointAuthorizationCheck(
        ObjectProvider<RequestMappingHandlerMapping> mappings) {
      return new EndpointAuthorizationCheck(mappings);
    }
  }
}
