package io.github.hansjone.auriga.iam;

import io.github.hansjone.auriga.common.audit.AuditLogger;
import io.github.hansjone.auriga.common.events.EventPublisher;
import io.github.hansjone.auriga.common.persistence.TenantTransactionManager;
import io.github.hansjone.auriga.iam.api.*;
import java.net.URI;
import java.nio.file.Path;
import java.time.*;
import java.util.*;
import java.util.UUID;
import org.springframework.boot.autoconfigure.condition.*;
import org.springframework.boot.context.properties.bind.*;
import org.springframework.context.annotation.*;
import org.springframework.core.annotation.Order;
import org.springframework.core.env.Environment;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.security.config.annotation.web.builders.HttpSecurity;
import org.springframework.security.config.annotation.web.configurers.AbstractHttpConfigurer;
import org.springframework.security.web.SecurityFilterChain;

/** Explicit issuer + encryption key required; an unconfigured skeleton exposes no IAM endpoints. */
@Configuration(proxyBeanMethods = false)
@ConditionalOnProperty(prefix = "auriga.iam", name = "issuer")
@org.springframework.boot.autoconfigure.condition.ConditionalOnExpression(
    "${auriga.services.iam.enabled:true}")
public class IamRuntimeConfiguration {
  @Bean
  ContractSchemas iamSchemas() {
    return new ContractSchemas();
  }

  @Bean
  Secrets iamSecrets(Environment env) {
    return new Secrets(env.getRequiredProperty("auriga.iam.encryption-key"));
  }

  @Bean
  IamStore iamStore(javax.sql.DataSource source, TenantTransactionManager manager) {
    return new IamStore(new JdbcTemplate(source), manager);
  }

  @Bean
  SigningKeys iamSigningKeys(IamStore store, Secrets secrets, Clock clock, Environment env) {
    return new SigningKeys(
        store,
        secrets,
        clock,
        Duration.ofSeconds(
            env.getProperty("auriga.iam.jwks-propagation-seconds", Long.class, 300L)));
  }

  @Bean
  @ConditionalOnMissingBean(PermissionResolver.class)
  PermissionResolver iamPermissionResolver(Environment env) {
    Map<String, String> configured =
        Binder.get(env)
            .bind("auriga.iam.permissions", Bindable.mapOf(String.class, String.class))
            .orElse(Map.of());
    return (tenant, subject, context) -> {
      String value = configured.getOrDefault(tenant + "/" + subject, "");
      return value.isBlank() ? Set.of() : Set.copyOf(List.of(value.split(" *, *")));
    };
  }

  @Bean
  TokenService iamTokens(
      SigningKeys keys,
      ContractSchemas schemas,
      PermissionResolver permissions,
      Clock clock,
      Environment env) {
    return new TokenService(
        keys,
        schemas,
        permissions,
        clock,
        env.getRequiredProperty("auriga.iam.issuer"),
        Binder.get(env)
            .bind("auriga.iam.internal-secret-hashes", Bindable.mapOf(String.class, String.class))
            .orElse(Map.of()));
  }

  @Bean
  SessionKv iamSessionKv(IamStore store, ContractSchemas schemas, Clock clock) {
    return new PostgresSessionKv(store, schemas, clock);
  }

  @Bean
  SessionManager iamSessions(
      IamStore store,
      SessionKv kv,
      TokenService tokens,
      Clock clock,
      EventPublisher events,
      AuditLogger audit) {
    return new SessionManager(store, kv, tokens, clock, events, audit);
  }

  @Bean
  IamGrpcSessions iamGrpcSessions(SessionManager sessions) {
    return new IamGrpcSessions(sessions);
  }

  @Bean
  IamAccounts iamAccounts(IamStore store) {
    return new IamAccounts(store);
  }

  @Bean
  @ConditionalOnProperty(prefix = "auriga.iam", name = "local-service-name")
  io.github.hansjone.auriga.common.grpc.ServiceTokenProvider iamLocalServiceTokenProvider(
      TokenService tokens, Environment env) {
    return () ->
        tokens.issue(
            env.getRequiredProperty("auriga.iam.local-service-name"),
            env.getRequiredProperty("auriga.iam.local-service-secret"));
  }

  @Bean
  IamAccountEvents iamAccountEvents(IamStore store, SessionManager sessions) {
    return new IamAccountEvents(store, sessions);
  }

  @Bean
  EmergencyAccounts iamEmergencyAccounts(
      IamStore store, Secrets secrets, Clock clock, Environment env) {
    return new EmergencyAccounts(
        store,
        secrets,
        clock,
        env.getProperty("auriga.iam.emergency-remote-enabled", Boolean.class, false));
  }

  @Bean
  TeamAccounts iamTeamAccounts(
      IamStore store, Secrets secrets, Clock clock, EventPublisher events) {
    return new TeamAccounts(store, secrets, clock, events);
  }

  @Bean
  TeamPasskeys iamTeamPasskeys(
      IamStore store, Secrets secrets, Clock clock, EventPublisher events) {
    return new TeamPasskeys(store, secrets, clock, events);
  }

  @Bean
  QrChallenges iamQrChallenges(IamStore store, Clock clock) {
    return new QrChallenges(store, clock);
  }

  @Bean
  IdentityConnector teamAccountConnector(
      TeamAccounts accounts, TeamPasskeys passkeys, QrChallenges qr, TokenService tokens) {
    return new TeamAccountConnector(accounts, passkeys, qr, tokens.issuer());
  }

  @Bean
  IdentityConnector oidcConnector() {
    return new OidcConnector();
  }

  @Bean
  @ConditionalOnMissingBean(IdentityPolicyHooks.class)
  IdentityPolicyHooks iamIdentityPolicyHooks(TeamAccounts accounts) {
    return new IdentityPolicyHooks() {
      @Override
      public boolean verifyMfa(UUID account, tools.jackson.databind.JsonNode input) {
        return accounts.verifyMfaInput(account, input);
      }
    };
  }

  public record RemoteRegistration(URI url, List<String> sharedKeys) {}

  @Bean(destroyMethod = "close")
  ConnectorRegistry iamConnectors(
      ContractSchemas schemas,
      Environment env,
      Clock clock,
      org.springframework.beans.factory.ObjectProvider<IdentityConnector> builtins)
      throws java.io.IOException {
    ConnectorRegistry registry = new ConnectorRegistry(schemas, List.of(new EmergencyConnector()));
    builtins.orderedStream().forEach(registry::register);
    registry.load(Path.of(env.getProperty("auriga.iam.plugins-directory", "plugins/identity")));
    for (RemoteRegistration remote :
        Binder.get(env)
            .bind("auriga.iam.remote-connectors", Bindable.listOf(RemoteRegistration.class))
            .orElse(List.of()))
      registry.register(
          new RemoteConnector(
              remote.url(),
              remote.sharedKeys().stream().map(k -> Base64.getDecoder().decode(k)).toList(),
              clock));
    return registry;
  }

  @Bean
  LoginManager iamLogin(
      IamStore store,
      Secrets secrets,
      ContractSchemas schemas,
      ConnectorRegistry registry,
      EmergencyAccounts emergency,
      TeamAccounts teamAccounts,
      SessionManager sessions,
      IdentityPolicyHooks hooks,
      EventPublisher events,
      AuditLogger audit,
      Clock clock,
      TokenService tokens) {
    return new LoginManager(
        store,
        secrets,
        schemas,
        registry,
        emergency,
        teamAccounts,
        sessions,
        hooks,
        events,
        audit,
        clock,
        tokens.issuer());
  }

  @Bean
  ProviderManager iamProviders(
      IamStore store,
      ConnectorRegistry registry,
      Secrets secrets,
      AuditLogger audit,
      Clock clock,
      TokenService tokens) {
    return new ProviderManager(store, registry, secrets, audit, clock, tokens.issuer());
  }

  @Bean
  OAuthProvider iamOAuth(
      IamStore store, SessionManager sessions, TokenService tokens, Clock clock, Environment env) {
    return new OAuthProvider(
        store,
        sessions,
        tokens,
        clock,
        Binder.get(env)
            .bind("auriga.iam.clients", Bindable.listOf(OAuthClient.class))
            .orElse(List.of()));
  }

  @Bean
  RequestIdentity iamRequestIdentity(Environment env) {
    Map<String, RequestIdentity.Host> hosts =
        Binder.get(env)
            .bind("auriga.iam.hosts", Bindable.mapOf(String.class, RequestIdentity.Host.class))
            .orElse(Map.of());
    if (hosts.isEmpty())
      throw new IllegalStateException("IAM host -> tenant/audience map must be configured");
    return new RequestIdentity(
        hosts,
        Set.copyOf(
            Binder.get(env)
                .bind("auriga.iam.trusted-proxies", Bindable.listOf(String.class))
                .orElse(List.of())));
  }

  @Bean
  @Order(0)
  @ConditionalOnWebApplication(type = ConditionalOnWebApplication.Type.SERVLET)
  SecurityFilterChain iamOAuthChain(HttpSecurity http) throws Exception {
    // Userinfo verifies third-party-audience tokens itself; common only accepts auriga tokens.
    http.securityMatcher("/oauth2/**", "/.well-known/openid-configuration", "/userinfo")
        .authorizeHttpRequests(a -> a.anyRequest().permitAll())
        .csrf(AbstractHttpConfigurer::disable)
        .requestCache(AbstractHttpConfigurer::disable)
        .sessionManagement(
            s ->
                s.sessionCreationPolicy(
                    org.springframework.security.config.http.SessionCreationPolicy.STATELESS));
    return http.build();
  }

  @Bean
  @Order(1)
  @ConditionalOnWebApplication(type = ConditionalOnWebApplication.Type.SERVLET)
  SecurityFilterChain iamApiChain(
      HttpSecurity http,
      org.springframework.security.oauth2.jwt.JwtDecoder decoder,
      io.github.hansjone.auriga.common.security.ProblemSecurityHandlers handlers,
      io.github.hansjone.auriga.common.autoconfigure.AurigaProperties properties,
      org.springframework.beans.factory.ObjectProvider<
              org.springframework.web.servlet.mvc.method.annotation.RequestMappingHandlerMapping>
          mappings)
      throws Exception {
    return io.github.hansjone.auriga.common.security.AurigaWebSecurity.build(
        http,
        decoder,
        handlers,
        properties.getSecurity().getPublicPaths(),
        io.github.hansjone.auriga.common.security.EndpointAuthorizationCheck.publicEndpoints(
            mappings));
  }
}
